package domain

import (
	"errors"
	"fmt"
	"time"
)

// RefundStatus 退款状态枚举值，表示退款审核的生命周期阶段。
// 状态流转：
//
//	None(0) → Pending(1)
//	Pending(1) → Approved(2) / Rejected(4)
//	Approved(2) → Refunded(3) / Processing(5)（过渡态：被 UpdateRefundAuditStatus 设置后立即进入 executeRefund，不会持久停留）
//	Processing(5) → Refunded(3) / Pending(1)（RPC 失败回退）
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

// TransitionTo 校验状态转换的合法性，作为状态机守卫方法。
// 仅校验不修改状态；调用方校验通过后自行更新 Status 字段。
// 合法转换返回 nil，非法转换返回 error 描述当前状态与目标状态。
// 合法转换：
//
//	None(0) → Pending(1)
//	Pending(1) → Approved(2) / Rejected(4)
//	Approved(2) → Refunded(3) / Processing(5)（过渡态）
//	Processing(5) → Refunded(3) / Pending(1)（RPC 失败回退）
func (r *RefundAudit) TransitionTo(newStatus int) error {
	if r == nil {
		return errors.New("RefundAudit is nil")
	}
	allowed := false
	switch r.Status {
	case RefundStatusNone:
		allowed = newStatus == RefundStatusPending
	case RefundStatusPending:
		allowed = newStatus == RefundStatusApproved || newStatus == RefundStatusRejected
	case RefundStatusApproved:
		allowed = newStatus == RefundStatusRefunded || newStatus == RefundStatusProcessing
	case RefundStatusProcessing:
		allowed = newStatus == RefundStatusRefunded || newStatus == RefundStatusPending
	}
	if !allowed {
		return fmt.Errorf("invalid refund audit status transition: %d -> %d", r.Status, newStatus)
	}
	return nil
}
