package mysql

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// ExceptionRepositoryImpl 是 ExceptionRepository 接口的 MySQL 实现。
// 从原 service/exception_manager.go 迁移，行为完全一致。
// 接口层已切换为 domain.ExceptionRecord 聚合根，本实现层在方法边界完成 domain ↔ model 转换，
// DB 操作仍基于 model.ExceptionRecord（携带 GORM tag 与 TableName）。
type ExceptionRepositoryImpl struct {
	db *gorm.DB
}

// NewExceptionRepository 构造 ExceptionRepositoryImpl 实例。
func NewExceptionRepository(db *gorm.DB) repository.ExceptionRepository {
	return &ExceptionRepositoryImpl{db: db}
}

// Create 创建异常记录。
// 行为与原 service 层实现完全一致。
func (r *ExceptionRepositoryImpl) Create(ctx context.Context, exception *domain.ExceptionRecord) error {
	mModel := exceptionDomainToModel(exception)
	if err := r.db.WithContext(ctx).Create(mModel).Error; err != nil {
		return fmt.Errorf("create exception failed: %w", err)
	}
	// 回填 DB 自动生成的字段（自增主键、时间戳）到 domain 聚合根，保持与原 Create(exception) 行为一致。
	exception.ID = mModel.ID
	exception.CreatedAt = mModel.CreatedAt
	exception.UpdatedAt = mModel.UpdatedAt
	return nil
}

// exceptionDomainToModel 将 domain 层聚合根转换为 model 层持久化实体。
func exceptionDomainToModel(d *domain.ExceptionRecord) *model.ExceptionRecord {
	if d == nil {
		return nil
	}
	return &model.ExceptionRecord{
		ID:              d.ID,
		ExceptionNo:     d.ExceptionNo,
		ExceptionType:   d.ExceptionType,
		BillID:          d.BillID,
		RoundTraceID:    d.RoundTraceID,
		RoundID:         d.RoundID,
		BillType:        d.BillType,
		UserID:          d.UserID,
		Amount:          d.Amount,
		Status:          d.Status,
		ExceptionDetail: d.ExceptionDetail,
		HandleType:      d.HandleType,
		HandleRemark:    d.HandleRemark,
		HandledAt:       d.HandledAt,
		HandledBy:       d.HandledBy,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
	}
}

// 编译时接口实现校验
var _ repository.ExceptionRepository = (*ExceptionRepositoryImpl)(nil)
