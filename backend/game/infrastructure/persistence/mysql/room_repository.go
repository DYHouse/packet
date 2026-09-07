package mysql

import (
	"context"
	"strconv"

	"github.com/cashparty/backend/common/message"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormRoomRepository struct {
	db *gorm.DB
	// minRoomFee 房间列表与自动匹配的最小房费（分）。小于该房费的房间在列表与匹配中被过滤。
	// 默认 500（5 元）；构造传入 0 时不过滤（防御性兜底，正常链路不会为 0）。
	minRoomFee int64
}

func NewGormRoomRepository(db *gorm.DB, minRoomFee int64) repository.RoomDBRepository {
	return &gormRoomRepository{db: db, minRoomFee: minRoomFee}
}

func (r *gormRoomRepository) GetRoom(ctx context.Context, roomID string) (*model.Room, error) {
	id, err := strconv.ParseInt(roomID, 10, 64)
	if err != nil {
		return nil, err
	}
	var room model.Room
	err = r.db.WithContext(ctx).First(&room, id).Error
	if err != nil {
		return nil, err
	}
	return &room, nil
}

func (r *gormRoomRepository) UpdateRoom(ctx context.Context, roomID string, updates map[string]interface{}) error {
	id, err := strconv.ParseInt(roomID, 10, 64)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.Room{}).Where("room_id = ?", id).Updates(updates).Error
}

func (r *gormRoomRepository) GetRoomList(ctx context.Context, configID, status, page, pageSize int) ([]*model.Room, error) {
	var rooms []*model.Room

	query := r.db.WithContext(ctx).Model(&model.Room{})
	if configID > 0 {
		query = query.Where("config_id = ?", configID)
	}

	if status == 0 {
		query = query.Where("status IN ?", []int{0, 1, 2})
	} else {
		query = query.Where("status = ?", status)
	}

	// 最小房费过滤：过滤掉低于阈值的房间（如低额体验房）
	if r.minRoomFee > 0 {
		query = query.Where("room_fee >= ?", r.minRoomFee)
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&rooms).Error; err != nil {
		return nil, err
	}

	return rooms, nil
}

func (r *gormRoomRepository) GetRoomCount(ctx context.Context, configID, status int) (int64, error) {
	var count int64

	query := r.db.WithContext(ctx).Model(&model.Room{})
	if configID > 0 {
		query = query.Where("config_id = ?", configID)
	}

	if status == 0 {
		query = query.Where("status IN ?", []int{0, 1, 2})
	} else {
		query = query.Where("status = ?", status)
	}

	// 最小房费过滤：与 GetRoomList 保持一致，保证分页总数与实际条数一致
	if r.minRoomFee > 0 {
		query = query.Where("room_fee >= ?", r.minRoomFee)
	}

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}

	return count, nil
}

func (r *gormRoomRepository) MatchRoomByBalance(ctx context.Context, balance int64) (string, error) {
	var roomID int64

	// 基础查询：按余额上限匹配有空位的房间，优先选择空位最多的房间
	querySQL := `
		SELECT room_id
		FROM rooms
		WHERE room_fee <= ?
		  AND status IN (0, 1)
		  AND player_count < max_players
	`
	args := []interface{}{balance}

	// 最小房费过滤：避免把用户自动匹配进低于阈值的房间（如低额体验房）
	if r.minRoomFee > 0 {
		querySQL += "  AND room_fee >= ?"
		args = append(args, r.minRoomFee)
	}

	querySQL += `
		ORDER BY (max_players - player_count) ASC
		LIMIT 1
	`

	err := r.db.WithContext(ctx).Raw(querySQL, args...).Scan(&roomID).Error
	if err != nil {
		return "", err
	}
	if roomID == 0 {
		return "", message.NewError(message.CodeNoIdleRoom)
	}
	return strconv.FormatInt(roomID, 10), nil
}
