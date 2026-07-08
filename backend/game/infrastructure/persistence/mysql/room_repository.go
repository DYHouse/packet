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
}

func NewGormRoomRepository(db *gorm.DB) repository.RoomDBRepository {
	return &gormRoomRepository{db: db}
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

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}

	return count, nil
}

func (r *gormRoomRepository) MatchRoomByBalance(ctx context.Context, balance int64) (string, error) {
	var roomID int64
	err := r.db.WithContext(ctx).Raw(`
		SELECT room_id
		FROM rooms
		WHERE room_fee <= ?
		  AND status IN (0, 1)
		  AND player_count < max_players
		ORDER BY (max_players - player_count) ASC
		LIMIT 1
	`, balance).Scan(&roomID).Error
	if err != nil {
		return "", err
	}
	if roomID == 0 {
		return "", message.NewError(message.CodeNoIdleRoom)
	}
	return strconv.FormatInt(roomID, 10), nil
}
