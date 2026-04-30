package algorithm

type GenerateRequest struct {
	TotalAmount int64  `json:"total_amount"`
	PacketCount int    `json:"packet_count"`
	RoomID      string `json:"room_id"`
	RoundID     string `json:"round_id"`
}

type GenerateResult struct {
	PacketAmounts []int64 `json:"packet_amounts"`
	RewardType    int     `json:"reward_type"`
	RewardAmount  int64   `json:"reward_amount"`
	TraceID       string  `json:"trace_id"`
}

const (
	RewardTypeNone     = 0
	RewardTypeStraight = 1
	RewardTypeLeopard  = 2
)

type ValidationResult struct {
	Passed    bool                   `json:"passed"`
	Errors    []string               `json:"errors"`
	Stats     map[string]interface{} `json:"stats"`
	TestCount int                    `json:"test_count"`
	Duration  int64                  `json:"duration"`
}
