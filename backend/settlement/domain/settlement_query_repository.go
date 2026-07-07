package domain

import "context"

// SettlementQueryRepository 结算聚合查询仓储接口（只读）。
// 收敛所有按会话聚合的只读查询方法，与 BillRecord/RoundSettlement 的 CRUD 仓储职责分离。
// 接口方法签名与原 BillManager 中对应方法完全一致，仅做位置迁移。
type SettlementQueryRepository interface {
	// AggregateBetBySession 按玩家聚合该会话的扣款金额（amount < 0 的账单），返回 map[userID]abs(sum(amount))
	AggregateBetBySession(ctx context.Context, sessionID int64) (map[int64]int64, error)
	// AggregatePayOutBySession 按玩家聚合该会话的入账金额（amount > 0 的账单），返回 map[userID]sum(amount)
	AggregatePayOutBySession(ctx context.Context, sessionID int64) (map[int64]int64, error)
	// IsPlayerGameSettled 检查指定玩家在指定会话中是否已完成游戏级结算（用于幂等检查）
	IsPlayerGameSettled(ctx context.Context, sessionID int64, userID int64) (bool, error)
	// GetUnsettledUsersBySession 查询会话中未完成游戏级结算的玩家列表
	GetUnsettledUsersBySession(ctx context.Context, sessionID int64) ([]int64, error)
}
