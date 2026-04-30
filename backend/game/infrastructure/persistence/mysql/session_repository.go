package mysql

import (
	"context"
	"strconv"
	"time"

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

func (r *gormSessionRepository) CreateSession(ctx context.Context, session *model.GameSession) error {
	return r.db.WithContext(ctx).Create(session).Error
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

func (r *gormSessionRepository) GetActiveSessionByRoom(ctx context.Context, roomID string) (*model.GameSession, error) {
	roomIDInt, err := strconv.ParseInt(roomID, 10, 64)
	if err != nil {
		return nil, err
	}
	var session model.GameSession
	err = r.db.WithContext(ctx).
		Where("room_id = ? AND status = ?", roomIDInt, model.SessionStatusPlaying).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *gormSessionRepository) UpdateSession(ctx context.Context, sessionID string, updates map[string]interface{}) error {
	id, err := parseSessionID(sessionID)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.GameSession{}).
		Where("session_id = ?", id).
		Updates(updates).Error
}

func (r *gormSessionRepository) EndSession(ctx context.Context, sessionID string, actualRounds int, reason string) error {
	id, err := parseSessionID(sessionID)
	if err != nil {
		return err
	}
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.GameSession{}).
		Where("session_id = ?", id).
		Updates(map[string]interface{}{
			"status":        model.SessionStatusCompleted,
			"actual_rounds": actualRounds,
			"ended_at":      &now,
			"end_reason":    reason,
		}).Error
}

func (r *gormSessionRepository) CreateSessionPlayer(ctx context.Context, player *model.SessionPlayer) error {
	return r.db.WithContext(ctx).Create(player).Error
}

func (r *gormSessionRepository) GetSessionPlayers(ctx context.Context, sessionID string) ([]*model.SessionPlayer, error) {
	id, err := parseSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	var players []*model.SessionPlayer
	err = r.db.WithContext(ctx).Where("session_id = ?", id).Find(&players).Error
	if err != nil {
		return nil, err
	}
	return players, nil
}

func (r *gormSessionRepository) UpdateSessionPlayer(ctx context.Context, sessionID string, userID string, updates map[string]interface{}) error {
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return err
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.SessionPlayer{}).
		Where("session_id = ? AND user_id = ?", sid, userIDInt).
		Updates(updates).Error
}

func (r *gormSessionRepository) BatchUpdateSessionPlayerStats(ctx context.Context, sessionID string, playerStats map[string]*domain.PlayerStatsUpdate) error {
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for userID, stats := range playerStats {
			userIDInt, err := strconv.ParseInt(userID, 10, 64)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.SessionPlayer{}).
				Where("session_id = ? AND user_id = ?", sid, userIDInt).
				Updates(map[string]interface{}{
					"total_send":   stats.TotalSend,
					"total_grab":   stats.TotalGrab,
					"total_profit": stats.TotalProfit,
					"send_count":   stats.SendCount,
					"grab_count":   stats.GrabCount,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
