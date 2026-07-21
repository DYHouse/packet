package mysql

import (
	"context"
	"strconv"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gormUserRepository struct {
	db *gorm.DB
}

func NewGormUserRepository(db *gorm.DB) repository.UserDBRepository {
	return &gormUserRepository{db: db}
}

func (r *gormUserRepository) CreateOrUpdateUser(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(user).Error
}

func (r *gormUserRepository) GetUser(ctx context.Context, userID string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *gormUserRepository) GetUserById(ctx context.Context, id string) (*model.User, error) {
	idInt, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, err
	}
	var user model.User
	err = r.db.WithContext(ctx).Where("id = ?", idInt).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *gormUserRepository) SetUserIsRobot(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", id).
		Update("is_robot", true).Error
}

// UpdateAvatar 按主键 id 更新用户头像 URL。
// 仅更新 avatar 列，避免覆盖其他字段。
func (r *gormUserRepository) UpdateAvatar(ctx context.Context, id int64, avatarURL string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", id).
		Update("avatar", avatarURL).Error
}
