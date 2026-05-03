package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/messaging"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/game/scheduler"
	settlementDto "github.com/cashparty/backend/settlement/dto"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type GameAppService struct {
	repo              domain.RoomRepository
	dbRepo            domain.DBRepository
	broadcaster       domain.Broadcaster
	publisher         domain.EventPublisher
	eventPublisher    *messaging.GameEventPublisher
	grabService       *GrabService
	penaltyService    *PenaltyService
	scheduler         *scheduler.TimeoutScheduler
	packetGenerator   *algorithm.PacketGenerator
	settlementService *settlementService.SettlementService
	deductSvc         *settlementService.DeductService
	refundSvc         *settlementService.RefundService
	rewardController  *algorithm.RewardController
	rewardSettler     *settlementService.RewardSettler
	redis             *cRedis.Client
	commissionCfg     *domain.CommissionConfig
	timeoutCfg        *config.TimeoutConfig
}

func NewGameAppService(
	repo domain.RoomRepository,
	dbRepo domain.DBRepository,
	broadcaster domain.Broadcaster,
	publisher domain.EventPublisher,
	eventPublisher *messaging.GameEventPublisher,
	grabService *GrabService,
	penaltyService *PenaltyService,
	scheduler *scheduler.TimeoutScheduler,
	packetGenerator *algorithm.PacketGenerator,
	settlementSvc *settlementService.SettlementService,
	deductSvc *settlementService.DeductService,
	refundSvc *settlementService.RefundService,
	rewardController *algorithm.RewardController,
	rewardSettler *settlementService.RewardSettler,
	redis *cRedis.Client,
	timeoutCfg *config.TimeoutConfig,
) *GameAppService {
	return &GameAppService{
		repo:              repo,
		dbRepo:            dbRepo,
		broadcaster:       broadcaster,
		publisher:         publisher,
		eventPublisher:    eventPublisher,
		grabService:       grabService,
		penaltyService:    penaltyService,
		scheduler:         scheduler,
		packetGenerator:   packetGenerator,
		settlementService: settlementSvc,
		deductSvc:         deductSvc,
		refundSvc:         refundSvc,
		rewardController:  rewardController,
		rewardSettler:     rewardSettler,
		redis:             redis,
		commissionCfg:     domain.DefaultCommissionConfig(),
		timeoutCfg:        timeoutCfg,
	}
}

type SendPacketRequest struct {
	RoomID string
	UserID string
}

type SendPacketResult struct {
	PacketCount int
}

type sendPacketResult struct {
	PacketCount  int
	RoundID      string
	PacketIDs    []string
	Amount       int64
	Commission   int64
	ActualAmount int64
	NextRound    int
	Nickname     string
	SenderType   string
}

func (s *GameAppService) sendPacketPipeline(ctx context.Context, params *domain.SendPacketParams) (*sendPacketResult, error) {
	commission := s.commissionCfg.Calculate(params.TotalAmount)
	actualAmount := params.TotalAmount - commission

	roundID := converter.FormatID(params.RoundID)

	genResult, err := s.packetGenerator.Generate(ctx, &algorithm.GenerateRequest{
		TotalAmount: actualAmount,
		PacketCount: params.PlayerCount,
		RoomID:      params.RoomID,
		RoundID:     roundID,
	})
	if err != nil {
		return nil, message.NewError(message.CodeSystemError)
	}

	senderID := params.Scenario.SenderID(params.SenderID)
	senderType := params.Scenario.SenderType()

	var rewardAmount int64
	if genResult.RewardType > 0 && s.rewardSettler != nil {
		rewardAmount = s.rewardSettler.CalculateRewardAmount(int(genResult.RewardType), params.TotalAmount)
	}

	_, packetIDs, err := s.grabService.InitRoundPackets(ctx,
		params.RoomID, roundID, senderID, senderType,
		params.TotalAmount, commission, actualAmount,
		genResult.PacketAmounts, params.RoundNo, params.Scenario,
		int(genResult.RewardType), rewardAmount,
	)
	if err != nil {
		return nil, message.NewError(message.CodeSystemError)
	}

	return &sendPacketResult{
		PacketCount:  len(genResult.PacketAmounts),
		RoundID:      roundID,
		PacketIDs:    packetIDs,
		Amount:       params.TotalAmount,
		Commission:   commission,
		ActualAmount: actualAmount,
		NextRound:    params.RoundNo,
		SenderType:   senderType,
	}, nil
}

func (s *GameAppService) SendPacket(ctx context.Context, req *SendPacketRequest) (*SendPacketResult, error) {
	lockKey := redis.SendPacketLockKey(req.RoomID, req.UserID)

	var result *sendPacketResult

	err := lock.WithRedisLock(ctx, s.redis, lockKey, 10, func() error {
		meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
		if err != nil {
			return message.NewError(message.CodeRoomNotFound)
		}

		if meta.Status != domain.RoomStatusPlaying {
			return message.NewError(message.CodeGameNotStarted)
		}

		nextRound := int(meta.CurrentRound) + 1

		if meta.CurrentRoundID != "" {
			return message.NewError(message.CodePacketsAlreadyExist)
		}

		player, err := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
		if err != nil || player == nil {
			return message.NewError(message.CodeNotInRoom)
		}

		if nextRound > 1 {
			roomHashKey := redis.RoomHashKey(req.RoomID)
			nextSenderID, _ := s.redis.HGet(ctx, roomHashKey, "next_sender_id").Result()
			if nextSenderID != "" && nextSenderID != req.UserID {
				return message.NewError(message.CodeNotYourTurn)
			}

			initResult, initErr := s.initLaterRoundAndDeduct(ctx, req.RoomID, meta, nextRound, req.UserID, meta.RoomFee)
			if initErr != nil {
				logger.Error("later round init and deduct failed",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"round_no", nextRound,
					"error", initErr)
				s.handleDeductFailure(ctx, req.RoomID, meta, message.ReasonLaterRoundDeductFailed, initErr)
				return message.NewErrorWithMsg(message.CodeSystemError, message.GetInterruptMessage(message.ReasonLaterRoundDeductFailed))
			}

			pipelineResult, pipelineErr := s.sendPacketPipeline(ctx, &domain.SendPacketParams{
				RoomID:      req.RoomID,
				SenderID:    req.UserID,
				Scenario:    domain.SendScenarioPlayerManual,
				TotalAmount: meta.RoomFee,
				RoundNo:     nextRound,
				RoundID:     initResult.RoundID,
				PlayerCount: meta.MaxPlayers,
			})
			if pipelineErr != nil {
				if updateErr := s.updateRoundFailed(ctx, initResult.RoundID, fmt.Sprintf("send packet failed: %v", pipelineErr)); updateErr != nil {
					logger.Error("update round failed status error", "round_id", initResult.RoundID, "error", updateErr)
				}
				return pipelineErr
			}

			if err := s.dbRepo.RoundDBRepo().UpdateRoundStatus(ctx, initResult.RoundID, model.RoundStatusSending); err != nil {
				logger.Error("update round status to sending failed", "round_id", initResult.RoundID, "error", err)
			}

			pipelineResult.Nickname = player.Nickname
			result = pipelineResult
			return nil
		}

		pipelineResult, pipelineErr := s.sendPacketPipeline(ctx, &domain.SendPacketParams{
			RoomID:      req.RoomID,
			SenderID:    req.UserID,
			Scenario:    domain.SendScenarioPlayerManual,
			TotalAmount: meta.RoomFee,
			RoundNo:     nextRound,
			PlayerCount: meta.MaxPlayers,
		})
		if pipelineErr != nil {
			return pipelineErr
		}

		pipelineResult.Nickname = player.Nickname
		result = pipelineResult
		return nil
	})

	if err != nil {
		if _, ok := message.IsGameError(err); ok {
			return nil, err
		}
		return nil, message.NewError(message.CodeOperationInProgress)
	}

	go s.postSendPacketAsync(context.Background(), &postSendPacketParams{
		RoomID:       req.RoomID,
		RoundID:      result.RoundID,
		PacketIDs:    result.PacketIDs,
		UserID:       req.UserID,
		Nickname:     result.Nickname,
		Amount:       result.Amount,
		Commission:   result.Commission,
		ActualAmount: result.ActualAmount,
		NextRound:    result.NextRound,
		PacketCount:  result.PacketCount,
		SenderType:   result.SenderType,
	})

	return &SendPacketResult{
		PacketCount: result.PacketCount,
	}, nil
}

