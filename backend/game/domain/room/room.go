package room

type RoomStatus int

const (
	RoomStatusIdle        RoomStatus = 0
	RoomStatusWaiting     RoomStatus = 1
	RoomStatusPlaying     RoomStatus = 2
	RoomStatusInterrupted RoomStatus = 4
)

// MaxPlayers 单个房间允许的最大玩家数。
const MaxPlayers = 5

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
	IsRobot        bool   `json:"is_robot"`
}

type Spectator struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	SeatNo   int    `json:"seat_no"`
	IsRobot  bool   `json:"is_robot"`
}

type QueueInfo struct {
	UserID        string `json:"user_id"`
	Nickname      string `json:"nickname"`
	Avatar        string `json:"avatar"`
	QueuePosition int    `json:"queue_position"`
	QueuedAt      int64  `json:"queued_at"`
}

func (p *Player) IsOnline() bool {
	return p.DisconnectedAt == nil
}

func (p *Player) CanGrab() bool {
	return p.IsOnline()
}

// CalculateRequiredFee 根据房费、最大玩家数和最大局数计算开局所需总费用（游戏领域规则）。
// 公式：首局费用 = 房费 / 最大玩家数；后续局费用 = 房费 × (最大局数 - 1)
// 总费用 = 首局费用 + 后续局费用
func CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64 {
	if maxPlayers <= 0 || maxRounds <= 0 {
		return 0
	}

	firstRoundFee := roomFee / int64(maxPlayers)
	laterRoundsFee := roomFee * int64(maxRounds-1)

	return firstRoundFee + laterRoundsFee
}
