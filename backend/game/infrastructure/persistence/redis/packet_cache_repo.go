package redis

import (
	"context"
	"fmt"
	"time"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	repository "github.com/cashparty/backend/game/domain/repository"
)

// packetCacheRepository 红包生成结果缓存仓储实现
type packetCacheRepository struct {
	client cRedis.RedisClient
}

// NewPacketCacheRepository 创建红包缓存仓储实例
func NewPacketCacheRepository(client cRedis.RedisClient) repository.PacketCacheRepository {
	return &packetCacheRepository{client: client}
}

// Get 获取红包生成结果缓存。key 不存在时返回 redis.Nil 错误。
func (r *packetCacheRepository) Get(ctx context.Context, roundID string) (string, error) {
	key := rediskeys.RoundPacketsKey(roundID)
	return r.client.Get(ctx, key).Result()
}

// SetNX 仅当 key 不存在时设置缓存。
func (r *packetCacheRepository) SetNX(ctx context.Context, roundID string, value string, ttl time.Duration) (bool, error) {
	key := rediskeys.RoundPacketsKey(roundID)
	return r.client.SetNX(ctx, key, value, ttl).Result()
}

// GetPacketInfo 根据 packetID 获取红包详情 JSON 字符串。key 不存在时返回 redis.Nil 错误。
func (r *packetCacheRepository) GetPacketInfo(ctx context.Context, packetID string) (string, error) {
	key := rediskeys.PacketInfoKey(packetID)
	return r.client.Get(ctx, key).Result()
}

// GetAvailablePacketIDs 获取轮次可用红包 ID 列表（按插入顺序返回）。
func (r *packetCacheRepository) GetAvailablePacketIDs(ctx context.Context, roundID string) ([]string, error) {
	key := rediskeys.RoundAvailablePacketsKey(roundID)
	return r.client.LRange(ctx, key, 0, -1).Result()
}

// rewardCacheRepository 奖励控制缓存仓储实现
type rewardCacheRepository struct {
	client cRedis.RedisClient
}

// NewRewardCacheRepository 创建奖励缓存仓储实例
func NewRewardCacheRepository(client cRedis.RedisClient) repository.RewardCacheRepository {
	return &rewardCacheRepository{client: client}
}

// rewardCycleKey 根据周期类型返回对应的 redis key
func rewardCycleKey(roomID, sessionID string, cycleType repository.RewardCycleType) (string, error) {
	switch cycleType {
	case repository.RewardCycleStraight:
		return rediskeys.RewardCycleStraightKey(roomID, sessionID), nil
	case repository.RewardCycleLeopard:
		return rediskeys.RewardCycleLeopardKey(roomID, sessionID), nil
	default:
		return "", fmt.Errorf("invalid reward cycle type: %d", cycleType)
	}
}

// GetCycleWon 获取会话内某奖励类型是否已触发（0=未触发, 1=已触发）
func (r *rewardCacheRepository) GetCycleWon(ctx context.Context, roomID, sessionID string, cycleType repository.RewardCycleType) (int, error) {
	key, err := rewardCycleKey(roomID, sessionID, cycleType)
	if err != nil {
		return 0, err
	}
	return r.client.Get(ctx, key).Int()
}

// SetCycleWon 标记会话内某奖励类型已触发
func (r *rewardCacheRepository) SetCycleWon(ctx context.Context, roomID, sessionID string, cycleType repository.RewardCycleType, ttl time.Duration) error {
	key, err := rewardCycleKey(roomID, sessionID, cycleType)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, key, 1, ttl).Err()
}

// ClearRewardCycles 清除会话的所有奖励周期标记
func (r *rewardCacheRepository) ClearRewardCycles(ctx context.Context, roomID, sessionID string) error {
	keys := []string{
		rediskeys.RewardCycleStraightKey(roomID, sessionID),
		rediskeys.RewardCycleLeopardKey(roomID, sessionID),
	}
	return r.client.Del(ctx, keys...).Err()
}

// GetDailyProfit 获取当日利润数据
func (r *rewardCacheRepository) GetDailyProfit(ctx context.Context, date string) (int64, int64, int64, error) {
	key := rediskeys.ProfitDailyKey(date)
	data, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return 0, 0, 0, err
	}

	var totalBet, totalWin, totalReward int64
	if v, ok := data["total_bet"]; ok {
		fmt.Sscanf(v, "%d", &totalBet)
	}
	if v, ok := data["total_win"]; ok {
		fmt.Sscanf(v, "%d", &totalWin)
	}
	if v, ok := data["total_reward"]; ok {
		fmt.Sscanf(v, "%d", &totalReward)
	}

	return totalBet, totalWin, totalReward, nil
}

// RecordDailyProfit 累加当日利润
func (r *rewardCacheRepository) RecordDailyProfit(ctx context.Context, date string, betAmount, winAmount, rewardAmount int64, ttl time.Duration) error {
	key := rediskeys.ProfitDailyKey(date)
	pipe := r.client.Pipeline()
	pipe.HIncrBy(ctx, key, "total_bet", betAmount)
	pipe.HIncrBy(ctx, key, "total_win", winAmount)
	pipe.HIncrBy(ctx, key, "total_reward", rewardAmount)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}
