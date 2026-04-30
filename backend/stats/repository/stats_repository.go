package repository

import (
	"context"
	"time"

	"github.com/cashparty/backend/stats/dto"
	"gorm.io/gorm"
)

type StatsRepository struct {
	db *gorm.DB
}

func NewStatsRepository(db *gorm.DB) *StatsRepository {
	return &StatsRepository{db: db}
}

func (r *StatsRepository) GetDashboardStats(ctx context.Context, startDate, endDate time.Time) (*dto.DashboardStats, error) {
	var stats dto.DashboardStats
	startDateStr := startDate.Format("2006-01-02")
	endDateStr := endDate.Format("2006-01-02")

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			(SELECT COUNT(*) FROM game_sessions WHERE created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as today_sessions,
			(SELECT COUNT(*) FROM rounds WHERE created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as today_rounds,
			(SELECT COUNT(DISTINCT user_id) FROM session_players WHERE joined_at >= ? AND joined_at < ? + INTERVAL 1 DAY) as active_players,
			(SELECT COALESCE(SUM(commission), 0) FROM rounds WHERE created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as total_commission,
			(SELECT COALESCE(SUM(amount), 0) FROM bill_record WHERE bill_type = 8 AND amount > 0 AND status = 1 AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as penalty_income,
			(SELECT COALESCE(SUM(total_amount), 0) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as system_packet_cost,
			(SELECT COUNT(*) FROM special_rewards WHERE reward_type = 1 AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as straight_count,
			(SELECT COUNT(*) FROM special_rewards WHERE reward_type = 2 AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as leopard_count,
			(SELECT COUNT(*) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY) as system_packet_count
	`, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr).Scan(&stats).Error

	if err != nil {
		return nil, err
	}

	stats.NetProfit = stats.TotalCommission + stats.PenaltyIncome - stats.SystemPacketCost

	return &stats, nil
}

func (r *StatsRepository) GetHourlyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.HourlyTrend, error) {
	var trends []dto.HourlyTrend
	startDateStr := startDate.Format("2006-01-02")
	endDateStr := endDate.Format("2006-01-02")

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			CONCAT('1970-01-01 ', LPAD(h, 2, '0'), ':00') as hour,
			round_count,
			commission
		FROM (
			SELECT 
				HOUR(created_at) as h,
				COUNT(*) as round_count,
				COALESCE(SUM(commission), 0) as commission
			FROM rounds
			WHERE created_at >= ? AND created_at < ? + INTERVAL 1 DAY
			GROUP BY HOUR(created_at)
		) t
		ORDER BY h
	`, startDateStr, endDateStr).Scan(&trends).Error

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
	startDateStr := startDate.Format("2006-01-02")
	endDateStr := endDate.Format("2006-01-02")

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			CASE 
				WHEN amount < 1000 THEN '0-10元'
				WHEN amount < 5000 THEN '10-50元'
				WHEN amount < 10000 THEN '50-100元'
				WHEN amount < 50000 THEN '100-500元'
				ELSE '500元以上'
			END as `+"`range`"+`,
			COUNT(*) as count,
			COALESCE(SUM(amount), 0) as total_amount,
			COALESCE(CAST(AVG(amount) AS SIGNED), 0) as avg_amount
		FROM packets
		WHERE created_at >= ? AND created_at < ? + INTERVAL 1 DAY
		GROUP BY 
			CASE 
				WHEN amount < 1000 THEN '0-10元'
				WHEN amount < 5000 THEN '10-50元'
				WHEN amount < 10000 THEN '50-100元'
				WHEN amount < 50000 THEN '100-500元'
				ELSE '500元以上'
			END
		ORDER BY MIN(amount)
	`, startDateStr, endDateStr).Scan(&distributions).Error

	if err != nil {
		return nil, err
	}
	if distributions == nil {
		distributions = []dto.AmountDistribution{}
	}
	return distributions, nil
}

func (r *StatsRepository) GetRoomRanking(ctx context.Context, startDate, endDate time.Time, limit int) ([]dto.RoomRanking, error) {
	var rankings []dto.RoomRanking
	startDateStr := startDate.Format("2006-01-02")
	endDateStr := endDate.Format("2006-01-02")

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			r.room_id,
			rm.config_name as room_name,
			COUNT(DISTINCT r.session_id) as session_count,
			COUNT(*) as round_count,
			(SELECT COUNT(DISTINCT user_id) FROM session_players sp2 WHERE sp2.room_id = r.room_id AND sp2.joined_at >= ? AND sp2.joined_at < ? + INTERVAL 1 DAY) as player_count,
			COALESCE(SUM(r.total_amount), 0) as total_amount,
			COALESCE(SUM(r.commission), 0) as commission,
			(SELECT COUNT(*) FROM special_rewards sr WHERE sr.room_id = r.room_id AND sr.reward_type = 1 AND sr.created_at >= ? AND sr.created_at < ? + INTERVAL 1 DAY) as straight_count,
			(SELECT COUNT(*) FROM special_rewards sr WHERE sr.room_id = r.room_id AND sr.reward_type = 2 AND sr.created_at >= ? AND sr.created_at < ? + INTERVAL 1 DAY) as leopard_count
		FROM rounds r
		LEFT JOIN rooms rm ON r.room_id = rm.room_id
		WHERE r.created_at >= ? AND r.created_at < ? + INTERVAL 1 DAY
		GROUP BY r.room_id, rm.config_name
		ORDER BY round_count DESC
		LIMIT ?
	`, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr, limit).Scan(&rankings).Error

	if err != nil {
		return nil, err
	}
	if rankings == nil {
		rankings = []dto.RoomRanking{}
	}
	return rankings, nil
}

func (r *StatsRepository) GetSystemPacketStats(ctx context.Context, date time.Time) ([]dto.SystemPacketStats, error) {
	var stats []dto.SystemPacketStats
	dateStr := date.Format("2006-01-02")

	err := r.db.WithContext(ctx).Raw(`
		SELECT 
			sender_type,
			COUNT(*) as send_count,
			COALESCE(SUM(total_amount), 0) as total_amount,
			COALESCE(CAST(AVG(total_amount) AS SIGNED), 0) as avg_amount
		FROM rounds
		WHERE sender_type IN ('system', 'system_forced', 'system_resume')
		AND DATE(created_at) = ?
		GROUP BY sender_type
	`, dateStr).Scan(&stats).Error

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

	startDateStr := startDate.Format("2006-01-02")
	endDateStr := endDate.Format("2006-01-02")

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
			WHERE created_at >= ? AND created_at < ? + INTERVAL 1 DAY
			GROUP BY DATE(created_at)
		) t
		LEFT JOIN (
			SELECT 
				DATE(created_at) as date,
				COALESCE(SUM(amount), 0) as penalty_income
			FROM bill_record 
			WHERE bill_type = 8 AND amount > 0 AND status = 1
			AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY
			GROUP BY DATE(created_at)
		) p ON t.date = p.date
		LEFT JOIN (
			SELECT 
				DATE(created_at) as date,
				COALESCE(SUM(total_amount), 0) as system_packet_cost
			FROM rounds 
			WHERE sender_type IN ('system', 'system_forced', 'system_resume')
			AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY
			GROUP BY DATE(created_at)
		) s ON t.date = s.date
		ORDER BY t.date
	`, startDateStr, endDateStr, startDateStr, endDateStr, startDateStr, endDateStr).Scan(&trends).Error

	if err != nil {
		return nil, err
	}
	if trends == nil {
		trends = []dto.DailyTrend{}
	}
	return trends, nil
}
