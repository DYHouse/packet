package limiter

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	limiterScripts "github.com/cashparty/backend/common/limiter/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
)

// RateLimiter 通用限流器,基于 Redis 滑动窗口实现
type RateLimiter struct {
	redis    cRedis.RedisClient
	failOpen bool
	metrics  *Metrics
}

// Option RateLimiter 配置选项
type Option func(*RateLimiter)

// WithFailOpen 设置 Redis 故障时的 fail-open 策略
func WithFailOpen(failOpen bool) Option {
	return func(rl *RateLimiter) {
		rl.failOpen = failOpen
	}
}

// NewRateLimiter 创建限流器
func NewRateLimiter(redis cRedis.RedisClient, opts ...Option) *RateLimiter {
	rl := &RateLimiter{
		redis:    redis,
		failOpen: true, // 默认 fail-open
		metrics:  &Metrics{},
	}
	for _, opt := range opts {
		opt(rl)
	}
	return rl
}

// LimitConfig 限流配置
type LimitConfig struct {
	Key    string
	Limit  int64
	Window time.Duration
}

// Allow 判断请求是否放行
func (l *RateLimiter) Allow(ctx context.Context, cfg *LimitConfig) (bool, error) {
	// cfg.Key 已由调用方使用 rediskeys 工厂函数构造完整 Redis key
	key := cfg.Key
	now := time.Now().UnixNano()
	// 唯一 member,避免同纳秒请求被 ZADD 去重
	member := fmt.Sprintf("%d-%d", now, rand.Int63())

	result, err := limiterScripts.SlidingWindowScript.Run(
		ctx, l.redis, []string{key}, cfg.Limit, int64(cfg.Window), now, member,
	).Int64()
	if err != nil {
		l.metrics.IncError()
		logger.Error("rate limiter redis error",
			"key", key, "error", err, "fail_open", l.failOpen)
		return l.failOpen, err
	}

	if result == 1 {
		l.metrics.IncAllow()
	} else {
		l.metrics.IncReject()
	}
	return result == 1, nil
}

// UserLimiter 用户级限流器,按命令维度限流
type UserLimiter struct {
	limiter *RateLimiter
	configs map[string]LimitConfig
	mu      sync.RWMutex
}

// NewUserLimiter 创建用户限流器
func NewUserLimiter(redis cRedis.RedisClient, configs map[string]LimitConfig) *UserLimiter {
	return &UserLimiter{
		limiter: NewRateLimiter(redis),
		configs: configs,
	}
}

// UpdateConfigs 热更新限流配置
func (ul *UserLimiter) UpdateConfigs(configs map[string]LimitConfig) {
	ul.mu.Lock()
	defer ul.mu.Unlock()
	ul.configs = configs
}

// Allow 统一限流入口,按命令维度限流
// userID: 用户 ID
// cmd: 命令名(如 "grab"),对应配置中的 key
func (ul *UserLimiter) Allow(ctx context.Context, userID, cmd string) (bool, error) {
	ul.mu.RLock()
	cfg, ok := ul.configs[cmd]
	ul.mu.RUnlock()
	if !ok {
		// 未配置限流的命令,默认放行
		return true, nil
	}

	// 构造完整 Redis key
	key := rediskeys.RateLimitCmdKey(cmd, userID)

	// 创建新 LimitConfig,不修改共享配置
	return ul.limiter.Allow(ctx, &LimitConfig{
		Key:    key,
		Limit:  cfg.Limit,
		Window: cfg.Window,
	})
}
