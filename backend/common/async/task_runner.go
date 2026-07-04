package async

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
)

// TaskRunner 管理应用层 fire-and-forget 异步任务。
// 所有任务从 rootCtx 派生 context，享有 per-task 超时与 panic recovery。
// 生命周期：NewTaskRunner → Start → Submit*N → Stop → Wait。
type TaskRunner struct {
	rootCtx    context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	closed     bool
	defaultTTL time.Duration
}

// NewTaskRunner 创建 runner。rootCtx 通常是 Application 的 app-level ctx。
// defaultTTL 是 Submit 未显式指定 timeout 时的兜底超时，建议 10s。
func NewTaskRunner(rootCtx context.Context, defaultTTL time.Duration) *TaskRunner {
	ctx, cancel := context.WithCancel(rootCtx)
	return &TaskRunner{
		rootCtx:    ctx,
		cancel:     cancel,
		defaultTTL: defaultTTL,
	}
}

// Start 标记 runner 可用。当前实现无副作用，保留以便未来扩展（如 metrics）。
func (r *TaskRunner) Start() error {
	return nil
}

// Submit 提交一个异步任务。
// taskID 用于日志标识（如 "publish_session_start"）。
// ttl=0 表示使用 runner.defaultTTL。
// 返回 error 仅当 runner 已 closed（Stop 后再 Submit）。
func (r *TaskRunner) Submit(taskID string, ttl time.Duration, task func(ctx context.Context)) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("task runner is closed, rejected task: %s", taskID)
	}
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("async task panic",
					"task", taskID,
					"panic", rec,
					"stack", string(debug.Stack()))
			}
		}()

		timeout := ttl
		if timeout == 0 {
			timeout = r.defaultTTL
		}
		ctx, cancel := context.WithTimeout(r.rootCtx, timeout)
		defer cancel()

		task(ctx)
	}()
	return nil
}

// Stop 取消 rootCtx 并标记 closed，拒绝新任务提交。
// 已提交的任务会收到 ctx.Done() 信号自行退出。
// 幂等：重复调用安全。
func (r *TaskRunner) Stop() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	r.cancel()
}

// Wait 阻塞等待所有已提交任务退出。
// 必须在 Stop 之后调用。建议外层包 select+超时兜底。
func (r *TaskRunner) Wait() {
	r.wg.Wait()
}
