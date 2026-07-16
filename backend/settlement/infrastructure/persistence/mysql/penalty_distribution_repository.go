package mysql

import (
	"context"

	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// gormPenaltyDistributionRepository 是 PenaltyDistributionRepository 接口的 MySQL 实现。
// 负责罚款分发记录及其接收方明细的持久化，事务边界由 AppService 通过
// DBRepository.WithTransaction 编排。
type gormPenaltyDistributionRepository struct {
	db *gorm.DB
}

// NewGormPenaltyDistributionRepository 构造 gormPenaltyDistributionRepository 实例。
func NewGormPenaltyDistributionRepository(db *gorm.DB) repository.PenaltyDistributionRepository {
	return &gormPenaltyDistributionRepository{db: db}
}

// Create 创建单条罚款分发记录。
func (r *gormPenaltyDistributionRepository) Create(ctx context.Context, distribution *model.PenaltyDistribution) error {
	return r.db.WithContext(ctx).Create(distribution).Error
}

// CreateRecipients 批量创建罚款分发接收方明细。空切片直接返回 nil。
func (r *gormPenaltyDistributionRepository) CreateRecipients(ctx context.Context, recipients []*model.PenaltyDistributionRecipient) error {
	if len(recipients) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(recipients, 100).Error
}

// 编译时接口实现校验
var _ repository.PenaltyDistributionRepository = (*gormPenaltyDistributionRepository)(nil)
