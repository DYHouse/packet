package domain

import (
	"context"
)

type RoomRepository interface {
	GetRoomMeta(ctx context.Context, roomID string) (*RoomMeta, error)
	UpdateRoomSessionID(ctx context.Context, roomID string, sessionID string) error
	InitRoom(ctx context.Context, room *RoomMeta) error

	GetPlayers(ctx context.Context, roomID string) (map[string]*Player, error)
	GetPlayer(ctx context.Context, roomID, userID string) (*Player, error)

	GetSpectator(ctx context.Context, roomID, userID string) (*Spectator, error)

	SelectSeat(ctx context.Context, roomID, userID string, seatNo int, isRobot bool) error
	CancelSeat(ctx context.Context, roomID, userID string) error

	JoinAsSpectator(ctx context.Context, roomID string, spectator *Spectator) (*JoinResult, error)
	LeaveRoom(ctx context.Context, roomID, userID string) error
	KickPlayerAndInterrupt(ctx context.Context, roomID, userID, reason string) (*KickPlayerResult, error)

	GetRoomStateData(ctx context.Context, roomID string) (*RoomStateData, error)
	GetRoomSeatsBatch(ctx context.Context, roomIDs []string) (map[string]*RoomStateData, error)

	AutoSeatAndReady(ctx context.Context, roomID, userID string, isRobot bool) (*AutoSeatResult, error)
	Enqueue(ctx context.Context, roomID, userID string) (int, error)
	Dequeue(ctx context.Context, roomID, userID string) error
	AutoSubstitute(ctx context.Context, roomID string, seatNo int) (*SubstituteResult, error)
	GetQueueList(ctx context.Context, roomID string) ([]*QueueInfo, error)
	RemoveFromQueue(ctx context.Context, roomID, userID string) error
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
	Queue          []*QueueInfo
	Meta           *RoomMeta
}

type AutoSeatResult struct {
	SeatNo               int
	PlayerCount          int
	MaxPlayers           int
	ShouldStartCountdown int
	CountdownEndTime     int64
	CurrentRound         int
	Player               *Player
}

type SubstituteResult struct {
	SubstituteUserID     string
	SeatNo               int
	PlayerCount          int
	MaxPlayers           int
	ShouldStartCountdown int
	CountdownEndTime     int64
	CurrentRound         int
	Player               *Player
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
