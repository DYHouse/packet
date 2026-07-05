package model

import "time"

type PlatformCallLog struct {
	ID           int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	CallType     string     `gorm:"size:20;index;not null" json:"call_type"` // debit / credit / settle
	BizOrderNo   string     `gorm:"index;size:128;not null" json:"biz_order_no"`
	RequestBody  string     `gorm:"type:text" json:"request_body"`
	ResponseBody string     `gorm:"type:text" json:"response_body"`
	Status       int        `gorm:"default:0;index" json:"status"` // 0=pending 1=success 2=failed
	ErrorMessage string     `gorm:"size:1024" json:"error_message"`
	RetryCount   int        `gorm:"default:0" json:"retry_count"`
	RequestTime  time.Time  `gorm:"not null" json:"request_time"`
	ResponseTime *time.Time `json:"response_time"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (PlatformCallLog) TableName() string {
	return "platform_call_log"
}

const (
	CallLogStatusPending = 0
	CallLogStatusSuccess = 1
	CallLogStatusFailed  = 2
)

const (
	CallTypeDebit  = "debit"
	CallTypeCredit = "credit"
	CallTypeSettle = "settle"
)
