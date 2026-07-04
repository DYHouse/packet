package scheduler

import (
	"context"
	"time"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	redisRepo "github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

// VirtualBalanceSyncScheduler periodically flushes dirty robot virtual balance
// values from Redis to the database by calling VirtualBalanceService.SyncToDB.
type VirtualBalanceSyncScheduler struct {
	base *csched.BaseScheduler
}

// NewVirtualBalanceSyncScheduler creates a new VirtualBalanceSyncScheduler.
// interval <= 0 时设默认 30s，防止 time.NewTicker(0) panic。
func NewVirtualBalanceSyncScheduler(virtualBalance *redisRepo.VirtualBalanceService, interval time.Duration, redis *cRedis.Client) *VirtualBalanceSyncScheduler {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	config := csched.BaseSchedulerConfig{
		Name:     "virtual_balance_sync",
		Interval: interval,
		// InitialDelay: 0, // 无初始延迟
		// LockKey/LockTTL: VirtualBalanceSync 依赖 SPOP 原子性，加锁不影响正确性
		LockKey: rediskeys.KeySchedulerVirtualBalanceSyncLock,
		LockTTL: int(interval.Seconds()) + 5, // 略大于 interval
	}
	task := func(ctx context.Context) error {
		return virtualBalance.SyncToDB(ctx)
	}
	return &VirtualBalanceSyncScheduler{
		base: csched.NewBaseScheduler(config, task, redis),
	}
}

func (s *VirtualBalanceSyncScheduler) Name() string                    { return s.base.Name() }
func (s *VirtualBalanceSyncScheduler) Start(ctx context.Context) error { return s.base.Start(ctx) }
func (s *VirtualBalanceSyncScheduler) Stop()                           { s.base.Stop() }
