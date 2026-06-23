package application

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/scheduler"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type RoomAppService struct {
	repo              domain.RoomRepository
	dbRepo            domain.DBRepository
	userService       *UserService
	broadcaster       domain.Broadcaster
	publisher         domain.EventPublisher
	scheduler         *scheduler.TimeoutScheduler
	settlementService *settlementService.SettlementService
}

func NewRoomAppService(
	repo domain.RoomRepository,
	dbRepo domain.DBRepository,
	userService *UserService,
	broadcaster domain.Broadcaster,
	publisher domain.EventPublisher,
	scheduler *scheduler.TimeoutScheduler,
	settlementSvc *settlementService.SettlementService,
) *RoomAppService {
	return &RoomAppService{
		repo:              repo,
		dbRepo:            dbRepo,
		userService:       userService,
		broadcaster:       broadcaster,
		publisher:         publisher,
		scheduler:         scheduler,
		settlementService: settlementSvc,
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
	SeatNo      int
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

		roomMeta := &domain.RoomMeta{
			RoomID:           req.RoomID,
			RoomNo:           room.RoomNo,
			ConfigID:         room.ConfigID,
			ConfigName:       room.ConfigName,
			RoomFee:          room.RoomFee,
			MaxPlayers:       room.MaxPlayers,
			MaxRounds:        room.MaxRounds,
			MaxSpectators:    room.MaxSpectators,
			Status:           domain.RoomStatus(room.Status),
			CurrentRound:     room.CurrentRound,
			CurrentSessionID: fmt.Sprintf("%d", room.CurrentSessionID),
		}

		if initErr := s.repo.InitRoom(ctx, roomMeta); initErr != nil {
			logger.Error("failed to init room in redis", "room_id", req.RoomID, "error", initErr)
			return nil, message.NewError(message.CodeSystemError)
		}
	}

	spectator := &domain.Spectator{
		UserID:   req.UserID,
		Nickname: userInfo.Nickname,
		Avatar:   userInfo.Avatar,
		IsRobot:  userInfo.IsRobot,
	}

	// 检查余额是否充足
	balanceSufficient := true
	if s.settlementService != nil {
		roomMeta, _ := s.repo.GetRoomMeta(ctx, req.RoomID)
		if roomMeta != nil {
			balance, isSufficient, err := s.settlementService.CheckBalance(ctx, userInfo.ID, roomMeta.RoomFee)
			if err != nil || !isSufficient || balance < roomMeta.RoomFee {
				balanceSufficient = false
			}
		}
	}

	// 余额不足时仅成为观战者
	if !balanceSufficient {
		result, err := s.repo.JoinAsSpectator(ctx, req.RoomID, spectator)
		if err != nil {
			return nil, err
		}

		if s.publisher != nil {
			s.publisher.Publish(ctx, domain.NewSpectatorJoinEvent(result.RoomID, req.UserID, userInfo.Nickname, userInfo.Avatar))
		}

		stateData, _ := s.repo.GetRoomStateData(ctx, result.RoomID)
		if s.broadcaster != nil && stateData != nil {
			s.broadcaster.Broadcast(result.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
		}

		return &JoinRoomResult{
			RoomID:      result.RoomID,
			RoomNo:      result.RoomNo,
			RoomState:   BuildFullRoomState(stateData),
			IsSpectator: true,
			SeatNo:      0,
		}, nil
	}

	// 余额充足，尝试自动上座
	autoSeatResult, err := s.repo.JoinAndAutoSeat(ctx, req.RoomID, spectator, userInfo.IsRobot)
	if err != nil {
		return nil, err
	}

	roomID := autoSeatResult.RoomID
	roomNo := autoSeatResult.RoomNo
	isSpectator := autoSeatResult.IsSpectator
	seatNo := autoSeatResult.SeatNo

	// 发布事件
	if s.publisher != nil {
		if isSpectator {
			s.publisher.Publish(ctx, domain.NewSpectatorJoinEvent(roomID, req.UserID, userInfo.Nickname, userInfo.Avatar))
		} else {
			// 自动上座成功，发布选座和准备事件
			s.publisher.Publish(ctx, domain.NewSeatSelectEvent(roomID, req.UserID, seatNo, userInfo.Nickname, userInfo.Avatar))
			s.publisher.Publish(ctx, domain.NewPlayerReadyEvent(roomID, req.UserID, seatNo, userInfo.Nickname, userInfo.Avatar))
		}
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, roomID)

	if s.broadcaster != nil && stateData != nil {
		s.broadcaster.Broadcast(roomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
	}

	logger.Info("user joined room",
		"room_id", roomID,
		"room_no", roomNo,
		"user_id", req.UserID,
		"is_spectator", isSpectator,
		"seat_no", seatNo,
	)

	return &JoinRoomResult{
		RoomID:      roomID,
		RoomNo:      roomNo,
		RoomState:   BuildFullRoomState(stateData),
		IsSpectator: isSpectator,
		SeatNo:      seatNo,
	}, nil
}

func (s *RoomAppService) AutoMatchAndJoin(ctx context.Context, req *AutoMatchRequest) (*JoinRoomResult, error) {
	userInfo, err := s.userService.GetUserById(ctx, req.UserID)
	if err != nil {
		return nil, message.NewError(message.CodeUserNotFound)
	}

	balance, _, err := s.settlementService.CheckBalance(ctx, userInfo.ID, 0)
	if err != nil {
		logger.Error("failed to check balance", "user_id", req.UserID, "error", err)
		return nil, message.NewError(message.CodePlatformAPIError)
	}

	roomID, err := s.dbRepo.RoomDBRepo().MatchRoomByBalance(ctx, balance)
	if err != nil {
		return nil, err
	}

	return s.JoinRoom(ctx, &JoinRoomRequest{
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
	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)

	leaveResult, err := s.repo.LeaveRoom(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, err
	}

	if s.scheduler != nil {
		s.scheduler.ClearAllUserTimeouts(ctx, req.RoomID, req.UserID)
	}

	// 从排队队列中移除该用户
	_ = s.repo.RemoveFromQueue(ctx, req.RoomID, req.UserID)

	if spectator != nil {
		if s.publisher != nil {
			s.publisher.Publish(ctx, domain.NewSpectatorLeaveEvent(req.RoomID, req.UserID, req.Reason))
		}
	}

	// 如果释放了座位，触发排队替补
	releasedSeatNo := 0
	if leaveResult != nil {
		releasedSeatNo = leaveResult.SeatNo
	}
	if releasedSeatNo > 0 {
		s.triggerAutoSubstitute(ctx, req.RoomID, releasedSeatNo)
	}

	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
		}
	}

	logger.Info("user left room",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"reason", req.Reason,
		"released_seat_no", releasedSeatNo,
	)

	return &LeaveRoomResult{}, nil
}

// triggerAutoSubstitute 触发排队替补逻辑
func (s *RoomAppService) triggerAutoSubstitute(ctx context.Context, roomID string, seatNo int) {
	for {
		subResult, err := s.repo.AutoSubstitute(ctx, roomID, seatNo)
		if err != nil {
			logger.Error("auto substitute failed", "room_id", roomID, "seat_no", seatNo, "error", err)
			return
		}
		if !subResult.Success {
			// 队列为空，无需替补
			return
		}

		// 替补成功后检查余额
		if s.settlementService != nil {
			meta, metaErr := s.repo.GetRoomMeta(ctx, roomID)
			if metaErr == nil && meta != nil {
				userIDInt, _ := strconv.ParseInt(subResult.UserID, 10, 64)
				balance, isSufficient, balanceErr := s.settlementService.CheckBalance(ctx, userIDInt, meta.RoomFee)
				if balanceErr != nil || !isSufficient || balance < meta.RoomFee {
					// 余额不足，撤销替补，通知用户，继续下一位
					logger.Warn("substitute user insufficient balance, skipping",
						"room_id", roomID,
						"user_id", subResult.UserID,
					)
					// 撤销替补：取消座位
					s.repo.CancelSeat(ctx, roomID, subResult.UserID)
					// 通知用户余额不足
					if s.broadcaster != nil {
						s.broadcaster.BroadcastToUser(subResult.UserID, message.PushError, &message.ErrorPush{
							Code: message.CodeInsufficientBalance,
							Msg:  message.GetErrorMsg(message.CodeInsufficientBalance),
						})
					}
					// 继续替补下一位
					continue
				}
			}
		}

		// 替补成功，发布替补事件
		if s.publisher != nil {
			s.publisher.Publish(ctx, domain.NewSubstituteEvent(roomID, subResult.UserID, seatNo, subResult.Nickname, subResult.Avatar))
		}

		// 推送替补事件给替补者
		if s.broadcaster != nil {
			s.broadcaster.BroadcastToUser(subResult.UserID, message.PushSubstitute, &message.SubstitutePush{
				RoomID:   roomID,
				SeatNo:   seatNo,
				Nickname: subResult.Nickname,
				Avatar:   subResult.Avatar,
			})
		}

		logger.Info("auto substitute success",
			"room_id", roomID,
			"seat_no", seatNo,
			"user_id", subResult.UserID,
		)
		return
	}
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
			s.publisher.Publish(ctx, domain.NewPlayerReconnectEvent(req.RoomID, req.UserID, player.SeatNo))
		}
	} else if spectator != nil {
		nickname = spectator.Nickname
		seatNo = 0
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(req.RoomID, message.PushPlayerReconnected, &message.PlayerReconnectedPush{
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
		s.broadcaster.BroadcastToUser(userID, msgType, data)
	}
}

func (s *RoomAppService) GetRoomMeta(ctx context.Context, roomID string) (*domain.RoomMeta, error) {
	return s.repo.GetRoomMeta(ctx, roomID)
}

func (s *RoomAppService) GetPlayer(ctx context.Context, roomID, userID string) (*domain.Player, error) {
	return s.repo.GetPlayer(ctx, roomID, userID)
}

func (s *RoomAppService) GetSpectator(ctx context.Context, roomID, userID string) (*domain.Spectator, error) {
	return s.repo.GetSpectator(ctx, roomID, userID)
}

type RoomListItem struct {
	RoomID         string      `json:"room_id"`
	RoomNo         string      `json:"room_no"`
	RoomFee        currency.Money `json:"room_fee"`
	MaxPlayers     int         `json:"max_players"`
	MaxRounds      int         `json:"max_rounds"`
	MaxSpectators  int         `json:"max_spectators"`
	CurrentRound   int         `json:"current_round"`
	PlayerCount    int         `json:"player_count"`
	SpectatorCount int         `json:"spectator_count"`
	Status         int         `json:"status"`
	Seats          []*SeatInfo `json:"seats"`
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

func (s *RoomAppService) GetRoomTypeList(ctx context.Context) ([]*domain.RoomTypeItem, error) {
	items, err := s.dbRepo.RoomConfigDBRepo().GetRoomTypeList(ctx)
	if err != nil {
		logger.Error("failed to get room type list", "error", err)
		return nil, err
	}
	return items, nil
}

type EnqueueRequest struct {
	RoomID string
	UserID string
}

type EnqueueResult struct {
	Position int64 `json:"position"`
}

func (s *RoomAppService) Enqueue(ctx context.Context, req *EnqueueRequest) (*EnqueueResult, error) {
	spectator, err := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, message.NewError(message.CodeNotInRoom)
	}

	// 只有纯观战者(seat_no=0)才能排队
	if spectator.SeatNo > 0 {
		return nil, message.NewError(message.CodeAlreadySeated)
	}

	result, err := s.repo.Enqueue(ctx, req.RoomID, req.UserID, spectator.Nickname, spectator.Avatar)
	if err != nil {
		return nil, err
	}

	if s.publisher != nil {
		s.publisher.Publish(ctx, domain.NewEnqueueEvent(req.RoomID, req.UserID, result.Position))
	}

	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
		}
	}

	logger.Info("user enqueued",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"position", result.Position,
	)

	return &EnqueueResult{
		Position: result.Position,
	}, nil
}

type DequeueRequest struct {
	RoomID string
	UserID string
}

type DequeueResult struct{}

func (s *RoomAppService) Dequeue(ctx context.Context, req *DequeueRequest) (*DequeueResult, error) {
	err := s.repo.Dequeue(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, err
	}

	if s.publisher != nil {
		s.publisher.Publish(ctx, domain.NewDequeueEvent(req.RoomID, req.UserID))
	}

	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
		}
	}

	logger.Info("user dequeued",
		"room_id", req.RoomID,
		"user_id", req.UserID,
	)

	return &DequeueResult{}, nil
}
