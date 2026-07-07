package mysql

import (
	"context"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// settlementQueryRepository 实现 domain.SettlementQueryRepository 接口，提供按会话聚合的只读查询。
// 代码由 BillManager 迁移而来，逻辑保持一致。
type settlementQueryRepository struct {
	db *gorm.DB
}

// NewSettlementQueryRepository 创建 SettlementQueryRepository 实例，返回接口类型。
func NewSettlementQueryRepository(db *gorm.DB) domain.SettlementQueryRepository {
	return &settlementQueryRepository{db: db}
}

// 编译期断言：确保 settlementQueryRepository 实现 domain.SettlementQueryRepository 接口。
var _ domain.SettlementQueryRepository = (*settlementQueryRepository)(nil)

// AggregateBetBySession 按玩家聚合该游戏的扣款金额（amount < 0 的 Bill），返回 map[userID]abs(sum(amount))
func (m *settlementQueryRepository) AggregateBetBySession(ctx context.Context, sessionID int64) (map[int64]int64, error) {
	type result struct {
		UserID      int64
		TotalAmount int64
	}
	var results []result
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Select("user_id, SUM(ABS(amount)) as total_amount").
		Where("session_id = ? AND amount < 0 AND status = ? AND user_id != ?",
			sessionID, dto.BillStatusSuccess, dto.PlatformAccountID).
		Group("user_id").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	resultMap := make(map[int64]int64, len(results))
	for _, r := range results {
		resultMap[r.UserID] = r.TotalAmount
	}
	return resultMap, nil
}

// AggregatePayOutBySession 按玩家聚合该游戏的入账金额（amount > 0 的 Bill），返回 map[userID]sum(amount)
func (m *settlementQueryRepository) AggregatePayOutBySession(ctx context.Context, sessionID int64) (map[int64]int64, error) {
	type result struct {
		UserID      int64
		TotalAmount int64
	}
	var results []result
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Select("user_id, SUM(amount) as total_amount").
		Where("session_id = ? AND amount > 0 AND status = ? AND user_id != ? AND bill_type != ?",
			sessionID, dto.BillStatusSuccess, dto.PlatformAccountID, dto.BillTypeSessionCredit).
		Group("user_id").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	resultMap := make(map[int64]int64, len(results))
	for _, r := range results {
		resultMap[r.UserID] = r.TotalAmount
	}
	return resultMap, nil
}

// IsPlayerGameSettled 检查指定玩家在指定会话中是否已完成游戏级结算
// 用于 settlePlayer 幂等检查：已 Settled 的玩家不再重复调用 platform.Settle
func (m *settlementQueryRepository) IsPlayerGameSettled(ctx context.Context, sessionID int64, userID int64) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("session_id = ? AND user_id = ? AND game_settle_status = ?",
			sessionID, userID, dto.BillGameSettleSettled).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetUnsettledUsersBySession 查询游戏中未完成游戏级结算的玩家列表
func (m *settlementQueryRepository) GetUnsettledUsersBySession(ctx context.Context, sessionID int64) ([]int64, error) {
	var userIDs []int64
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Select("DISTINCT user_id").
		Where("session_id = ? AND status = ? AND game_settle_status = ? AND user_id != ?",
			sessionID, dto.BillStatusSuccess, dto.BillGameSettleNone, dto.PlatformAccountID).
		Find(&userIDs).Error
	return userIDs, err
}
