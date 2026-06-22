package scheduler

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

// VirtualBalanceSyncScheduler periodically flushes dirty robot virtual balance
// values from Redis to the database by calling VirtualBalanceService.SyncToDB.
type VirtualBalanceSyncScheduler struct {
	virtualBalance *redis.VirtualBalanceService
	interval       time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewVirtualBalanceSyncScheduler creates a new VirtualBalanceSyncScheduler.
func NewVirtualBalanceSyncScheduler(virtualBalance *redis.VirtualBalanceService, interval time.Duration) *VirtualBalanceSyncScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &VirtualBalanceSyncScheduler{
		virtualBalance: virtualBalance,
		interval:       interval,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start launches the background goroutine that periodically syncs dirty virtual
// balances to the database.
func (s *VirtualBalanceSyncScheduler) Start() {
	go s.run()
	logger.Info("virtual balance sync scheduler started", "interval", s.interval)
}

// Stop cancels the background goroutine.
func (s *VirtualBalanceSyncScheduler) Stop() {
	s.cancel()
	logger.Info("virtual balance sync scheduler stopped")
}

func (s *VirtualBalanceSyncScheduler) run() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.virtualBalance.SyncToDB(s.ctx); err != nil {
				logger.Error("virtual balance sync to db failed", "error", err)
			}
		}
	}
}
