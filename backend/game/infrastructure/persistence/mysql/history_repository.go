package mysql

import (
	"errors"
	"time"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormHistoryRepository struct {
	db *gorm.DB
}

func NewGormHistoryRepository(db *gorm.DB) repository.HistoryDBRepository {
	return &gormHistoryRepository{db: db}
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

// ListPlayerSessionsWithBill 基于 bill_record 聚合的玩家历史会话查询（仅已完成会话 status=1）。
// 通过 SUM(CASE WHEN ...) 计算 total_grab/total_send/total_bet/total_income/profit 等，
// profit = total_income - total_bet。
func (r *gormHistoryRepository) ListPlayerSessionsWithBill(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]repository.PlayerSessionBillRow, int64, error) {
	join := "game_sessions gs INNER JOIN bill_record b ON b.session_id = gs.session_id AND b.user_id = ? AND b.status = 1 LEFT JOIN session_players sp ON sp.session_id = gs.session_id AND sp.user_id = ?"
	joinArgs := []interface{}{userID, userID}

	where := "gs.status = ? AND b.user_id != 0 AND b.bill_type != 12"
	args := []interface{}{model.SessionStatusCompleted}
	if startTime != nil {
		where += " AND gs.started_at >= ?"
		args = append(args, *startTime)
	}
	if endTime != nil {
		where += " AND gs.started_at < ?"
		args = append(args, *endTime)
	}
	if configName != "" {
		where += " AND gs.config_name = ?"
		args = append(args, configName)
	}

	// 总数（不分组、不排序）
	type countResult struct {
		Total int64
	}
	countSQL := "SELECT COUNT(DISTINCT gs.session_id) AS total FROM " + join + " WHERE " + where
	countArgs := append(append([]interface{}{}, joinArgs...), args...)
	var cr countResult
	if err := r.db.Raw(countSQL, countArgs...).Scan(&cr).Error; err != nil {
		return nil, 0, err
	}

	// 列表
	listSQL := `SELECT
       gs.session_id, gs.room_no, gs.config_name, gs.room_fee, gs.max_rounds, gs.actual_rounds,
       gs.status, gs.started_at, gs.ended_at, gs.end_reason,
       MAX(sp.seat_no) AS seat_no, MAX(sp.joined_at) AS joined_at,
       COALESCE(SUM(CASE WHEN b.bill_type = 3 AND b.amount > 0 THEN b.amount ELSE 0 END), 0) AS total_grab,
       COALESCE(SUM(CASE WHEN b.bill_type = 4 AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS total_send,
       COALESCE(SUM(CASE WHEN b.bill_type = 2 AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS first_round_fee,
       COALESCE(SUM(CASE WHEN b.bill_type = 8 AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS penalty,
       COALESCE(SUM(CASE WHEN b.bill_type IN (2,4,8) AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS total_bet,
       COALESCE(SUM(CASE WHEN b.bill_type IN (3,10,11) AND b.amount > 0 THEN b.amount ELSE 0 END), 0) AS total_income,
       COALESCE(SUM(CASE WHEN b.bill_type IN (10,11) AND b.amount > 0 THEN b.amount ELSE 0 END), 0) AS reward,
       COALESCE(SUM(CASE WHEN b.bill_type IN (3,10,11) AND b.amount > 0 THEN b.amount ELSE 0 END), 0)
         - COALESCE(SUM(CASE WHEN b.bill_type IN (2,4,8) AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS profit,
       COALESCE(SUM(CASE WHEN b.bill_type = 3 THEN 1 ELSE 0 END), 0) AS grab_count,
       COALESCE(SUM(CASE WHEN b.bill_type = 4 THEN 1 ELSE 0 END), 0) AS send_count
       FROM ` + join + `
       WHERE ` + where + `
       GROUP BY gs.session_id
       ORDER BY gs.started_at DESC
       LIMIT ? OFFSET ?`
	listArgs := append(append([]interface{}{}, joinArgs...), args...)
	listArgs = append(listArgs, limit, offset)

	var rows []repository.PlayerSessionBillRow
	if err := r.db.Raw(listSQL, listArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, cr.Total, nil
}

// GetPlayerSessionBillSummary 单局个人结果卡片（基于 bill_record 聚合）。
// 按 (session_id, user_id) 聚合，profit = total_income - total_bet。
func (r *gormHistoryRepository) GetPlayerSessionBillSummary(userID, sessionID int64) (*repository.PlayerSessionBillSummary, error) {
	query := `SELECT
       COALESCE(SUM(CASE WHEN bill_type = 3 AND amount > 0 THEN amount ELSE 0 END), 0) AS total_grab,
       COALESCE(SUM(CASE WHEN bill_type = 4 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS total_send,
       COALESCE(SUM(CASE WHEN bill_type = 2 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS first_round_fee,
       COALESCE(SUM(CASE WHEN bill_type = 8 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS penalty,
       COALESCE(SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS total_bet,
       COALESCE(SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END), 0) AS total_income,
       COALESCE(SUM(CASE WHEN bill_type IN (10,11) AND amount > 0 THEN amount ELSE 0 END), 0) AS reward,
       COALESCE(SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END), 0)
         - COALESCE(SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS profit,
       COALESCE(SUM(CASE WHEN bill_type = 3 THEN 1 ELSE 0 END), 0) AS grab_count,
       COALESCE(SUM(CASE WHEN bill_type = 4 THEN 1 ELSE 0 END), 0) AS send_count
       FROM bill_record
       WHERE session_id = ? AND user_id = ? AND status = 1 AND user_id != 0 AND bill_type != 12`
	var summary repository.PlayerSessionBillSummary
	if err := r.db.Raw(query, sessionID, userID).Scan(&summary).Error; err != nil {
		return nil, err
	}
	return &summary, nil
}

// AggregatePlayerStatsFromBill 玩家累计统计（基于 bill_record 聚合）。
// 内层按 session_id 分组计算每局盈亏，外层汇总 games/win/各总额。
func (r *gormHistoryRepository) AggregatePlayerStatsFromBill(userID int64) (*repository.PlayerStatsBillAggregate, error) {
	query := `SELECT
       COUNT(*) AS total_games,
       COALESCE(SUM(CASE WHEN profit > 0 THEN 1 ELSE 0 END), 0) AS win_count,
       COALESCE(SUM(total_grab), 0) AS total_grab,
       COALESCE(SUM(total_send), 0) AS total_send,
       COALESCE(SUM(first_round_fee), 0) AS first_round_fee,
       COALESCE(SUM(penalty), 0) AS penalty,
       COALESCE(SUM(total_bet), 0) AS total_bet,
       COALESCE(SUM(total_income), 0) AS total_income,
       COALESCE(SUM(reward), 0) AS reward,
       COALESCE(SUM(profit), 0) AS total_profit,
       COALESCE(SUM(grab_count), 0) AS total_grab_count,
       COALESCE(SUM(send_count), 0) AS total_send_count
       FROM (
           SELECT
               session_id,
               COALESCE(SUM(CASE WHEN bill_type = 3 AND amount > 0 THEN amount ELSE 0 END), 0) AS total_grab,
               COALESCE(SUM(CASE WHEN bill_type = 4 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS total_send,
               COALESCE(SUM(CASE WHEN bill_type = 2 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS first_round_fee,
               COALESCE(SUM(CASE WHEN bill_type = 8 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS penalty,
               COALESCE(SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS total_bet,
               COALESCE(SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END), 0) AS total_income,
               COALESCE(SUM(CASE WHEN bill_type IN (10,11) AND amount > 0 THEN amount ELSE 0 END), 0) AS reward,
               COALESCE(SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END), 0)
                 - COALESCE(SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS profit,
               COALESCE(SUM(CASE WHEN bill_type = 3 THEN 1 ELSE 0 END), 0) AS grab_count,
               COALESCE(SUM(CASE WHEN bill_type = 4 THEN 1 ELSE 0 END), 0) AS send_count
           FROM bill_record
           WHERE user_id = ? AND status = 1 AND user_id != 0 AND bill_type != 12
           GROUP BY session_id
       ) sub`
	var agg repository.PlayerStatsBillAggregate
	if err := r.db.Raw(query, userID).Scan(&agg).Error; err != nil {
		return nil, err
	}
	return &agg, nil
}

// GetPlayerSendRounds 查询会话中玩家作为发送者的回合（含 player 与 system_resume），按 round_no 升序。
func (r *gormHistoryRepository) GetPlayerSendRounds(sessionID, userID int64) ([]model.Round, error) {
	var rounds []model.Round
	err := r.db.Where("session_id = ? AND sender_id = ? AND sender_type IN ?", sessionID, userID, []string{"player", "system_resume"}).
		Order("round_no ASC").
		Find(&rounds).Error
	if err != nil {
		return nil, err
	}
	return rounds, nil
}

// ListSessionSpecialRewards 查询会话内所有特殊奖励记录（顺子/豹子，按 round_no 升序）。
// 利用 special_rewards 表的 session_id 索引，一次性获取整局所有回合的特殊奖励。
func (r *gormHistoryRepository) ListSessionSpecialRewards(sessionID int64) ([]model.SpecialReward, error) {
	var rewards []model.SpecialReward
	err := r.db.Where("session_id = ?", sessionID).
		Order("round_no ASC").
		Find(&rewards).Error
	if err != nil {
		return nil, err
	}
	return rewards, nil
}

// ListPlayerRoundPenalties 查询玩家在会话内每个 round 的罚款扣款明细。
// 数据源：bill_record（bill_type=8 罚款收入，amount<0 表示支出）。
// 按 round_id/round_no 分组聚合，过滤 status=1（Success）与 user_id!=0。
// 与会话级 summary 的 penalty 字段同源，保证 round 级与 session 级对账一致。
func (r *gormHistoryRepository) ListPlayerRoundPenalties(sessionID, userID int64) ([]repository.PlayerRoundPenaltyRow, error) {
	query := `SELECT round_id, round_no,
       ABS(SUM(amount)) AS amount,
       MAX(penalty_type) AS penalty_type,
       COUNT(*) AS count
       FROM bill_record
       WHERE session_id = ? AND user_id = ? AND bill_type = 8
         AND amount < 0 AND status = 1 AND user_id != 0
       GROUP BY round_id, round_no
       ORDER BY round_no ASC`
	var rows []repository.PlayerRoundPenaltyRow
	if err := r.db.Raw(query, sessionID, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListPlayerRoundPenaltyDistributions 查询玩家在会话内每个 round 收到的罚款分红。
// 数据源：bill_record（bill_type=10 罚款分发，amount>0 表示收入）。
// 按 round_id/round_no 分组聚合，过滤 status=1（Success）与 user_id!=0（排除平台总账行）。
// 与会话级 summary 的 reward 字段中 bill_type=10 部分同源，保证对账一致。
func (r *gormHistoryRepository) ListPlayerRoundPenaltyDistributions(sessionID, userID int64) ([]repository.PlayerRoundPenaltyDistRow, error) {
	query := `SELECT round_id, round_no, SUM(amount) AS amount
       FROM bill_record
       WHERE session_id = ? AND user_id = ? AND bill_type = 10
         AND amount > 0 AND status = 1 AND user_id != 0
       GROUP BY round_id, round_no
       ORDER BY round_no ASC`
	var rows []repository.PlayerRoundPenaltyDistRow
	if err := r.db.Raw(query, sessionID, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
