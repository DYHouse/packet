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

type GameSettleTimeoutScheduler struct {
	base            *csched.BaseScheduler
	schedulerApp    *settlementApplication.SchedulerAppService
	timeoutDuration time.Duration
	limit           int
}

func NewGameSettleTimeoutScheduler(schedulerApp *settlementApplication.SchedulerAppService, redis cRedis.RedisClient, cfg commonconfig.SettlementSchedulerSubConfig) *GameSettleTimeoutScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "game_settle_timeout",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerGameSettleTimeoutLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &GameSettleTimeoutScheduler{
		schedulerApp:    schedulerApp,
		timeoutDuration: cfg.TimeoutDuration,
		limit:           cfg.Limit,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *GameSettleTimeoutScheduler) Name() string { return s.base.Name() }

func (s *GameSettleTimeoutScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *GameSettleTimeoutScheduler) execute(ctx context.Context) error {
	return s.schedulerApp.SettleGameByTimeout(ctx, s.timeoutDuration, s.limit)
}

func (s *GameSettleTimeoutScheduler) Stop() {
	s.base.Stop()
}