type postSendPacketParams struct {
	RoomID       string
	RoundID      string
	PacketIDs    []string
	UserID       string
	Nickname     string
	Amount       int64
	Commission   int64
	ActualAmount int64
	NextRound    int
	PacketCount  int
	SenderType   string
}

func (s *GameAppService) postSendPacketAsync(ctx context.Context, params *postSendPacketParams) {
	s.publishPacketCreatedEvent(ctx, params.RoomID, params.RoundID, params.PacketIDs, params.UserID, params.Amount, params.Commission, params.NextRound)

	if s.scheduler != nil {
		if params.UserID != "0" {
			s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSend, params.RoomID, params.UserID)
		}
		s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeGrab, params.RoomID, params.RoundID)
	}

	packets := make([]message.PacketInfo, len(params.PacketIDs))
	for i, packetID := range params.PacketIDs {
		packets[i] = message.PacketInfo{
			PacketID: packetID,
			Position: int32(i + 1),
		}
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(params.RoomID, message.PushRoundStart, &message.RoundStartPush{
			RoomID:         params.RoomID,
			RoundID:        params.RoundID,
			CurrentRound:   int32(params.NextRound),
			SenderID:       params.UserID,
			SenderNickname: params.Nickname,
			SenderType:     params.SenderType,
			TotalAmount:    currency.NewMoneyFromFen(params.ActualAmount),
			Commission:     currency.NewMoneyFromFen(params.Commission),
			PacketCount:    int32(params.PacketCount),
			GrabTimeout:    int32(s.timeoutCfg.Grab.Seconds()),
			Packets:        packets,
		}, "")
	}

	logger.Info("packet sent",
		"room_id", params.RoomID,
		"user_id", params.UserID,
		"sender_type", params.SenderType,
		"amount", params.Amount,
		"round_id", params.RoundID,
		"packet_count", params.PacketCount,
	)
}

type GrabPacketRequest struct {
	RoomID   string
	UserID   string
	PacketID string
}

type GrabPacketResult struct {
	PacketID string
	Amount   int64
	Position int
	IsLast   bool
}

func (s *GameAppService) GrabPacket(ctx context.Context, req *GrabPacketRequest) (*GrabPacketResult, error) {
	meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	if meta.Status != domain.RoomStatusPlaying {
		return nil, message.NewError(message.CodeGameNotStarted)
	}

	roundID := meta.CurrentRoundID

	result, err := s.grabService.GrabPacket(ctx, req.RoomID, roundID, req.UserID, req.PacketID)
	if err != nil {
		return nil, err
	}

	player, _ := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
	nickname := ""
	if player != nil {
		nickname = player.Nickname
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(req.RoomID, message.PushPacketGrabbed, &message.PacketGrabbedPush{
			RoomID:   req.RoomID,
			RoundID:  roundID,
			PacketID: result.PacketID,
			Position: int32(result.Position),
			UserID:   req.UserID,
			Nickname: nickname,
			Amount:   currency.NewMoneyFromFen(result.Amount),
			IsLast:   result.IsLast,
		}, req.UserID)
	}

	if result.IsLast {
		if s.scheduler != nil {
			s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeGrab, req.RoomID, roundID)
		}
		go s.settleRound(context.Background(), req.RoomID, roundID)
	}

	logger.Info("packet grabbed",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"round_id", roundID,
		"amount", result.Amount,
		"position", result.Position,
	)

	return &GrabPacketResult{
		PacketID: result.PacketID,
		Amount:   result.Amount,
		Position: result.Position,
		IsLast:   result.IsLast,
	}, nil
}

