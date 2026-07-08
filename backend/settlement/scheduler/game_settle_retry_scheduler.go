package scheduler

import (
	"context"

	commonconfig "github.com/cashparty/backend/common/config"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
)

type GameSettleRetryScheduler struct {
	base         *csched.BaseScheduler
	schedulerApp *settlementApplication.SchedulerAppService
	limit        int
}

func NewGameSettleRetryScheduler(schedulerApp *settlementApplication.SchedulerAppService, redis cRedis.RedisClient, cfg commonconfig.SettlementSchedulerSubConfig) *GameSettleRetryScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "game_settle_retry",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerGameSettleRetryLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &GameSettleRetryScheduler{
		schedulerApp: schedulerApp,
		limit:        cfg.Limit,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *GameSettleRetryScheduler) Name() string { return s.base.Name() }

func (s *GameSettleRetryScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *GameSettleRetryScheduler) execute(ctx context.Context) error {
	return s.schedulerApp.RetryGameSettle(ctx, s.limit)
}

func (s *GameSettleRetryScheduler) Stop() {
	s.base.Stop()
}
