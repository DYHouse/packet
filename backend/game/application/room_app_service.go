package application

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/trace"
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/push"
	repository "github.com/cashparty/backend/game/domain/repository"
	roomDom "github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
	"github.com/cashparty/backend/settlement/dto"
)

type RoomAppService struct {
	repo               repository.RoomRepository
	dbRepo             repository.DBRepository
	userService        *UserService
	broadcaster        events.Broadcaster
	publisher          events.RoomEventPublisher
	scheduler          *scheduler.TimeoutScheduler
	settleAppService   *settlementApplication.SettleAppService
	resumeGameCallback ResumeGameCallback
	taskRunner         async.TaskRunner
}

// ResumeGameCallback 由 GameAppService 注入，用于自动上座补满后恢复中断游戏
type ResumeGameCallback func(ctx context.Context, roomID string, currentRound int)

// SetResumeGameCallback 注入恢复游戏回调（避免与 GameAppService 循环依赖）
func (s *RoomAppService) SetResumeGameCallback(cb ResumeGameCallback) {
	s.resumeGameCallback = cb
}

func NewRoomAppService(
	repo repository.RoomRepository,
	dbRepo repository.DBRepository,
	userService *UserService,
	broadcaster events.Broadcaster,
	publisher events.RoomEventPublisher,
	scheduler *scheduler.TimeoutScheduler,
	settleAppSvc *settlementApplication.SettleAppService,
	taskRunner async.TaskRunner,
) *RoomAppService {
	return &RoomAppService{
		repo:             repo,
		dbRepo:           dbRepo,
		userService:      userService,
		broadcaster:      broadcaster,
		publisher:        publisher,
		scheduler:        scheduler,
		settleAppService: settleAppSvc,
		taskRunner:       taskRunner,
	}
}

type JoinRoomRequest struct {
	RoomID string
	UserID string
}

type JoinRoomResult struct {
	RoomID      string
	RoomNo      string
	RoomState   *RoomState
	IsSpectator bool
	SeatNo      int  // 自动上座成功时的座位号，0 表示未上座
	AutoSeated  bool // 是否自动上座并准备
}

type AutoMatchRequest struct {
	UserID string
}

func (s *RoomAppService) JoinRoom(ctx context.Context, req *JoinRoomRequest) (*JoinRoomResult, error) {
	userInfo, err := s.userService.GetUserById(ctx, req.UserID)
	if err != nil {
		return nil, message.NewError(message.CodeUserNotFound)
	}

	_, err = s.repo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		room, dbErr := s.dbRepo.RoomDBRepo().GetRoom(ctx, req.RoomID)
		if dbErr != nil {
			return nil, message.NewError(message.CodeRoomNotFound)
		}

		roomMeta := &roomDom.RoomMeta{
			RoomID:           req.RoomID,
			RoomNo:           room.RoomNo,
			ConfigID:         room.ConfigID,
			ConfigName:       room.ConfigName,
			RoomFee:          room.RoomFee,
			MaxPlayers:       room.MaxPlayers,
			MaxRounds:        room.MaxRounds,
			MaxSpectators:    room.MaxSpectators,
			Status:           roomDom.RoomStatus(room.Status),
			CurrentRound:     room.CurrentRound,
			CurrentSessionID: fmt.Sprintf("%d", room.CurrentSessionID),
		}

		if initErr := s.repo.InitRoom(ctx, roomMeta); initErr != nil {
			logger.Error("failed to init room in redis", "room_id", req.RoomID, "error", initErr)
			return nil, message.NewError(message.CodeSystemError)
		}
	}

	spectator := &roomDom.Spectator{
		UserID:   req.UserID,
		Nickname: userInfo.Nickname,
		Avatar:   userInfo.Avatar,
		IsRobot:  userInfo.IsRobot,
	}

	result, err := s.repo.JoinAsSpectator(ctx, req.RoomID, spectator)
	if err != nil {
		return nil, err
	}

	roomID := result.RoomID
	roomNo := result.RoomNo

	if s.publisher != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewSpectatorJoinEvent(roomID, req.UserID, userInfo.Nickname, userInfo.Avatar)); err != nil {
			logger.Warn("publish spectator_join event failed",
				"room_id", roomID,
				"user_id", req.UserID,
				"trace_id", trace.FromContext(ctx),
				"error", err)
		}
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, roomID)

	if s.broadcaster != nil && stateData != nil {
		s.broadcaster.Broadcast(ctx, roomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
	}

	logger.Info("user joined room",
		"room_id", roomID,
		"room_no", roomNo,
		"user_id", req.UserID,
	)

	return &JoinRoomResult{
		RoomID:      roomID,
		RoomNo:      roomNo,
		RoomState:   BuildFullRoomState(stateData),
		IsSpectator: true,
	}, nil
}

