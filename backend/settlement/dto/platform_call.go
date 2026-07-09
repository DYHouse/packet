package dto

import "time"

// CallLogCreateParams 创建平台调用日志的参数。
// 由原 service/platform_call_manager.go 迁移而来，供 PlatformCallLogRepository.CreateLog 使用。
type CallLogCreateParams struct {
	TraceID        string      // 链路追踪 ID（来自 ctx，由 service 层提取）
	CallType       string      // debit / credit / settle
	BizOrderNo     string      // 业务订单号
	UserID         int64       // 内部用户 ID
	PlatformUserID string      // 平台侧用户 ID
	SessionID      int64       // 场次 ID（罚款等场景可为 0）
	RoundID        int64       // 回合 ID（session 级派奖可为 0）
	Amount         int64       // 调用金额（分）
	Currency       string      // 币种
	ReqBody        interface{} // 请求体（JSON 序列化存储）
	NodeID         int         // 发起节点 ID
}

// CallLogUpdateParams 更新平台调用日志的参数。
// 由原 service/platform_call_manager.go 迁移而来，供 PlatformCallLogRepository.UpdateLog 使用。
type CallLogUpdateParams struct {
	ID           int64
	RespBody     interface{}
	Status       int
	ErrorMessage string
	RequestTime  time.Time // 由 service 层传入 CreateLog 返回的 RequestTime，用于计算 duration_ms
}
