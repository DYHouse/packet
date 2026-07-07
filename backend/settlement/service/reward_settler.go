package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

type RewardSettlementConfig struct {
	Enabled                  bool    `yaml:"enabled"`
	StraightRewardMultiplier float64 `yaml:"straight_reward_multiplier"`
	LeopardRewardMultiplier  float64 `yaml:"leopard_reward_multiplier"`
}

func DefaultRewardSettlementConfig() *RewardSettlementConfig {
	return &RewardSettlementConfig{
		Enabled:                  true,
		StraightRewardMultiplier: 1.0,
		LeopardRewardMultiplier:  10.0,
	}
}

type RewardSettler struct {
	config       *RewardSettlementConfig
	billRepo     domain.BillRepository
	traceIDGen   *TraceIDGenerator
	robotChecker RobotChecker
}

func NewRewardSettler(rewardCfg *RewardSettlementConfig, billRepo domain.BillRepository, traceIDGen *TraceIDGenerator, robotChecker RobotChecker) *RewardSettler {
	return &RewardSettler{
		config:       rewardCfg,
		billRepo:     billRepo,
		traceIDGen:   traceIDGen,
		robotChecker: robotChecker,
	}
}

func (s *RewardSettler) CalculateRewardAmount(rewardType int, totalAmount int64) int64 {
	if !s.config.Enabled {
		return 0
	}

	switch rewardType {
	case 1:
		return int64(float64(totalAmount) * s.config.StraightRewardMultiplier)
	case 2:
		return int64(float64(totalAmount) * s.config.LeopardRewardMultiplier)
	default:
		return 0
	}
}

func (s *RewardSettler) SettleReward(ctx context.Context, settlement *model.RoundSettlement, players []*dto.PlayerSettleInfo) error {
	if settlement.RewardType == 0 || settlement.RewardAmount == 0 {
		return nil
	}

	playerCount := int64(len(players))
	if playerCount == 0 {
		return nil
	}

	// 幂等检查：若平台支出 bill 已存在且成功，视为已结算，直接返回 nil。
	// 防止 Kafka 重试时 SettleRound 上抛 err 触发重试，导致 reward bills 被重复创建。
	// 与 creditRound 中 GetBillByRoundTypeAndUser 跳过已成功 grab bill 的模式一致。
	existingBill, err := s.billRepo.GetBillByRoundTypeAndUser(ctx, settlement.RoundID, dto.BillTypeSystemReward, dto.PlatformAccountID)
	if err == nil && existingBill != nil && existingBill.Status == dto.BillStatusSuccess {
		return nil
	}

	totalDeductAmount := settlement.RewardAmount * playerCount

	allBills := make([]*model.BillRecord, 0, 1+int(playerCount))

	platformBill := &model.BillRecord{
		RoundTraceID: settlement.RoundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(settlement.RoundTraceID, dto.BillTypeSystemReward, dto.PlatformAccountID),
		BillType:     dto.BillTypeSystemReward,
		RoomID:       settlement.RoomID,
		SessionID:    settlement.SessionID,
		RoundID:      settlement.RoundID,
		RoundNo:      settlement.RoundNo,
		UserID:       dto.PlatformAccountID,
		Amount:       -totalDeductAmount,
		Status:       dto.BillStatusSuccess,
		Remark:       fmt.Sprintf("系统奖励支出,类型:%d,局ID:%d,玩家数:%d,每人金额:%d", settlement.RewardType, settlement.RoundID, playerCount, settlement.RewardAmount),
	}
	allBills = append(allBills, platformBill)

	for _, player := range players {
		playerBill := &model.BillRecord{
			RoundTraceID: settlement.RoundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(settlement.RoundTraceID, dto.BillTypeSystemReward, player.UserID),
			BillType:     dto.BillTypeSystemReward,
			RoomID:       settlement.RoomID,
			SessionID:    settlement.SessionID,
			RoundID:      settlement.RoundID,
			RoundNo:      settlement.RoundNo,
			UserID:       player.UserID,
			Amount:       settlement.RewardAmount,
			Status:       dto.BillStatusSuccess,
			Remark:       fmt.Sprintf("系统奖励收入(待会话级入账),类型:%d,局ID:%d", settlement.RewardType, settlement.RoundID),
		}
		playerBill.IsRobot = s.robotChecker != nil && s.robotChecker.IsRobot(ctx, player.UserID)
		allBills = append(allBills, playerBill)
	}

	return s.billRepo.CreateBillsInTransaction(ctx, allBills)
}
