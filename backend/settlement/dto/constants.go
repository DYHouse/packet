package dto

import (
	"time"
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
