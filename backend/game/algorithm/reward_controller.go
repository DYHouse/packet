package algorithm

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

const (
	TriggerTypeGuarantee   = 1
	TriggerTypeProbability = 2
)

type RewardController struct {
	config *RewardControlConfig
	redis  *cRedis.Client
	rngMu  sync.Mutex
}

func NewRewardController(config *RewardControlConfig, redis *cRedis.Client) *RewardController {
	return &RewardController{
		config: config,
		redis:  redis,
	}
}

func (c *RewardController) DetermineRewardType(
	ctx context.Context,
	roomID string,
	sessionID string,
	currentRoundNo int,
	maxRounds int,
) (int, int) {
	roomConfig := c.getRoomConfig(roomID)
	if roomConfig == nil {
		logger.Info("no room config found", "room_id", roomID, "available_configs", len(c.config.RoomConfigs))
		return RewardTypeNone, 0
	}

	logger.Info("checking reward", "room_id", roomID, "session_id", sessionID, "round", currentRoundNo, "max_rounds", maxRounds, "guarantee_enabled", roomConfig.GuaranteeEnabled)

	if roomConfig.GuaranteeEnabled {
		if rewardType := c.checkGuarantee(
			ctx, roomID, sessionID, currentRoundNo, maxRounds, roomConfig,
		); rewardType != RewardTypeNone {
			logger.Info("guarantee reward triggered", "room_id", roomID, "reward_type", rewardType)
			return rewardType, TriggerTypeGuarantee
		}
	}

	if roomConfig.ProbabilityEnabled && c.isProbabilityAllowed(ctx) {
		if rewardType := c.checkProbability(roomConfig); rewardType != RewardTypeNone {
			return rewardType, TriggerTypeProbability
		}
	}

	return RewardTypeNone, 0
}

func (c *RewardController) checkGuarantee(
	ctx context.Context,
	roomID, sessionID string,
	currentRoundNo, maxRounds int,
	config *RoomRewardConfig,
) int {
	remainingRounds := maxRounds - currentRoundNo + 1

	logger.Info("checkGuarantee", "room_id", roomID, "session_id", sessionID, "remaining_rounds", remainingRounds, "guarantee_straight", config.GuaranteeStraight, "guarantee_leopard", config.GuaranteeLeopard)

	if config.GuaranteeStraight {
		straightKey := redis.RewardCycleStraightKey(roomID, sessionID)
		straightWon, _ := c.redis.Get(ctx, straightKey).Int()

		logger.Info("straight check", "straight_key", straightKey, "straight_won", straightWon)

		if straightWon == 0 {
			if c.shouldTriggerGuarantee(remainingRounds) {
				c.redis.Set(ctx, straightKey, 1, 24*time.Hour)
				logger.Info("straight guarantee triggered", "room_id", roomID, "remaining_rounds", remainingRounds)
				return RewardTypeStraight
			}
		}
	}

	if config.GuaranteeLeopard {
		leopardKey := redis.RewardCycleLeopardKey(roomID, sessionID)
		leopardWon, _ := c.redis.Get(ctx, leopardKey).Int()

		logger.Info("leopard check", "leopard_key", leopardKey, "leopard_won", leopardWon)

		if leopardWon == 0 {
			if c.shouldTriggerGuarantee(remainingRounds) {
				c.redis.Set(ctx, leopardKey, 1, 24*time.Hour)
				logger.Info("leopard guarantee triggered", "room_id", roomID, "remaining_rounds", remainingRounds)
				return RewardTypeLeopard
			}
		}
	}

	return RewardTypeNone
}

func (c *RewardController) shouldTriggerGuarantee(remainingRounds int) bool {
	if remainingRounds <= 1 {
		logger.Info("shouldTriggerGuarantee: last round, must trigger", "remaining_rounds", remainingRounds)
		return true
	}

	prob := 1.0 / float64(remainingRounds)
	randVal := c.randomFloat()
	triggered := randVal < prob
	logger.Info("shouldTriggerGuarantee: probability check", "remaining_rounds", remainingRounds, "probability", prob, "random_value", randVal, "triggered", triggered)
	return triggered
}

func (c *RewardController) isProbabilityAllowed(ctx context.Context) bool {
	if c.config == nil || !c.config.GlobalSwitchEnabled {
		return false
	}

	if c.config.ProfitRatioThreshold > 0 {
		currentRatio, err := c.GetCurrentProfitRatio(ctx)
		if err != nil || currentRatio < c.config.ProfitRatioThreshold {
			return false
		}
	}

	return true
}

func (c *RewardController) checkProbability(config *RoomRewardConfig) int {
	randVal := c.randomFloat()

	if randVal < config.LeopardProbability {
		return RewardTypeLeopard
	}

	randVal -= config.LeopardProbability
	if randVal < config.StraightProbability {
		return RewardTypeStraight
	}

	return RewardTypeNone
}

func (c *RewardController) GetCurrentProfitRatio(ctx context.Context) (float64, error) {
	today := time.Now().Format("2006-01-02")
	key := redis.ProfitDailyKey(today)

	data, err := c.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return 0, err
	}

	var totalBet, totalWin, totalReward int64
	if v, ok := data["total_bet"]; ok {
		fmt.Sscanf(v, "%d", &totalBet)
	}
	if v, ok := data["total_win"]; ok {
		fmt.Sscanf(v, "%d", &totalWin)
	}
	if v, ok := data["total_reward"]; ok {
		fmt.Sscanf(v, "%d", &totalReward)
	}

	if totalBet == 0 {
		return 0, nil
	}

	profit := totalBet - totalWin - totalReward
	return float64(profit) / float64(totalBet), nil
}

func (c *RewardController) RecordProfit(ctx context.Context, betAmount, winAmount, rewardAmount int64) error {
	today := time.Now().Format("2006-01-02")
	key := redis.ProfitDailyKey(today)

	pipe := c.redis.Pipeline()
	pipe.HIncrBy(ctx, key, "total_bet", betAmount)
	pipe.HIncrBy(ctx, key, "total_win", winAmount)
	pipe.HIncrBy(ctx, key, "total_reward", rewardAmount)
	pipe.Expire(ctx, key, 7*24*time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

func (c *RewardController) OnSessionEnd(ctx context.Context, roomID, sessionID string) {
	c.redis.Del(ctx,
		redis.RewardCycleStraightKey(roomID, sessionID),
		redis.RewardCycleLeopardKey(roomID, sessionID),
	)
}

func (c *RewardController) getRoomConfig(roomID string) *RoomRewardConfig {
	if c.config == nil || c.config.RoomConfigs == nil {
		return nil
	}
	roomIDInt64 := converter.ParseInt64(roomID)
	return c.config.RoomConfigs[roomIDInt64]
}

func (c *RewardController) randomFloat() float64 {
	c.rngMu.Lock()
	defer c.rngMu.Unlock()

	n, _ := crand.Int(crand.Reader, big.NewInt(1000000))
	return float64(n.Int64()) / 1000000.0
}
