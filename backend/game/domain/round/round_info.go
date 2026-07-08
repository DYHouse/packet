package round

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
