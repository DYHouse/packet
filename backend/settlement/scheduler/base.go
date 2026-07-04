package scheduler

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

type TaskFunc func(ctx context.Context) error

type SchedulerConfig struct {
	Name         string
	Interval     time.Duration
	InitialDelay time.Duration
	LockKey      string
	LockTTL      int
}

type BaseScheduler struct {
	config SchedulerConfig
	task   TaskFunc
	redis  *cRedis.Client
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

func NewBaseScheduler(ctx context.Context, config SchedulerConfig, task TaskFunc, redis *cRedis.Client) *BaseScheduler {
	ctx, cancel := context.WithCancel(ctx)
	return &BaseScheduler{
		config: config,
		task:   task,
		redis:  redis,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *BaseScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wg.Add(1)
	go s.run()
}

func (s *BaseScheduler) run() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("scheduler run panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	if s.config.InitialDelay > 0 {
		time.Sleep(s.config.InitialDelay)
	}

	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()

	logger.Info("scheduler started", "name", s.config.Name, "interval", s.config.Interval)

	for {
		select {
		case <-s.ctx.Done():
			logger.Info("scheduler stopped", "name", s.config.Name)
			return
		case <-ticker.C:
			s.executeTask()
		}
	}
}

func (s *BaseScheduler) executeTask() {
	ctx, cancel := context.WithTimeout(s.ctx, s.config.Interval)
	defer cancel()

	lock.WithRedisLock(ctx, s.redis, s.config.LockKey, s.config.LockTTL, func() error {
		return s.task(ctx)
	})
}

func (s *BaseScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel()

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("scheduler stop timeout", "lock_key", s.config.LockKey)
	}
}
