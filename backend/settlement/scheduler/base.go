package scheduler

import (
	"context"
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
	stopCh chan struct{}
	mu     sync.Mutex
}

func NewBaseScheduler(config SchedulerConfig, task TaskFunc, redis *cRedis.Client) *BaseScheduler {
	return &BaseScheduler{
		config: config,
		task:   task,
		redis:  redis,
		stopCh: make(chan struct{}),
	}
}

func (s *BaseScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	go s.run()
}

func (s *BaseScheduler) run() {
	if s.config.InitialDelay > 0 {
		time.Sleep(s.config.InitialDelay)
	}

	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()

	logger.Info("scheduler started", "name", s.config.Name, "interval", s.config.Interval)

	for {
		select {
		case <-ticker.C:
			s.executeTask()
		case <-s.stopCh:
			logger.Info("scheduler stopped", "name", s.config.Name)
			return
		}
	}
}

func (s *BaseScheduler) executeTask() {
	ctx := context.Background()

	lock.WithRedisLock(ctx, s.redis, s.config.LockKey, s.config.LockTTL, func() error {
		return s.task(ctx)
	})
}

func (s *BaseScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.stopCh)
}
