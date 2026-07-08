package mysql

import (
	"context"

	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// ExceptionRepositoryImpl 是 ExceptionRepository 接口的 MySQL 实现。
// 从原 service/exception_manager.go 迁移，行为完全一致。
type ExceptionRepositoryImpl struct {
	db *gorm.DB
}

// NewExceptionRepository 构造 ExceptionRepositoryImpl 实例。
func NewExceptionRepository(db *gorm.DB) *ExceptionRepositoryImpl {
	return &ExceptionRepositoryImpl{db: db}
}

// Create 创建异常记录。
// 行为与原 service 层实现完全一致。
func (r *ExceptionRepositoryImpl) Create(ctx context.Context, exception *model.ExceptionRecord) error {
	return r.db.WithContext(ctx).Create(exception).Error
}

// 编译时接口实现校验
var _ repository.ExceptionRepository = (*ExceptionRepositoryImpl)(nil)
