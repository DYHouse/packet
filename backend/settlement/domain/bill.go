package domain

import "time"

// BillType 账单类型枚举值，标识账单的业务来源。
// 以下常量为 untyped int，确保与原 dto 层常量完全兼容。
const (
	BillTypeFirstRoundDeduct  = 2
	BillTypeGrabPacket        = 3
	BillTypeLaterRoundDeduct  = 4
	BillTypeCommission        = 7
	BillTypePenaltyIncome     = 8
	BillTypeSystemPacket      = 9
	BillTypePenaltyDistribute = 10
	BillTypeSystemReward      = 11
	BillTypeSessionCredit     = 12
	BillTypeGameSettle        = 13
)

// BillStatus 账单状态枚举值，表示账单的生命周期阶段。
// 状态流转：Processing(0) → Success(1) / Failed(2)；Success(1) → Refunded(3)。
const (
	BillStatusProcessing = 0
	BillStatusSuccess    = 1
	BillStatusFailed     = 2
	BillStatusRefunded   = 3
)

// DeductScene 扣款场景枚举值，标识触发扣款的游戏阶段。
const (
	DeductSceneFirstRoundShare = 1
	DeductSceneLaterRoundMin   = 2
	DeductSceneSystemPacket    = 3
)

// ReconcileStatus 对账状态枚举值，标识账单的对账结果。
const (
	ReconcileStatusPending  = 0
	ReconcileStatusSuccess  = 1
	ReconcileStatusAbnormal = 2
)

// ReconcileType 对账类型枚举值，标识对账的触发方式。
const (
	ReconcileTypeScheduled = 1
	ReconcileTypeAbnormal  = 2
	ReconcileTypeManual    = 3
)

// ReconcileScope 对账范围枚举值，标识对账的粒度。
const (
	ReconcileScopeSession = 1
	ReconcileScopeRound   = 2
	ReconcileScopeBill    = 3
)

// GameSettleStatus 游戏级结算状态枚举值（记录在 RoundSettlement 上）。
const (
	GameSettleStatusNone     = 0
	GameSettleStatusSettling = 1
	GameSettleStatusSuccess  = 2
	GameSettleStatusFailed   = 3
)

// BillGameSettleStatus 单笔 Bill 的游戏级结算标记枚举值。
const (
	BillGameSettleNone       = 0
	BillGameSettleSettled    = 1
	BillGameSettleProcessing = 2
)

// BillRecord 账单聚合根，表示一笔不可变的核心账务记录。
// 纯领域类型，无 GORM tag 与 TableName 方法；持久化由 model.BillRecord 承载，
// Repository 实现层负责 domain ↔ model 转换。
type BillRecord struct {
	ID               int64
	RoundTraceID     string
	BizOrderNo       string
	PlatformTransID  string
	BillType         int
	DeductScene      int
	RoomID           int64
	SessionID        int64
	RoundID          int64
	RoundNo          int
	UserID           int64
	BatchID          string
	Amount           int64
	BalanceBefore    int64
	BalanceAfter     int64
	Status           int
	ReconcileStatus  int
	RefundStatus     int
	RefundOrderNo    string
	RefundAmount     int64
	RefundReason     string
	RefundAppliedAt  *time.Time
	RefundApprovedAt *time.Time
	RefundApprovedBy int64
	RetryCount       int
	NextRetryAt      *time.Time
	ErrorCode        string
	ErrorMessage     string
	ExceptionID      int64
	GameSettleStatus int
	GameSettledAt    *time.Time
	Remark           string
	IsRobot          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CanRefund 校验当前账单是否可发起退款。
// 仅 Success 状态的账单允许退款；Processing/Failed/Refunded 状态拒绝退款。
func (b *BillRecord) CanRefund() bool {
	return b.Status == BillStatusSuccess
}

// IsTerminalStatus 校验当前账单是否处于终态（不再发生状态流转）。
// Refunded 为终态；Processing/Success/Failed 均非终态（Success 可退款，Failed 可重试）。
func (b *BillRecord) IsTerminalStatus() bool {
	return b.Status == BillStatusRefunded
}
