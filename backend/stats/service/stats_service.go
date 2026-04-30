package service

import (
	"context"
	"time"

	"github.com/cashparty/backend/stats/dto"
	"github.com/cashparty/backend/stats/repository"
)

type StatsService struct {
	repo *repository.StatsRepository
}

func NewStatsService(repo *repository.StatsRepository) *StatsService {
	return &StatsService{repo: repo}
}

func (s *StatsService) GetDashboardStats(ctx context.Context, startDate, endDate time.Time) (*dto.DashboardStats, error) {
	return s.repo.GetDashboardStats(ctx, startDate, endDate)
}

func (s *StatsService) GetHourlyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.HourlyTrend, error) {
	return s.repo.GetHourlyTrend(ctx, startDate, endDate)
}

func (s *StatsService) GetAmountDistribution(ctx context.Context, startDate, endDate time.Time) ([]dto.AmountDistribution, error) {
	return s.repo.GetAmountDistribution(ctx, startDate, endDate)
}

func (s *StatsService) GetRoomRanking(ctx context.Context, startDate, endDate time.Time, limit int) ([]dto.RoomRanking, error) {
	if limit <= 0 {
		limit = 10
	}
	return s.repo.GetRoomRanking(ctx, startDate, endDate, limit)
}

func (s *StatsService) GetSystemPacketStats(ctx context.Context, date time.Time) ([]dto.SystemPacketStats, error) {
	return s.repo.GetSystemPacketStats(ctx, date)
}

func (s *StatsService) GetDailyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.DailyTrend, error) {
	return s.repo.GetDailyTrend(ctx, startDate, endDate)
}
