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

func (r *gormSessionRepository) GetSession(ctx context.Context, sessionID string) (*model.GameSession, error) {
	id, err := parseSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	return r.GetSessionByID(ctx, id)
}

func (r *gormSessionRepository) GetSessionByID(ctx context.Context, sessionID int64) (*model.GameSession, error) {
	var session model.GameSession
	if err := r.db.WithContext(ctx).First(&session, sessionID).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *gormSessionRepository) CreateSession(ctx context.Context, session *model.GameSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *gormSessionRepository) UpdateSessionEnded(ctx context.Context, sessionID int64, actualRounds int, endedAt time.Time, endReason string) error {
	return r.db.WithContext(ctx).Model(&model.GameSession{}).
		Where("session_id = ?", sessionID).
		Updates(map[string]interface{}{
			"status":        model.SessionStatusCompleted,
			"actual_rounds": actualRounds,
			"ended_at":      &endedAt,
			"end_reason":    endReason,
		}).Error
}

func (r *gormSessionRepository) UpdateSessionCurrentRound(ctx context.Context, sessionID int64, currentRound int) error {
	return r.db.WithContext(ctx).Model(&model.GameSession{}).
		Where("session_id = ?", sessionID).
		Update("current_round", currentRound).Error
}

func (r *gormSessionRepository) CreateOrUpdateSessionPlayer(ctx context.Context, player *model.SessionPlayer) error {
	return r.db.WithContext(ctx).Where(player).
		Assign(model.SessionPlayer{
			RoomID:   player.RoomID,
			Nickname: player.Nickname,
			Avatar:   player.Avatar,
			SeatNo:   player.SeatNo,
			JoinedAt: player.JoinedAt,
		}).
		FirstOrCreate(player).Error
}

func (r *gormSessionRepository) IncrementSessionPlayerGrab(ctx context.Context, sessionID, userID int64, amount int64) error {
	return r.db.WithContext(ctx).Model(&model.SessionPlayer{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Updates(map[string]interface{}{
			"grab_count": gorm.Expr("grab_count + 1"),
			"total_grab": gorm.Expr("total_grab + ?", amount),
		}).Error
}

func (r *gormSessionRepository) IncrementSessionPlayerSend(ctx context.Context, sessionID, userID int64, amount int64) error {
	return r.db.WithContext(ctx).Model(&model.SessionPlayer{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Updates(map[string]interface{}{
			"send_count": gorm.Expr("send_count + 1"),
			"total_send": gorm.Expr("total_send + ?", amount),
		}).Error
}

func (r *gormSessionRepository) UpdateSessionPlayerProfit(ctx context.Context, sessionID, userID int64, totalProfit int64) error {
	return r.db.WithContext(ctx).Model(&model.SessionPlayer{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Update("total_profit", totalProfit).Error
}
