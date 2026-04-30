package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cashparty/backend/stats/config"
	"github.com/cashparty/backend/stats/dto"
	"gorm.io/gorm"
)

type StatsRepository struct {
	db           *gorm.DB
	amountRanges []config.AmountRange
}

func NewStatsRepository(db *gorm.DB, amountRanges []config.AmountRange) *StatsRepository {
	return &StatsRepository{db: db, amountRanges: amountRanges}
}

// buildAmountCaseWhen 根据配置动态生成 CASE WHEN 表达式.
func buildAmountCaseWhen(ranges []config.AmountRange) string {
	var whens []string
	for i, r := range ranges {
		if i == len(ranges)-1 {
			// 最后一项是 ELSE (开放式区间)
			whens = append(whens, fmt.Sprintf("ELSE '%s'", r.Label))
		} else {
			whens = append(whens, fmt.Sprintf("WHEN amount < %d THEN '%s'", r.Max, r.Label))
		}
	}
	return "CASE " + strings.Join(whens, " ") + " END"
}

// dateRange 返回起止日期字符串和下一天字符串, 用于 created_at >= ? AND created_at < ? 的范围查询.
func dateRange(startDate, endDate time.Time) (start, nextDay string) {
	return startDate.Format("2006-01-02"), endDate.AddDate(0, 0, 1).Format("2006-01-02")
}

func (r *StatsRepository) GetDashboardStats(ctx context.Context, startDate, endDate time.Time) (*dto.DashboardStats, error) {
	var stats dto.DashboardStats
	start, nextDay := dateRange(startDate, endDate)

	// 合并 rounds 相关的聚合: today_rounds, total_commission, system_packet_cost, system_packet_count
	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			(SELECT COUNT(*) FROM game_sessions WHERE created_at >= ? AND created_at < ?) as today_sessions,
			today_rounds,
			(SELECT COUNT(DISTINCT user_id) FROM session_players WHERE joined_at >= ? AND joined_at < ?) as active_players,
			total_commission,
			(SELECT COALESCE(SUM(amount), 0) FROM bill_record WHERE bill_type = 8 AND amount > 0 AND status = 1 AND created_at >= ? AND created_at < ?) as penalty_income,
			system_packet_cost,
			(SELECT COUNT(*) FROM special_rewards WHERE reward_type = 1 AND created_at >= ? AND created_at < ?) as straight_count,
			(SELECT COUNT(*) FROM special_rewards WHERE reward_type = 2 AND created_at >= ? AND created_at < ?) as leopard_count,
			system_packet_count,
			total_commission + (SELECT COALESCE(SUM(amount), 0) FROM bill_record WHERE bill_type = 8 AND amount > 0 AND status = 1 AND created_at >= ? AND created_at < ?) - system_packet_cost as net_profit
		FROM (
			SELECT 
				COUNT(*) as today_rounds,
				COALESCE(SUM(commission), 0) as total_commission,
				COALESCE(SUM(CASE WHEN sender_type IN ('system', 'system_forced', 'system_resume') THEN total_amount ELSE 0 END), 0) as system_packet_cost,
				SUM(CASE WHEN sender_type IN ('system', 'system_forced', 'system_resume') THEN 1 ELSE 0 END) as system_packet_count
			FROM rounds
			WHERE created_at >= ? AND created_at < ?
		) r
	`, start, nextDay, start, nextDay, start, nextDay, start, nextDay, start, nextDay, start, nextDay, start, nextDay).Scan(&stats).Error

	if err != nil {
		return nil, err
	}

	return &stats, nil
}

func (r *StatsRepository) GetHourlyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.HourlyTrend, error) {
	var trends []dto.HourlyTrend
	start, nextDay := dateRange(startDate, endDate)

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			LPAD(h, 2, '0') as hour,
			round_count,
			commission
		FROM (
			SELECT 
				HOUR(created_at) as h,
				COUNT(*) as round_count,
				COALESCE(SUM(commission), 0) as commission
			FROM rounds
			WHERE created_at >= ? AND created_at < ?
			GROUP BY HOUR(created_at)
		) t
		ORDER BY h
	`, start, nextDay).Scan(&trends).Error

	if err != nil {
		return nil, err
	}
	if trends == nil {
		trends = []dto.HourlyTrend{}
	}
	return trends, nil
}

func (r *StatsRepository) GetAmountDistribution(ctx context.Context, startDate, endDate time.Time) ([]dto.AmountDistribution, error) {
	var distributions []dto.AmountDistribution
	start, nextDay := dateRange(startDate, endDate)

	caseExpr := buildAmountCaseWhen(r.amountRanges)

	sql := fmt.Sprintf(`
		SELECT 
			%s as `+"`range`"+`,
			COUNT(*) as count,
			COALESCE(SUM(amount), 0) as total_amount,
			COALESCE(CAST(AVG(amount) AS SIGNED), 0) as avg_amount
		FROM packets
		WHERE created_at >= ? AND created_at < ?
		GROUP BY 
			%s
		ORDER BY MIN(amount)
	`, caseExpr, caseExpr)

	err := r.db.WithContext(ctx).Raw(sql, start, nextDay).Scan(&distributions).Error

	if err != nil {
		return nil, err
	}
	if distributions == nil {
		distributions = []dto.AmountDistribution{}
	}
	return distributions, nil
}

