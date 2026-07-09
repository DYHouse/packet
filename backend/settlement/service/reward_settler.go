package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
)

type RewardSettler struct {
	billRepo     repository.BillRepository
	traceIDGen   *TraceIDGenerator
	robotChecker RobotChecker
}

func NewRewardSettler(billRepo repository.BillRepository, traceIDGen *TraceIDGenerator, robotChecker RobotChecker) *RewardSettler {
	return &RewardSettler{
		billRepo:     billRepo,
		traceIDGen:   traceIDGen,
		robotChecker: robotChecker,
	}
}

// SettleReward 创建系统奖励 BillRecord。tx 由 SettleRound 从 AppService 事务回调传入，
// 所有 DB 操作纳入同一事务。
func (s *RewardSettler) SettleReward(ctx context.Context, tx repository.Transaction, settlement *domain.RoundSettlement, players []*dto.PlayerSettleInfo) error {
	if settlement.RewardType == 0 || settlement.RewardAmount == 0 {
		return nil
	}

	playerCount := int64(len(players))
	if playerCount == 0 {
		return nil
	}

	billRepo := tx.BillRepo()

	// 幂等检查：若平台支出 bill 已存在且成功，视为已结算，直接返回 nil。
	// 防止 Kafka 重试时 SettleRound 上抛 err 触发重试，导致 reward bills 被重复创建。
	// 与 creditRound 中 GetBillByRoundTypeAndUser 跳过已成功 grab bill 的模式一致。
	existingBill, err := billRepo.GetBillByRoundTypeAndUser(ctx, settlement.RoundID, domain.BillTypeSystemReward, dto.PlatformAccountID)
	if err == nil && existingBill != nil && existingBill.Status == domain.BillStatusSuccess {
		return nil
	}

	totalDeductAmount := settlement.RewardAmount * playerCount

	allBills := make([]*domain.BillRecord, 0, 1+int(playerCount))

	platformBill := &domain.BillRecord{
		RoundTraceID: settlement.RoundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(settlement.RoundTraceID, domain.BillTypeSystemReward, dto.PlatformAccountID),
		BillType:     domain.BillTypeSystemReward,
		RoomID:       settlement.RoomID,
		SessionID:    settlement.SessionID,
		RoundID:      settlement.RoundID,
		RoundNo:      settlement.RoundNo,
		UserID:       dto.PlatformAccountID,
		Amount:       -totalDeductAmount,
		Status:       domain.BillStatusSuccess,
		Remark:       fmt.Sprintf("系统奖励支出,类型:%d,局ID:%d,玩家数:%d,每人金额:%d", settlement.RewardType, settlement.RoundID, playerCount, settlement.RewardAmount),
	}
	allBills = append(allBills, platformBill)

	for _, player := range players {
		playerBill := &domain.BillRecord{
			RoundTraceID: settlement.RoundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(settlement.RoundTraceID, domain.BillTypeSystemReward, player.UserID),
			BillType:     domain.BillTypeSystemReward,
			RoomID:       settlement.RoomID,
			SessionID:    settlement.SessionID,
			RoundID:      settlement.RoundID,
			RoundNo:      settlement.RoundNo,
			UserID:       player.UserID,
			Amount:       settlement.RewardAmount,
			Status:       domain.BillStatusSuccess,
			Remark:       fmt.Sprintf("系统奖励收入(待会话级入账),类型:%d,局ID:%d", settlement.RewardType, settlement.RoundID),
		}
		if s.robotChecker == nil {
			return fmt.Errorf("robot checker is nil")
		}
		isRobot, err := s.robotChecker.IsRobot(ctx, player.UserID)
		if err != nil {
			return fmt.Errorf("check robot failed: %w", err)
		}
		playerBill.IsRobot = isRobot
		allBills = append(allBills, playerBill)
	}

	return billRepo.CreateBills(ctx, allBills)
}
