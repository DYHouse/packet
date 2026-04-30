package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/scheduler"
	"github.com/cashparty/backend/settlement/dto"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type SeatAppService struct {
	repo              domain.RoomRepository
	dbRepo            domain.DBRepository
	broadcaster       domain.Broadcaster
	publisher         domain.EventPublisher
	scheduler         *scheduler.TimeoutScheduler
	settlementService *settlementService.SettlementService
	gameService       *GameAppService
	redis             *cRedis.Client
	balanceService    *settlementService.BalanceService
	readyCountdown    time.Duration
}

func NewSeatAppService(
	repo domain.RoomRepository,
	dbRepo domain.DBRepository,
	broadcaster domain.Broadcaster,
	publisher domain.EventPublisher,
	scheduler *scheduler.TimeoutScheduler,
	settlementSvc *settlementService.SettlementService,
	gameService *GameAppService,
	redis *cRedis.Client,
	balanceService *settlementService.BalanceService,
	readyCountdown time.Duration,
) *SeatAppService {
	return &SeatAppService{
		repo:              repo,
		dbRepo:            dbRepo,
		broadcaster:       broadcaster,
		publisher:         publisher,
		scheduler:         scheduler,
		settlementService: settlementSvc,
		gameService:       gameService,
		redis:             redis,
		balanceService:    balanceService,
		readyCountdown:    readyCountdown,
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

	err = s.repo.SelectSeat(ctx, req.RoomID, req.UserID, req.SeatNo)
	if err != nil {
		return nil, err
	}

	if s.scheduler != nil {
		s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSeat, req.RoomID, req.UserID)
	}

	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)
	nickname := ""
	avatar := ""
	if spectator != nil {
		nickname = spectator.Nickname
		avatar = spectator.Avatar
	}

	if s.publisher != nil {
		s.publisher.Publish(ctx, domain.NewSeatSelectEvent(req.RoomID, req.UserID, req.SeatNo, nickname, avatar))
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)

	if s.broadcaster != nil && stateData != nil {
		s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), req.UserID)
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

	if meta.Status != domain.RoomStatusWaiting {
		return nil, message.NewError(message.CodeGameInProgress)
	}

	err = s.repo.CancelSeat(ctx, req.RoomID, req.UserID)
	if err != nil {
		return nil, err
	}

	if s.scheduler != nil {
		s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSeat, req.RoomID, req.UserID)
	}

	spectator, _ := s.repo.GetSpectator(ctx, req.RoomID, req.UserID)
	nickname := ""
	if spectator != nil {
		nickname = spectator.Nickname
	}

	if s.publisher != nil {
		s.publisher.Publish(ctx, domain.NewSeatCancelEvent(req.RoomID, req.UserID, 0, nickname))
	}

	var roomState *RoomState
	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			roomState = BuildFullRoomState(stateData)
			s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, roomState, req.UserID)
		}
	}

	logger.Info("seat cancelled",
		"room_id", req.RoomID,
		"user_id", req.UserID,
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

	if s.balanceService != nil {
		userIDInt := converter.ParseID(req.UserID)
		balanceResult, err := s.balanceService.CheckBalanceForReady(ctx, &dto.BalanceCheckRequest{
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
			return nil, message.NewErrorWithMsg(message.CodeInsufficientBalance, message.GetInsufficientBalanceMsg(balanceResult.RequiredFee, balanceResult.Balance))
		}
	}

	roomHashKey := redis.RoomHashKey(req.RoomID)
	playersKey := redis.RoomPlayersKey(req.RoomID)
	spectatorsKey := redis.RoomSpectatorsKey(req.RoomID)
	now := time.Now().Unix()

	result, err := s.redis.Eval(ctx, redis.LuaPlayerReady,
		[]string{roomHashKey, playersKey, spectatorsKey}, req.UserID, now).Slice()

	if err != nil {
		logger.Error("failed to execute player ready lua script", "error", err)
		return nil, message.NewError(message.CodeSystemBusy)
	}

	if len(result) < 8 {
		return nil, message.NewErrorWithMsg(message.CodeSystemError, "invalid result")
	}

	code := converter.ParseInt(result[0])
	if code != 1 {
		errMsg := "operation failed"
		if len(result) > 7 {
			errMsg = converter.ParseString(result[7])
		}
		return nil, message.NewErrorWithMsg(message.CodeInvalidGameState, errMsg)
	}

	if s.scheduler != nil {
		s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSeat, req.RoomID, req.UserID)
	}

	playerCount := converter.ParseInt(result[1])
	shouldStartCountdown := converter.ParseInt(result[3])
	countdownEndTime := converter.ParseInt64(result[4])
	currentRound := converter.ParseInt(result[5])
	playerDataStr := converter.ParseString(result[6])

	if s.publisher != nil {
		var player struct {
			Nickname string `json:"nickname"`
			Avatar   string `json:"avatar"`
			SeatNo   int    `json:"seat_no"`
		}
		json.Unmarshal([]byte(playerDataStr), &player)

		s.publisher.Publish(ctx, domain.NewPlayerReadyEvent(
			req.RoomID, req.UserID, player.SeatNo, player.Nickname, player.Avatar))
	}

	var roomState *RoomState
	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
		if stateData != nil {
			roomState = BuildFullRoomState(stateData)
			s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, roomState, req.UserID)
		}
	}

	if shouldStartCountdown == 1 {
		if s.broadcaster != nil {
			s.broadcaster.Broadcast(req.RoomID, message.PushCountdownStart, &message.CountdownStartPush{
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
			go s.gameService.ResumeGame(context.Background(), &ResumeGameRequest{
				RoomID:       req.RoomID,
				CurrentRound: currentRound,
			})
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
	roomHashKey := redis.RoomHashKey(roomID)
	playersKey := redis.RoomPlayersKey(roomID)
	spectatorsKey := redis.RoomSpectatorsKey(roomID)
	seatsKey := redis.RoomSeatsKey(roomID)
	seatOwnerKey := redis.RoomSeatOwnerKey(roomID)
	userRoomKey := redis.PlayerRoomKey(userID)

	result, err := s.redis.Eval(ctx, redis.LuaHandleSeatTimeout,
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

	if code != 1 {
		logger.Warn("seat timeout failed",
			"room_id", roomID,
			"user_id", userID,
			"message", resultMsg)
		return
	}

	if s.publisher != nil {
		s.publisher.Publish(ctx, domain.NewSpectatorKickEvent(roomID, userID, seatNo, message.ReasonSeatTimeout))
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(userID, message.PushKicked, &message.KickedPush{
			RoomID:  roomID,
			UserID:  userID,
			Reason:  message.ReasonSeatTimeout,
			Message: message.GetKickMessage(message.ReasonSeatTimeout),
		})

		stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
		if stateData != nil {
			s.broadcaster.Broadcast(roomID, message.PushRoomState, BuildFullRoomState(stateData), userID)
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
