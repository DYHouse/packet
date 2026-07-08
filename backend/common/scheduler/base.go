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

// TaskFunc 是调度器每次执行的任务函数。
type TaskFunc func(ctx context.Context) error

// BaseSchedulerConfig 是 BaseScheduler 的配置。
// 与 common/config/types.go 的 RobotSchedulerConfig 区分（P1-4）。
type BaseSchedulerConfig struct {
	Name         string
	Interval     time.Duration
	InitialDelay time.Duration
	LockKey      string
	LockTTL      int
}

type BaseScheduler struct {
	config  BaseSchedulerConfig
	task    TaskFunc
	redis   cRedis.RedisClient
	metrics *Metrics
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
}

// NewBaseScheduler 创建 BaseScheduler。
// task 在构造时传入，避免在 Start 中赋值导致 nil task 风险（P1-7）。
func NewBaseScheduler(config BaseSchedulerConfig, task TaskFunc, redis cRedis.RedisClient) *BaseScheduler {
	return &BaseScheduler{
		config:  config,
		task:    task,
		redis:   redis,
		metrics: NewMetrics(),
	}
}

// Name 返回调度器名称。
func (s *BaseScheduler) Name() string {
	return s.config.Name
}

// Start 启动调度器，ctx MUST 为 appCtx 派生的 context。
// 内部通过 context.WithCancel(ctx) 派生 ctx，便于 Stop 时取消。
func (s *BaseScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.run()
	return nil
}

func (s *BaseScheduler) run() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("scheduler run panic",
				"name", s.config.Name,
				"panic", r, "stack", string(debug.Stack()))
			s.metrics.RecordPanic(s.config.Name)
		}
	}()

	// P0-5: 使用 select 替代 time.Sleep，使 InitialDelay 期间可响应 Stop。
	if s.config.InitialDelay > 0 {
		select {
		case <-s.ctx.Done():
			logger.Info("scheduler stopped during initial delay", "name", s.config.Name)
			return
		case <-time.After(s.config.InitialDelay):
		}
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
	start := time.Now()

	ctx, cancel := context.WithTimeout(s.ctx, s.config.Interval)
	defer cancel()

	// P0-4: 检查并记录 WithRedisLock 返回值（锁获取失败与 task 错误均记录）。
	err := lock.WithRedisLock(ctx, s.config.LockKey, s.config.LockTTL, func() error {
		return s.task(ctx)
	})

	s.metrics.RecordExecution(s.config.Name, time.Since(start))

	if err != nil {
		logger.Warn("scheduler task failed",
			"name", s.config.Name,
			"lock_key", s.config.LockKey,
			"error", err)
		s.metrics.RecordError(s.config.Name)
	}
}

// Stop 停止调度器，阻塞等待 goroutine 退出，带 10s 超时兜底。
func (s *BaseScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("scheduler stop timeout",
			"name", s.config.Name,
			"lock_key", s.config.LockKey)
	}
}
