package scheduler

import (
	"context"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
)

// VirtualBalanceSyncScheduler 定时将 Redis 中脏的机器人虚拟余额同步到 DB。
// 从 game/scheduler 迁移至 settlement/scheduler，职责归属 settlement 域。
type VirtualBalanceSyncScheduler struct {
	base         *csched.BaseScheduler
	schedulerApp *settlementApplication.SchedulerAppService
}

// NewVirtualBalanceSyncScheduler 创建虚拟余额同步调度器。
// interval <= 0 时设默认 30s，防止 time.NewTicker(0) panic。
// LockTTL <= 0 时兜底为 interval.Seconds()+5，与原 game/scheduler 实现一致。
func NewVirtualBalanceSyncScheduler(schedulerApp *settlementApplication.SchedulerAppService, redis cRedis.RedisClient, cfg commonconfig.SettlementSchedulerSubConfig) *VirtualBalanceSyncScheduler {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	lockTTL := cfg.LockTTL
	if lockTTL <= 0 {
		lockTTL = int(interval.Seconds()) + 5 // 略大于 interval
	}
	config := csched.BaseSchedulerConfig{
		Name:     "virtual_balance_sync",
		Interval: interval,
		// InitialDelay: 0，无初始延迟
		// LockKey/LockTTL: VirtualBalanceSync 依赖 SPOP 原子性，加锁不影响正确性
		LockKey: rediskeys.KeySchedulerVirtualBalanceSyncLock,
		LockTTL: lockTTL,
	}
	s := &VirtualBalanceSyncScheduler{
		schedulerApp: schedulerApp,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *VirtualBalanceSyncScheduler) Name() string { return s.base.Name() }

func (s *VirtualBalanceSyncScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *VirtualBalanceSyncScheduler) execute(ctx context.Context) error {
	return s.schedulerApp.RunVirtualBalanceSync(ctx)
}

func (s *VirtualBalanceSyncScheduler) Stop() {
	s.base.Stop()
}
