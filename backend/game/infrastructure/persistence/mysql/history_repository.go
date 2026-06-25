package mysql

import (
	"errors"
	"time"

	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormHistoryRepository struct {
	db *gorm.DB
}

func NewGormHistoryRepository(db *gorm.DB) domain.HistoryDBRepository {
	return &gormHistoryRepository{db: db}
}

// ListPlayerSessions 分页查询玩家历史会话（关联 game_sessions，仅已完成会话 status=1）。
// 同时返回符合过滤条件的总数。盈亏由 total_grab - total_send 计算（不取 total_profit）。
func (r *gormHistoryRepository) ListPlayerSessions(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]domain.PlayerSessionRow, int64, error) {
	where := "sp.user_id = ? AND gs.status = ?"
	args := []interface{}{userID, model.SessionStatusCompleted}
	if startTime != nil {
		where += " AND sp.joined_at >= ?"
		args = append(args, *startTime)
	}
	if endTime != nil {
		where += " AND sp.joined_at < ?"
		args = append(args, *endTime)
	}
	if configName != "" {
		where += " AND gs.config_name = ?"
		args = append(args, configName)
	}

	// 总数
	type countResult struct {
		Total int64
	}
	countSQL := "SELECT COUNT(*) AS total FROM session_players sp INNER JOIN game_sessions gs ON sp.session_id = gs.session_id WHERE " + where
	var cr countResult
	if err := r.db.Raw(countSQL, args...).Scan(&cr).Error; err != nil {
		return nil, 0, err
	}

	// 列表
	listSQL := `SELECT
       sp.session_id, sp.nickname, sp.avatar, sp.seat_no,
       sp.send_count, sp.grab_count, sp.total_send, sp.total_grab,
       sp.joined_at, sp.left_at,
       gs.room_no, gs.config_name, gs.room_fee, gs.max_rounds, gs.actual_rounds,
       gs.status, gs.started_at, gs.ended_at, gs.end_reason
       FROM session_players sp
       INNER JOIN game_sessions gs ON sp.session_id = gs.session_id
       WHERE ` + where + `
       ORDER BY sp.joined_at DESC
       LIMIT ? OFFSET ?`
	listArgs := append(args, limit, offset)

	var rows []domain.PlayerSessionRow
	if err := r.db.Raw(listSQL, listArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, cr.Total, nil
}

// GetSessionRounds 查询会话所有回合（按 round_no 升序）
func (r *gormHistoryRepository) GetSessionRounds(sessionID int64) ([]model.Round, error) {
	var rounds []model.Round
	err := r.db.Where("session_id = ?", sessionID).
		Order("round_no ASC").
		Find(&rounds).Error
	if err != nil {
		return nil, err
	}
	return rounds, nil
}

// ListPlayerGrabRecords 查询玩家在某会话的抢包记录（按 grabbed_at 升序）
func (r *gormHistoryRepository) ListPlayerGrabRecords(sessionID, userID int64) ([]model.RoundGrabRecord, error) {
	var records []model.RoundGrabRecord
	err := r.db.Where("session_id = ? AND user_id = ?", sessionID, userID).
		Order("grabbed_at ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}

// AggregatePlayerStats 玩家累计统计聚合（仅统计已完成会话 status=1）。
// 盈亏由 SUM(total_grab) - SUM(total_send) 计算（不使用 total_profit 字段）。
func (r *gormHistoryRepository) AggregatePlayerStats(userID int64) (*domain.PlayerStatsAggregate, error) {
	query := `
		SELECT
		    COUNT(*) AS total_games,
		    COALESCE(SUM(CASE WHEN (total_grab - total_send) > 0 THEN 1 ELSE 0 END), 0) AS win_count,
		    COALESCE(SUM(total_send), 0) AS total_send,
		    COALESCE(SUM(total_grab), 0) AS total_grab,
		    COALESCE(SUM(send_count), 0) AS total_send_count,
		    COALESCE(SUM(grab_count), 0) AS total_grab_count
		FROM session_players
		INNER JOIN game_sessions gs ON session_players.session_id = gs.session_id
		WHERE session_players.user_id = ? AND gs.status = ?`
	var agg domain.PlayerStatsAggregate
	err := r.db.Raw(query, userID, model.SessionStatusCompleted).Scan(&agg).Error
	if err != nil {
		return nil, err
	}
	return &agg, nil
}

// GetPlayerSession 查询玩家在某会话中的记录（未找到返回 nil, nil）
func (r *gormHistoryRepository) GetPlayerSession(userID, sessionID int64) (*model.SessionPlayer, error) {
	var player model.SessionPlayer
	err := r.db.Where("session_id = ? AND user_id = ?", sessionID, userID).
		First(&player).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &player, nil
}

// GetSession 查询会话基本信息（未找到返回 nil, nil）
func (r *gormHistoryRepository) GetSession(sessionID int64) (*model.GameSession, error) {
	var session model.GameSession
	err := r.db.Where("session_id = ?", sessionID).
		First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}
