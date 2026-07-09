package model

import (
	"time"

	"github.com/cashparty/backend/settlement/domain"
)

// ExceptionType 异常类型枚举（type alias 引用 domain 层定义，反转依赖方向）。
type ExceptionType = domain.ExceptionType

// ExceptionStatus 异常处理状态枚举（type alias 引用 domain 层定义）。
type ExceptionStatus = domain.ExceptionStatus

// HandleType 异常处理方式枚举（type alias 引用 domain 层定义）。
type HandleType = domain.HandleType

type ExceptionRecord struct {
	ID              int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	ExceptionNo     string          `gorm:"uniqueIndex;size:32;not null" json:"exception_no"`
	ExceptionType   ExceptionType   `gorm:"not null;index" json:"exception_type"`
	BillID          int64           `gorm:"index" json:"bill_id"`
	RoundTraceID    string          `gorm:"index;size:64" json:"round_trace_id"`
	RoundID         int64           `gorm:"index" json:"round_id"`
	BillType        int             `gorm:"index" json:"bill_type"`
	UserID          int64           `gorm:"index" json:"user_id"`
	Amount          int64           `json:"amount"`
	Status          ExceptionStatus `gorm:"default:0;index" json:"status"`
	ExceptionDetail string          `gorm:"type:text" json:"exception_detail"`
	HandleType      HandleType      `gorm:"default:0" json:"handle_type"`
	HandleRemark    string          `gorm:"size:512" json:"handle_remark"`
	HandledAt       *time.Time      `json:"handled_at"`
	HandledBy       int64           `gorm:"default:0" json:"handled_by"`
	CreatedAt       time.Time       `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time       `gorm:"autoUpdateTime" json:"updated_at"`
}

func (ExceptionRecord) TableName() string {
	return "exception_record"
}
