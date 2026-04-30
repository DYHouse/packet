package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cashparty/backend/stats/dto"
	"github.com/cashparty/backend/stats/repository"
	"github.com/redis/go-redis/v9"
)

const (
	cacheTTL      = 5 * time.Minute
	emptyCacheTTL = 1 * time.Minute
	cachePrefix   = "stats"
)

type StatsService struct {
	repo  *repository.StatsRepository
	cache redis.Cmdable
}

func NewStatsService(repo *repository.StatsRepository, cache redis.Cmdable) *StatsService {
	return &StatsService{repo: repo, cache: cache}
}

// formatDateRange 返回日期范围的缓存 key 后缀.
func formatDateRange(startDate, endDate time.Time) string {
	return fmt.Sprintf("%s:%s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
}

// cacheKey 生成缓存 key.
func cacheKey(prefix, method string, startDate, endDate time.Time) string {
	return fmt.Sprintf("%s:%s:%s", cachePrefix, method, formatDateRange(startDate, endDate))
}

// getCache 尝试从缓存读取, 如果缓存不可用则静默降级.
func getCache[T any](s *StatsService, ctx context.Context, key string) (T, bool) {
	var zero T
	if s.cache == nil {
		return zero, false
	}
	val, err := s.cache.Get(ctx, key).Result()
	if err != nil {
		return zero, false
	}
	var result T
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		return zero, false
	}
	return result, true
}

// setCache 写入缓存, 如果缓存不可用则静默降级.
func setCache(s *StatsService, ctx context.Context, key string, value interface{}, ttl time.Duration) {
	if s.cache == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	s.cache.Set(ctx, key, data, ttl)
}

func (s *StatsService) GetDashboardStats(ctx context.Context, startDate, endDate time.Time) (*dto.DashboardStats, error) {
	key := cacheKey(cachePrefix, "dashboard", startDate, endDate)

	if cached, ok := getCache[*dto.DashboardStats](s, ctx, key); ok {
		return cached, nil
	}

	result, err := s.repo.GetDashboardStats(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	setCache(s, ctx, key, result, cacheTTL)
	return result, nil
}

func (s *StatsService) GetHourlyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.HourlyTrend, error) {
	key := cacheKey(cachePrefix, "hourly", startDate, endDate)

	if cached, ok := getCache[[]dto.HourlyTrend](s, ctx, key); ok {
		return cached, nil
	}

	result, err := s.repo.GetHourlyTrend(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	ttl := cacheTTL
	if len(result) == 0 {
		ttl = emptyCacheTTL
	}
	setCache(s, ctx, key, result, ttl)
	return result, nil
}

func (s *StatsService) GetAmountDistribution(ctx context.Context, startDate, endDate time.Time) ([]dto.AmountDistribution, error) {
	key := cacheKey(cachePrefix, "distribution", startDate, endDate)

	if cached, ok := getCache[[]dto.AmountDistribution](s, ctx, key); ok {
		return cached, nil
	}

	result, err := s.repo.GetAmountDistribution(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	ttl := cacheTTL
	if len(result) == 0 {
		ttl = emptyCacheTTL
	}
	setCache(s, ctx, key, result, ttl)
	return result, nil
}

func (s *StatsService) GetRoomRanking(ctx context.Context, startDate, endDate time.Time, limit, offset int) ([]dto.RoomRanking, error) {
	key := fmt.Sprintf("%s:%s:%s:%d:%d", cachePrefix, "room_ranking", formatDateRange(startDate, endDate), limit, offset)

	if cached, ok := getCache[[]dto.RoomRanking](s, ctx, key); ok {
		return cached, nil
	}

	result, err := s.repo.GetRoomRanking(ctx, startDate, endDate, limit, offset)
	if err != nil {
		return nil, err
	}

	ttl := cacheTTL
	if len(result) == 0 {
		ttl = emptyCacheTTL
	}
	setCache(s, ctx, key, result, ttl)
	return result, nil
}

func (s *StatsService) GetSystemPacketStats(ctx context.Context, startDate, endDate time.Time) ([]dto.SystemPacketStats, error) {
	key := cacheKey(cachePrefix, "system_packets", startDate, endDate)

	if cached, ok := getCache[[]dto.SystemPacketStats](s, ctx, key); ok {
		return cached, nil
	}

	result, err := s.repo.GetSystemPacketStats(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	ttl := cacheTTL
	if len(result) == 0 {
		ttl = emptyCacheTTL
	}
	setCache(s, ctx, key, result, ttl)
	return result, nil
}

func (s *StatsService) GetDailyTrend(ctx context.Context, startDate, endDate time.Time) ([]dto.DailyTrend, error) {
	key := cacheKey(cachePrefix, "daily", startDate, endDate)

	if cached, ok := getCache[[]dto.DailyTrend](s, ctx, key); ok {
		return cached, nil
	}

	result, err := s.repo.GetDailyTrend(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	ttl := cacheTTL
	if len(result) == 0 {
		ttl = emptyCacheTTL
	}
	setCache(s, ctx, key, result, ttl)
	return result, nil
}
