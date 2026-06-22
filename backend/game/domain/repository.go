package domain

import (
	"context"
)

type RoomRepository interface {
	GetRoomMeta(ctx context.Context, roomID string) (*RoomMeta, error)
	UpdateRoomStatus(ctx context.Context, roomID string, status RoomStatus) error
	UpdateRoomSessionID(ctx context.Context, roomID string, sessionID string) error
	InitRoom(ctx context.Context, room *RoomMeta) error

	GetPlayers(ctx context.Context, roomID string) (map[string]*Player, error)
	GetPlayer(ctx context.Context, roomID, userID string) (*Player, error)
	SavePlayer(ctx context.Context, roomID string, player *Player) error
	SetAllPlayersOnline(ctx context.Context, roomID string, isOnline bool) error

	GetSpectator(ctx context.Context, roomID, userID string) (*Spectator, error)

	SelectSeat(ctx context.Context, roomID, userID string, seatNo int, isRobot bool) error
	CancelSeat(ctx context.Context, roomID, userID string) error

	JoinAsSpectator(ctx context.Context, roomID string, spectator *Spectator) (*JoinResult, error)
	LeaveRoom(ctx context.Context, roomID, userID string) error
	KickPlayer(ctx context.Context, roomID, userID string, reason string) error
	KickPlayerAndInterrupt(ctx context.Context, roomID, userID, reason string) (*KickPlayerResult, error)

	ResetRoomForNextGame(ctx context.Context, roomID string) error

	GetRoomStateData(ctx context.Context, roomID string) (*RoomStateData, error)
	GetRoomSeatsBatch(ctx context.Context, roomIDs []string) (map[string]*RoomStateData, error)
}

type RoomStateData struct {
	RoomID         string
	RoomNo         string
	RoomType       int
	RoomFee        int64
	Status         int
	CurrentRound   int
	MaxRounds      int
	PlayerCount    int
	SpectatorCount int
	MaxPlayers     int
	MaxSpectators  int
	Players        map[string]*Player
	Spectators     map[string]*Spectator
	SeatOwners     map[int]string
	Meta           *RoomMeta
}

type EventPublisher interface {
	Publish(ctx context.Context, event *RoomEvent) error
}

type Broadcaster interface {
	Broadcast(roomID string, cmd string, data interface{}, excludeUserID string)
	BroadcastToUser(userID string, cmd string, data interface{})
}

type JoinResult struct {
	RoomID   string
	RoomNo   string
	ConfigID int64
}

type KickPlayerResult struct {
	SeatNo     int
	RoomStatus int
}
