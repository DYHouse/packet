package model

import "time"

type Game struct {
	ID         int       `json:"id" gorm:"primaryKey;autoIncrement"`
	Name       string    `json:"name" gorm:"size:255;not null"`
	GameCode   string    `json:"game_code" gorm:"size:100;uniqueIndex;not null"`
	Category   string    `json:"category" gorm:"size:50;not null"`
	Provider   string    `json:"provider" gorm:"size:100;not null"`
	ResourceID int       `json:"resource_id" gorm:"default:1"`
	CoverURL   string    `json:"cover_url" gorm:"size:500"`
	Status     int       `json:"status" gorm:"default:1"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Game) TableName() string {
	return "games"
}

type GameStatus int

const (
	GameStatusActive   GameStatus = 1
	GameStatusInactive GameStatus = 2
)

func (g *Game) IsActive() bool {
	return g.Status == int(GameStatusActive)
}
