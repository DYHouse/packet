package algorithm

import (
	"context"
	crand "crypto/rand"
	"math/big"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/game/domain"
)

type RewardController struct {
	config    *RewardControlConfig
	cacheRepo domain.RewardCacheRepository
}

func NewRewardController(config *RewardControlConfig, cacheRepo domain.RewardCacheRepository) *RewardController {
	return &RewardController{
		config:    config,
		cacheRepo: cacheRepo,
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
			return rewardType, domain.TriggerTypeGuarantee
		}
	}

	if roomConfig.ProbabilityEnabled && c.isProbabilityAllowed(ctx) {
		if rewardType := c.checkProbability(roomConfig); rewardType != RewardTypeNone {
			return rewardType, domain.TriggerTypeProbability
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
		straightWon, _ := c.cacheRepo.GetCycleWon(ctx, roomID, sessionID, domain.RewardCycleStraight)

		logger.Info("straight check", "straight_won", straightWon)

		if straightWon == 0 {
			if c.shouldTriggerGuarantee(remainingRounds) {
				c.cacheRepo.SetCycleWon(ctx, roomID, sessionID, domain.RewardCycleStraight, 24*time.Hour)
				logger.Info("straight guarantee triggered", "room_id", roomID, "remaining_rounds", remainingRounds)
				return domain.RewardTypeStraight
			}
		}
	}

	if config.GuaranteeLeopard {
		leopardWon, _ := c.cacheRepo.GetCycleWon(ctx, roomID, sessionID, domain.RewardCycleLeopard)

		logger.Info("leopard check", "leopard_won", leopardWon)

		if leopardWon == 0 {
			if c.shouldTriggerGuarantee(remainingRounds) {
				c.cacheRepo.SetCycleWon(ctx, roomID, sessionID, domain.RewardCycleLeopard, 24*time.Hour)
				logger.Info("leopard guarantee triggered", "room_id", roomID, "remaining_rounds", remainingRounds)
				return domain.RewardTypeLeopard
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
		return domain.RewardTypeLeopard
	}

	randVal -= config.LeopardProbability
	if randVal < config.StraightProbability {
		return domain.RewardTypeStraight
	}

	return RewardTypeNone
}

func (c *RewardController) GetCurrentProfitRatio(ctx context.Context) (float64, error) {
	today := time.Now().Format("2006-01-02")

	totalBet, totalWin, totalReward, err := c.cacheRepo.GetDailyProfit(ctx, today)
	if err != nil {
		return 0, err
	}

	if totalBet == 0 {
		return 0, nil
	}

	profit := totalBet - totalWin - totalReward
	return float64(profit) / float64(totalBet), nil
}

func (c *RewardController) RecordProfit(ctx context.Context, betAmount, winAmount, rewardAmount int64) error {
	today := time.Now().Format("2006-01-02")
	return c.cacheRepo.RecordDailyProfit(ctx, today, betAmount, winAmount, rewardAmount, 7*24*time.Hour)
}

func (c *RewardController) OnSessionEnd(ctx context.Context, roomID, sessionID string) {
	c.cacheRepo.ClearRewardCycles(ctx, roomID, sessionID)
}

func (c *RewardController) getRoomConfig(roomID string) *RoomRewardConfig {
	if c.config == nil || c.config.RoomConfigs == nil {
		return nil
	}
	roomIDInt64 := converter.ParseInt64(roomID)
	return c.config.RoomConfigs[roomIDInt64]
}

// randomFloat 使用 crypto/rand 生成随机数。crand.Reader 本身并发安全，无需额外加锁。
func (c *RewardController) randomFloat() float64 {
	n, _ := crand.Int(crand.Reader, big.NewInt(1000000))
	return float64(n.Int64()) / 1000000.0
}
