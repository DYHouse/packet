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
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	settlementDto "github.com/cashparty/backend/settlement/dto"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type PenaltyService struct {
	redis             *cRedis.Client
	policy            *domain.PenaltyPolicy
	settlementService *settlementService.SettlementService
	redisTTL          config.RedisTTLConfig
}

func NewPenaltyService(redis *cRedis.Client, policy *domain.PenaltyPolicy, settlementService *settlementService.SettlementService, redisTTL config.RedisTTLConfig) *PenaltyService {
	if policy == nil {
		policy = domain.DefaultPenaltyPolicy()
	}
	return &PenaltyService{
		redis:             redis,
		policy:            policy,
		settlementService: settlementService,
		redisTTL:          redisTTL,
	}
}

func (s *PenaltyService) ApplyPenalty(ctx context.Context, roomID, userID string, penaltyType domain.PenaltyType, roomFee int64, sessionID int64, currentRound int) (*domain.PenaltyResult, error) {
	keys := []string{
		redis.PenaltyCountKey(roomID, userID),
		redis.RoomHashKey(roomID),
		redis.RoomPlayersKey(roomID),
		redis.RoundStateKey(roomID),
		redis.PenaltyRecordKey(roomID, userID),
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
		RoundNo:     currentRound,
		UserID:      userIDInt,
		Amount:      amount,
		PenaltyType: penaltyType.String(),
	}

	var deductErr error
	if err := s.settlementService.DeductPenaltyToPlatform(ctx, deductReq); err != nil {
		logger.Error("deduct penalty to platform failed",
			"room_id", roomID,
			"user_id", userID,
			"error", err)
		deductErr = err
	}

	logger.Info("penalty applied",
		"room_id", roomID,
		"user_id", userID,
		"penalty_type", penaltyType,
		"count", count,
		"amount", amount,
		"kick_required", kickRequired,
	)

	return &domain.PenaltyResult{
		Applied:      true,
		Amount:       amount,
		Count:        count,
		KickRequired: kickRequired,
		Reason:       penaltyType.String(),
		DeductFailed: deductErr != nil,
		DeductError:  deductErr,
	}, nil
}

func (s *PenaltyService) DistributePenalty(ctx context.Context, roomID string, penaltyAmount int64, excludeUserIDs []string) (*domain.PenaltyDistribution, error) {
	keys := []string{
		redis.RoomHashKey(roomID),
		redis.RoomPlayersKey(roomID),
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

	return &domain.PenaltyDistribution{
		RoomID:        roomID,
		TotalAmount:   penaltyAmount,
		ShareAmount:   shareAmount,
		Recipients:    recipients,
		ExcludeUsers:  excludeUserIDs,
		DistributedAt: time.Now().Unix(),
	}, nil
}