func (s *GameAppService) startGameCore(ctx context.Context, roomID string, meta *domain.RoomMeta) string {
	sessionID := idgen.GenerateString()

	if err := s.repo.UpdateRoomSessionID(ctx, roomID, sessionID); err != nil {
		logger.Error("failed to update session_id", "room_id", roomID, "error", err)
	}
	meta.CurrentSessionID = sessionID

	if s.scheduler != nil {
		s.scheduler.ClearAllRoomTimeouts(ctx, roomID)
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(roomID, message.PushGameStart, &message.GameStartPush{
			RoomID:       roomID,
			CurrentRound: 1,
			MaxRounds:    int32(meta.MaxRounds),
		}, "")
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
	if stateData != nil && s.broadcaster != nil {
		s.broadcaster.Broadcast(roomID, message.PushRoomState, BuildFullRoomState(stateData), "")
	}

	if s.eventPublisher != nil && stateData != nil {
		players := make([]*domain.PlayerInfo, 0, len(stateData.Players))
		for _, p := range stateData.Players {
			players = append(players, &domain.PlayerInfo{
				UserID:   p.UserID,
				Nickname: p.Nickname,
				Avatar:   p.Avatar,
				SeatNo:   p.SeatNo,
			})
		}

		event := &domain.GameEvent{
			RoomID:    roomID,
			SessionID: sessionID,
			Timestamp: time.Now().Unix(),
			TraceID:   idgen.GenerateString(),
			Data: &domain.SessionStartData{
				RoomNo:     meta.RoomNo,
				ConfigID:   meta.ConfigID,
				ConfigName: meta.ConfigName,
				RoomFee:    meta.RoomFee,
				MaxRounds:  meta.MaxRounds,
				Players:    players,
			},
		}
		go func() {
			if err := s.eventPublisher.PublishSessionStart(context.Background(), event); err != nil {
				logger.Error("publish session start event failed", "error", err)
			}
		}()
	}

	logger.Info("game started",
		"room_id", roomID,
		"session_id", sessionID,
		"max_rounds", meta.MaxRounds,
	)

	return sessionID
}

func (s *GameAppService) StartGame(ctx context.Context, roomID string) {
	roomHashKey := redis.RoomHashKey(roomID)
	now := time.Now().Unix()

	result, err := s.redis.Eval(ctx, redis.LuaTryStartGame, []string{roomHashKey}, now).Result()
	if err != nil {
		logger.Error("failed to execute start game lua script",
			"room_id", roomID,
			"error", err,
		)
		return
	}

	if resultArray, ok := result.([]interface{}); ok && len(resultArray) >= 1 {
		if success, ok := resultArray[0].(int64); !ok || success != 1 {
			logger.Info("game start skipped, already started by another instance",
				"room_id", roomID,
				"reason", resultArray[1],
			)
			return
		}
	} else {
		logger.Warn("unexpected result from start game lua script",
			"room_id", roomID,
			"result", result,
		)
		return
	}

	meta, err := s.repo.GetRoomMeta(ctx, roomID)
	if err != nil {
		logger.Error("failed to get room meta", "room_id", roomID, "error", err)
		return
	}

	s.startGameCore(ctx, roomID, meta)
	s.startFirstRound(ctx, roomID, meta)
}

func (s *GameAppService) OnGrabTimeout(ctx context.Context, roomID string, roundID string) {
	logger.Warn("grab timeout, auto distributing", "room_id", roomID, "round_id", roundID)

	count, results, err := s.grabService.AutoDistribute(ctx, roomID, roundID)
	if err != nil {
		logger.Error("auto distribute failed", "room_id", roomID, "round_id", roundID, "error", err)
		return
	}

	if s.broadcaster != nil {
		msgResults := make([]message.DistributeResult, len(results))
		for i, r := range results {
			msgResults[i] = message.DistributeResult{
				UserID:   r.UserID,
				Amount:   currency.NewMoneyFromFen(r.Amount),
				Position: r.Position,
			}
		}
		s.broadcaster.Broadcast(roomID, message.PushAutoDistribute, &message.AutoDistributePush{
			RoomID:           roomID,
			RoundID:          roundID,
			DistributedCount: int32(count),
			Results:          msgResults,
		}, "")
	}

	s.settleRound(ctx, roomID, roundID)
}

func (s *GameAppService) OnSendTimeout(ctx context.Context, roomID string, userID string) {
	logger.Warn("send timeout triggered", "room_id", roomID, "user_id", userID)

	if userID == "0" {
		s.handleSystemSendTimeout(ctx, roomID)
		return
	}

	lockKey := redis.SendPacketLockKey(roomID, userID)

	err := lock.WithRedisLock(ctx, s.redis, lockKey, 10, func() error {
		meta, err := s.repo.GetRoomMeta(ctx, roomID)
		if err != nil {
			logger.Error("failed to get room meta for timeout", "room_id", roomID, "error", err)
			return nil
		}

		if meta.CurrentRoundID != "" {
			logger.Info("packet already sent, skip timeout handling",
				"room_id", roomID,
				"user_id", userID,
				"current_round_id", meta.CurrentRoundID,
			)
			return nil
		}

		roomHashKey := redis.RoomHashKey(roomID)
		nextSenderID, _ := s.redis.HGet(ctx, roomHashKey, "next_sender_id").Result()
		if nextSenderID != "" && nextSenderID != userID {
			logger.Info("not this player's turn, skip timeout handling",
				"room_id", roomID,
				"user_id", userID,
				"next_sender_id", nextSenderID,
			)
			return nil
		}

		roomIDInt := converter.ParseID(roomID)
		sessionID := roomIDInt
		if meta.CurrentSessionID != "" {
			sessionID = converter.ParseID(meta.CurrentSessionID)
		}

		result, err := s.penaltyService.ApplyPenalty(ctx, roomID, userID, domain.PenaltyTypeSendTimeout, meta.RoomFee, sessionID, int(meta.CurrentRound))
		if err != nil {
			logger.Error("apply penalty failed", "room_id", roomID, "user_id", userID, "error", err)
			return nil
		}

		if result.DeductFailed {
			s.handleDeductFailure(ctx, roomID, meta, "penalty_deduct_failed", result.DeductError)
			return nil
		}

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(roomID, message.PushPenalty, &message.PenaltyPush{
				RoomID:        roomID,
				UserID:        userID,
				PenaltyType:   "send_timeout",
				PenaltyAmount: currency.NewMoneyFromFen(result.Amount),
				PenaltyCount:  int32(result.Count),
				KickRequired:  result.KickRequired,
				Reason:        result.Reason,
			}, "")
		}

		if result.KickRequired {
			s.handleKickAndReplace(ctx, roomID, userID, meta.RoomFee)
		} else {
			s.forceSendPacketForPlayer(ctx, roomID, userID, result.Amount)
		}

		return nil
	})

	if err != nil {
		logger.Error("failed to acquire lock for send timeout",
			"room_id", roomID,
			"user_id", userID,
			"error", err,
		)
	}
}

func (s *GameAppService) handleSystemSendTimeout(ctx context.Context, roomID string) {
	logger.Info("system send packet triggered by leopard reward", "room_id", roomID)

	meta, err := s.repo.GetRoomMeta(ctx, roomID)
	if err != nil || meta == nil {
		logger.Error("failed to get room meta for system send", "room_id", roomID, "error", err)
		return
	}

	nextRound := int(meta.CurrentRound) + 1

	s.executeSystemSendPacket(ctx, &systemSendPacketParams{
		RoomID:      roomID,
		Meta:        meta,
		RoundNo:     nextRound,
		TotalAmount: meta.RoomFee,
		Reason:      "leopard_reward",
		Scenario:    domain.SendScenarioLeopardReward,
		SenderType:  domain.SenderTypeSystem,
		Nickname:    "system",
	})
}

