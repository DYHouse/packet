package scheduler

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/service"
)

type CreditRetryScheduler struct {
	base        *BaseScheduler
	creditRetry *service.CreditRetryService
}

func NewCreditRetryScheduler(ctx context.Context, creditRetry *service.CreditRetryService, redis *cRedis.Client) *CreditRetryScheduler {
	config := SchedulerConfig{
		Name:         "credit_retry",
		Interval:     30 * time.Second,
		InitialDelay: 10 * time.Second,
		LockKey:      rediskeys.KeySchedulerCreditRetryLock,
		LockTTL:      60,
	}

	return &CreditRetryScheduler{
		base:        NewBaseScheduler(ctx, config, nil, redis),
		creditRetry: creditRetry,
	}
}

func (s *CreditRetryScheduler) Start() {
	s.base.task = s.execute
	s.base.Start()
}

func (s *CreditRetryScheduler) execute(ctx context.Context) error {
	bills, err := s.creditRetry.GetRetryableCredits(ctx, 100)
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
