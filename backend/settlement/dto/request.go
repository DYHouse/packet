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
	RoundNo     int
	UserID      int64
	Amount      int64
	PenaltyType string
}

type PenaltyDistributeRequest struct {
	RoomID     int64
	SessionID  int64
	RoundID    int64
	Amount     int64
	Recipients []int64
	Reason     string
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

type GameSettleRequest struct {
	RoomID    int64
	SessionID int64
}
