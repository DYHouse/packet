package dto

type FirstRoundDeductResult struct {
	BatchID        string              `json:"batch_id"`
	AllSuccess     bool                `json:"all_success"`
	SuccessCount   int                 `json:"success_count"`
	FailedCount    int                 `json:"failed_count"`
	SuccessPlayers []int64             `json:"success_players"`
	FailedPlayers  []*FailedPlayerInfo `json:"failed_players"`
}

type FailedPlayerInfo struct {
	UserID    int64  `json:"user_id"`
	ErrorCode string `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

type BalanceCheckResult struct {
	UserID       int64 `json:"user_id"`
	Balance      int64 `json:"balance"`
	RequiredFee  int64 `json:"required_fee"`
	IsSufficient bool  `json:"is_sufficient"`
}
