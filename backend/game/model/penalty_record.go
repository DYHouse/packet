package model

import "time"

// PenaltyDeductStatus 罚款扣款状态枚举值
const (
	PenaltyDeductProcessing = 0
	PenaltyDeductSuccess    = 1
	PenaltyDeductFailed     = 2
)

// PenaltyRecord 罚款记录持久化模型，关联到具体 round
type PenaltyRecord struct {
	ID           int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RoomID       int64     `json:"room_id" gorm:"index:idx_penalty_room,priority:1;not null"`
	SessionID    int64     `json:"session_id" gorm:"index:idx_penalty_session_round,priority:1;not null"`
	RoundID      int64     `json:"round_id" gorm:"index:idx_penalty_session_round,priority:2;not null"`
	RoundNo      int       `json:"round_no" gorm:"not null"`
	UserID       int64     `json:"user_id" gorm:"index:idx_penalty_user,priority:1;not null"`
	PenaltyType  string    `json:"penalty_type" gorm:"size:32;not null"`
	Amount       int64     `json:"amount" gorm:"not null"`
	Count        int       `json:"count" gorm:"not null"`
	KickRequired bool      `json:"kick_required" gorm:"not null"`
	DeductStatus int       `json:"deduct_status" gorm:"default:0;index"`
	BillID       int64     `json:"bill_id" gorm:"index"`
	DeductError  string    `json:"deduct_error" gorm:"size:512;default:''"`
	CreatedAt    time.Time `json:"created_at" gorm:"autoCreateTime;index:idx_penalty_room,priority:2;index:idx_penalty_user,priority:2"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (PenaltyRecord) TableName() string { return "penalty_records" }
