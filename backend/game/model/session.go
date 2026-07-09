package model

import "time"

type SessionStatus int

const (
	SessionStatusPlaying   SessionStatus = 0
	SessionStatusCompleted SessionStatus = 1
	SessionStatusAbnormal  SessionStatus = 2
)

type GameSession struct {
	SessionID    int64         `json:"session_id" gorm:"primaryKey"`
	RoomID       int64         `json:"room_id" gorm:"index;not null"`
	RoomNo       string        `json:"room_no" gorm:"size:20;not null"`
	ConfigID     int64         `json:"config_id" gorm:"index;not null"`
	ConfigName   string        `json:"config_name" gorm:"size:50"`
	RoomFee      int64         `json:"room_fee" gorm:"not null"`
	MaxRounds    int           `json:"max_rounds" gorm:"not null"`
	ActualRounds int           `json:"actual_rounds"`
	CurrentRound int           `json:"current_round"`
	PlayerCount  int           `json:"player_count" gorm:"not null"`
	Status       SessionStatus `json:"status" gorm:"index;not null"`
	StartedAt    *time.Time    `json:"started_at"`
	EndedAt      *time.Time    `json:"ended_at"`
	EndReason    string        `json:"end_reason" gorm:"size:50"`
	CreatedAt    time.Time     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time     `json:"updated_at" gorm:"autoUpdateTime"`
}

func (GameSession) TableName() string { return "game_sessions" }

type SessionPlayer struct {
	ID          int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	SessionID   int64      `json:"session_id" gorm:"uniqueIndex:idx_session_user;not null"`
	RoomID      int64      `json:"room_id" gorm:"index;not null"`
	UserID      int64      `json:"user_id" gorm:"uniqueIndex:idx_session_user;index:idx_user_joined,priority:1;not null"`
	Nickname    string     `json:"nickname" gorm:"size:50"`
	Avatar      string     `json:"avatar" gorm:"size:255"`
	SeatNo      int        `json:"seat_no"`
	SendCount   int        `json:"send_count"`
	GrabCount   int        `json:"grab_count"`
	TotalSend   int64      `json:"total_send"`
	TotalGrab   int64      `json:"total_grab"`
	TotalProfit int64      `json:"total_profit"`
	IP          string     `json:"ip" gorm:"size:45"`
	DeviceID    string     `json:"device_id" gorm:"size:100"`
	JoinedAt    time.Time  `json:"joined_at" gorm:"index:idx_user_joined,priority:2"`
	LeftAt      *time.Time `json:"left_at"`
	CreatedAt   time.Time  `json:"created_at" gorm:"autoCreateTime"`
}

func (SessionPlayer) TableName() string { return "session_players" }
