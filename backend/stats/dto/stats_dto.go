package dto

type DashboardStats struct {
	TodaySessions     int64 `json:"today_sessions"`
	TodayRounds       int64 `json:"today_rounds"`
	ActivePlayers     int64 `json:"active_players"`
	TotalCommission   int64 `json:"total_commission"`
	PenaltyIncome     int64 `json:"penalty_income"`
	SystemPacketCost  int64 `json:"system_packet_cost"`
	NetProfit         int64 `json:"net_profit"`
	StraightCount     int64 `json:"straight_count"`
	LeopardCount      int64 `json:"leopard_count"`
	SystemPacketCount int64 `json:"system_packet_count"`
}

type HourlyTrend struct {
	Hour       string `json:"hour"`
	RoundCount int64  `json:"round_count"`
	Commission int64  `json:"commission"`
}

type AmountDistribution struct {
	Range       string `json:"range"`
	Count       int64  `json:"count"`
	TotalAmount int64  `json:"total_amount"`
	AvgAmount   int64  `json:"avg_amount"`
}

type RoomRanking struct {
	RoomID        int64  `json:"room_id"`
	RoomName      string `json:"room_name"`
	SessionCount  int64  `json:"session_count"`
	RoundCount    int64  `json:"round_count"`
	PlayerCount   int64  `json:"player_count"`
	TotalAmount   int64  `json:"total_amount"`
	Commission    int64  `json:"commission"`
	StraightCount int64  `json:"straight_count"`
	LeopardCount  int64  `json:"leopard_count"`
}

type SystemPacketStats struct {
	SenderType  string `json:"sender_type"`
	SendCount   int64  `json:"send_count"`
	TotalAmount int64  `json:"total_amount"`
	AvgAmount   int64  `json:"avg_amount"`
}

type DailyTrend struct {
	Date             string `json:"date"`
	RoundCount       int64  `json:"round_count"`
	TotalCommission  int64  `json:"total_commission"`
	PenaltyIncome    int64  `json:"penalty_income"`
	SystemPacketCost int64  `json:"system_packet_cost"`
	NetProfit        int64  `json:"net_profit"`
}
