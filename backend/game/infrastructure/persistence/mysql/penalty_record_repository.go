package mysql

import (
	"context"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormPenaltyRecordRepository struct {
	db *gorm.DB
}

// NewGormPenaltyRecordRepository 创建罚款记录数据库仓储实例。
func NewGormPenaltyRecordRepository(db *gorm.DB) repository.PenaltyRecordRepository {
	return &gormPenaltyRecordRepository{db: db}
}

func (r *gormPenaltyRecordRepository) Create(ctx context.Context, record *model.PenaltyRecord) error {
	return r.db.WithContext(ctx).Create(record).Error
}
