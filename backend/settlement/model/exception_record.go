package model

import "time"

type ExceptionType int

const (
	ExceptionTypeDebitFailed         ExceptionType = 1
	ExceptionTypeCreditRetryExceed   ExceptionType = 2
	ExceptionTypeDeductedNotSettled  ExceptionType = 3
)

type ExceptionStatus int

const (
	ExceptionStatusPending    ExceptionStatus = 0
	ExceptionStatusProcessing ExceptionStatus = 1
	ExceptionStatusResolved   ExceptionStatus = 2
	ExceptionStatusIgnored    ExceptionStatus = 3
)

type HandleType int

const (
	HandleTypeManual HandleType = 1
	HandleTypeRefund HandleType = 2
	HandleTypeRetry  HandleType = 3
	HandleTypeIgnore HandleType = 4
)

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
