package lock

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
)

var (
	redsyncClient *redsync.Redsync
	redisClient   cRedis.RedisClient
	initOnce      sync.Once
)

func InitLocker(redis cRedis.RedisClient) {
	initOnce.Do(func() {
		redisClient = redis
		pool := goredis.NewPool(redis.Raw())
		redsyncClient = redsync.New(pool)
		logger.Info("redis locker initialized with redsync")
	})
}

type LockOptions struct {
	Expiry           time.Duration
	RetryCount       int
	RetryDelay       time.Duration
	EnableWatchdog   bool
	WatchdogInterval time.Duration
}

func DefaultLockOptions() *LockOptions {
	return &LockOptions{
		Expiry:           30 * time.Second,
		RetryCount:       3,
		RetryDelay:       100 * time.Millisecond,
		EnableWatchdog:   true,
		WatchdogInterval: 10 * time.Second,
	}
}

type Lock struct {
	mutex        *redsync.Mutex
	key          string
	watchdogStop chan struct{}
	wg           sync.WaitGroup
}

func Obtain(ctx context.Context, key string, opts *LockOptions) (*Lock, error) {
	if redsyncClient == nil {
		return nil, fmt.Errorf("locker not initialized, call InitLocker first")
	}

	if opts == nil {
		opts = DefaultLockOptions()
	}

	mutex := redsyncClient.NewMutex(
		key,
		redsync.WithExpiry(opts.Expiry),
		redsync.WithTries(opts.RetryCount),
		redsync.WithRetryDelay(opts.RetryDelay),
	)

	if err := mutex.LockContext(ctx); err != nil {
		return nil, fmt.Errorf("lock not obtained: %s, error: %w", key, err)
	}

	logger.Debug("lock obtained", "key", key, "expiry", opts.Expiry)

	lock := &Lock{
		mutex: mutex,
		key:   key,
	}

	if opts.EnableWatchdog {
		lock.watchdogStop = make(chan struct{})
		lock.startWatchdog(ctx, opts.WatchdogInterval)
	}

	return lock, nil
}

func (l *Lock) Release(ctx context.Context) error {
	if l.watchdogStop != nil {
		close(l.watchdogStop)
		l.wg.Wait()
	}

	if _, err := l.mutex.UnlockContext(ctx); err != nil {
		logger.Error("failed to release lock", "key", l.key, "error", err)
		return fmt.Errorf("failed to release lock %s: %w", l.key, err)
	}

	logger.Debug("lock released", "key", l.key)
	return nil
}

func (l *Lock) startWatchdog(ctx context.Context, interval time.Duration) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("watchdog panic",
					"key", l.key, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if ok, err := l.mutex.Extend(); !ok || err != nil {
					logger.Warn("failed to extend lock", "key", l.key, "error", err)
					return
				}
				logger.Debug("lock extended by watchdog", "key", l.key)
			case <-l.watchdogStop:
				return
			case <-ctx.Done():
				logger.Info("watchdog stopped by ctx done", "key", l.key)
				return
			}
		}
	}()
}

func WithLock(ctx context.Context, key string, opts *LockOptions, fn func() error) (err error) {
	lock, err := Obtain(ctx, key, opts)
	if err != nil {
		return err
	}
	defer lock.Release(ctx)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in lock fn: %v", r)
			logger.Error("panic recovered in WithLock",
				"key", key, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	return fn()
}

// WithRedisLock 使用包级 redsyncClient 获取分布式锁后执行 fn，无需调用方传入 Redis 客户端。
func WithRedisLock(ctx context.Context, key string, expirySeconds int, fn func() error) error {
	opts := &LockOptions{
		Expiry:           time.Duration(expirySeconds) * time.Second,
		RetryCount:       3,
		RetryDelay:       100 * time.Millisecond,
		EnableWatchdog:   true,
		WatchdogInterval: time.Duration(expirySeconds/3) * time.Second,
	}

	return WithLock(ctx, key, opts, fn)
}
