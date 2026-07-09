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
	// GetByID 根据主键查询异常记录。
	GetByID(ctx context.Context, id int64) (*domain.ExceptionRecord, error)
	// GetByStatus 分页查询指定状态的异常记录（按创建时间倒序）。
	GetByStatus(ctx context.Context, status domain.ExceptionStatus, limit int, offset int) ([]*domain.ExceptionRecord, error)
	// UpdateStatus 更新异常记录的处理状态。
	UpdateStatus(ctx context.Context, id int64, newStatus domain.ExceptionStatus, handleType domain.HandleType, handleRemark string, handledBy int64) error
}
