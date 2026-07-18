package application

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/push"
	repository "github.com/cashparty/backend/game/domain/repository"
)

// LeaderboardService 负责游戏结束时构建完整排行榜。
// 数据源为 MySQL session_players LEFT JOIN bill_record（包含所有参与玩家：
// 原始/被踢/替补），替代原 Redis session_player_totals 作为排行榜数据源。
// total_profit 口径：收入(bill_type IN 3,10,11) - 支出(bill_type IN 2,4,8,14)。
// 包含替补费(14)和罚款(8支出/10分红)，排除 SessionCredit(12)。
type LeaderboardService struct {
	dbRepo repository.DBRepository
	redis  cRedis.RedisClient
}

// NewLeaderboardService 创建 LeaderboardService 实例。
func NewLeaderboardService(dbRepo repository.DBRepository, redis cRedis.RedisClient) *LeaderboardService {
	return &LeaderboardService{
		dbRepo: dbRepo,
		redis:  redis,
	}
}

// BuildFinalLeaderboard 构建完整排行榜，包含所有参与过本 session 的玩家。
// 数据源为 session_players LEFT JOIN bill_record，一次聚合查询。
// session_players 作为基准表保证含被踢玩家（踢人仅删 Redis 不删 DB）和
// 替补玩家（替补时 UpsertPlayer 写入 DB）。
// 返回按 total_profit 降序排列的 GameResult 列表（含 rank）。
// 失败时返回 error，调用方负责降级处理（如使用空 finalResults 广播）。
func (s *LeaderboardService) BuildFinalLeaderboard(
	ctx context.Context, sessionID string,
) ([]push.GameResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("empty session_id")
	}
	sessionIDInt, err := strconv.ParseInt(sessionID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid session_id %q: %w", sessionID, err)
	}

	rows, err := s.dbRepo.HistoryDBRepo().GetSessionLeaderboard(sessionIDInt)
	if err != nil {
		return nil, fmt.Errorf("query session leaderboard failed: %w", err)
	}

	results := make([]push.GameResult, 0, len(rows))
	for i, row := range rows {
		results = append(results, push.GameResult{
			UserID:      converter.FormatID(row.UserID),
			Nickname:    row.Nickname,
			Avatar:      row.Avatar,
			TotalProfit: currency.NewMoneyFromFen(row.TotalProfit),
			Rank:        int32(i + 1),
		})
	}
	return results, nil
}

// CleanupRedisTotals 清理 Redis session_player_totals 缓存。
// DB 已成为权威数据源，Redis totals 不再用于 finalResults 生成，
// 清理避免内存泄漏与下一局数据污染。
func (s *LeaderboardService) CleanupRedisTotals(ctx context.Context, sessionID string) {
	if sessionID == "" || s.redis == nil {
		return
	}
	if err := s.redis.Del(ctx, rediskeys.SessionPlayerTotalsKey(sessionID)).Err(); err != nil {
		logger.Warn("cleanup redis session_player_totals failed",
			"session_id", sessionID, "error", err)
	}
}
