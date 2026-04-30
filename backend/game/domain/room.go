package domain

type RoomStatus int

const (
	RoomStatusIdle        RoomStatus = 0
	RoomStatusWaiting     RoomStatus = 1
	RoomStatusPlaying     RoomStatus = 2
	RoomStatusInterrupted RoomStatus = 4
)

type RoomMeta struct {
	RoomID           string
	RoomNo           string
	ConfigID         int64
	ConfigName       string
	RoomFee          int64
	MaxPlayers       int
	MaxRounds        int
	MaxSpectators    int
	Status           RoomStatus
	CurrentRound     int
	CurrentRoundID   string
	CurrentSessionID string
	PlayerCount      int
	SpectatorCount   int
	StartedAt        *int64
}

type Player struct {
	UserID         string `json:"user_id"`
	Nickname       string `json:"nickname"`
	Avatar         string `json:"avatar"`
	SeatNo         int    `json:"seat_no"`
	DisconnectedAt *int64 `json:"disconnected_at"`
}

type Spectator struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	SeatNo   int    `json:"seat_no"`
}

func (p *Player) IsOnline() bool {
	return p.DisconnectedAt == nil
}

func (p *Player) CanGrab() bool {
	return p.IsOnline()
}
