package limiter

import (
	"context"
	"fmt"
	"time"

	limiterScripts "github.com/cashparty/backend/common/limiter/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
)

type RateLimiter struct {
	redis  *cRedis.Client
	prefix string
}

func NewRateLimiter(redis *cRedis.Client, prefix string) *RateLimiter {
	return &RateLimiter{
		redis: redis,
		// 加上 cashparty: 命名空间前缀，避免被清理脚本漏掉（规约 SC-5 / P1-4 修复）
		prefix: rediskeys.KeyPrefix + ":" + prefix,
	}
}

type LimitConfig struct {
	Key    string
	Limit  int64
	Window time.Duration
}

func (l *RateLimiter) Allow(ctx context.Context, cfg *LimitConfig) (bool, error) {
	key := fmt.Sprintf("%s:%s", l.prefix, cfg.Key)
	now := time.Now().UnixNano()

	result, err := limiterScripts.SlidingWindowScript.Run(ctx, l.redis, []string{key}, cfg.Limit, int64(cfg.Window), now).Int64()
	if err != nil {
		logger.Error("rate limiter error", "key", key, "error", err)
		return true, nil
	}

	return result == 1, nil
}

func (l *RateLimiter) AllowN(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	return l.Allow(ctx, &LimitConfig{
		Key:    key,
		Limit:  limit,
		Window: window,
	})
}

func (l *RateLimiter) FixedWindow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	fullKey := fmt.Sprintf("%s:fixed:%s:%d", l.prefix, key, time.Now().Unix()/int64(window.Seconds()))

	count, err := l.redis.Incr(ctx, fullKey).Result()
	if err != nil {
		logger.Error("fixed window error", "key", fullKey, "error", err)
		return true, nil
	}

	if count == 1 {
		l.redis.Expire(ctx, fullKey, window)
	}

	return count <= limit, nil
}

type UserLimiter struct {
	limiter *RateLimiter
	configs map[string]*LimitConfig
}

func NewUserLimiter(redis *cRedis.Client) *UserLimiter {
	return &UserLimiter{
		limiter: NewRateLimiter(redis, "user"),
		configs: map[string]*LimitConfig{
			"grab": {
				Key:    "grab",
				Limit:  10,
				Window: time.Second,
			},
			"join": {
				Key:    "join",
				Limit:  5,
				Window: time.Minute,
			},
			"create": {
				Key:    "create",
				Limit:  3,
				Window: time.Minute,
			},
		},
	}
}

func (ul *UserLimiter) AllowGrab(ctx context.Context, userID string) (bool, error) {
	cfg := ul.configs["grab"]
	cfg.Key = fmt.Sprintf("grab:%s", userID)
	return ul.limiter.Allow(ctx, cfg)
}

func (ul *UserLimiter) AllowJoin(ctx context.Context, userID string) (bool, error) {
	cfg := ul.configs["join"]
	cfg.Key = fmt.Sprintf("join:%s", userID)
	return ul.limiter.Allow(ctx, cfg)
}

func (ul *UserLimiter) AllowCreate(ctx context.Context, userID string) (bool, error) {
	cfg := ul.configs["create"]
	cfg.Key = fmt.Sprintf("create:%s", userID)
	return ul.limiter.Allow(ctx, cfg)
}

type IPLimiter struct {
	limiter *RateLimiter
}

func NewIPLimiter(redis *cRedis.Client) *IPLimiter {
	return &IPLimiter{
		limiter: NewRateLimiter(redis, "ip"),
	}
}

func (il *IPLimiter) Allow(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	return il.limiter.AllowN(ctx, ip, limit, window)
}

func (il *IPLimiter) AllowConnection(ctx context.Context, ip string) (bool, error) {
	return il.Allow(ctx, ip, 100, time.Minute)
}

func (il *IPLimiter) AllowRequest(ctx context.Context, ip string) (bool, error) {
	return il.Allow(ctx, ip, 1000, time.Minute)
}
