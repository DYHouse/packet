package dto

import "time"

const (
	BillTypeFirstRoundDeduct  = 2
	BillTypeGrabPacket        = 3
	BillTypeLaterRoundDeduct  = 4
	BillTypeCommission        = 7
	BillTypePenaltyIncome     = 8
	BillTypeSystemPacket      = 9
	BillTypePenaltyDistribute = 10
	BillTypeSystemReward      = 11
	BillTypeSessionCredit     = 12
	BillTypeGameSettle        = 13
)

const (
	TraceTypePenaltyDeduct = "PENALTY_DED"
	TraceTypePenaltyDist   = "PENALTY_DIST"
)

const (
	PlatformAccountID    int64 = 0
	MaxRetryCount        int   = 3
	PenaltyRoundID       int64 = 0
	CreditRetryBaseDelay       = 5 * time.Second
	CreditRetryMaxDelay        = 5 * time.Minute
)

const (
	BillStatusProcessing = 0
	BillStatusSuccess    = 1
	BillStatusFailed     = 2
	BillStatusRefunded   = 3
)

const (
	DeductSceneFirstRoundShare = 1
	DeductSceneLaterRoundMin   = 2
	DeductSceneSystemPacket    = 3
)

const (
	RoundStatusDeducting = 0
	RoundStatusDeducted  = 1
	RoundStatusSettling  = 2
	RoundStatusSuccess   = 3
	RoundStatusPartial   = 4
	RoundStatusFailed    = 5
	RoundStatusCredited  = 6
)

const (
	ReconcileStatusPending  = 0
	ReconcileStatusSuccess  = 1
	ReconcileStatusAbnormal = 2
)

const (
	RefundStatusNone       = 0
	RefundStatusPending    = 1
	RefundStatusApproved   = 2
	RefundStatusRefunded   = 3
	RefundStatusRejected   = 4
	RefundStatusProcessing = 5
)

const (
	RefundTypeFirstRoundFail = 1
	RefundTypeOther          = 2
)

const (
	ReconcileTypeScheduled = 1
	ReconcileTypeAbnormal  = 2
	ReconcileTypeManual    = 3
)

const (
	ReconcileScopeSession = 1
	ReconcileScopeRound   = 2
	ReconcileScopeBill    = 3
)

// GameSettleStatus — 游戏级结算状态（记录在 RoundSettlement 上）
const (
	GameSettleStatusNone     = 0
	GameSettleStatusSettling = 1
	GameSettleStatusSuccess  = 2
	GameSettleStatusFailed   = 3
)

// BillGameSettleStatus — 单笔 Bill 的游戏级结算标记
const (
	BillGameSettleNone       = 0
	BillGameSettleSettled    = 1
	BillGameSettleProcessing = 2
)
