package mysql

import (
	"context"

	repository "github.com/cashparty/backend/game/domain/repository"
	"gorm.io/gorm"
)

type gormRoomConfigRepository struct {
	db *gorm.DB
	// minRoomFee 房间列表与自动匹配的最小房费（分）。小于该房费的房间类型在首页列表中被过滤。
	// 默认 500（5 元）；构造传入 0 时不过滤（防御性兜底，正常链路不会为 0）。
	minRoomFee int64
}

func NewGormRoomConfigRepository(db *gorm.DB, minRoomFee int64) repository.RoomConfigDBRepository {
	return &gormRoomConfigRepository{db: db, minRoomFee: minRoomFee}
}

func (r *gormRoomConfigRepository) GetRoomTypeList(ctx context.Context) ([]*repository.RoomTypeItem, error) {
	var items []*repository.RoomTypeItem

	// 基础查询：聚合各房间类型的当前总人数
	query := `
		SELECT rc.id, rc.name, rc.room_fee, rc.max_rounds,
			   COALESCE(SUM(r.player_count + r.spectator_count), 0) as total_people
		FROM room_configs rc
		LEFT JOIN rooms r ON rc.id = r.config_id
		WHERE rc.status = 1
	`
	args := []interface{}{}

	// 最小房费过滤：与房间列表保持一致，过滤掉低于阈值的房间类型（如低额体验房）
	if r.minRoomFee > 0 {
		query += `  AND rc.room_fee >= ?`
		args = append(args, r.minRoomFee)
	}

	query += `
		GROUP BY rc.id, rc.name, rc.room_fee, rc.max_rounds
		ORDER BY rc.sort_order ASC
	`

	err := r.db.WithContext(ctx).Raw(query, args...).Scan(&items).Error
	if err != nil {
		return nil, err
	}

	return items, nil
}
