package scheduler

import (
	"context"
	"time"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/service"
)

type SettlementCheckScheduler struct {
	base            *BaseScheduler
	settlementCheck *service.SettlementCheckService
}

func NewSettlementCheckScheduler(ctx context.Context, settlementCheck *service.SettlementCheckService, redis *cRedis.Client) *SettlementCheckScheduler {
	config := SchedulerConfig{
		Name:         "settlement_check",
		Interval:     5 * time.Minute,
		InitialDelay: time.Minute,
		LockKey:      rediskeys.KeySchedulerSettlementCheckLock,
		LockTTL:      300,
	}

	return &SettlementCheckScheduler{
		base:            NewBaseScheduler(ctx, config, nil, redis),
		settlementCheck: settlementCheck,
	}
}

func (s *SettlementCheckScheduler) Start() {
	s.base.task = s.execute
	s.base.Start()
}

func (s *SettlementCheckScheduler) execute(ctx context.Context) error {
	s.settlementCheck.CheckFirstRoundDeductFailure(ctx)
	s.settlementCheck.CheckDeductedButNotSettled(ctx, time.Now().Add(-5*time.Minute))
	return nil
}

func (s *SettlementCheckScheduler) Stop() {
	s.base.Stop()
}
