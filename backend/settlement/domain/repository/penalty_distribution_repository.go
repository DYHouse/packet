package repository

import (
	"context"

	"github.com/cashparty/backend/settlement/model"
)

// PenaltyDistributionRepository 罚款分发记录仓储接口
type PenaltyDistributionRepository interface {
	Create(ctx context.Context, distribution *model.PenaltyDistribution) error
	CreateRecipients(ctx context.Context, recipients []*model.PenaltyDistributionRecipient) error
}
