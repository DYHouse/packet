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

type CreditRetryScheduler struct {
	base        *csched.BaseScheduler
	creditRetry *service.CreditRetryService
	limit       int
}

func NewCreditRetryScheduler(creditRetry *service.CreditRetryService, redis *cRedis.Client, cfg commonconfig.SettlementSchedulerSubConfig) *CreditRetryScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "credit_retry",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerCreditRetryLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &CreditRetryScheduler{
		creditRetry: creditRetry,
		limit:       cfg.Limit,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *CreditRetryScheduler) Name() string { return s.base.Name() }

func (s *CreditRetryScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *CreditRetryScheduler) execute(ctx context.Context) error {
	bills, err := s.creditRetry.GetRetryableCredits(ctx, s.limit)
	if err != nil {
		logger.Error("get retryable credits failed", "error", err)
		return err
	}

	for _, bill := range bills {
		if bill.NextRetryAt != nil && bill.NextRetryAt.After(time.Now()) {
			continue
		}

		if err := s.creditRetry.RetryCredit(ctx, bill.ID); err != nil {
			logger.Error("retry credit failed", "bill_id", bill.ID, "error", err)
		}
	}

	return nil
}

func (s *CreditRetryScheduler) Stop() {
	s.base.Stop()
}
