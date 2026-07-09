package model

import "time"

// PlatformCallLog 平台调用排查日志，记录对外部平台（GamingPanda）每次调用的请求/响应原文。
// 定位：排查型日志，非对账依据；对账以 bill_record 表为准。
type PlatformCallLog struct {
	ID             int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	TraceID        string     `gorm:"size:64;index;not null" json:"trace_id"`                                   // 链路追踪 ID（来自 ctx）
	BizOrderNo     string     `gorm:"size:128;not null;index:idx_biz_call_time,priority:1" json:"biz_order_no"` // 业务订单号
	CallType       string     `gorm:"size:20;not null;index:idx_biz_call_time,priority:2" json:"call_type"`     // 调用类型：debit / credit / settle
	UserID         int64      `gorm:"not null;default:0;index:idx_user_time,priority:1" json:"user_id"`         // 内部用户 ID
	PlatformUserID string     `gorm:"size:64;not null;default:''" json:"platform_user_id"`                      // 平台侧用户 ID
	SessionID      int64      `gorm:"default:0;index:idx_session_time,priority:1" json:"session_id"`            // 场次 ID（罚款等场景可为 0）
	RoundID        int64      `gorm:"default:0" json:"round_id"`                                                // 回合 ID（session 级派奖可为 0）
	Amount         int64      `gorm:"not null;default:0" json:"amount"`                                         // 调用金额（分，冗余便于排查）
	Currency       string     `gorm:"size:10;not null;default:''" json:"currency"`                              // 币种
	Status         int        `gorm:"default:0;index:idx_status_time,priority:1" json:"status"`                 // 0=pending 1=success 2=failed 3=timeout
	ErrorMessage   string     `gorm:"size:1024" json:"error_message"`
	RequestBody    string     `gorm:"type:text" json:"request_body"`
	ResponseBody   string     `gorm:"type:text" json:"response_body"`
	RequestTime    time.Time  `gorm:"not null;index:idx_biz_call_time,priority:3;index:idx_status_time,priority:2;index:idx_user_time,priority:2;index:idx_session_time,priority:2" json:"request_time"`
	ResponseTime   *time.Time `json:"response_time"`
	DurationMs     int        `gorm:"default:0" json:"duration_ms"`      // 调用耗时毫秒
	NodeID         int        `gorm:"not null;default:0" json:"node_id"` // 发起节点 ID
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (PlatformCallLog) TableName() string {
	return "platform_call_log"
}
