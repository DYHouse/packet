package message

type PingResponse struct {
	ServerTime int64 `json:"server_time"`
}

type GameStartPush struct {
	RoomID       string `json:"room_id"`
	CurrentRound int32  `json:"current_round"`
	MaxRounds    int32  `json:"max_rounds"`
}

type GameResumedPush struct {
	RoomID       string `json:"room_id"`
	CurrentRound int32  `json:"current_round"`
	NextSenderID string `json:"next_sender_id"`
	Message      string `json:"message"`
}

type CountdownStartPush struct {
	RoomID    string `json:"room_id"`
	Countdown int32  `json:"countdown"`
}

type RoundResult struct {
	UserID         string `json:"user_id"`
	Nickname       string `json:"nickname"`
	Avatar         string `json:"avatar"`
	Amount         int64  `json:"amount"`
	Position       int32  `json:"position"`
	PacketID       string `json:"packet_id"`
	IsAutoAssigned bool   `json:"is_auto_assigned"`
}

type GameResult struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	Avatar      string `json:"avatar"`
	TotalProfit int64  `json:"total_profit"`
	Rank        int32  `json:"rank"`
}

type KickedPush struct {
	RoomID  string `json:"room_id"`
	UserID  string `json:"user_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type PlayerReconnectedPush struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	SeatNo   int    `json:"seat_no"`
}

// ==================== 游戏相关推送结构 ====================

// RoundStartPush 回合开始推送
type RoundStartPush struct {
	RoomID         string       `json:"room_id"`
	RoundID        string       `json:"round_id"`
	CurrentRound   int32        `json:"current_round"`
	SenderID       string       `json:"sender_id"`
	SenderNickname string       `json:"sender_nickname"`
	SenderType     string       `json:"sender_type"`
	TotalAmount    int64        `json:"total_amount"`
	Commission     int64        `json:"commission"`
	ActualAmount   int64        `json:"actual_amount"`
	PacketCount    int32        `json:"packet_count"`
	GrabTimeout    int32        `json:"grab_timeout"`
	Packets        []PacketInfo `json:"packets"`
}

// PacketInfo 红包信息
type PacketInfo struct {
	PacketID string `json:"packet_id"`
	Position int32  `json:"position"`
}

// PacketGrabbedPush 红包被抢推送
type PacketGrabbedPush struct {
	RoomID   string `json:"room_id"`
	RoundID  string `json:"round_id"`
	PacketID string `json:"packet_id"`
	Position int32  `json:"position"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Amount   int64  `json:"amount"`
	IsLast   bool   `json:"is_last"`
}

// RoundEndPush 回合结束推送
type RoundEndPush struct {
	RoomID          string        `json:"room_id"`
	RoundID         string        `json:"round_id"`
	CurrentRound    int32         `json:"current_round"`
	SenderID        string        `json:"sender_id"`
	TotalAmount     int64         `json:"total_amount"`
	Commission      int64         `json:"commission"`
	Results         []RoundResult `json:"results"`
	MinAmountPlayer string        `json:"min_amount_player"`
	NextSenderID    string        `json:"next_sender_id"`
	IsGameEnd       bool          `json:"is_game_end"`
	RewardType      int           `json:"reward_type,omitempty"`
	RewardAmount    int64         `json:"reward_amount,omitempty"`
	FinalResults    []GameResult  `json:"final_results,omitempty"`
}

// AutoDistributePush 自动分配推送
type AutoDistributePush struct {
	RoomID           string             `json:"room_id"`
	RoundID          string             `json:"round_id"`
	DistributedCount int32              `json:"distributed_count"`
	Results          []DistributeResult `json:"results"`
}

// DistributeResult 分配结果
type DistributeResult struct {
	UserID   string `json:"user_id"`
	Amount   int64  `json:"amount"`
	Position int32  `json:"position"`
}

// PenaltyPush 惩罚推送
type PenaltyPush struct {
	RoomID        string `json:"room_id"`
	UserID        string `json:"user_id"`
	PenaltyType   string `json:"penalty_type"`
	PenaltyAmount int64  `json:"penalty_amount"`
	PenaltyCount  int32  `json:"penalty_count"`
	KickRequired  bool   `json:"kick_required"`
	Reason        string `json:"reason"`
}

// WaitReplacementPush 等待补位推送
type WaitReplacementPush struct {
	RoomID     string   `json:"room_id"`
	VacantSeat int32    `json:"vacant_seat"`
	LeftUserID string   `json:"left_user_id"`
	WaitTime   int32    `json:"wait_time"`
	Recipients []string `json:"recipients,omitempty"`
}

// GameInterruptedPush 游戏中断推送
type GameInterruptedPush struct {
	RoomID       string   `json:"room_id"`
	Reason       string   `json:"reason"`
	PenaltyShare int64    `json:"penalty_share"`
	Recipients   []string `json:"recipients"`
}
