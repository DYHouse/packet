package domain

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/model"
)

// RoundSettlementRepository 回合结算仓储接口，负责 RoundSettlement 的 CRUD 与状态机更新。
// 接口方法签名与原 BillManager 中对应方法完全一致，仅做位置迁移。
type RoundSettlementRepository interface {
	// ExistsRoundSettlement 检查指定回合的结算记录是否已存在（幂等检查）
	ExistsRoundSettlement(ctx context.Context, roundID int64) (bool, error)
	// UpdateRoundSettlementCredited 乐观锁更新回合结算为 Credited 终态
	UpdateRoundSettlementCredited(ctx context.Context, traceID string, settleAmount int64, settleUserCount int, settledAt *time.Time) error
	// UpdateRoundSettlementStatus 乐观锁更新回合结算状态（已是 Credited 终态则拒绝覆盖）
	UpdateRoundSettlementStatus(ctx context.Context, traceID string, status int, errMsg string) error
	// GetRoundSettlementByRoundID 根据回合 id 查询结算记录
	GetRoundSettlementByRoundID(ctx context.Context, roundID int64) (*model.RoundSettlement, error)
	// CreateRoundSettlementAndBills 在单事务内创建回合结算与配对账单
	CreateRoundSettlementAndBills(ctx context.Context, settlement *model.RoundSettlement, bills []*model.BillRecord) error
	// UpdateRoundSettlementDeductSuccess 乐观锁更新扣款成功计数与时间（deducted_at 未设置时才更新）
	UpdateRoundSettlementDeductSuccess(ctx context.Context, roundTraceID string, successCount int, deductedAt time.Time) error
	// UpdateRoundSettlementSettleInfo 乐观锁填充结算信息（sender_id = 0 时才更新）
	UpdateRoundSettlementSettleInfo(ctx context.Context, roundTraceID string, senderID int64, senderType string, totalAmount, commission int64, playerCount int, minPlayerID int64) error
	// GetFailedFirstRoundSettlements 查询首回合扣款失败的结算记录（用于补偿）
	GetFailedFirstRoundSettlements(ctx context.Context, since time.Time, limit int) ([]*model.RoundSettlement, error)
	// GetDeductedButNotSettled 查询已扣款但未结算的记录（用于异常检测）
	GetDeductedButNotSettled(ctx context.Context, since time.Time, limit int) ([]*model.RoundSettlement, error)
	// GetAllRoundSettlementsBySession 查询该会话所有回合的结算记录（按 round_no 正序）
	GetAllRoundSettlementsBySession(ctx context.Context, sessionID int64) ([]*model.RoundSettlement, error)
	// UpdateGameSettleStatusBySession 乐观锁更新该会话全部回合结算的游戏级结算状态
	UpdateGameSettleStatusBySession(ctx context.Context, sessionID int64, fromStatus, toStatus int) error
	// GetFailedGameSettlements 查询游戏级结算失败的会话 id（用于重试）
	GetFailedGameSettlements(ctx context.Context, limit int) ([]int64, error)
	// GetTimedOutGameSettlements 查询超时未结算的会话 id（所有回合 Credited 但 GameSettleStatus 仍为 None 且超时）
	GetTimedOutGameSettlements(ctx context.Context, timeout time.Duration, limit int) ([]int64, error)
}
