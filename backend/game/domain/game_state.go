package domain

type GamePhase int

const (
	PhaseWaiting GamePhase = iota + 1
	PhaseCountdown
	PhaseRoundStart
	PhaseGrabbing
	PhaseSettling
	PhaseWaitSend
	PhaseGameEnd
)

func (p GamePhase) String() string {
	switch p {
	case PhaseWaiting:
		return "WAITING"
	case PhaseCountdown:
		return "COUNTDOWN"
	case PhaseRoundStart:
		return "ROUND_START"
	case PhaseGrabbing:
		return "GRABBING"
	case PhaseSettling:
		return "SETTLING"
	case PhaseWaitSend:
		return "WAIT_SEND"
	case PhaseGameEnd:
		return "GAME_END"
	default:
		return "UNKNOWN"
	}
}

func ParseGamePhase(s string) GamePhase {
	switch s {
	case "WAITING":
		return PhaseWaiting
	case "COUNTDOWN":
		return PhaseCountdown
	case "ROUND_START":
		return PhaseRoundStart
	case "GRABBING":
		return PhaseGrabbing
	case "SETTLING":
		return PhaseSettling
	case "WAIT_SEND":
		return PhaseWaitSend
	case "GAME_END":
		return PhaseGameEnd
	default:
		return PhaseWaiting
	}
}

type GameState struct {
	RoomID         string    `json:"room_id"`
	Phase          GamePhase `json:"phase"`
	CurrentRound   int       `json:"current_round"`
	MaxRounds      int       `json:"max_rounds"`
	NextSenderID   string    `json:"next_sender_id"`
	PhaseEnteredAt int64     `json:"phase_entered_at"`
	PhaseDeadline  int64     `json:"phase_deadline"`
}

type RoundInfo struct {
	RoundNo      int    `json:"round_no"`
	RoundID      string `json:"round_id"`
	SenderID     string `json:"sender_id"`
	SenderType   string `json:"sender_type"`
	TotalAmount  int64  `json:"total_amount"`
	Commission   int64  `json:"commission"`
	ActualAmount int64  `json:"actual_amount"`
	PacketCount  int    `json:"packet_count"`
	Status       int    `json:"status"`
	GrabEndTime  int64  `json:"grab_end_time"`
	CreatedAt    int64  `json:"created_at"`
}

type CommissionConfig struct {
	Rate float64 `json:"rate"`
}

func DefaultCommissionConfig() *CommissionConfig {
	return &CommissionConfig{Rate: 0.05}
}

func (c *CommissionConfig) Calculate(totalAmount int64) int64 {
	return int64(float64(totalAmount) * c.Rate)
}


