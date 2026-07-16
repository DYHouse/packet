package model

import "time"

// PenaltyDistribution 罚款分发记录持久化模型，关联到具体 round
type PenaltyDistribution struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RoomID         int64     `json:"room_id" gorm:"index;not null"`
	SessionID      int64     `json:"session_id" gorm:"index;not null"`
	RoundID        int64     `json:"round_id" gorm:"index;not null"`
	RoundNo        int       `json:"round_no" gorm:"not null"`
	TriggerType    string    `json:"trigger_type" gorm:"size:32;not null"`
	TotalAmount    int64     `json:"total_amount" gorm:"not null"`
	ShareAmount    int64     `json:"share_amount" gorm:"not null"`
	RecipientCount int       `json:"recipient_count" gorm:"not null"`
	ExcludeUsers   string    `json:"exclude_users" gorm:"type:text"`
	PlatformBillID int64     `json:"platform_bill_id" gorm:"index"`
	CreatedAt      time.Time `json:"created_at" gorm:"autoCreateTime;index"`
}

func (PenaltyDistribution) TableName() string { return "penalty_distributions" }

// PenaltyDistributionRecipient 罚款分发接收方明细
type PenaltyDistributionRecipient struct {
	ID             int64 `json:"id" gorm:"primaryKey;autoIncrement"`
	DistributionID int64 `json:"distribution_id" gorm:"index;not null"`
	UserID         int64 `json:"user_id" gorm:"index;not null"`
	Amount         int64 `json:"amount" gorm:"not null"`
	BillID         int64 `json:"bill_id" gorm:"index"`
}

func (PenaltyDistributionRecipient) TableName() string { return "penalty_distribution_recipients" }
