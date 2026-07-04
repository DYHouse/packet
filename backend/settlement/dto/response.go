package dto

type FirstRoundDeductResult struct {
	BatchID        string
	AllSuccess     bool
	SuccessCount   int
	FailedCount    int
	SuccessPlayers []int64
	FailedPlayers  []*FailedPlayerInfo
}

type FailedPlayerInfo struct {
	UserID    int64
	ErrorCode string
	ErrorMsg  string
}

type RefundResult struct {
	RefundOrderNo string
	Status        int
	RefundedAt    string
	ErrorMsg      string
}

type BillQueryResult struct {
	Total int64
	List  []*BillInfo
}

type BillInfo struct {
	ID            int64  `json:"id"`
	RoundTraceID  string `json:"round_trace_id"`
	BizOrderNo    string `json:"biz_order_no"`
	BillType      int    `json:"bill_type"`
	DeductScene   int    `json:"deduct_scene"`
	RoomID        int64  `json:"room_id"`
	SessionID     int64  `json:"session_id"`
	RoundID       int64  `json:"round_id"`
	RoundNo       int    `json:"round_no"`
	UserID        int64  `json:"user_id"`
	Amount        int64  `json:"amount"`
	BalanceBefore int64  `json:"balance_before"`
	BalanceAfter  int64  `json:"balance_after"`
	Status        int    `json:"status"`
	RefundStatus  int    `json:"refund_status"`
	RefundAmount  int64  `json:"refund_amount"`
	RefundOrderNo string `json:"refund_order_no"`
	ErrorMessage  string `json:"error_message"`
	Remark        string `json:"remark"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type RoundSettlementInfo struct {
	ID                 int64  `json:"id"`
	RoundTraceID       string `json:"round_trace_id"`
	RoomID             int64  `json:"room_id"`
	SessionID          int64  `json:"session_id"`
	RoundID            int64  `json:"round_id"`
	RoundNo            int    `json:"round_no"`
	DeductScene        int    `json:"deduct_scene"`
	DeductAmount       int64  `json:"deduct_amount"`
	DeductUserCount    int    `json:"deduct_user_count"`
	DeductSuccessCount int    `json:"deduct_success_count"`
	DeductedAt         string `json:"deducted_at"`
	SettleAmount       int64  `json:"settle_amount"`
	SettleUserCount    int    `json:"settle_user_count"`
	SettleSuccessCount int    `json:"settle_success_count"`
	SettledAt          string `json:"settled_at"`
	Status             int    `json:"status"`
	RefundStatus       int    `json:"refund_status"`
	RefundReason       string `json:"refund_reason"`
	ErrorMessage       string `json:"error_message"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type RefundAuditInfo struct {
	ID              int64  `json:"id"`
	RefundOrderNo   string `json:"refund_order_no"`
	RoundTraceID    string `json:"round_trace_id"`
	BatchID         string `json:"batch_id"`
	RoomID          int64  `json:"room_id"`
	SessionID       int64  `json:"session_id"`
	RoundID         int64  `json:"round_id"`
	UserID          int64  `json:"user_id"`
	BillID          int64  `json:"bill_id"`
	BillOrderNo     string `json:"bill_order_no"`
	RefundAmount    int64  `json:"refund_amount"`
	RefundReason    string `json:"refund_reason"`
	RefundType      int    `json:"refund_type"`
	Status          int    `json:"status"`
	AppliedAt       string `json:"applied_at"`
	AppliedBy       int64  `json:"applied_by"`
	ApprovedAt      string `json:"approved_at"`
	ApprovedBy      int64  `json:"approved_by"`
	ApproveRemark   string `json:"approve_remark"`
	RefundedAt      string `json:"refunded_at"`
	PlatformTransID string `json:"platform_trans_id"`
	ErrorMessage    string `json:"error_message"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type BalanceCheckResult struct {
	UserID       int64 `json:"user_id"`
	Balance      int64 `json:"balance"`
	RequiredFee  int64 `json:"required_fee"`
	IsSufficient bool  `json:"is_sufficient"`
}

type BatchBalanceCheckResult struct {
	AllSufficient bool                  `json:"all_sufficient"`
	Results       []*BalanceCheckResult `json:"results"`
}

type GameSettleInfo struct {
	SessionID    int64                   `json:"session_id"`
	RoomID       int64                   `json:"room_id"`
	TotalRounds  int                     `json:"total_rounds"`
	SettleStatus int                     `json:"settle_status"`
	SettledAt    string                  `json:"settled_at"`
	Players      []*GamePlayerSettleInfo `json:"players"`
}

type GamePlayerSettleInfo struct {
	UserID     int64  `json:"user_id"`
	BetAmount  int64  `json:"bet_amount"`
	PayOut     int64  `json:"pay_out"`
	NetAmount  int64  `json:"net_amount"`
	GameResult string `json:"game_result"`
	Status     int    `json:"status"`
}
