package model

import "time"

type RoundStatus int

const (
	RoundStatusPending  RoundStatus = 0
	RoundStatusSending  RoundStatus = 1
	RoundStatusGrabbing RoundStatus = 2
	RoundStatusEnded    RoundStatus = 3
	RoundStatusFailed   RoundStatus = 4
)

type Round struct {
	RoundID       int64       `json:"round_id" gorm:"primaryKey"`
	SessionID     int64       `json:"session_id" gorm:"index;not null"`
	RoomID        int64       `json:"room_id" gorm:"index;not null"`
	RoundNo       int         `json:"round_no" gorm:"not null;index:idx_session_round"`
	Status        RoundStatus `json:"status" gorm:"default:0;index"`
	SenderID      int64       `json:"sender_id" gorm:"default:0"`
	SenderType    string      `json:"sender_type" gorm:"size:20;default:''"`
	TotalAmount   int64       `json:"total_amount" gorm:"default:0"`
	Commission    int64       `json:"commission" gorm:"default:0"`
	DeductScene   int         `json:"deduct_scene" gorm:"default:0"`
	DeductAmount  int64       `json:"deduct_amount" gorm:"default:0"`
	DeductStatus  int         `json:"deduct_status" gorm:"default:0"`
	BatchID       string      `json:"batch_id" gorm:"size:32;default:''"`
	FailedReason  string      `json:"failed_reason" gorm:"size:512;default:''"`
	SettleTraceID int64       `json:"settle_trace_id" gorm:"index"`
	StartedAt     *time.Time  `json:"started_at"`
	EndedAt       *time.Time  `json:"ended_at"`
	CreatedAt     time.Time   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     time.Time   `json:"updated_at" gorm:"autoUpdateTime"`
}

func (Round) TableName() string { return "rounds" }

type RoundGrabRecord struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RoundID        int64     `json:"round_id" gorm:"index;not null"`
	PacketID       int64     `json:"packet_id" gorm:"index;not null"`
	SessionID      int64     `json:"session_id" gorm:"index;not null"`
	UserID         int64     `json:"user_id" gorm:"index;not null"`
	Amount         int64     `json:"amount;not null"`
	IsMin          int       `json:"is_min;default:0"`
	IsAutoAssigned int       `json:"is_auto_assigned;default:0"`
	GrabbedAt      time.Time `json:"grabbed_at"`
	CreatedAt      time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (RoundGrabRecord) TableName() string { return "round_grab_records" }
