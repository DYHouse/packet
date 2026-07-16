package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/round"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/model"
	settlementApplication "github.com/cashparty/backend/settlement/application"
	settlementDto "github.com/cashparty/backend/settlement/dto"
)

type PenaltyService struct {
	redis            cRedis.RedisClient
	policy           *round.PenaltyPolicy
	settleAppService *settlementApplication.SettleAppService
	redisTTL         config.RedisTTLConfig
	dbRepo           repository.DBRepository
}

func NewPenaltyService(redis cRedis.RedisClient, policy *round.PenaltyPolicy, settleAppService *settlementApplication.SettleAppService, redisTTL config.RedisTTLConfig, dbRepo repository.DBRepository) *PenaltyService {
	if policy == nil {
		policy = round.DefaultPenaltyPolicy()
	}
	return &PenaltyService{
		redis:            redis,
		policy:           policy,
		settleAppService: settleAppService,
		redisTTL:         redisTTL,
		dbRepo:           dbRepo,
	}
}

func (s *PenaltyService) ApplyPenalty(ctx context.Context, roomID, userID string, penaltyType round.PenaltyType, roomFee int64, sessionID int64, currentRound int, currentRoundID int64) (*round.PenaltyResult, error) {
	keys := []string{
		rediskeys.PenaltyCountKey(roomID, userID),
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoundStateKey(roomID),
		rediskeys.PenaltyRecordKey(roomID, userID),
	}

	args := []interface{}{
		userID,
		roomFee,
		time.Now().Unix(),
		penaltyType.String(),
		int64(s.redisTTL.PenaltyCountTTL.Seconds()),
		int64(s.redisTTL.PenaltyRecordTTL.Seconds()),
	}

	res, err := scripts.HandlePenalty.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("apply penalty lua failed", "error", err, "room_id", roomID, "user_id", userID)
		return nil, err
	}

	code := converter.ParseInt(res[0])
	if code != 0 {
		logger.Warn("handle penalty failed", "lua_code", code, "room_id", roomID, "user_id", userID)
		return nil, message.NewError(message.CodeSystemError)
	}

	count := converter.ParseInt(res[1])
	amount := converter.ParseInt64(res[2])
	kickRequired := converter.ParseInt(res[3]) == 1

	roomIDInt := converter.ParseID(roomID)
	userIDInt := converter.ParseID(userID)

	deductReq := &settlementDto.PenaltyDeductRequest{
		RoomID:      roomIDInt,
		SessionID:   sessionID,
		RoundID:     currentRoundID,
		RoundNo:     currentRound,
		UserID:      userIDInt,
		Amount:      amount,
		PenaltyType: penaltyType.String(),
	}

	var deductErr error
	if err := s.settleAppService.DeductPenaltyToPlatform(ctx, deductReq); err != nil {
		logger.Error("deduct penalty to platform failed",
			"room_id", roomID,
			"user_id", userID,
			"error", err)
		deductErr = err
	}

	// 同步持久化到 DB（与 DeductPenaltyToPlatform 调用后）
	penaltyRecord := &model.PenaltyRecord{
		RoomID:       roomIDInt,
		SessionID:    sessionID,
		RoundID:      currentRoundID,
		RoundNo:      currentRound,
		UserID:       userIDInt,
		PenaltyType:  penaltyType.String(),
		Amount:       amount,
		Count:        int(count),
		KickRequired: kickRequired,
		DeductStatus: model.PenaltyDeductProcessing,
	}
	if err := s.dbRepo.PenaltyRecordRepo().Create(ctx, penaltyRecord); err != nil {
		logger.Warn("persist penalty record failed",
			"room_id", roomID,
			"user_id", userID,
			"round_id", currentRoundID,
			"error", err)
		// 不阻塞主流程，记录已写入 Redis，后续对账补偿
	}

	logger.Info("penalty applied",
		"room_id", roomID,
		"user_id", userID,
		"penalty_type", penaltyType,
		"count", count,
		"amount", amount,
		"kick_required", kickRequired,
	)

	return &round.PenaltyResult{
		Applied:      true,
		Amount:       amount,
		Count:        count,
		KickRequired: kickRequired,
		Reason:       penaltyType.String(),
		DeductFailed: deductErr != nil,
		DeductError:  deductErr,
	}, nil
}

func (s *PenaltyService) DistributePenalty(ctx context.Context, roomID string, penaltyAmount int64, excludeUserIDs []string) (*round.PenaltyDistribution, error) {
	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
	}

	excludeJSON, _ := json.Marshal(excludeUserIDs)

	args := []interface{}{
		penaltyAmount,
		string(excludeJSON),
	}

	res, err := scripts.DistributePenalty.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("distribute penalty lua failed", "error", err, "room_id", roomID)
		return nil, err
	}

	code := converter.ParseInt(res[0])
	if code != 0 {
		logger.Warn("distribute penalty failed", "lua_code", code, "room_id", roomID)
		return nil, message.NewError(message.CodeSystemError)
	}

	shareAmount := converter.ParseInt64(res[1])
	recipientCount := converter.ParseInt(res[2])
	recipientsRaw := res[3]

	var recipients []string
	if arr, ok := recipientsRaw.([]interface{}); ok {
		for _, v := range arr {
			recipients = append(recipients, converter.ParseString(v))
		}
	}

	logger.Info("penalty distributed",
		"room_id", roomID,
		"penalty_amount", penaltyAmount,
		"share_amount", shareAmount,
		"recipient_count", recipientCount,
	)

	return &round.PenaltyDistribution{
		RoomID:        roomID,
		TotalAmount:   penaltyAmount,
		ShareAmount:   shareAmount,
		Recipients:    recipients,
		ExcludeUsers:  excludeUserIDs,
		DistributedAt: time.Now().Unix(),
	}, nil
}
