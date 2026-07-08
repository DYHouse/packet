package domain

import "time"

// RefundStatus 退款状态枚举值，表示退款审核的生命周期阶段。
// 状态流转：
//
//	None(0) → Pending(1)
//	Pending(1) → Processing(5) / Rejected(4)
//	Processing(5) → Refunded(3) / Pending(1, 重试回退)
//	Refunded(3) / Rejected(4) 为终态。
const (
	RefundStatusNone       = 0
	RefundStatusPending    = 1
	RefundStatusApproved   = 2
	RefundStatusRefunded   = 3
	RefundStatusRejected   = 4
	RefundStatusProcessing = 5
)

// RefundType 退款类型枚举值，标识退款的业务触发原因。
const (
	RefundTypeFirstRoundFail = 1
	RefundTypeOther          = 2
)

// RefundAudit 退款审核聚合根，表示一笔退款的审核与执行记录。
// 纯领域类型，无 GORM tag 与 TableName 方法；持久化由 model.RefundAudit 承载，
// Repository 实现层负责 domain ↔ model 转换。
type RefundAudit struct {
	ID              int64
	RefundOrderNo   string
	RoundTraceID    string
	BatchID         string
	RoomID          int64
	SessionID       int64
	RoundID         int64
	UserID          int64
	BillID          int64
	BillOrderNo     string
	RefundAmount    int64
	RefundReason    string
	RefundType      int
	Status          int
	AppliedAt       time.Time
	AppliedBy       int64
	ApprovedAt      *time.Time
	ApprovedBy      int64
	ApproveRemark   string
	RefundedAt      *time.Time
	PlatformTransID string
	ErrorMessage    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CanApprove 校验当前退款审核是否可执行审批操作。
// 仅 Pending 状态的退款审核允许审批；其他状态拒绝。
func (r *RefundAudit) CanApprove() bool {
	return r.Status == RefundStatusPending
}

// CanRetry 校验当前退款审核是否可重试（回退到 Pending）。
// 仅 Processing 状态且执行失败的退款审核允许重试回退。
func (r *RefundAudit) CanRetry() bool {
	return r.Status == RefundStatusProcessing
}

// IsTerminalStatus 校验当前退款审核是否处于终态（不再发生状态流转）。
// Refunded 与 Rejected 为终态。
func (r *RefundAudit) IsTerminalStatus() bool {
	return r.Status == RefundStatusRefunded || r.Status == RefundStatusRejected
}
