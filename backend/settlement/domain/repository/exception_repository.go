package repository

import (
	"context"

	"github.com/cashparty/backend/settlement/model"
)

// ExceptionRepository 提供异常记录的持久化能力。
// 接口提取自原 service/exception_manager.go 的 Create 方法。
type ExceptionRepository interface {
	// Create 创建异常记录。
	Create(ctx context.Context, exception *model.ExceptionRecord) error
}
