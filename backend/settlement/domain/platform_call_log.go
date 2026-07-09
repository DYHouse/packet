package domain

import "time"

// PlatformCallLog 平台调用日志相关常量（原定义在 model/platform_call_log.go，迁入 domain 作为单一真理源）。

// CallLogStatus 平台调用日志状态枚举值。
const (
	CallLogStatusPending = 0
	CallLogStatusSuccess = 1
	CallLogStatusFailed  = 2
	CallLogStatusTimeout = 3 // RPC 超时（context.DeadlineExceeded），排查时区分超时 vs 明确拒绝
)

// CallType 平台调用类型枚举值。
const (
	CallTypeDebit  = "debit"
	CallTypeCredit = "credit"
	CallTypeSettle = "settle"
)

// PlatformCallLog 平台调用排查日志聚合根，记录对外部平台调用的请求与响应信息。
// 定位：排查型日志，非对账依据；对账以 bill_record 表为准。
// 纯领域类型，无 GORM tag 与 TableName 方法；持久化由 model.PlatformCallLog 承载，
// Repository 实现层负责 domain ↔ model 转换。
type PlatformCallLog struct {
	ID             int64
	TraceID        string
	BizOrderNo     string
	CallType       string
	UserID         int64
	PlatformUserID string
	SessionID      int64
	RoundID        int64
	Amount         int64
	Currency       string
	Status         int
	ErrorMessage   string
	RequestBody    string
	ResponseBody   string
	RequestTime    time.Time
	ResponseTime   *time.Time
	DurationMs     int
	NodeID         int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
