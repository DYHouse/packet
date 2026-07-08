package scheduler

import (
	"context"

	commonconfig "github.com/cashparty/backend/common/config"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
)

type CreditRetryScheduler struct {
	base         *csched.BaseScheduler
	schedulerApp *settlementApplication.SchedulerAppService
	limit        int
}

func NewCreditRetryScheduler(schedulerApp *settlementApplication.SchedulerAppService, redis cRedis.RedisClient, cfg commonconfig.SettlementSchedulerSubConfig) *CreditRetryScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "credit_retry",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerCreditRetryLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &CreditRetryScheduler{
		schedulerApp: schedulerApp,
		limit:        cfg.Limit,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *CreditRetryScheduler) Name() string { return s.base.Name() }

func (s *CreditRetryScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *CreditRetryScheduler) execute(ctx context.Context) error {
	return s.schedulerApp.RetryCreditBills(ctx, s.limit)
}

func (s *CreditRetryScheduler) Stop() {
	s.base.Stop()
}
