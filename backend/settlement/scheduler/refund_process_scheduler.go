package scheduler

import (
	"context"

	commonconfig "github.com/cashparty/backend/common/config"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
)

type RefundProcessScheduler struct {
	base         *csched.BaseScheduler
	schedulerApp *settlementApplication.SchedulerAppService
	limit        int
	offset       int
}

func NewRefundProcessScheduler(schedulerApp *settlementApplication.SchedulerAppService, redis cRedis.RedisClient, cfg commonconfig.SettlementSchedulerSubConfig) *RefundProcessScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "refund_process",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerRefundProcessLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &RefundProcessScheduler{
		schedulerApp: schedulerApp,
		limit:        cfg.Limit,
		offset:       cfg.Offset,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *RefundProcessScheduler) Name() string { return s.base.Name() }

func (s *RefundProcessScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *RefundProcessScheduler) execute(ctx context.Context) error {
	return s.schedulerApp.ProcessPendingRefunds(ctx, s.limit, s.offset)
}

func (s *RefundProcessScheduler) Stop() {
	s.base.Stop()
}
