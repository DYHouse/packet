package mysql

import (
	"context"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormSpecialRewardRepository struct {
	db *gorm.DB
}

// NewGormSpecialRewardRepository 创建特殊奖励数据库仓储实例。
func NewGormSpecialRewardRepository(db *gorm.DB) repository.SpecialRewardRepository {
	return &gormSpecialRewardRepository{db: db}
}

func (r *gormSpecialRewardRepository) CreateSpecialReward(ctx context.Context, reward *model.SpecialReward) error {
	return r.db.WithContext(ctx).Create(reward).Error
}
