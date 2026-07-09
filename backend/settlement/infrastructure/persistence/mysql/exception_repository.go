package mysql

import (
	"context"
	"fmt"
	"time"

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

// GetByID 根据主键查询异常记录。
// 不存在时返回 gorm.ErrRecordNotFound（由 First 抛出）。
func (r *ExceptionRepositoryImpl) GetByID(ctx context.Context, id int64) (*domain.ExceptionRecord, error) {
	var log model.ExceptionRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&log).Error
	if err != nil {
		return nil, err
	}
	return exceptionModelToDomain(&log), nil
}

// GetByStatus 分页查询指定状态的异常记录（按创建时间倒序）。
// limit 为每页条数，offset 为偏移量。空结果返回长度为 0 的切片（非 nil）。
func (r *ExceptionRepositoryImpl) GetByStatus(ctx context.Context, status domain.ExceptionStatus, limit int, offset int) ([]*domain.ExceptionRecord, error) {
	var records []*model.ExceptionRecord
	err := r.db.WithContext(ctx).
		Where("status = ?", status).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return exceptionModelSliceToDomain(records), nil
}

// UpdateStatus 更新异常记录的处理状态。
// 仅按主键定位（WHERE id = ?）；RowsAffected == 0 表示记录不存在，返回 gorm.ErrRecordNotFound。
// 保守做法：不附加 status 条件，避免改变原语义；并发场景由调用方配合 CanHandle 守卫方法保护。
func (r *ExceptionRepositoryImpl) UpdateStatus(ctx context.Context, id int64, newStatus domain.ExceptionStatus, handleType domain.HandleType, handleRemark string, handledBy int64) error {
	now := time.Now()
	updates := map[string]interface{}{
		"status":        newStatus,
		"handle_type":   handleType,
		"handle_remark": handleRemark,
		"handled_by":    handledBy,
		"handled_at":    &now,
	}
	result := r.db.WithContext(ctx).Model(&model.ExceptionRecord{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update exception status failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// exceptionModelToDomain 将 model 层异常记录转换为 domain 层聚合根。
func exceptionModelToDomain(m *model.ExceptionRecord) *domain.ExceptionRecord {
	if m == nil {
		return nil
	}
	return &domain.ExceptionRecord{
		ID:              m.ID,
		ExceptionNo:     m.ExceptionNo,
		ExceptionType:   m.ExceptionType,
		BillID:          m.BillID,
		RoundTraceID:    m.RoundTraceID,
		RoundID:         m.RoundID,
		BillType:        m.BillType,
		UserID:          m.UserID,
		Amount:          m.Amount,
		Status:          m.Status,
		ExceptionDetail: m.ExceptionDetail,
		HandleType:      m.HandleType,
		HandleRemark:    m.HandleRemark,
		HandledAt:       m.HandledAt,
		HandledBy:       m.HandledBy,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

// exceptionModelSliceToDomain 批量转换 model 切片为 domain 切片。
// 入参为 nil 时返回 nil；空切片返回长度为 0 的非 nil 切片。
func exceptionModelSliceToDomain(ms []*model.ExceptionRecord) []*domain.ExceptionRecord {
	if ms == nil {
		return nil
	}
	result := make([]*domain.ExceptionRecord, 0, len(ms))
	for _, m := range ms {
		result = append(result, exceptionModelToDomain(m))
	}
	return result
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
