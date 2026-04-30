package middleware

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway"
	gatewayConfig "github.com/cashparty/backend/gateway/config"
	"github.com/gin-gonic/gin"
)

func DefaultRateLimiterConfig() *gatewayConfig.RateLimiterConfig {
	return &gatewayConfig.RateLimiterConfig{
		IPRequestsPerSecond:   100,
		IPBurstSize:           200,
		UserRequestsPerSecond: 50,
		UserBurstSize:         100,
		GlobalRequestsPerSec:  10000,
		CleanupInterval:       time.Minute,
	}
}

type RateLimiter struct {
	redis  *cRedis.Client
	config *gatewayConfig.RateLimiterConfig
	mu     sync.RWMutex
}

func NewRateLimiter(redis *cRedis.Client, config *gatewayConfig.RateLimiterConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimiterConfig()
	}
	return &RateLimiter{
		redis:  redis,
		config: config,
	}
}

func (rl *RateLimiter) UpdateConfig(config *gatewayConfig.RateLimiterConfig) {
	if config == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.config = config
	logger.Info("rate limiter config updated",
		"ip_requests_per_second", config.IPRequestsPerSecond,
		"ip_burst_size", config.IPBurstSize,
		"user_requests_per_second", config.UserRequestsPerSecond,
		"user_burst_size", config.UserBurstSize,
		"global_requests_per_sec", config.GlobalRequestsPerSec,
	)
}

func (rl *RateLimiter) getConfig() *gatewayConfig.RateLimiterConfig {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.config
}

func (rl *RateLimiter) Stop() {
}

func (rl *RateLimiter) AllowIP(ctx context.Context, ip string) (bool, error) {
	cfg := rl.getConfig()
	return rl.slidingWindowAllow(ctx,
		gateway.RateLimitIPKey(ip),
		cfg.IPRequestsPerSecond,
		time.Second)
}

func (rl *RateLimiter) AllowUser(ctx context.Context, userID string) (bool, error) {
	cfg := rl.getConfig()
	return rl.slidingWindowAllow(ctx,
		gateway.RateLimitUserKey(userID),
		cfg.UserRequestsPerSecond,
		time.Second)
}

func (rl *RateLimiter) AllowGlobal(ctx context.Context) (bool, error) {
	cfg := rl.getConfig()
	return rl.slidingWindowAllow(ctx,
		gateway.RateLimitGlobalKey(),
		cfg.GlobalRequestsPerSec,
		time.Second)
}

func (rl *RateLimiter) AllowCommand(ctx context.Context, userID, cmd string, limit int, window time.Duration) (bool, error) {
	key := gateway.RateLimitCmdKey(cmd, userID)
	return rl.slidingWindowAllow(ctx, key, limit, window)
}

func (rl *RateLimiter) slidingWindowAllow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	now := time.Now().UnixNano()
	windowNano := int64(window)

	script := `
		local key = KEYS[1]
		local limit = tonumber(ARGV[1])
		local window = tonumber(ARGV[2])
		local now = tonumber(ARGV[3])
		local windowStart = now - window

		redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStart)
		
		local count = redis.call('ZCARD', key)
		
		if count < limit then
			redis.call('ZADD', key, now, now)
			redis.call('PEXPIRE', key, window / 1000000)
			return 1
		end
		
		return 0
	`

	result, err := rl.redis.Eval(ctx, script, []string{key}, limit, windowNano, now).Int()
	if err != nil {
		logger.Error("rate limiter error", "key", key, "error", err)
		return true, nil
	}

	return result == 1, nil
}

func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		ip := c.ClientIP()

		allowed, err := limiter.AllowIP(ctx, ip)
		if err != nil {
			logger.Error("ip rate limit check failed", "error", err)
			c.Next()
			return
		}

		if !allowed {
			logger.Warn("ip rate limit exceeded", "ip", ip, "path", c.Request.URL.Path)
			c.JSON(429, gin.H{
				"success": false,
				"code":    429,
				"msg":     "rate limit exceeded",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func UserRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		userID, exists := c.Get("user_id")
		if !exists {
			c.Next()
			return
		}

		allowed, err := limiter.AllowUser(ctx, fmt.Sprintf("%v", userID))
		if err != nil {
			logger.Error("user rate limit check failed", "error", err)
			c.Next()
			return
		}

		if !allowed {
			logger.Warn("user rate limit exceeded", "user_id", userID, "path", c.Request.URL.Path)
			c.JSON(429, gin.H{
				"success": false,
				"code":    429,
				"msg":     "rate limit exceeded",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func CommandRateLimitMiddleware(limiter *RateLimiter, cmd string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		userID, exists := c.Get("user_id")
		if !exists {
			c.Next()
			return
		}

		allowed, err := limiter.AllowCommand(ctx, fmt.Sprintf("%v", userID), cmd, limit, window)
		if err != nil {
			logger.Error("command rate limit check failed", "error", err)
			c.Next()
			return
		}

		if !allowed {
			logger.Warn("command rate limit exceeded", "user_id", userID, "cmd", cmd)
			c.JSON(429, gin.H{
				"success": false,
				"code":    429,
				"msg":     "command rate limit exceeded",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func GrabRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return CommandRateLimitMiddleware(limiter, "grab", 10, time.Second)
}

func SendPacketRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return CommandRateLimitMiddleware(limiter, "send_packet", 5, time.Second)
}

func JoinRoomRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return CommandRateLimitMiddleware(limiter, "join_room", 5, time.Minute)
}

func SelectSeatRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return CommandRateLimitMiddleware(limiter, "select_seat", 10, time.Second)
}
