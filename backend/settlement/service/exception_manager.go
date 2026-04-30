package service

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

type ExceptionManager struct {
	db *gorm.DB
}

func NewExceptionManager(db *gorm.DB) *ExceptionManager {
	return &ExceptionManager{db: db}
}

func (m *ExceptionManager) Create(ctx context.Context, exception *model.ExceptionRecord) error {
	return m.db.WithContext(ctx).Create(exception).Error
}

func (m *ExceptionManager) GetByID(ctx context.Context, id int64) (*model.ExceptionRecord, error) {
	var exception model.ExceptionRecord
	err := m.db.WithContext(ctx).First(&exception, id).Error
	if err != nil {
		return nil, err
	}
	return &exception, nil
}

func (m *ExceptionManager) GetPendingExceptions(ctx context.Context, limit int) ([]*model.ExceptionRecord, error) {
	var exceptions []*model.ExceptionRecord
	err := m.db.WithContext(ctx).Where("status = ?", model.ExceptionStatusPending).
		Order("created_at asc").
		Limit(limit).
		Find(&exceptions).Error
	return exceptions, err
}

func (m *ExceptionManager) UpdateStatus(ctx context.Context, id int64, status model.ExceptionStatus, handleType model.HandleType, remark string, handledBy int64) error {
	now := time.Now()
	return m.db.WithContext(ctx).Model(&model.ExceptionRecord{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        status,
			"handle_type":   handleType,
			"handle_remark": remark,
			"handled_at":    &now,
			"handled_by":    handledBy,
		}).Error
}
