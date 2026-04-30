package mysql

import (
	"context"
	"strconv"

	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormUserRepository struct {
	db *gorm.DB
}

func NewGormUserRepository(db *gorm.DB) *GormUserRepository {
	return &GormUserRepository{db: db}
}

func (r *GormUserRepository) CreateOrUpdateUser(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(user).Error
}

func (r *GormUserRepository) GetUser(ctx context.Context, userID string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *GormUserRepository) GetUserById(ctx context.Context, id string) (*model.User, error) {
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