// JoinAndAutoSeat 真实玩家入房：先以观战者身份加入，再尝试自动选座并准备。
// 座位未满 → 自动上座并准备；座位已满 → 纯观战（可后续排队）。
func (s *RoomAppService) JoinAndAutoSeat(ctx context.Context, req *JoinRoomRequest) (*JoinRoomResult, error) {
	joinResult, err := s.JoinRoom(ctx, req)
	if err != nil {
		return nil, err
	}

	meta, metaErr := s.repo.GetRoomMeta(ctx, joinResult.RoomID)
	if metaErr != nil || meta == nil {
		logger.Warn("auto seat: failed to get room meta, remain spectator",
			"room_id", joinResult.RoomID, "error", metaErr)
		return joinResult, nil
	}

	// 游戏进行中（非中断态）不自动上座
	if meta.Status == roomDom.RoomStatusPlaying {
		return joinResult, nil
	}

	// 余额校验：不足则保持观战
	userInfo, err := s.userService.GetUserById(ctx, req.UserID)
	if err != nil {
		return joinResult, nil
	}
	if s.settleAppService != nil {
		balanceResult, balErr := s.settleAppService.CheckBalanceForReady(ctx, &dto.BalanceCheckRequest{
			UserID:     userInfo.ID,
			RoomFee:    meta.RoomFee,
			MaxPlayers: meta.MaxPlayers,
			MaxRounds:  meta.MaxRounds,
		})
		if balErr != nil {
			logger.Warn("auto seat: balance check failed, remain spectator",
				"room_id", joinResult.RoomID, "user_id", req.UserID, "error", balErr)
			return joinResult, nil
		}
		if !balanceResult.IsSufficient {
			logger.Info("auto seat: insufficient balance, remain spectator",
				"room_id", joinResult.RoomID, "user_id", req.UserID)
			return joinResult, nil
		}
	}

	autoResult, autoErr := s.repo.AutoSeatAndReady(ctx, joinResult.RoomID, req.UserID, userInfo.IsRobot)
	if autoErr != nil {
		logger.Info("auto seat: no empty seat or failed, remain spectator",
			"room_id", joinResult.RoomID, "user_id", req.UserID, "error", autoErr)
		return joinResult, nil
	}

	if s.scheduler != nil {
		s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSeat, joinResult.RoomID, req.UserID)
	}

	if s.publisher != nil && autoResult.Player != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewPlayerReadyEvent(
			joinResult.RoomID, req.UserID, autoResult.SeatNo,
			autoResult.Player.Nickname, autoResult.Player.Avatar)); err != nil {
			logger.Warn("publish player_ready event failed",
				"room_id", joinResult.RoomID,
				"user_id", req.UserID,
				"trace_id", trace.FromContext(ctx),
				"error", err)
		}
	}

	var roomState *RoomState
	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, joinResult.RoomID)
		if stateData != nil {
			roomState = BuildFullRoomState(stateData)
			s.broadcaster.Broadcast(ctx, joinResult.RoomID, message.PushRoomState, roomState, req.UserID)
		}
	}

	s.handleCountdownAfterSeat(ctx, joinResult.RoomID, autoResult.ShouldStartCountdown,
		autoResult.CountdownEndTime, autoResult.CurrentRound)

	logger.Info("user auto seated and ready",
		"room_id", joinResult.RoomID,
		"user_id", req.UserID,
		"seat_no", autoResult.SeatNo,
		"player_count", autoResult.PlayerCount,
	)

	return &JoinRoomResult{
		RoomID:      joinResult.RoomID,
		RoomNo:      joinResult.RoomNo,
		RoomState:   roomState,
		IsSpectator: false,
		SeatNo:      autoResult.SeatNo,
		AutoSeated:  true,
	}, nil
}

