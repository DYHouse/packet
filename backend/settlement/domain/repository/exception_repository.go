package repository

import (
	"context"

	"github.com/cashparty/backend/settlement/domain"
)

// ExceptionRepository 提供异常记录的持久化能力。
// 接口提取自原 service/exception_manager.go 的 Create 方法。
// 接口层已切换为 domain.ExceptionRecord 聚合根，实现层负责 domain ↔ model 转换。
type ExceptionRepository interface {
	// Create 创建异常记录。
	Create(ctx context.Context, exception *domain.ExceptionRecord) error
}