func (s *GameAppService) OnReplaceTimeout(ctx context.Context, roomID string, leftUserID string) {
	logger.Warn("replacement timeout", "room_id", roomID, "left_user_id", leftUserID)

	lockKey := redis.ReplaceTimeoutLockKey(roomID, leftUserID)

	err := lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
		meta, err := s.repo.GetRoomMeta(ctx, roomID)
		if err != nil || meta == nil {
			logger.Error("failed to get room meta for replacement timeout", "room_id", roomID, "error", err)
			return nil
		}

		if meta.Status != domain.RoomStatusInterrupted {
			logger.Info("room status changed, skip replacement timeout", "room_id", roomID, "status", meta.Status)
			return nil
		}

		dist, err := s.penaltyService.DistributePenalty(ctx, roomID, meta.RoomFee, []string{leftUserID})
		if err != nil {
			logger.Error("distribute penalty failed", "room_id", roomID, "error", err)
			return err
		}

		roomIDInt := converter.ParseID(roomID)
		sessionID := roomIDInt
		if meta.CurrentSessionID != "" {
			sessionID = converter.ParseID(meta.CurrentSessionID)
		}

		recipientIDs := make([]int64, 0, len(dist.Recipients))
		for _, r := range dist.Recipients {
			recipientIDs = append(recipientIDs, converter.ParseID(r))
		}

		roundID := sessionID
		if meta.CurrentRoundID != "" {
			roundID = converter.ParseID(meta.CurrentRoundID)
		}

		distReq := &settlementDto.PenaltyDistributeRequest{
			RoomID:     roomIDInt,
			SessionID:  sessionID,
			RoundID:    roundID,
			Amount:     meta.RoomFee,
			Recipients: recipientIDs,
			Reason:     "replacement_timeout",
		}

		if err := s.settlementService.DistributePenaltyFromPlatform(ctx, distReq); err != nil {
			logger.Error("distribute penalty from platform failed", "room_id", roomID, "error", err)
		}

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(roomID, message.PushGameInterrupted, &message.GameInterruptedPush{
				RoomID:       roomID,
				Reason:       message.ReasonReplacementTimeout,
				PenaltyShare: currency.NewMoneyFromFen(dist.ShareAmount),
				Recipients:   dist.Recipients,
			}, "")
		}

		return s.endGameWithOptions(ctx, roomID, &EndGameOptions{
			AllowedStatus: int(domain.RoomStatusInterrupted),
			EndReason:     message.ReasonReplacementTimeout,
			SessionID:     meta.CurrentSessionID,
			ActualRounds:  int(meta.CurrentRound),
		})
	})

	if err != nil {
		logger.Error("replace timeout handling failed", "room_id", roomID, "left_user_id", leftUserID, "error", err)
	}
}

func (s *GameAppService) startFirstRound(ctx context.Context, roomID string, meta *domain.RoomMeta) {
	playersMap, err := s.repo.GetPlayers(ctx, roomID)
	if err != nil {
		logger.Error("get players failed", "room_id", roomID, "error", err)
		return
	}

	if len(playersMap) == 0 {
		logger.Error("no players in room", "room_id", roomID)
		return
	}

	players := make([]*domain.Player, 0, len(playersMap))
	for _, player := range playersMap {
		players = append(players, player)
	}

	initResult, err := s.initRoundAndDeduct(ctx, roomID, meta, 1, players)
	if err != nil {
		logger.Error("first round init and deduct failed", "room_id", roomID, "error", err)
		s.handleDeductFailure(ctx, roomID, meta, message.ReasonFirstRoundDeductFailed, err)
		return
	}

	result, err := s.sendPacketPipeline(ctx, &domain.SendPacketParams{
		RoomID:      roomID,
		Scenario:    domain.SendScenarioFirstRound,
		TotalAmount: meta.RoomFee,
		RoundNo:     1,
		RoundID:     initResult.RoundID,
		PlayerCount: meta.MaxPlayers,
	})
	if err != nil {
		logger.Error("first round send failed", "room_id", roomID, "error", err)
		if updateErr := s.updateRoundFailed(ctx, initResult.RoundID, fmt.Sprintf("send packet failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", initResult.RoundID, "error", updateErr)
		}
		return
	}

	if err := s.dbRepo.RoundDBRepo().UpdateRoundStatus(ctx, initResult.RoundID, model.RoundStatusSending); err != nil {
		logger.Error("update round status to sending failed", "round_id", initResult.RoundID, "error", err)
	}

	go s.postSendPacketAsync(context.Background(), &postSendPacketParams{
		RoomID:       roomID,
		RoundID:      result.RoundID,
		PacketIDs:    result.PacketIDs,
		UserID:       "0",
		Nickname:     "system",
		Amount:       result.Amount,
		Commission:   result.Commission,
		ActualAmount: result.ActualAmount,
		NextRound:    1,
		PacketCount:  result.PacketCount,
		SenderType:   domain.SenderTypeSystem,
	})
}

func (s *GameAppService) handleDeductFailure(ctx context.Context, roomID string, meta *domain.RoomMeta, reason string, err error) {
	if s.broadcaster != nil {
		s.broadcaster.Broadcast(roomID, message.PushGameInterrupted, &message.GameInterruptedPush{
			RoomID: roomID,
			Reason: reason,
		}, "")
	}

	logger.Error("deduct failed, game ended", "room_id", roomID, "reason", reason, "error", err)

	var sessionID string
	var actualRounds int
	if meta != nil {
		sessionID = meta.CurrentSessionID
		actualRounds = int(meta.CurrentRound)
	}

	go s.endGameWithOptions(context.Background(), roomID, &EndGameOptions{
		AllowedStatus: int(domain.RoomStatusPlaying),
		EndReason:     reason,
		SessionID:     sessionID,
		ActualRounds:  actualRounds,
	})
}

