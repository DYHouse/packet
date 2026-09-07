package model

import "time"

type RoomStatus int

const (
	RoomStatusIdle        RoomStatus = 0
	RoomStatusWaiting     RoomStatus = 1
	RoomStatusPlaying     RoomStatus = 2
	RoomStatusInterrupted RoomStatus = 4

	// RoomStatusRetired 房间已下架（运营管理态）。负数取值与游戏运行态（正数）隔离，
	// 房间列表查询按 status IN (0,1,2) 过滤，下架房间不会出现在列表与自动匹配中。
	RoomStatusRetired RoomStatus = -1
)

type Room struct {
	RoomID           int64      `json:"room_id" gorm:"primaryKey"`
	RoomNo           string     `json:"room_no" gorm:"uniqueIndex;size:20"`
	ConfigID         int64      `json:"config_id" gorm:"index"`
	ConfigName       string     `json:"config_name" gorm:"size:50"`
	RoomFee          int64      `json:"room_fee"`
	MaxPlayers       int        `json:"max_players"`
	MaxRounds        int        `json:"max_rounds"`
	MaxSpectators    int        `json:"max_spectators"`
	Status           RoomStatus `json:"status" gorm:"index"`
	CurrentRound     int        `json:"current_round"`
	CurrentSessionID int64      `json:"current_session_id" gorm:"index"`
	PlayerCount      int        `json:"player_count"`
	SpectatorCount   int        `json:"spectator_count" gorm:"default:0"`
	CreatorID        int64      `json:"creator_id"`
	StartedAt        *time.Time `json:"started_at"`
	CreatedAt        time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (Room) TableName() string { return "rooms" }
