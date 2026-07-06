package mysql

import (
	"context"
	"strconv"

	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormSessionRepository struct {
	db *gorm.DB
}

func NewGormSessionRepository(db *gorm.DB) domain.SessionDBRepository {
	return &gormSessionRepository{db: db}
}

func parseSessionID(sessionID string) (int64, error) {
	return strconv.ParseInt(sessionID, 10, 64)
}

func (r *gormSessionRepository) GetSession(ctx context.Context, sessionID string) (*model.GameSession, error) {
	id, err := parseSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	var session model.GameSession
	err = r.db.WithContext(ctx).First(&session, id).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}
