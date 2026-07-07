package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormGrabRecordRepository struct {
	db *gorm.DB
}

// NewGormGrabRecordRepository 创建抢包记录数据库仓储实例。
func NewGormGrabRecordRepository(db *gorm.DB) domain.GrabRecordRepository {
	return &gormGrabRecordRepository{db: db}
}

// FirstOrCreateGrabRecord 按 round_id + user_id 幂等创建抢包记录。
// 已存在则更新 Amount/IsMin/IsAutoAssigned/GrabbedAt 字段，不存在则插入。
func (r *gormGrabRecordRepository) FirstOrCreateGrabRecord(ctx context.Context, record *model.RoundGrabRecord) error {
	return r.db.WithContext(ctx).Where(record).
		Assign(model.RoundGrabRecord{
			Amount:         record.Amount,
			IsMin:          record.IsMin,
			IsAutoAssigned: record.IsAutoAssigned,
			GrabbedAt:      record.GrabbedAt,
		}).
		FirstOrCreate(record).Error
}