// handleCountdownAfterSeat 处理上座/替补后的倒计时与游戏恢复逻辑
func (s *RoomAppService) handleCountdownAfterSeat(ctx context.Context, roomID string,
	shouldStartCountdown int, countdownEndTime int64, currentRound int) {
	if shouldStartCountdown == 1 {
		countdownDuration := int(countdownEndTime - time.Now().Unix())
		if countdownDuration <= 0 {
			countdownDuration = 3
		}
		if s.broadcaster != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushCountdownStart, &push.CountdownStartPush{
				RoomID:    roomID,
				Countdown: int32(countdownDuration),
			}, "")
		}
		if s.scheduler != nil {
			s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeReady, roomID,
				converter.FormatID(countdownEndTime))
		}
		logger.Info("game countdown started (auto seat)",
			"room_id", roomID, "countdown_end_time", countdownEndTime)
	} else if shouldStartCountdown == 2 {
		if s.resumeGameCallback != nil {
			if err := s.taskRunner.Submit("resume_game_callback", 10*time.Second, func(ctx context.Context) {
				s.resumeGameCallback(ctx, roomID, currentRound)
			}); err != nil {
				logger.Warn("submit resume_game_callback task failed", "error", err)
			}
		}
		logger.Info("game resume triggered (auto seat substitute)",
			"room_id", roomID, "current_round", currentRound)
	}
}

// tryAutoSubstitute 座位释放后尝试从排队队列自动替补。
// 替补前会逐个校验排队者余额，余额不足者被移出队列并通知，继续下一位。
// 返回替补结果（nil 表示无替补）。
func (s *RoomAppService) tryAutoSubstitute(ctx context.Context, roomID string, seatNo int) *repository.SubstituteResult {
	// 取房间元信息用于余额校验
	meta, metaErr := s.repo.GetRoomMeta(ctx, roomID)
	if metaErr != nil || meta == nil {
		logger.Warn("auto substitute: failed to get room meta",
			"room_id", roomID, "seat_no", seatNo, "error", metaErr)
		return nil
	}

	// 余额校验：逐个检查排队者，余额不足者移出队列并通知
	if s.settleAppService != nil {
		queueList, _ := s.repo.GetQueueList(ctx, roomID)
		for _, q := range queueList {
			if q == nil {
				continue
			}
			userInfo, err := s.userService.GetUserById(ctx, q.UserID)
			if err != nil {
				continue
			}
			balanceResult, balErr := s.settleAppService.CheckBalanceForReady(ctx, &dto.BalanceCheckRequest{
				UserID:     userInfo.ID,
				RoomFee:    meta.RoomFee,
				MaxPlayers: meta.MaxPlayers,
				MaxRounds:  meta.MaxRounds,
			})
			if balErr != nil || balanceResult == nil || !balanceResult.IsSufficient {
				// 余额不足：移出队列并通知
				s.repo.Dequeue(ctx, roomID, q.UserID)
				if s.broadcaster != nil {
					s.broadcaster.BroadcastToUser(ctx, q.UserID, message.PushDequeued, &push.DequeuedPush{
						RoomID:  roomID,
						UserID:  q.UserID,
						Reason:  "insufficient_balance",
						Message: "余额不足，已移出排队队列",
					})
				}
				logger.Info("auto substitute: dequeue insufficient balance user",
					"room_id", roomID, "user_id", q.UserID)
				continue
			}
			// 找到余额充足的候选者，停止校验
			break
		}
	}

	subResult, err := s.repo.AutoSubstitute(ctx, roomID, seatNo)
	if err != nil {
		logger.Warn("auto substitute failed",
			"room_id", roomID, "seat_no", seatNo, "error", err)
		return nil
	}
	if subResult == nil {
		logger.Info("auto substitute: no one in queue, skip",
			"room_id", roomID, "seat_no", seatNo)
		return nil
	}

	if s.publisher != nil && subResult.Player != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewSubstituteEvent(
			roomID, subResult.SubstituteUserID, subResult.SeatNo,
			subResult.Player.Nickname, subResult.Player.Avatar)); err != nil {
			logger.Warn("publish substitute event failed",
				"room_id", roomID,
				"user_id", subResult.SubstituteUserID,
				"trace_id", trace.FromContext(ctx),
				"error", err)
		}
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, roomID, message.PushSubstitute, &push.SubstitutePush{
			RoomID: roomID,
			UserID: subResult.SubstituteUserID,
			SeatNo: int32(subResult.SeatNo),
		}, "")

		stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
		if stateData != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushRoomState, BuildFullRoomState(stateData), "")
		}
	}

	s.handleCountdownAfterSeat(ctx, roomID, subResult.ShouldStartCountdown,
		subResult.CountdownEndTime, subResult.CurrentRound)

	logger.Info("auto substitute success",
		"room_id", roomID,
		"seat_no", subResult.SeatNo,
		"substitute_user_id", subResult.SubstituteUserID,
		"player_count", subResult.PlayerCount,
	)

	return subResult
}

