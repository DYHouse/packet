package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormPacketRepository struct {
	db *gorm.DB
}

// NewGormPacketRepository 创建红包数据库仓储实例。
func NewGormPacketRepository(db *gorm.DB) domain.PacketDBRepository {
	return &gormPacketRepository{db: db}
}

func (r *gormPacketRepository) CreatePacket(ctx context.Context, packet *model.Packet) error {
	return r.db.WithContext(ctx).Create(packet).Error
}

func (r *gormPacketRepository) CountPacketsByRoundID(ctx context.Context, roundID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Packet{}).
		Where("round_id = ?", roundID).Count(&count).Error
	return count, err
}
