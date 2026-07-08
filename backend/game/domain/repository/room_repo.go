package repository

import (
	"context"

	"github.com/cashparty/backend/game/domain/room"
)

type RoomRepository interface {
	GetRoomMeta(ctx context.Context, roomID string) (*room.RoomMeta, error)
	UpdateRoomSessionID(ctx context.Context, roomID string, sessionID string) error
	InitRoom(ctx context.Context, room *room.RoomMeta) error

	// GetNextSenderID 读取房间哈希中的 next_sender_id 字段，用于判断当前轮次的发包轮到哪位玩家。
	GetNextSenderID(ctx context.Context, roomID string) (string, error)

	GetPlayers(ctx context.Context, roomID string) (map[string]*room.Player, error)
	GetPlayer(ctx context.Context, roomID, userID string) (*room.Player, error)

	GetSpectator(ctx context.Context, roomID, userID string) (*room.Spectator, error)

	SelectSeat(ctx context.Context, roomID, userID string, seatNo int, isRobot bool) error
	CancelSeat(ctx context.Context, roomID, userID string) error

	JoinAsSpectator(ctx context.Context, roomID string, spectator *room.Spectator) (*JoinResult, error)
	LeaveRoom(ctx context.Context, roomID, userID string) error
	KickPlayerAndInterrupt(ctx context.Context, roomID, userID, reason string) (*KickPlayerResult, error)

	GetRoomStateData(ctx context.Context, roomID string) (*RoomStateData, error)
	GetRoomSeatsBatch(ctx context.Context, roomIDs []string) (map[string]*RoomStateData, error)

	AutoSeatAndReady(ctx context.Context, roomID, userID string, isRobot bool) (*AutoSeatResult, error)
	Enqueue(ctx context.Context, roomID, userID string) (int, error)
	Dequeue(ctx context.Context, roomID, userID string) error
	AutoSubstitute(ctx context.Context, roomID string, seatNo int) (*SubstituteResult, error)
	GetQueueList(ctx context.Context, roomID string) ([]*room.QueueInfo, error)
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
	Players        map[string]*room.Player
	Spectators     map[string]*room.Spectator
	SeatOwners     map[int]string
	Queue          []*room.QueueInfo
	Meta           *room.RoomMeta
}

type AutoSeatResult struct {
	SeatNo               int
	PlayerCount          int
	MaxPlayers           int
	ShouldStartCountdown int
	CountdownEndTime     int64
	CurrentRound         int
	Player               *room.Player
}

type SubstituteResult struct {
	SubstituteUserID     string
	SeatNo               int
	PlayerCount          int
	MaxPlayers           int
	ShouldStartCountdown int
	CountdownEndTime     int64
	CurrentRound         int
	Player               *room.Player
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
