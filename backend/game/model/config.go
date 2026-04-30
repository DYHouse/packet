package model

import "time"

type RoomConfig struct {
	ID            int64     `json:"id" gorm:"primaryKey"`
	Name          string    `json:"name" gorm:"size:50"`
	RoomFee       int64     `json:"room_fee"`
	MaxPlayers    int       `json:"max_players" gorm:"default:5"`
	MaxRounds     int       `json:"max_rounds" gorm:"default:10"`
	MaxSpectators int       `json:"max_spectators" gorm:"default:10"`
	SortOrder     int       `json:"sort_order"`
	Status        int       `json:"status" gorm:"default:1"`
	CreatedAt     time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (RoomConfig) TableName() string { return "room_configs" }
