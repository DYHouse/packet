package model

import "time"

type RefundAudit struct {
	ID              int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	RefundOrderNo   string     `gorm:"uniqueIndex;size:64;not null" json:"refund_order_no"`
	RoundTraceID    string     `gorm:"index;size:64" json:"round_trace_id"`
	BatchID         string     `gorm:"index;size:32" json:"batch_id"`
	RoomID          int64      `gorm:"not null" json:"room_id"`
	SessionID       int64      `gorm:"not null" json:"session_id"`
	RoundID         int64      `gorm:"default:0" json:"round_id"`
	UserID          int64      `gorm:"not null" json:"user_id"`
	BillID          int64      `gorm:"not null;index" json:"bill_id"`
	BillOrderNo     string     `gorm:"size:64;not null" json:"bill_order_no"`
	RefundAmount    int64      `gorm:"not null" json:"refund_amount"`
	RefundReason    string     `gorm:"size:512;not null" json:"refund_reason"`
	RefundType      int        `gorm:"not null" json:"refund_type"`
	Status          int        `gorm:"default:0;index" json:"status"`
	AppliedAt       time.Time  `gorm:"not null" json:"applied_at"`
	AppliedBy       int64      `gorm:"default:0" json:"applied_by"`
	ApprovedAt      *time.Time `json:"approved_at"`
	ApprovedBy      int64      `gorm:"default:0" json:"approved_by"`
	ApproveRemark   string     `gorm:"size:256" json:"approve_remark"`
	RefundedAt      *time.Time `json:"refunded_at"`
	PlatformTransID string     `gorm:"size:64" json:"platform_trans_id"`
	ErrorMessage    string     `gorm:"size:512" json:"error_message"`
	CreatedAt       time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (RefundAudit) TableName() string {
	return "refund_audit"
}
