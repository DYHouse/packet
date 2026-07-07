package model

import "time"

type SpecialReward struct {
	ID          int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	RoomID      int64     `json:"room_id" gorm:"index;not null"`
	SessionID   int64     `json:"session_id" gorm:"index;not null"`
	RoundID     int64     `json:"round_id" gorm:"index;not null"`
	RoundNo     int       `json:"round_no" gorm:"not null"`
	RewardType  int       `json:"reward_type" gorm:"not null;comment:1顺子,2豹子"`
	TriggerType int       `json:"trigger_type" gorm:"not null;comment:1保底,2概率"`
	TotalAmount int64     `json:"total_amount" gorm:"not null;comment:红包总额"`
	Amount      int64     `json:"amount" gorm:"not null;comment:每个玩家的奖励金额"`
	PlayerCount int       `json:"player_count" gorm:"not null;comment:参与玩家数"`
	TotalReward int64     `json:"total_reward" gorm:"not null;comment:平台总奖励支出"`
	Details     string    `json:"details" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (SpecialReward) TableName() string { return "special_rewards" }
