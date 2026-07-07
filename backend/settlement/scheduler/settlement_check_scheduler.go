package scheduler

import (
	"context"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	"github.com/cashparty/backend/settlement/service"
)

type SettlementCheckScheduler struct {
	base                       *csched.BaseScheduler
	settlementCheck            *service.SettlementCheckService
	deductedNotSettledLookback time.Duration
	failedFirstRoundLookback   time.Duration
}

func NewSettlementCheckScheduler(settlementCheck *service.SettlementCheckService, redis *cRedis.Client, cfg commonconfig.SettlementSchedulerSubConfig) *SettlementCheckScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "settlement_check",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerSettlementCheckLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &SettlementCheckScheduler{
		settlementCheck:            settlementCheck,
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
	if err := s.settlementCheck.CheckFirstRoundDeductFailure(ctx, time.Now().Add(-s.failedFirstRoundLookback)); err != nil {
		logger.Error("check first round deduct failure failed", "error", err)
		return err
	}
	if err := s.settlementCheck.CheckDeductedButNotSettled(ctx, time.Now().Add(-s.deductedNotSettledLookback)); err != nil {
		logger.Error("check deducted but not settled failed", "error", err)
		return err
	}
	return nil
}

func (s *SettlementCheckScheduler) Stop() {
	s.base.Stop()
}
