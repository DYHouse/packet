package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
)

// Registry 是调度器注册中心，统一管理调度器的启动与停止。
type Registry struct {
	schedulers []Scheduler
	mu         sync.Mutex
}

// NewRegistry 创建注册中心。
func NewRegistry() *Registry {
	return &Registry{}
}

// Register 注册一个调度器。
func (r *Registry) Register(s Scheduler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schedulers = append(r.schedulers, s)
}

// StartAll 启动所有已注册的调度器。
// 单个调度器启动失败不会中断其他调度器的启动，返回聚合错误（errors.Join）。
func (r *Registry) StartAll(ctx context.Context) error {
	r.mu.Lock()
	schedulers := make([]Scheduler, len(r.schedulers))
	copy(schedulers, r.schedulers)
	r.mu.Unlock()

	var errs []error
	for _, s := range schedulers {
		if err := s.Start(ctx); err != nil {
			logger.Error("scheduler start failed", "name", s.Name(), "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// StopAll 并行停止所有已注册的调度器，带全局预算超时。
func (r *Registry) StopAll(timeout time.Duration) {
	r.mu.Lock()
	schedulers := make([]Scheduler, len(r.schedulers))
	copy(schedulers, r.schedulers)
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range schedulers {
		wg.Add(1)
		go func(s Scheduler) {
			defer wg.Done()
			s.Stop()
		}(s)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.Warn("registry stop all timeout", "timeout", timeout)
	}
}
