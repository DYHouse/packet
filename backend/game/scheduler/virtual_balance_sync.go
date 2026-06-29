package scheduler

import (
	"context"
	"runtime/debug"
	"sync"
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
	wg             sync.WaitGroup
}

// NewVirtualBalanceSyncScheduler creates a new VirtualBalanceSyncScheduler.
// interval <= 0 时设默认 30s，防止 time.NewTicker(0) panic。
func NewVirtualBalanceSyncScheduler(virtualBalance *redis.VirtualBalanceService, interval time.Duration) *VirtualBalanceSyncScheduler {
	if interval <= 0 {
		interval = 30 * time.Second
	}
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
	s.wg.Add(1)
	go s.run()
	logger.Info("virtual balance sync scheduler started", "interval", s.interval)
}

// Stop cancels the background goroutine and waits for it to exit (with timeout).
func (s *VirtualBalanceSyncScheduler) Stop() {
	s.cancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("virtual balance sync scheduler stopped")
	case <-time.After(10 * time.Second):
		logger.Warn("virtual balance sync scheduler stop timeout")
	}
}

func (s *VirtualBalanceSyncScheduler) run() {
	defer s.wg.Done()

	// defer recover 防止 panic 导致进程崩溃
	defer func() {
		if r := recover(); r != nil {
			logger.Error("virtual balance sync panic",
				"error", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// per-task 超时，防止 SyncToDB 内部 SPOP 循环永久阻塞
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			err := s.virtualBalance.SyncToDB(ctx)
			cancel()
			if err != nil {
				logger.Error("virtual balance sync to db failed", "error", err)
			}
		}
	}
}