// EnqueueRequest 排队入队请求
type EnqueueRequest struct {
	RoomID string
	UserID string
}

// EnqueueResult 排队入队结果
type EnqueueResult struct {
	QueuePosition int        `json:"queue_position"`
	RoomState     *RoomState `json:"room_state"`
}

// Enqueue 真实玩家加入排队队列
func (s *RoomAppService) Enqueue(ctx context.Context, req *EnqueueRequest) (*EnqueueResult, error) {
	position, err := s.repo.Enqueue(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, err
	}

	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)
	nickname := ""
	avatar := ""
	if spectator != nil {
		nickname = spectator.Nickname
		avatar = spectator.Avatar
	}

	if s.publisher != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewQueueJoinEvent(req.RoomID, req.UserID, position, nickname, avatar)); err != nil {
			logger.Warn("publish queue_join event failed",
				"room_id", req.RoomID,
				"user_id", req.UserID,
				"trace_id", trace.FromContext(ctx),
				"error", err)
		}
	}

	var roomState *RoomState
	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			roomState = BuildFullRoomState(stateData)
			s.broadcaster.Broadcast(ctx, req.RoomID, message.PushRoomState, roomState, req.UserID)
		}
	}

	logger.Info("user enqueued",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"position", position,
	)

	return &EnqueueResult{
		QueuePosition: position,
		RoomState:     roomState,
	}, nil
}

// DequeueRequest 排队出队请求
type DequeueRequest struct {
	RoomID string
	UserID string
}

// DequeueResult 排队出队结果
type DequeueResult struct {
	RoomState *RoomState `json:"room_state"`
}

// Dequeue 从排队队列移除
func (s *RoomAppService) Dequeue(ctx context.Context, req *DequeueRequest) (*DequeueResult, error) {
	if err := s.repo.Dequeue(ctx, req.RoomID, req.UserID); err != nil {
		return nil, err
	}

	if s.publisher != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewQueueLeaveEvent(req.RoomID, req.UserID, "user_cancel")); err != nil {
			logger.Warn("publish queue_leave event failed",
				"room_id", req.RoomID,
				"user_id", req.UserID,
				"trace_id", trace.FromContext(ctx),
				"error", err)
		}
	}

	var roomState *RoomState
	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			roomState = BuildFullRoomState(stateData)
			s.broadcaster.Broadcast(ctx, req.RoomID, message.PushRoomState, roomState, req.UserID)
		}
	}

	logger.Info("user dequeued",
		"room_id", req.RoomID,
		"user_id", req.UserID,
	)

	return &DequeueResult{
		RoomState: roomState,
	}, nil
}

// TryAutoSubstitute 暴露给其他应用服务调用的替补入口
func (s *RoomAppService) TryAutoSubstitute(ctx context.Context, roomID string, seatNo int) *repository.SubstituteResult {
	return s.tryAutoSubstitute(ctx, roomID, seatNo)
}

