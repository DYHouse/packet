package dto

import (
	"time"

	"github.com/cashparty/backend/settlement/domain"
)

// DTO 层特有常量（流程控制 / 追踪标识），不属于领域层。

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

// 以下常量已迁入 settlement/domain，此处保留为兼容别名（const 重导出），
// 确保现有调用方零修改即可编译。新代码应直接引用 domain 层常量。

const (
	BillTypeFirstRoundDeduct  = domain.BillTypeFirstRoundDeduct
	BillTypeGrabPacket        = domain.BillTypeGrabPacket
	BillTypeLaterRoundDeduct  = domain.BillTypeLaterRoundDeduct
	BillTypeCommission        = domain.BillTypeCommission
	BillTypePenaltyIncome     = domain.BillTypePenaltyIncome
	BillTypeSystemPacket      = domain.BillTypeSystemPacket
	BillTypePenaltyDistribute = domain.BillTypePenaltyDistribute
	BillTypeSystemReward      = domain.BillTypeSystemReward
	BillTypeSessionCredit     = domain.BillTypeSessionCredit
	BillTypeGameSettle        = domain.BillTypeGameSettle
)

const (
	BillStatusProcessing = domain.BillStatusProcessing
	BillStatusSuccess    = domain.BillStatusSuccess
	BillStatusFailed     = domain.BillStatusFailed
	BillStatusRefunded   = domain.BillStatusRefunded
)

const (
	DeductSceneFirstRoundShare = domain.DeductSceneFirstRoundShare
	DeductSceneLaterRoundMin   = domain.DeductSceneLaterRoundMin
	DeductSceneSystemPacket    = domain.DeductSceneSystemPacket
)

const (
	RoundStatusDeducting = domain.RoundStatusDeducting
	RoundStatusDeducted  = domain.RoundStatusDeducted
	RoundStatusSettling  = domain.RoundStatusSettling
	RoundStatusSuccess   = domain.RoundStatusSuccess
	RoundStatusPartial   = domain.RoundStatusPartial
	RoundStatusFailed    = domain.RoundStatusFailed
	RoundStatusCredited  = domain.RoundStatusCredited
)

const (
	ReconcileStatusPending  = domain.ReconcileStatusPending
	ReconcileStatusSuccess  = domain.ReconcileStatusSuccess
	ReconcileStatusAbnormal = domain.ReconcileStatusAbnormal
)

const (
	RefundStatusNone       = domain.RefundStatusNone
	RefundStatusPending    = domain.RefundStatusPending
	RefundStatusApproved   = domain.RefundStatusApproved
	RefundStatusRefunded   = domain.RefundStatusRefunded
	RefundStatusRejected   = domain.RefundStatusRejected
	RefundStatusProcessing = domain.RefundStatusProcessing
)

const (
	RefundTypeFirstRoundFail = domain.RefundTypeFirstRoundFail
	RefundTypeOther          = domain.RefundTypeOther
)

const (
	ReconcileTypeScheduled = domain.ReconcileTypeScheduled
	ReconcileTypeAbnormal  = domain.ReconcileTypeAbnormal
	ReconcileTypeManual    = domain.ReconcileTypeManual
)

const (
	ReconcileScopeSession = domain.ReconcileScopeSession
	ReconcileScopeRound   = domain.ReconcileScopeRound
	ReconcileScopeBill    = domain.ReconcileScopeBill
)

// GameSettleStatus — 游戏级结算状态（记录在 RoundSettlement 上）
const (
	GameSettleStatusNone     = domain.GameSettleStatusNone
	GameSettleStatusSettling = domain.GameSettleStatusSettling
	GameSettleStatusSuccess  = domain.GameSettleStatusSuccess
	GameSettleStatusFailed   = domain.GameSettleStatusFailed
)

// BillGameSettleStatus — 单笔 Bill 的游戏级结算标记
const (
	BillGameSettleNone       = domain.BillGameSettleNone
	BillGameSettleSettled    = domain.BillGameSettleSettled
	BillGameSettleProcessing = domain.BillGameSettleProcessing
)