func (r *StatsRepository) GetRoomRanking(ctx context.Context, startDate, endDate time.Time, limit, offset int) ([]dto.RoomRanking, error) {
	var rankings []dto.RoomRanking
	start, nextDay := dateRange(startDate, endDate)

	// 消除 correlated subquery, 改用 JOIN + 预聚合子查询
	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			r.room_id,
			rm.config_name as room_name,
			COUNT(DISTINCT r.session_id) as session_count,
			COUNT(*) as round_count,
			COALESCE(sp.player_count, 0) as player_count,
			COALESCE(SUM(r.total_amount), 0) as total_amount,
			COALESCE(SUM(r.commission), 0) as commission,
			COALESCE(sr.straight_count, 0) as straight_count,
			COALESCE(lr.leopard_count, 0) as leopard_count
		FROM rounds r
		LEFT JOIN rooms rm ON r.room_id = rm.room_id
		LEFT JOIN (
			SELECT room_id, COUNT(DISTINCT user_id) as player_count 
			FROM session_players 
			WHERE joined_at >= ? AND joined_at < ? 
			GROUP BY room_id
		) sp ON r.room_id = sp.room_id
		LEFT JOIN (
			SELECT room_id, COUNT(*) as straight_count 
			FROM special_rewards 
			WHERE reward_type = 1 AND created_at >= ? AND created_at < ? 
			GROUP BY room_id
		) sr ON r.room_id = sr.room_id
		LEFT JOIN (
			SELECT room_id, COUNT(*) as leopard_count 
			FROM special_rewards 
			WHERE reward_type = 2 AND created_at >= ? AND created_at < ? 
			GROUP BY room_id
		) lr ON r.room_id = lr.room_id
		WHERE r.created_at >= ? AND r.created_at < ?
		GROUP BY r.room_id, rm.config_name, sp.player_count, sr.straight_count, lr.leopard_count
		ORDER BY round_count DESC
		LIMIT ? OFFSET ?
	`, start, nextDay, start, nextDay, start, nextDay, start, nextDay, limit, offset).Scan(&rankings).Error

	if err != nil {
		return nil, err
	}
	if rankings == nil {
		rankings = []dto.RoomRanking{}
	}
	return rankings, nil
}

func (r *StatsRepository) GetSystemPacketStats(ctx context.Context, startDate, endDate time.Time) ([]dto.SystemPacketStats, error) {
	var stats []dto.SystemPacketStats
	start, nextDay := dateRange(startDate, endDate)

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			sender_type,
			COUNT(*) as send_count,
			COALESCE(SUM(total_amount), 0) as total_amount,
			COALESCE(CAST(AVG(total_amount) AS SIGNED), 0) as avg_amount
		FROM rounds
		WHERE sender_type IN ('system', 'system_forced', 'system_resume')
		AND created_at >= ? AND created_at < ?
		GROUP BY sender_type
	`, start, nextDay).Scan(&stats).Error

	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = []dto.SystemPacketStats{}
	}
	return stats, nil
}

func (r *StatsRepository) GetDailyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.DailyTrend, error) {
	var trends []dto.DailyTrend
	start, nextDay := dateRange(startDate, endDate)

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			t.date,
			t.round_count,
			t.total_commission,
			COALESCE(p.penalty_income, 0) as penalty_income,
			COALESCE(s.system_packet_cost, 0) as system_packet_cost,
			t.total_commission + COALESCE(p.penalty_income, 0) - COALESCE(s.system_packet_cost, 0) as net_profit
		FROM (
			SELECT 
				DATE(created_at) as date,
				COUNT(*) as round_count,
				COALESCE(SUM(commission), 0) as total_commission
			FROM rounds 
			WHERE created_at >= ? AND created_at < ?
			GROUP BY DATE(created_at)
		) t
		LEFT JOIN (
			SELECT 
				DATE(created_at) as date,
				COALESCE(SUM(amount), 0) as penalty_income
			FROM bill_record 
			WHERE bill_type = 8 AND amount > 0 AND status = 1
			AND created_at >= ? AND created_at < ?
			GROUP BY DATE(created_at)
		) p ON t.date = p.date
		LEFT JOIN (
			SELECT 
				DATE(created_at) as date,
				COALESCE(SUM(total_amount), 0) as system_packet_cost
			FROM rounds 
			WHERE sender_type IN ('system', 'system_forced', 'system_resume')
			AND created_at >= ? AND created_at < ?
			GROUP BY DATE(created_at)
		) s ON t.date = s.date
		ORDER BY t.date
	`, start, nextDay, start, nextDay, start, nextDay).Scan(&trends).Error

	if err != nil {
		return nil, err
	}
	if trends == nil {
		trends = []dto.DailyTrend{}
	}
	return trends, nil
}