func (s *GameAppService) settleRound(ctx context.Context, roomID, roundID string) {
	lockKey := redis.SettleLockKey(roomID, roundID)

	err := lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
		meta, _ := s.repo.GetRoomMeta(ctx, roomID)

		var sessionPlayerTotalsKey string
		if meta != nil && meta.CurrentSessionID != "" {
			sessionPlayerTotalsKey = redis.SessionPlayerTotalsKey(meta.CurrentSessionID)
		}

		keys := []string{
			redis.RoundStateKey(roundID),
			redis.RoundGrabbersKey(roundID),
			redis.RoomPlayersKey(roomID),
			redis.RoomHashKey(roomID),
			redis.RoundAvailablePacketsKey(roundID),
			sessionPlayerTotalsKey,
		}

		args := []interface{}{
			roundID,
			time.Now().Unix(),
			"cashparty",
		}

		res, err := s.redis.Eval(ctx, redis.LuaSettleRound, keys, args...).Slice()
		if err != nil {
			logger.Error("settle round failed", "room_id", roomID, "round_id", roundID, "error", err)
			return err
		}

		code := converter.ParseInt(res[0])
		if code == 2 {
			logger.Info("round already settled (idempotent)", "room_id", roomID, "round_id", roundID)
			return nil
		}
		if code != 0 {
			logger.Error("settle round lua failed", "room_id", roomID, "round_id", roundID, "lua_code", code)
			return domain.MapLuaError(code)
		}

		roundNo := converter.ParseInt(res[1])
		senderID := converter.ParseString(res[2])
		totalAmount := converter.ParseInt64(res[3])
		minAmountPlayer := converter.ParseString(res[4])
		isGameEnd := converter.ParseInt(res[5]) == 1

		var results []message.RoundResult
		if len(res) > 6 {
			if arr, ok := res[6].([]interface{}); ok {
				for _, item := range arr {
					if tuple, ok := item.([]interface{}); ok && len(tuple) >= 7 {
						isAutoAssigned := converter.ParseInt(tuple[5]) == 1
						packetID := converter.ParseInt64(tuple[6])
						results = append(results, message.RoundResult{
							UserID:         converter.ParseString(tuple[0]),
							Amount:         currency.NewMoneyFromFen(converter.ParseInt64(tuple[1])),
							Nickname:       converter.ParseString(tuple[2]),
							Position:       int32(converter.ParseInt(tuple[3])),
							Avatar:         converter.ParseString(tuple[4]),
							IsAutoAssigned: isAutoAssigned,
							PacketID:       converter.FormatID(packetID),
						})
					}
				}
			}
		}

		sort.Slice(results, func(i, j int) bool {
			return results[i].Position < results[j].Position
		})

		commission := int64(0)
		if meta != nil {
			commission = s.commissionCfg.Calculate(totalAmount)
		}

		var rewardType int
		var rewardAmount int64
		if len(res) > 8 {
			rewardType = converter.ParseInt(res[7])
			rewardAmount = converter.ParseInt64(res[8])
		}

		var finalResults []message.GameResult
		if isGameEnd && len(res) > 9 {
			if arr, ok := res[9].([]interface{}); ok {
				for _, item := range arr {
					if tuple, ok := item.([]interface{}); ok && len(tuple) >= 5 {
						finalResults = append(finalResults, message.GameResult{
							UserID:      converter.ParseString(tuple[0]),
							Nickname:    converter.ParseString(tuple[1]),
							Avatar:      converter.ParseString(tuple[2]),
							TotalProfit: currency.NewMoneyFromFen(converter.ParseInt64(tuple[3])),
							Rank:        int32(converter.ParseInt(tuple[4])),
						})
					}
				}
			}
		}

		logger.Info("reward from redis", "rewardType", rewardType, "rewardAmount", rewardAmount)

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(roomID, message.PushRoundEnd, &message.RoundEndPush{
				RoomID:          roomID,
				RoundID:         roundID,
				CurrentRound:    int32(roundNo),
				SenderID:        senderID,
				TotalAmount:     currency.NewMoneyFromFen(totalAmount),
				Commission:      currency.NewMoneyFromFen(commission),
				Results:         results,
				MinAmountPlayer: minAmountPlayer,
				NextSenderID:    minAmountPlayer,
				IsGameEnd:       isGameEnd,
				RewardType:      rewardType,
				RewardAmount:    currency.NewMoneyFromFen(rewardAmount),
				FinalResults:    finalResults,
			}, "")
		}

		if s.eventPublisher != nil && meta != nil {
			roundResults := make([]*domain.RoundResult, 0, len(results))
			for _, r := range results {
				roundResults = append(roundResults, &domain.RoundResult{
					UserID:         r.UserID,
					PacketID:       r.PacketID,
					Position:       int(r.Position),
					Amount:         r.Amount.Fen(),
					IsAutoAssigned: r.IsAutoAssigned,
				})
			}

			senderType := "player"
			if senderID == "0" {
				senderType = "system"
			}

			var roomFeePerPlayer int64
			if roundNo == 1 && senderType == "system" && len(results) > 0 {
				roomFeePerPlayer = meta.RoomFee / int64(len(results))
			}

			event := &domain.GameEvent{
				RoomID:    roomID,
				SessionID: meta.CurrentSessionID,
				RoundID:   roundID,
				Timestamp: time.Now().Unix(),
				TraceID:   idgen.GenerateString(),
				Data: &domain.RoundSettleData{
					RoundNo:          roundNo,
					SenderID:         senderID,
					SenderType:       senderType,
					TotalAmount:      totalAmount,
					Commission:       commission,
					RoomFeePerPlayer: roomFeePerPlayer,
					PacketCount:      len(results),
					Results:          roundResults,
					MinPlayerID:      minAmountPlayer,
					IsGameEnd:        isGameEnd,
					RewardType:       rewardType,
					RewardAmount:     rewardAmount,
					TriggerType:      0,
				},
			}
			go func() {
				if err := s.eventPublisher.PublishRoundSettle(context.Background(), event); err != nil {
					logger.Error("publish round settle event failed", "error", err)
				}
			}()
		}

		if isGameEnd {
			var finalResultsForEvent []*domain.FinalResult
			if meta != nil {
				finalResultsForEvent = make([]*domain.FinalResult, 0, len(finalResults))
				for _, r := range finalResults {
					finalResultsForEvent = append(finalResultsForEvent, &domain.FinalResult{
						UserID:      r.UserID,
						Nickname:    r.Nickname,
						TotalProfit: r.TotalProfit.Fen(),
						Rank:        int(r.Rank),
					})
				}
			}

			var sessionID string
			if meta != nil {
				sessionID = meta.CurrentSessionID
			}

			go s.endGameWithOptions(context.Background(), roomID, &EndGameOptions{
				AllowedStatus: int(domain.RoomStatusPlaying),
				EndReason:     message.ReasonNormalEnd,
				SessionID:     sessionID,
				ActualRounds:  roundNo,
				FinalResults:  finalResultsForEvent,
			})
		} else {
			if s.scheduler != nil {
				var sendDuration time.Duration
				if minAmountPlayer == "0" {
					sendDuration = 8 * time.Second
				} else {
					sendDuration = s.timeoutCfg.Send
					if sendDuration == 0 {
						sendDuration = 30 * time.Second
					}
				}
				if rewardType == algorithm.RewardTypeStraight {
					sendDuration += 5 * time.Second
				}
				s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSend, roomID, minAmountPlayer, sendDuration)
			}
		}

		logger.Info("round settled",
			"room_id", roomID,
			"round_id", roundID,
			"round_no", roundNo,
			"is_game_end", isGameEnd,
			"reward_type", rewardType,
			"reward_amount", rewardAmount,
		)

		return nil
	})

	if err != nil {
		logger.Error("settle round with lock failed", "room_id", roomID, "round_id", roundID, "error", err)
	}
}

type EndGameOptions struct {
	AllowedStatus int
	EndReason     string
	SessionID     string
	ActualRounds  int
	FinalResults  []*domain.FinalResult
}

