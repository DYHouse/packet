package dto

type SendPacketRequest struct {
	RoomID     int64
	SessionID  int64
	RoundID    int64
	UserID     int64
	SenderType string
	RoomFee    int64
	Commission int64
}

type RoundSettleRequest struct {
	RoomID           int64
	SessionID        int64
	RoundID          int64
	RoundNo          int
	SenderID         int64
	SenderType       string
	TotalAmount      int64
	Commission       int64
	RoomFeePerPlayer int64
	MinPlayerID      int64
	Players          []*PlayerSettleInfo
	RewardType       int
	RewardAmount     int64
}

type PlayerSettleInfo struct {
	UserID int64
	Amount int64
	IsMin  bool
}

type PenaltyDeductRequest struct {
	RoomID      int64
	SessionID   int64
	RoundID     int64
	RoundNo     int
	UserID      int64
	Amount      int64
	PenaltyType string
}

// SubstituteFeeDeductRequest 替补费扣款请求。
// 每次补位事件独立扣款，RoundNo 标识补位发生时的轮次（区分同一玩家多次补位）。
// Amount 由 game 层通过 commissionCfg.Calculate(meta.RoomFee) 计算后传入
// （settlement 层不持有 CommissionConfig）。
type SubstituteFeeDeductRequest struct {
	RoomID    int64
	SessionID int64
	UserID    int64
	RoundID   int64 // 中断替补关联的当前轮次 ID（中断替补场景必填；自动入座场景为 0）
	RoundNo   int   // 补位发生时的轮次，用于区分同一玩家多次补位
	Amount    int64
}

type PenaltyDistributeRequest struct {
	RoomID       int64
	SessionID    int64
	RoundID      int64
	RoundNo      int    // 下一轮编号（inter-round 罚款场景下为 CurrentRound + 1）
	Amount       int64
	Recipients   []int64
	Reason       string
	TriggerPhase string // 触发阶段：inter_round（轮间触发）/ in_round（轮内触发）
}

type FirstRoundDeductRequest struct {
	RoomID           int64
	SessionID        int64
	RoundID          int64
	RoundNo          int
	RoomFeePerPlayer int64
	Players          []*PlayerDeductInfo
}

type PlayerDeductInfo struct {
	UserID   int64
	Nickname string
}

type LaterRoundDeductRequest struct {
	RoomID       int64
	SessionID    int64
	RoundID      int64
	RoundNo      int
	RoomFee      int64
	MinPlayerID  int64
	RoundTraceID string
}

type SystemPacketDeductRequest struct {
	RoomID       int64
	SessionID    int64
	RoundID      int64
	RoundNo      int
	TotalAmount  int64
	RoundTraceID string
	Reason       string
}

type SingleDeductRequest struct {
	RoomID       int64
	SessionID    int64
	RoundID      int64
	RoundNo      int
	UserID       int64
	Amount       int64
	BillType     int
	DeductScene  int
	RoundTraceID string
	Remark       string
}

type RefundApplyRequest struct {
	BillID       int64
	RefundAmount int64
	RefundReason string
	RefundType   int
	AppliedBy    int64
}

type RefundApproveRequest struct {
	RefundOrderNo string
	ApprovedBy    int64
	Remark        string
}

type RefundRejectRequest struct {
	RefundOrderNo string
	RejectedBy    int64
	Remark        string
}

type BalanceCheckRequest struct {
	UserID     int64
	RoomFee    int64
	MaxPlayers int
	MaxRounds  int
}
