package repository

import (
	"context"

	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

// PlatformCallLogRepository 提供平台调用审计日志的持久化与查询能力。
// 接口提取自原 service/platform_call_manager.go 的 4 个公开方法。
type PlatformCallLogRepository interface {
	// CreateLog 创建平台调用日志。
	CreateLog(ctx context.Context, params *dto.CallLogCreateParams) (*model.PlatformCallLog, error)
	// UpdateLog 更新平台调用日志。
	UpdateLog(ctx context.Context, params *dto.CallLogUpdateParams) error
	// GetLogByID 根据 ID 查询平台调用日志。
	GetLogByID(ctx context.Context, id int64) (*model.PlatformCallLog, error)
	// GetFailedLogs 查询失败的平台调用日志。
	GetFailedLogs(ctx context.Context, limit int) ([]*model.PlatformCallLog, error)
}