func (s *GameAppService) endGameWithOptions(ctx context.Context, roomID string, opts *EndGameOptions) error {
	if opts == nil {
		opts = &EndGameOptions{
			AllowedStatus: int(domain.RoomStatusPlaying),
			EndReason:     message.ReasonNormalEnd,
		}
	}

	keys := []string{
		redis.RoomHashKey(roomID),
		redis.RoomPlayersKey(roomID),
		redis.RoomSpectatorsKey(roomID),
		redis.RoomSeatsKey(roomID),
		redis.RoomSeatOwnerKey(roomID),
	}

	args := []interface{}{
		time.Now().Unix(),
		opts.AllowedStatus,
	}

	res, err := s.redis.Eval(ctx, redis.LuaEndGame, keys, args...).Slice()
	if err != nil {
		logger.Error("end game lua failed", "room_id", roomID, "error", err)
		return err
	}

	code := converter.ParseInt(res[0])
	if code == 1 {
		logger.Info("game already ended (idempotent)", "room_id", roomID)
		return nil
	}
	if code != 0 {
		logger.Error("end game failed", "room_id", roomID, "lua_code", code)
		return domain.MapLuaError(code)
	}

	var results []message.GameResult
	if arr, ok := res[1].([]interface{}); ok {
		for _, item := range arr {
			if tuple, ok := item.([]interface{}); ok && len(tuple) >= 2 {
				results = append(results, message.GameResult{
					UserID:   converter.ParseString(tuple[0]),
					Nickname: converter.ParseString(tuple[1]),
				})
			}
		}
	}

	if s.scheduler != nil {
		for _, r := range results {
			s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSeat, roomID, r.UserID)
		}
	}

	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
		if stateData != nil {
			s.broadcaster.Broadcast(roomID, message.PushRoomState, BuildFullRoomState(stateData), "")
		}
	}

	// 统一发布 SessionEnd 事件（所有游戏结束路径都经过这里）
	if s.eventPublisher != nil && opts.SessionID != "" {
		sessionEndEvent := &domain.GameEvent{
			RoomID:    roomID,
			SessionID: opts.SessionID,
			Timestamp: time.Now().Unix(),
			TraceID:   idgen.GenerateString(),
			Data: &domain.SessionEndData{
				ActualRounds: opts.ActualRounds,
				EndReason:    opts.EndReason,
				FinalResults: opts.FinalResults,
			},
		}
		go func() {
			if err := s.eventPublisher.PublishSessionEnd(context.Background(), sessionEndEvent); err != nil {
				logger.Error("publish session end event failed", "room_id", roomID, "error", err)
			}
		}()
	}

	logger.Info("game ended", "room_id", roomID, "player_count", len(results), "reason", opts.EndReason)

	return nil
}

func (s *GameAppService) handleKickAndReplace(ctx context.Context, roomID, userID string, penaltyAmount int64) {
	result, err := s.repo.KickPlayerAndInterrupt(ctx, roomID, userID, "penalty_kick")
	if err != nil {
		logger.Error("kick player and interrupt failed",
			"room_id", roomID,
			"user_id", userID,
			"error", err)
		return
	}

	logger.Info("player kicked due to penalty",
		"room_id", roomID,
		"user_id", userID,
		"seat_no", result.SeatNo,
		"room_status", result.RoomStatus)

	if s.scheduler != nil {
		s.scheduler.ClearAllUserTimeouts(ctx, roomID, userID)
		s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeReplace, roomID, userID)
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(userID, message.PushKicked, &message.KickedPush{
			RoomID:  roomID,
			UserID:  userID,
			Reason:  "penalty_kick",
			Message: message.GetKickMessage("penalty_kick"),
		})

		stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
		if stateData != nil {
			s.broadcaster.Broadcast(roomID, message.PushRoomState, BuildFullRoomState(stateData), userID)
		}

		s.broadcaster.Broadcast(roomID, message.PushWaitReplacement, &message.WaitReplacementPush{
			RoomID:     roomID,
			VacantSeat: int32(result.SeatNo),
			LeftUserID: userID,
			WaitTime:   int32(s.timeoutCfg.Replace.Seconds()),
		}, "")
	}
}

type systemSendPacketParams struct {
	RoomID      string
	Meta        *domain.RoomMeta
	RoundNo     int
	TotalAmount int64
	Reason      string
	Scenario    domain.SendScenario
	SenderType  string
	Nickname    string
}

func (s *GameAppService) executeSystemSendPacket(ctx context.Context, params *systemSendPacketParams) {
	initResult, err := s.initSystemRoundAndDeduct(ctx, params.RoomID, params.Meta, params.RoundNo, params.TotalAmount, params.Reason)
	if err != nil {
		logger.Error("init system round failed",
			"room_id", params.RoomID,
			"round_no", params.RoundNo,
			"reason", params.Reason,
			"error", err)
		return
	}

	result, err := s.sendPacketPipeline(ctx, &domain.SendPacketParams{
		RoomID:      params.RoomID,
		SenderID:    "0",
		Scenario:    params.Scenario,
		TotalAmount: params.TotalAmount,
		RoundNo:     params.RoundNo,
		RoundID:     initResult.RoundID,
		PlayerCount: params.Meta.MaxPlayers,
	})
	if err != nil {
		logger.Error("system send packet failed",
			"room_id", params.RoomID,
			"reason", params.Reason,
			"error", err)
		if updateErr := s.updateRoundFailed(ctx, initResult.RoundID, fmt.Sprintf("send packet failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", initResult.RoundID, "error", updateErr)
		}
		return
	}

	if err := s.dbRepo.RoundDBRepo().UpdateRoundStatus(ctx, initResult.RoundID, model.RoundStatusSending); err != nil {
		logger.Error("update round status to sending failed", "round_id", initResult.RoundID, "error", err)
	}

	result.Nickname = params.Nickname

	go s.postSendPacketAsync(context.Background(), &postSendPacketParams{
		RoomID:       params.RoomID,
		RoundID:      result.RoundID,
		PacketIDs:    result.PacketIDs,
		UserID:       "0",
		Nickname:     params.Nickname,
		Amount:       result.Amount,
		Commission:   result.Commission,
		ActualAmount: result.ActualAmount,
		NextRound:    params.RoundNo,
		PacketCount:  result.PacketCount,
		SenderType:   params.SenderType,
	})
}

func (s *GameAppService) forceSendPacketForPlayer(ctx context.Context, roomID, userID string, penaltyAmount int64) {
	meta, err := s.repo.GetRoomMeta(ctx, roomID)
	if err != nil || meta == nil {
		logger.Error("failed to get room meta for force send", "room_id", roomID, "user_id", userID, "error", err)
		return
	}

	nextRound := int(meta.CurrentRound) + 1

	player, _ := s.repo.GetPlayer(ctx, roomID, userID)
	nickname := ""
	if player != nil {
		nickname = player.Nickname
	}

	s.executeSystemSendPacket(ctx, &systemSendPacketParams{
		RoomID:      roomID,
		Meta:        meta,
		RoundNo:     nextRound,
		TotalAmount: penaltyAmount,
		Reason:      "send_timeout_forced",
		Scenario:    domain.SendScenarioTimeoutForced,
		SenderType:  domain.SenderTypeSystemForced,
		Nickname:    nickname,
	})
}

