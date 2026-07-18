package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/i18n"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/common/trace"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/push"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
	"github.com/cashparty/backend/settlement/dto"
)

type SeatAppService struct {
	repo                 repository.RoomRepository
	dbRepo               repository.DBRepository
	broadcaster          events.Broadcaster
	publisher            events.RoomEventPublisher
	scheduler            *scheduler.TimeoutScheduler
	settleAppService     *settlementApplication.SettleAppService
	gameService          *GameAppService
	roomAppService       *RoomAppService
	redis                cRedis.RedisClient
	readyCountdown       time.Duration
	taskRunner           async.TaskRunner
	deductFailureHandler DeductFailureHandler
	robotChecker         RobotChecker
}

// SetRoomAppService 注入 RoomAppService（用于 CancelSeat 后触发自动替补）
func (s *SeatAppService) SetRoomAppService(svc *RoomAppService) {
	s.roomAppService = svc
}

// SetDeductFailureHandler 注入扣款失败处理器（GameLifecycleService），
// 用于观众补位替补费扣款失败时结束游戏（与 PacketOrchestrator 模式一致）。
func (s *SeatAppService) SetDeductFailureHandler(h DeductFailureHandler) {
	s.deductFailureHandler = h
}

// SetRobotChecker 注入机器人身份识别器（基于 Redis SISMEMBER，O(1) 查询）。
// 用于观众补位时短路机器人：机器人不扣替补费，无 platform API 调用，无入账资格要求。
func (s *SeatAppService) SetRobotChecker(rc RobotChecker) {
	s.robotChecker = rc
}

func NewSeatAppService(
	repo repository.RoomRepository,
	dbRepo repository.DBRepository,
	broadcaster events.Broadcaster,
	publisher events.RoomEventPublisher,
	scheduler *scheduler.TimeoutScheduler,
	settleAppSvc *settlementApplication.SettleAppService,
	gameService *GameAppService,
	redis cRedis.RedisClient,
	readyCountdown time.Duration,
	taskRunner async.TaskRunner,
) *SeatAppService {
	return &SeatAppService{
		repo:             repo,
		dbRepo:           dbRepo,
		broadcaster:      broadcaster,
		publisher:        publisher,
		scheduler:        scheduler,
		settleAppService: settleAppSvc,
		gameService:      gameService,
		redis:            redis,
		readyCountdown:   readyCountdown,
		taskRunner:       taskRunner,
	}
}

type SelectSeatRequest struct {
	RoomID string
	UserID string
	SeatNo int
}

type SelectSeatResult struct {
	SeatNo    int
	RoomState *RoomState
}

func (s *SeatAppService) SelectSeat(ctx context.Context, req *SelectSeatRequest) (*SelectSeatResult, error) {
	meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	if req.SeatNo < 1 || req.SeatNo > meta.MaxPlayers {
		return nil, message.NewError(message.CodeInvalidSeatNo)
	}

	isRobot := false
	if user, userErr := s.dbRepo.UserDBRepo().GetUserById(ctx, req.UserID); userErr == nil && user != nil {
		isRobot = user.IsRobot
	}

	err = s.repo.SelectSeat(ctx, req.RoomID, req.UserID, req.SeatNo, isRobot)
	if err != nil {
		return nil, err
	}

	if s.scheduler != nil {
		s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSeat, req.RoomID, req.UserID)
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)

	if s.broadcaster != nil && stateData != nil {
		s.broadcaster.Broadcast(ctx, req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
	}

	logger.Info("seat selected",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"seat_no", req.SeatNo,
	)

	return &SelectSeatResult{
		SeatNo:    req.SeatNo,
		RoomState: BuildFullRoomState(stateData),
	}, nil
}

type CancelSeatRequest struct {
	RoomID string
	UserID string
}

type CancelSeatResult struct {
	RoomState *RoomState
}

