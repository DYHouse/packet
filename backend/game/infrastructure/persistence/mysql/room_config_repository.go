package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"gorm.io/gorm"
)

type gormRoomConfigRepository struct {
	db *gorm.DB
}

func NewGormRoomConfigRepository(db *gorm.DB) domain.RoomConfigDBRepository {
	return &gormRoomConfigRepository{db: db}
}

func (r *gormRoomConfigRepository) GetRoomTypeList(ctx context.Context) ([]*domain.RoomTypeItem, error) {
	var items []*domain.RoomTypeItem

	query := `
		SELECT rc.id, rc.name, rc.room_fee, rc.max_rounds,
			   COALESCE(SUM(r.player_count + r.spectator_count), 0) as total_people
		FROM room_configs rc
		LEFT JOIN rooms r ON rc.id = r.config_id
		WHERE rc.status = 1
		GROUP BY rc.id, rc.name, rc.room_fee, rc.max_rounds
		ORDER BY rc.sort_order ASC
	`

	err := r.db.WithContext(ctx).Raw(query).Scan(&items).Error
	if err != nil {
		return nil, err
	}

	return items, nil
}