type ResumeGameRequest struct {
	RoomID       string
	CurrentRound int
}

func (s *GameAppService) ResumeGame(ctx context.Context, req *ResumeGameRequest) error {
	logger.Info("resuming game from interrupt",
		"room_id", req.RoomID,
		"current_round", req.CurrentRound,
	)

	if s.scheduler != nil {
		s.scheduler.ClearRoomTimeouts(ctx, scheduler.TimeoutTypeReplace, req.RoomID)
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(req.RoomID, message.PushGameResumed, &message.GameResumedPush{
			RoomID:       req.RoomID,
			CurrentRound: int32(req.CurrentRound),
			NextSenderID: "0",
			Message:      message.GetErrorMsg(message.CodeGameResumed),
		}, "")
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
	if stateData != nil && s.broadcaster != nil {
		s.broadcaster.Broadcast(req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), "")
	}

	meta, _ := s.repo.GetRoomMeta(ctx, req.RoomID)
	if meta != nil {
		s.systemSendRound(ctx, req.RoomID, meta, req.CurrentRound)
	}

	return nil
}

func (s *GameAppService) systemSendRound(ctx context.Context, roomID string, meta *domain.RoomMeta, roundNo int) {
	nextRound := roundNo + 1

	s.executeSystemSendPacket(ctx, &systemSendPacketParams{
		RoomID:      roomID,
		Meta:        meta,
		RoundNo:     nextRound,
		TotalAmount: meta.RoomFee,
		Reason:      "resume_interrupt",
		Scenario:    domain.SendScenarioResumeInterrupt,
		SenderType:  domain.SenderTypeSystemResume,
		Nickname:    "system",
	})
}

func (s *GameAppService) publishPacketCreatedEvent(ctx context.Context, roomID, roundID string, packetIDs []string, senderID string, totalAmount, commission int64, roundNo int) {
	if s.eventPublisher == nil {
		return
	}

	meta, _ := s.repo.GetRoomMeta(ctx, roomID)
	if meta == nil {
		logger.Error("failed to get room meta for packet created event", "room_id", roomID)
		return
	}

	packets := make([]*domain.PacketData, 0, len(packetIDs))
	for _, packetIDStr := range packetIDs {
		packetKey := redis.PacketInfoKey(packetIDStr)
		packetData, err := s.redis.Get(ctx, packetKey).Result()
		if err != nil {
			logger.Error("failed to get packet info", "packet_id", packetIDStr, "error", err)
			continue
		}

		var packet struct {
			PacketID int64  `json:"packet_id"`
			RoomID   string `json:"room_id"`
			RoundID  string `json:"round_id"`
			Amount   int64  `json:"amount"`
			Position int    `json:"position"`
		}
		if err := json.Unmarshal([]byte(packetData), &packet); err != nil {
			logger.Error("failed to unmarshal packet info", "packet_id", packetIDStr, "error", err)
			continue
		}

		packets = append(packets, &domain.PacketData{
			PacketID: converter.FormatID(packet.PacketID),
			RoomID:   packet.RoomID,
			RoundID:  packet.RoundID,
			Amount:   packet.Amount,
			Position: packet.Position,
		})
	}

	senderType := "player"
	if senderID == "0" {
		senderType = "system"
	}

	event := &domain.GameEvent{
		RoomID:    roomID,
		SessionID: meta.CurrentSessionID,
		RoundID:   roundID,
		Timestamp: time.Now().Unix(),
		TraceID:   idgen.GenerateString(),
		Data: &domain.PacketCreatedData{
			RoomID:      roomID,
			SessionID:   meta.CurrentSessionID,
			RoundID:     roundID,
			RoundNo:     roundNo,
			SenderID:    senderID,
			SenderType:  senderType,
			TotalAmount: totalAmount,
			Commission:  commission,
			Packets:     packets,
		},
	}

	go func() {
		if err := s.eventPublisher.PublishPacketCreated(context.Background(), event); err != nil {
			logger.Error("publish packet created event failed", "error", err)
		}
	}()
}

type initRoundResult struct {
	RoundID      int64
	RoundNo      int
	SessionID    int64
	RoomID       int64
	SenderID     int64
	SenderType   string
	TotalAmount  int64
	Commission   int64
	DeductScene  int
	DeductAmount int64
	BatchID      string
}

func (s *GameAppService) createRoundRecord(ctx context.Context, roomID, sessionID int64, roundNo int) (*model.Round, error) {
	roundID := idgen.GenerateInt64()
	round := &model.Round{
		RoundID:   roundID,
		SessionID: sessionID,
		RoomID:    roomID,
		RoundNo:   roundNo,
		Status:    model.RoundStatusPending,
	}
	if err := s.dbRepo.RoundDBRepo().CreateRound(ctx, round); err != nil {
		logger.Error("create round record failed",
			"room_id", roomID,
			"session_id", sessionID,
			"round_no", roundNo,
			"error", err)
		return nil, err
	}
	return round, nil
}

func (s *GameAppService) updateRoundDeductSuccess(ctx context.Context, roundID int64, deductScene, deductStatus int, deductAmount int64, batchID string) error {
	return s.dbRepo.RoundDBRepo().UpdateRoundDeductInfo(ctx, roundID, deductScene, deductStatus, deductAmount, batchID)
}

func (s *GameAppService) updateRoundFailed(ctx context.Context, roundID int64, reason string) error {
	return s.dbRepo.RoundDBRepo().UpdateRoundFailed(ctx, roundID, reason)
}

