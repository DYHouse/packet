package service

import (
	"context"

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
