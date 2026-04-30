package model

import "time"

type Packet struct {
	PacketID  int64     `json:"packet_id" gorm:"primaryKey"`
	RoundID   int64     `json:"round_id" gorm:"index"`
	RoomID    int64     `json:"room_id" gorm:"index"`
	Amount    int64     `json:"amount"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (Packet) TableName() string { return "packets" }