func (s *SeatAppService) CancelSeat(ctx context.Context, req *CancelSeatRequest) (*CancelSeatResult, error) {
	meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	if meta.Status != room.RoomStatusWaiting {
		return nil, message.NewError(message.CodeGameInProgress)
	}

	// 记录释放前的座位号，用于后续自动替补
	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)
	freedSeatNo := 0
	if spectator != nil {
		freedSeatNo = spectator.SeatNo
	}

	err = s.repo.CancelSeat(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, err
	}

	if s.scheduler != nil {
		s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSeat, req.RoomID, req.UserID)
	}

	nickname := ""
	if spectator != nil {
		nickname = spectator.Nickname
	}

	if s.publisher != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewSeatCancelEvent(req.RoomID, req.UserID, freedSeatNo, nickname)); err != nil {
			logger.Warn("publish seat_cancel event failed",
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

	// 座位释放后从排队队列自动替补
	if freedSeatNo > 0 && s.roomAppService != nil {
		if err := s.taskRunner.Submit("auto_substitute_on_cancel", 10*time.Second, func(ctx context.Context) {
			s.roomAppService.TryAutoSubstitute(ctx, req.RoomID, freedSeatNo)
		}); err != nil {
			logger.Warn("submit auto_substitute_on_cancel task failed", "error", err)
		}
	}

	logger.Info("seat cancelled",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"freed_seat_no", freedSeatNo,
	)

	return &CancelSeatResult{
		RoomState: roomState,
	}, nil
}

type SetReadyRequest struct {
	RoomID string
	UserID string
}

type SetReadyResult struct {
	RoomState *RoomState
}

func (s *SeatAppService) SetReady(ctx context.Context, req *SetReadyRequest) (*SetReadyResult, error) {
	meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	if s.settleAppService != nil {
		userIDInt := converter.ParseID(req.UserID)
		balanceResult, err := s.settleAppService.CheckBalanceForReady(ctx, &dto.BalanceCheckRequest{
			UserID:     userIDInt,
			RoomFee:    meta.RoomFee,
			MaxPlayers: meta.MaxPlayers,
			MaxRounds:  meta.MaxRounds,
		})

		if err != nil {
			logger.Error("check balance failed", "error", err)
			return nil, message.NewError(message.CodeSystemBusy)
		}

		if !balanceResult.IsSufficient {
			return nil, message.NewErrorWithMsg(message.CodeInsufficientBalance, i18n.GetInsufficientBalanceMsg(balanceResult.RequiredFee, balanceResult.Balance))
		}
	}

	roomHashKey := rediskeys.RoomHashKey(req.RoomID)
	playersKey := rediskeys.RoomPlayersKey(req.RoomID)
	spectatorsKey := rediskeys.RoomSpectatorsKey(req.RoomID)
	now := time.Now().Unix()

	result, err := scripts.PlayerReady.Run(ctx, s.redis,
		[]string{roomHashKey, playersKey, spectatorsKey}, req.UserID, now).Slice()

	if err != nil {
		logger.Error("failed to execute player ready lua script", "error", err)
		return nil, message.NewError(message.CodeSystemBusy)
	}

	if len(result) < 8 {
		return nil, message.NewError(message.CodeSystemError)
	}

	code := converter.ParseInt(result[0])
	if code != 0 {
		luaErr := domain.MapLuaError(code)
		logger.Warn("player ready failed", "lua_code", code, "room_id", req.RoomID, "user_id", req.UserID)
		return nil, luaErr
	}

	if s.scheduler != nil {
		s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSeat, req.RoomID, req.UserID)
	}

	playerCount := converter.ParseInt(result[1])
	shouldStartCountdown := converter.ParseInt(result[3])
	countdownEndTime := converter.ParseInt64(result[4])
	currentRound := converter.ParseInt(result[5])
	playerDataStr := converter.ParseString(result[6])

	// 解析 player 信息（用于事件发布）
	var player struct {
		Nickname string `json:"nickname"`
		Avatar   string `json:"avatar"`
		SeatNo   int    `json:"seat_no"`
	}
	json.Unmarshal([]byte(playerDataStr), &player)

	// 游戏进行中（currentRound > 0）：观众补位，扣替补费 + 发 Substitute 事件（消费者创建 snapshot）。
	// 游戏未开始（currentRound == 0）：正常入座，发 PlayerReady 事件（仅同步计数）。
	// 扣款失败则结束游戏（与 PacketOrchestrator 扣款失败处理一致）。
	// 机器人不扣替补费（机器人无 platform API 调用，无入账资格要求），通过 robotChecker 短路。
	isMidRoundSubstitute := currentRound > 0
	if isMidRoundSubstitute && s.robotChecker != nil {
		// fail-closed：robotChecker 查询失败视为无法识别身份，按扣款失败处理结束游戏，
		// 避免机器人误走真人扣款流程或真人误走机器人虚拟钱包路径。
		isRobot, robotErr := s.robotChecker.IsRobot(ctx, converter.ParseID(req.UserID))
		if robotErr != nil {
			logger.Error("set ready: check robot failed, ending game",
				"room_id", req.RoomID,
				"user_id", req.UserID,
				"error", robotErr)
			if s.deductFailureHandler != nil {
				s.deductFailureHandler.HandleDeductFailure(ctx, req.RoomID, meta,
					message.ReasonSubstituteFeeDeductFailed, robotErr)
			}
			return nil, message.NewErrorWithMsg(message.CodeSystemError,
				i18n.GetInterruptMessage(message.ReasonSubstituteFeeDeductFailed))
		}
		if isRobot {
			// 机器人补位：跳过替补费扣款，直接发 Substitute 事件
			logger.Info("set ready: robot substitute, skip substitute fee",
				"room_id", req.RoomID,
				"user_id", req.UserID)
		} else if s.settleAppService != nil {
			substituteFee := calculateSubstituteFee(meta.RoomFee)
			if substituteFee > 0 {
				deductReq := &dto.SubstituteFeeDeductRequest{
					RoomID:    converter.ParseID(req.RoomID),
					SessionID: converter.ParseID(meta.CurrentSessionID),
					UserID:    converter.ParseID(req.UserID),
					RoundID:   converter.ParseID(meta.CurrentRoundID),
					RoundNo:   currentRound,
					Amount:    substituteFee,
				}
				if err := s.settleAppService.DeductSubstituteFee(ctx, deductReq); err != nil {
					logger.Error("set ready: deduct substitute fee failed, ending game",
						"room_id", req.RoomID,
						"user_id", req.UserID,
						"amount", substituteFee,
						"error", err)
					if s.deductFailureHandler != nil {
						s.deductFailureHandler.HandleDeductFailure(ctx, req.RoomID, meta,
							message.ReasonSubstituteFeeDeductFailed, err)
					}
					return nil, message.NewErrorWithMsg(message.CodeSystemError,
						i18n.GetInterruptMessage(message.ReasonSubstituteFeeDeductFailed))
				}
				logger.Info("set ready: substitute fee deducted",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"amount", substituteFee)
			}
		}
	}

	if s.publisher != nil {
		traceID := trace.FromContext(ctx)
		if isMidRoundSubstitute {
			// 游戏进行中：发 Substitute 事件（消费者创建 snapshot + session_players）
			if err := s.publisher.PublishRoomEvent(ctx, events.NewSubstituteEvent(
				req.RoomID, req.UserID, player.SeatNo,
				player.Nickname, player.Avatar, traceID, "spectator")); err != nil {
				logger.Warn("publish substitute event failed",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"trace_id", traceID,
					"error", err)
			}
		} else {
			// 游戏未开始：发 PlayerReady 事件（仅同步计数）
			if err := s.publisher.PublishRoomEvent(ctx, events.NewPlayerReadyEvent(
				req.RoomID, req.UserID, player.SeatNo, player.Nickname, player.Avatar)); err != nil {
				logger.Warn("publish player_ready event failed",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"trace_id", traceID,
					"error", err)
			}
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

	if shouldStartCountdown == 1 {
		if s.broadcaster != nil {
			s.broadcaster.Broadcast(ctx, req.RoomID, message.PushCountdownStart, &push.CountdownStartPush{
				RoomID:    req.RoomID,
				Countdown: int32(s.readyCountdown.Seconds()),
			}, "")
		}

		if s.scheduler != nil {
			s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeReady, req.RoomID,
				converter.FormatID(countdownEndTime))
		}

		logger.Info("game countdown started",
			"room_id", req.RoomID,
			"countdown_end_time", countdownEndTime,
		)
	} else if shouldStartCountdown == 2 {
		if s.gameService != nil {
			if err := s.taskRunner.Submit("resume_game_on_ready", 10*time.Second, func(ctx context.Context) {
				s.gameService.ResumeGame(ctx, &ResumeGameRequest{
					RoomID:       req.RoomID,
					CurrentRound: currentRound,
				})
			}); err != nil {
				logger.Warn("submit resume_game_on_ready task failed", "error", err)
			}
		}

		logger.Info("game resume triggered",
			"room_id", req.RoomID,
			"current_round", currentRound,
		)
	}

	logger.Info("player ready",
		"user_id", req.UserID,
		"room_id", req.RoomID,
		"player_count", playerCount,
	)

	return &SetReadyResult{
		RoomState: roomState,
	}, nil
}

func (s *SeatAppService) HandleSeatTimeout(ctx context.Context, roomID, userID string) {
	roomHashKey := rediskeys.RoomHashKey(roomID)
	playersKey := rediskeys.RoomPlayersKey(roomID)
	spectatorsKey := rediskeys.RoomSpectatorsKey(roomID)
	seatsKey := rediskeys.RoomSeatsKey(roomID)
	seatOwnerKey := rediskeys.RoomSeatOwnerKey(roomID)
	userRoomKey := rediskeys.PlayerRoomKey(userID)

	result, err := scripts.HandleSeatTimeout.Run(ctx, s.redis,
		[]string{roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey, userRoomKey},
		userID).Slice()

	if err != nil {
		logger.Error("failed to handle seat timeout",
			"room_id", roomID,
			"user_id", userID,
			"error", err)
		return
	}

	if len(result) < 3 {
		logger.Warn("invalid result from seat timeout lua script",
			"room_id", roomID,
			"user_id", userID)
		return
	}

	code := converter.ParseInt(result[0])
	seatNo := converter.ParseInt(result[1])
	resultMsg := converter.ParseString(result[2])

	if code == 2 {
		logger.Info("seat timeout skipped, user already became player",
			"room_id", roomID,
			"user_id", userID,
			"seat_no", seatNo)
		return
	}

	if code != 0 {
		logger.Warn("seat timeout failed",
			"room_id", roomID,
			"user_id", userID,
			"message", resultMsg)
		return
	}

	if s.publisher != nil {
		if err := s.publisher.PublishRoomEvent(ctx, events.NewSpectatorKickEvent(roomID, userID, seatNo, message.ReasonSeatTimeout)); err != nil {
			logger.Warn("publish spectator_kick event failed",
				"room_id", roomID,
				"user_id", userID,
				"trace_id", trace.FromContext(ctx),
				"error", err)
		}
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(ctx, userID, message.PushKicked, &push.KickedPush{
			RoomID:  roomID,
			UserID:  userID,
			Reason:  message.ReasonSeatTimeout,
			Message: i18n.GetKickMessage(message.ReasonSeatTimeout),
		})

		stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
		if stateData != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushRoomState, BuildFullRoomState(stateData), userID)
		}
	}

	logger.Info("seat timeout, user kicked from room",
		"room_id", roomID,
		"user_id", userID,
		"seat_no", seatNo,
	)
}

func (s *SeatAppService) HandleReadyTimeout(ctx context.Context, roomID string, data string) {
	logger.Info("countdown end triggered",
		"room_id", roomID,
		"countdown_end_time", data,
	)

	if s.gameService != nil {
		s.gameService.StartGame(ctx, roomID)
	}
}

type PlayerReadyRequest struct {
	RoomID string
	UserID string
}

type PlayerReadyResult struct {
	RoomState *RoomState
}

func (s *SeatAppService) PlayerReady(ctx context.Context, req *PlayerReadyRequest) (*PlayerReadyResult, error) {
	result, err := s.SetReady(ctx, &SetReadyRequest{
		RoomID: req.RoomID,
		UserID: req.UserID,
	})
	if err != nil {
		return nil, err
	}
	return &PlayerReadyResult{
		RoomState: result.RoomState,
	}, nil
}