func (s *RoomAppService) AutoMatchAndJoin(ctx context.Context, req *AutoMatchRequest) (*JoinRoomResult, error) {
	userInfo, err := s.userService.GetUserById(ctx, req.UserID)
	if err != nil {
		return nil, message.NewError(message.CodeUserNotFound)
	}

	balance, _, err := s.settleAppService.CheckBalance(ctx, userInfo.ID, 0)
	if err != nil {
		logger.Error("failed to check balance", "user_id", req.UserID, "error", err)
		return nil, message.NewError(message.CodePlatformAPIError)
	}

	roomID, err := s.dbRepo.RoomDBRepo().MatchRoomByBalance(ctx, balance)
	if err != nil {
		return nil, err
	}

	return s.JoinAndAutoSeat(ctx, &JoinRoomRequest{
		RoomID: roomID,
		UserID: req.UserID,
	})
}

type LeaveRoomRequest struct {
	RoomID string
	UserID string
	Reason string
}

type LeaveRoomResult struct{}

func (s *RoomAppService) LeaveRoom(ctx context.Context, req *LeaveRoomRequest) (*LeaveRoomResult, error) {
	// 先判断离开者是否为玩家（拥有座位），以便离开后触发自动替补
	player, _ := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
	var freedSeatNo int
	if player != nil {
		freedSeatNo = player.SeatNo
	}

	// 从排队队列中移除（若存在，best-effort）
	s.repo.RemoveFromQueue(ctx, req.RoomID, req.UserID)

	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)

	err := s.repo.LeaveRoom(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, err
	}

	if s.scheduler != nil {
		s.scheduler.ClearAllUserTimeouts(ctx, req.RoomID, req.UserID)
	}

	if spectator != nil {
		if s.publisher != nil {
			if err := s.publisher.PublishRoomEvent(ctx, events.NewSpectatorLeaveEvent(req.RoomID, req.UserID, req.Reason)); err != nil {
				logger.Warn("publish spectator_leave event failed",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"trace_id", trace.FromContext(ctx),
					"error", err)
			}
		}
	}

	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			s.broadcaster.Broadcast(ctx, req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
		}
	}

	// 玩家离开释放座位 → 尝试从排队队列自动替补
	if freedSeatNo > 0 {
		if err := s.taskRunner.Submit("auto_substitute_on_leave", 10*time.Second, func(ctx context.Context) {
			s.tryAutoSubstitute(ctx, req.RoomID, freedSeatNo)
		}); err != nil {
			logger.Warn("submit auto_substitute_on_leave task failed", "error", err)
		}
	}

	logger.Info("user left room",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"reason", req.Reason,
		"freed_seat_no", freedSeatNo,
	)

	return &LeaveRoomResult{}, nil
}

type GetRoomStateRequest struct {
	RoomID string
	UserID string
}

type GetRoomStateResult struct {
	RoomState *RoomState
}

func (s *RoomAppService) GetRoomState(ctx context.Context, req *GetRoomStateRequest) (*GetRoomStateResult, error) {
	stateData, err := s.repo.GetRoomStateData(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	return &GetRoomStateResult{
		RoomState: BuildFullRoomState(stateData),
	}, nil
}

type ReconnectRequest struct {
	RoomID string
	UserID string
}

type ReconnectResult struct {
	RoomID    string
	RoomState *RoomState
}

func (s *RoomAppService) HandleReconnect(ctx context.Context, req *ReconnectRequest) (*ReconnectResult, error) {
	player, _ := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)

	if player == nil && spectator == nil {
		return nil, message.NewError(message.CodeNotInRoom)
	}

	var nickname string
	var seatNo int
	if player != nil {
		nickname = player.Nickname
		seatNo = player.SeatNo
		if s.publisher != nil {
			if err := s.publisher.PublishRoomEvent(ctx, events.NewPlayerReconnectEvent(req.RoomID, req.UserID, player.SeatNo)); err != nil {
				logger.Warn("publish player_reconnect event failed",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"trace_id", trace.FromContext(ctx),
					"error", err)
			}
		}
	} else if spectator != nil {
		nickname = spectator.Nickname
		seatNo = 0
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, req.RoomID, message.PushPlayerReconnected, &push.PlayerReconnectedPush{
			UserID:   req.UserID,
			Nickname: nickname,
			SeatNo:   seatNo,
		}, req.UserID)
	}

	stateData, err := s.repo.GetRoomStateData(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	logger.Info("user reconnected",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"seat_no", seatNo,
		"is_player", player != nil,
	)

	return &ReconnectResult{
		RoomID:    req.RoomID,
		RoomState: BuildFullRoomState(stateData),
	}, nil
}

