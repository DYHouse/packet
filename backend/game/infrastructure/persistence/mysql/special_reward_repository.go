package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormSpecialRewardRepository struct {
	db *gorm.DB
}

// NewGormSpecialRewardRepository 创建特殊奖励数据库仓储实例。
func NewGormSpecialRewardRepository(db *gorm.DB) domain.SpecialRewardRepository {
	return &gormSpecialRewardRepository{db: db}
}

func (r *gormSpecialRewardRepository) CreateSpecialReward(ctx context.Context, reward *model.SpecialReward) error {
	return r.db.WithContext(ctx).Create(reward).Error
}
