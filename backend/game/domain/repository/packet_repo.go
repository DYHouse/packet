package repository

import (
	"context"
	"time"
)

// PacketCacheRepository 红包缓存仓储接口
// 抽象 PacketGenerator 在 Redis 上的缓存依赖，让 algorithm 包不再直接依赖 infrastructure。
// 当 key 不存在时 Get 返回非 nil error（沿用 redis.Nil 语义），调用方据此判断是否命中缓存。
type PacketCacheRepository interface {
	// Get 获取红包生成结果缓存
	Get(ctx context.Context, roomID, roundID string) (string, error)
	// SetNX 仅当 key 不存在时设置缓存，返回 true 表示设置成功
	SetNX(ctx context.Context, roomID, roundID string, value string, ttl time.Duration) (bool, error)
	// GetPacketInfo 根据 packetID 获取红包详情 JSON 字符串，key 不存在时返回 redis.Nil 错误。
	GetPacketInfo(ctx context.Context, roomID, packetID string) (string, error)
	// GetAvailablePacketIDs 获取轮次可用红包 ID 列表（按插入顺序返回）。
	GetAvailablePacketIDs(ctx context.Context, roomID, roundID string) ([]string, error)
}

// RewardCycleType 奖励周期类型，区分顺子与豹子
type RewardCycleType int

const (
	// RewardCycleStraight 顺子奖励周期
	RewardCycleStraight RewardCycleType = iota + 1
	// RewardCycleLeopard 豹子奖励周期
	RewardCycleLeopard
)

// RewardCacheRepository 奖励控制缓存仓储接口
// 抽象 RewardController 在 Redis 上的依赖，让 algorithm 包不再直接依赖 infrastructure。
type RewardCacheRepository interface {
	// GetCycleWon 获取会话内某奖励类型是否已触发（0=未触发, 1=已触发）
	GetCycleWon(ctx context.Context, roomID, sessionID string, cycleType RewardCycleType) (int, error)
	// SetCycleWon 标记会话内某奖励类型已触发
	SetCycleWon(ctx context.Context, roomID, sessionID string, cycleType RewardCycleType, ttl time.Duration) error
	// ClearRewardCycles 清除会话的所有奖励周期标记
	ClearRewardCycles(ctx context.Context, roomID, sessionID string) error
	// GetDailyProfit 获取当日利润数据
	GetDailyProfit(ctx context.Context, date string) (totalBet, totalWin, totalReward int64, err error)
	// RecordDailyProfit 累加当日利润
	RecordDailyProfit(ctx context.Context, date string, betAmount, winAmount, rewardAmount int64, ttl time.Duration) error
}
