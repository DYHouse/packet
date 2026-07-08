package dto

// CallLogCreateParams 创建平台调用日志的参数。
// 由原 service/platform_call_manager.go 迁移而来，供 PlatformCallLogRepository.CreateLog 使用。
type CallLogCreateParams struct {
	CallType   string
	BizOrderNo string
	ReqBody    interface{}
}

// CallLogUpdateParams 更新平台调用日志的参数。
// 由原 service/platform_call_manager.go 迁移而来，供 PlatformCallLogRepository.UpdateLog 使用。
type CallLogUpdateParams struct {
	ID           int64
	RespBody     interface{}
	Status       int
	ErrorMessage string
}
