package model

import "time"

type BillRecord struct {
	ID               int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	RoundTraceID     string     `gorm:"size:128;uniqueIndex:idx_round_trace_bill_user,priority:1" json:"round_trace_id"`
	BizOrderNo       string     `gorm:"uniqueIndex;size:128" json:"biz_order_no"`
	PlatformTransID  string     `gorm:"index;size:64" json:"platform_trans_id"`
	BillType         int        `gorm:"not null;uniqueIndex:idx_round_trace_bill_user,priority:2;index:idx_session_user_type,priority:3" json:"bill_type"`
	DeductScene      int        `gorm:"default:0" json:"deduct_scene"`
	RoomID           int64      `gorm:"index;not null" json:"room_id"`
	SessionID        int64      `gorm:"index:idx_user_status_session,priority:3;index:idx_session_user_type,priority:1;index:idx_user_session,priority:2" json:"session_id"`
	RoundID          int64      `gorm:"index" json:"round_id"`
	RoundNo          int        `gorm:"default:0" json:"round_no"`
	UserID           int64      `gorm:"not null;uniqueIndex:idx_round_trace_bill_user,priority:3;index:idx_user_status_session,priority:1;index:idx_session_user_type,priority:2;index:idx_user_session,priority:1" json:"user_id"`
	BatchID          string     `gorm:"index;size:32" json:"batch_id"`
	Amount           int64      `gorm:"not null" json:"amount"`
	BalanceBefore    int64      `gorm:"not null;default:0" json:"balance_before"`
	BalanceAfter     int64      `gorm:"not null;default:0" json:"balance_after"`
	Status           int        `gorm:"default:0;index:idx_user_status_session,priority:2;index:idx_session_user_type,priority:4" json:"status"`
	ReconcileStatus  int        `gorm:"default:0;index" json:"reconcile_status"`
	RefundStatus     int        `gorm:"default:0;index" json:"refund_status"`
	RefundOrderNo    string     `gorm:"size:128" json:"refund_order_no"`
	RefundAmount     int64      `gorm:"default:0" json:"refund_amount"`
	RefundReason     string     `gorm:"size:256" json:"refund_reason"`
	RefundAppliedAt  *time.Time `json:"refund_applied_at"`
	RefundApprovedAt *time.Time `json:"refund_approved_at"`
	RefundApprovedBy int64      `gorm:"default:0" json:"refund_approved_by"`
	RetryCount       int        `gorm:"default:0" json:"retry_count"`
	NextRetryAt      *time.Time `json:"next_retry_at"`
	ErrorCode        string     `gorm:"size:32" json:"error_code"`
	ErrorMessage     string     `gorm:"size:512" json:"error_message"`
	ExceptionID      int64      `gorm:"index" json:"exception_id"`
	GameSettleStatus int        `gorm:"default:0;index" json:"game_settle_status"`
	GameSettledAt    *time.Time `gorm:"index" json:"game_settled_at"`
	Remark           string     `gorm:"size:256" json:"remark"`
	IsRobot          bool       `gorm:"default:false;index" json:"is_robot"`
	CreatedAt        time.Time  `gorm:"autoCreateTime;index:idx_user_session,priority:3" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	// 关联字段：惩罚/奖励业务的溯源标识，便于反向查询关联实体
	PenaltyType           string `json:"penalty_type" gorm:"size:32;index;default:''"`
	PenaltyRecordID       int64  `json:"penalty_record_id" gorm:"index;default:0"`
	SpecialRewardID       int64  `json:"special_reward_id" gorm:"index;default:0"`
	PenaltyDistributionID int64  `json:"penalty_distribution_id" gorm:"index;default:0"`
}

func (BillRecord) TableName() string {
	return "bill_record"
}

type RoundSettlement struct {
	ID                 int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	RoundTraceID       string     `gorm:"uniqueIndex;size:128" json:"round_trace_id"`
	RoomID             int64      `gorm:"index;not null" json:"room_id"`
	SessionID          int64      `gorm:"index;not null" json:"session_id"`
	RoundID            int64      `gorm:"uniqueIndex;not null" json:"round_id"`
	RoundNo            int        `gorm:"not null" json:"round_no"`
	DeductScene        int        `gorm:"not null" json:"deduct_scene"`
	DeductAmount       int64      `gorm:"default:0" json:"deduct_amount"`
	DeductUserCount    int        `gorm:"default:0" json:"deduct_user_count"`
	DeductSuccessCount int        `gorm:"default:0" json:"deduct_success_count"`
	DeductedAt         *time.Time `json:"deducted_at"`
	SettleAmount       int64      `gorm:"default:0" json:"settle_amount"`
	SettleUserCount    int        `gorm:"default:0" json:"settle_user_count"`
	SettleSuccessCount int        `gorm:"default:0" json:"settle_success_count"`
	SettledAt          *time.Time `json:"settled_at"`
	SenderID           int64      `gorm:"not null" json:"sender_id"`
	SenderType         string     `gorm:"size:20;not null" json:"sender_type"`
	TotalAmount        int64      `gorm:"not null" json:"total_amount"`
	Commission         int64      `gorm:"not null" json:"commission"`
	PlayerCount        int        `gorm:"not null" json:"player_count"`
	MinPlayerID        int64      `gorm:"not null" json:"min_player_id"`
	RewardType         int        `gorm:"default:0" json:"reward_type"`
	RewardAmount       int64      `gorm:"default:0" json:"reward_amount"`
	Status             int        `gorm:"default:0;index" json:"status"`
	ReconcileStatus    int        `gorm:"default:0;index" json:"reconcile_status"`
	RefundStatus       int        `gorm:"default:0;index" json:"refund_status"`
	RefundReason       string     `gorm:"size:256" json:"refund_reason"`
	ErrorMessage       string     `gorm:"size:512" json:"error_message"`
	GameSettleStatus   int        `gorm:"default:0;index" json:"game_settle_status"`
	GameSettledAt      *time.Time `gorm:"index" json:"game_settled_at"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (RoundSettlement) TableName() string {
	return "round_settlement"
}
