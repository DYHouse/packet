package repository

import (
	"context"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
)

// PlatformCallLogRepository 提供平台调用排查日志的持久化能力。
// 定位：排查型日志，非对账依据；对账以 bill_record 表为准。
// 查询与清理场景由运维直接通过数据库执行，无需 Repository 接口。
type PlatformCallLogRepository interface {
	// CreateLog 创建平台调用日志（pending 状态）。
	CreateLog(ctx context.Context, params *dto.CallLogCreateParams) (*domain.PlatformCallLog, error)

	// UpdateLog 更新平台调用日志状态。
	UpdateLog(ctx context.Context, params *dto.CallLogUpdateParams) error
}