func (s *RoomAppService) BroadcastToUser(ctx context.Context, userID string, msgType string, data interface{}) {
	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(ctx, userID, msgType, data)
	}
}

func (s *RoomAppService) GetRoomMeta(ctx context.Context, roomID string) (*roomDom.RoomMeta, error) {
	return s.repo.GetRoomMeta(ctx, roomID)
}

func (s *RoomAppService) GetPlayer(ctx context.Context, roomID, userID string) (*roomDom.Player, error) {
	return s.repo.GetPlayer(ctx, roomID, userID)
}

func (s *RoomAppService) GetSpectator(ctx context.Context, roomID, userID string) (*roomDom.Spectator, error) {
	return s.repo.GetSpectator(ctx, roomID, userID)
}

type RoomListItem struct {
	RoomID         string         `json:"room_id"`
	RoomNo         string         `json:"room_no"`
	RoomFee        currency.Money `json:"room_fee"`
	MaxPlayers     int            `json:"max_players"`
	MaxRounds      int            `json:"max_rounds"`
	MaxSpectators  int            `json:"max_spectators"`
	CurrentRound   int            `json:"current_round"`
	PlayerCount    int            `json:"player_count"`
	SpectatorCount int            `json:"spectator_count"`
	Status         int            `json:"status"`
	Seats          []*SeatInfo    `json:"seats"`
}

func (s *RoomAppService) GetRoomList(ctx context.Context, roomType, status, page, pageSize int) ([]*RoomListItem, int) {
	rooms, err := s.dbRepo.RoomDBRepo().GetRoomList(ctx, roomType, status, page, pageSize)
	if err != nil {
		logger.Error("failed to get room list", "error", err)
		return []*RoomListItem{}, 0
	}

	total, err := s.dbRepo.RoomDBRepo().GetRoomCount(ctx, roomType, status)
	if err != nil {
		logger.Error("failed to get room count", "error", err)
	}

	roomIDs := make([]string, 0, len(rooms))
	for _, r := range rooms {
		roomIDs = append(roomIDs, fmt.Sprintf("%d", r.RoomID))
	}

	seatsData, err := s.repo.GetRoomSeatsBatch(ctx, roomIDs)
	if err != nil {
		logger.Error("failed to get room seats batch", "error", err)
	}

	items := make([]*RoomListItem, 0, len(rooms))
	for _, r := range rooms {
		roomID := fmt.Sprintf("%d", r.RoomID)
		var seats []*SeatInfo
		var currentRound int
		var status int
		if stateData := seatsData[roomID]; stateData != nil {
			stateData.MaxPlayers = r.MaxPlayers
			seats = BuildFullRoomState(stateData).Seats
			currentRound = stateData.CurrentRound
			status = stateData.Status
		} else {
			status = int(r.Status)
		}
		item := &RoomListItem{
			RoomID:         roomID,
			RoomNo:         r.RoomNo,
			RoomFee:        currency.NewMoneyFromFen(r.RoomFee),
			MaxPlayers:     r.MaxPlayers,
			MaxRounds:      r.MaxRounds,
			MaxSpectators:  r.MaxSpectators,
			CurrentRound:   currentRound,
			PlayerCount:    r.PlayerCount,
			SpectatorCount: r.SpectatorCount,
			Status:         status,
			Seats:          seats,
		}
		items = append(items, item)
	}

	return items, int(total)
}

func (s *RoomAppService) GetRoomDetail(ctx context.Context, roomID string) (*RoomState, error) {
	stateData, err := s.repo.GetRoomStateData(ctx, roomID)
	if err != nil {
		return nil, err
	}
	return BuildFullRoomState(stateData), nil
}

func (s *RoomAppService) GetRoomTypeList(ctx context.Context) ([]*repository.RoomTypeItem, error) {
	items, err := s.dbRepo.RoomConfigDBRepo().GetRoomTypeList(ctx)
	if err != nil {
		logger.Error("failed to get room type list", "error", err)
		return nil, err
	}
	return items, nil
}