func (s *GameAppService) initRoundAndDeduct(ctx context.Context, roomID string, meta *domain.RoomMeta, roundNo int, players []*domain.Player) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	round, err := s.createRoundRecord(ctx, roomIDInt, sessionID, roundNo)
	if err != nil {
		logger.Error("create round record failed", "room_id", roomID, "round_no", roundNo, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	deductPlayers := make([]*settlementDto.PlayerDeductInfo, 0, len(players))
	for _, player := range players {
		userID := converter.ParseID(player.UserID)
		deductPlayers = append(deductPlayers, &settlementDto.PlayerDeductInfo{
			UserID:   userID,
			Nickname: player.Nickname,
		})
	}

	roomFeePerPlayer := meta.RoomFee / int64(len(players))

	deductReq := &settlementDto.FirstRoundDeductRequest{
		RoomID:           roomIDInt,
		SessionID:        sessionID,
		RoundID:          round.RoundID,
		RoundNo:          roundNo,
		RoomFeePerPlayer: roomFeePerPlayer,
		Players:          deductPlayers,
	}

	deductResult, err := s.deductSvc.DeductForFirstRound(ctx, deductReq)
	if err != nil {
		if updateErr := s.updateRoundFailed(ctx, round.RoundID, fmt.Sprintf("deduct failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", round.RoundID, "error", updateErr)
		}
		logger.Error("first round deduct failed", "room_id", roomID, "round_id", round.RoundID, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	if !deductResult.AllSuccess {
		if updateErr := s.updateRoundFailed(ctx, round.RoundID, fmt.Sprintf("partial deduct failed, success: %d, failed: %d",
			deductResult.SuccessCount, deductResult.FailedCount)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", round.RoundID, "error", updateErr)
		}
		logger.Error("partial deduct failed", "room_id", roomID, "round_id", round.RoundID, "success", deductResult.SuccessCount, "failed", deductResult.FailedCount)
		return nil, message.NewError(message.CodeSystemError)
	}

	if err := s.updateRoundDeductSuccess(ctx, round.RoundID, settlementDto.DeductSceneFirstRoundShare, 1, meta.RoomFee, deductResult.BatchID); err != nil {
		logger.Error("update round deduct info failed", "round_id", round.RoundID, "error", err)
	}

	if err := s.dbRepo.RoundDBRepo().UpdateRoundSender(ctx, round.RoundID, 0, "system"); err != nil {
		logger.Error("update round sender failed", "round_id", round.RoundID, "error", err)
	}

	commission := s.commissionCfg.Calculate(meta.RoomFee)
	if err := s.dbRepo.RoundDBRepo().UpdateRoundAmount(ctx, round.RoundID, meta.RoomFee, commission); err != nil {
		logger.Error("update round amount failed", "round_id", round.RoundID, "error", err)
	}

	return &initRoundResult{
		RoundID:      round.RoundID,
		RoundNo:      roundNo,
		SessionID:    sessionID,
		RoomID:       roomIDInt,
		SenderID:     0,
		SenderType:   domain.SenderTypeSystem,
		TotalAmount:  meta.RoomFee,
		Commission:   commission,
		DeductScene:  settlementDto.DeductSceneFirstRoundShare,
		DeductAmount: meta.RoomFee,
		BatchID:      deductResult.BatchID,
	}, nil
}

func (s *GameAppService) initLaterRoundAndDeduct(ctx context.Context, roomID string, meta *domain.RoomMeta, roundNo int, senderID string, roomFee int64) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	round, err := s.createRoundRecord(ctx, roomIDInt, sessionID, roundNo)
	if err != nil {
		logger.Error("create round record failed", "room_id", roomID, "round_no", roundNo, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	senderIDInt := converter.ParseID(senderID)
	senderType := "player"

	roundTraceID := fmt.Sprintf("RT_%d_%d", sessionID, roundNo)

	deductReq := &settlementDto.LaterRoundDeductRequest{
		RoomID:       roomIDInt,
		SessionID:    sessionID,
		RoundID:      round.RoundID,
		RoundNo:      roundNo,
		RoomFee:      roomFee,
		MinPlayerID:  senderIDInt,
		RoundTraceID: roundTraceID,
	}

	err = s.deductSvc.DeductForLaterRound(ctx, deductReq)
	if err != nil {
		if updateErr := s.updateRoundFailed(ctx, round.RoundID, fmt.Sprintf("deduct failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", round.RoundID, "error", updateErr)
		}
		logger.Error("later round deduct failed", "room_id", roomID, "round_id", round.RoundID, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	if err := s.updateRoundDeductSuccess(ctx, round.RoundID, settlementDto.DeductSceneLaterRoundMin, 1, roomFee, roundTraceID); err != nil {
		logger.Error("update round deduct info failed", "round_id", round.RoundID, "error", err)
	}

	if err := s.dbRepo.RoundDBRepo().UpdateRoundSender(ctx, round.RoundID, senderIDInt, senderType); err != nil {
		logger.Error("update round sender failed", "round_id", round.RoundID, "error", err)
	}

	commission := s.commissionCfg.Calculate(roomFee)
	if err := s.dbRepo.RoundDBRepo().UpdateRoundAmount(ctx, round.RoundID, roomFee, commission); err != nil {
		logger.Error("update round amount failed", "round_id", round.RoundID, "error", err)
	}

	return &initRoundResult{
		RoundID:      round.RoundID,
		RoundNo:      roundNo,
		SessionID:    sessionID,
		RoomID:       roomIDInt,
		SenderID:     senderIDInt,
		SenderType:   senderType,
		TotalAmount:  roomFee,
		Commission:   commission,
		DeductScene:  settlementDto.DeductSceneLaterRoundMin,
		DeductAmount: roomFee,
		BatchID:      roundTraceID,
	}, nil
}

func (s *GameAppService) initSystemRoundAndDeduct(ctx context.Context, roomID string, meta *domain.RoomMeta, roundNo int, totalAmount int64, reason string) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	round, err := s.createRoundRecord(ctx, roomIDInt, sessionID, roundNo)
	if err != nil {
		logger.Error("create round record failed", "room_id", roomID, "round_no", roundNo, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	roundTraceID := fmt.Sprintf("RT_%d_%d", sessionID, roundNo)

	deductReq := &settlementDto.SystemPacketDeductRequest{
		RoomID:       roomIDInt,
		SessionID:    sessionID,
		RoundID:      round.RoundID,
		RoundNo:      roundNo,
		TotalAmount:  totalAmount,
		RoundTraceID: roundTraceID,
		Reason:       reason,
	}

	err = s.deductSvc.DeductForSystemPacket(ctx, deductReq)
	if err != nil {
		if updateErr := s.updateRoundFailed(ctx, round.RoundID, fmt.Sprintf("deduct failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", round.RoundID, "error", updateErr)
		}
		logger.Error("system packet deduct failed", "room_id", roomID, "round_id", round.RoundID, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	if err := s.updateRoundDeductSuccess(ctx, round.RoundID, settlementDto.DeductSceneSystemPacket, 1, totalAmount, roundTraceID); err != nil {
		logger.Error("update round deduct info failed", "round_id", round.RoundID, "error", err)
	}

	if err := s.dbRepo.RoundDBRepo().UpdateRoundSender(ctx, round.RoundID, 0, "system"); err != nil {
		logger.Error("update round sender failed", "round_id", round.RoundID, "error", err)
	}

	commission := s.commissionCfg.Calculate(totalAmount)
	if err := s.dbRepo.RoundDBRepo().UpdateRoundAmount(ctx, round.RoundID, totalAmount, commission); err != nil {
		logger.Error("update round amount failed", "round_id", round.RoundID, "error", err)
	}

	return &initRoundResult{
		RoundID:      round.RoundID,
		RoundNo:      roundNo,
		SessionID:    sessionID,
		RoomID:       roomIDInt,
		SenderID:     0,
		SenderType:   "system",
		TotalAmount:  totalAmount,
		Commission:   commission,
		DeductScene:  settlementDto.DeductSceneSystemPacket,
		DeductAmount: totalAmount,
		BatchID:      roundTraceID,
	}, nil
}
