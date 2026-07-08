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

type SettlementCheckScheduler struct {
	base                       *csched.BaseScheduler
	schedulerApp               *settlementApplication.SchedulerAppService
	deductedNotSettledLookback time.Duration
	failedFirstRoundLookback   time.Duration
}

func NewSettlementCheckScheduler(schedulerApp *settlementApplication.SchedulerAppService, redis cRedis.RedisClient, cfg commonconfig.SettlementSchedulerSubConfig) *SettlementCheckScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "settlement_check",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerSettlementCheckLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &SettlementCheckScheduler{
		schedulerApp:               schedulerApp,
		deductedNotSettledLookback: cfg.DeductedNotSettledLookback,
		failedFirstRoundLookback:   cfg.FailedFirstRoundLookback,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *SettlementCheckScheduler) Name() string { return s.base.Name() }

func (s *SettlementCheckScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *SettlementCheckScheduler) execute(ctx context.Context) error {
	return s.schedulerApp.RunSettlementCheck(ctx, s.failedFirstRoundLookback, s.deductedNotSettledLookback)
}

func (s *SettlementCheckScheduler) Stop() {
	s.base.Stop()
}
