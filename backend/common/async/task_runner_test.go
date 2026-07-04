package async

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newRunner 构造一个具备合理 defaultTTL 的 TaskRunner，用于测试。
func newRunner(t *testing.T, defaultTTL time.Duration) (*TaskRunner, context.CancelFunc) {
	t.Helper()
	rootCtx, cancel := context.WithCancel(context.Background())
	r := NewTaskRunner(rootCtx, defaultTTL)
	if err := r.Start(); err != nil {
		cancel()
		t.Fatalf("Start returned error: %v", err)
	}
	return r, cancel
}

// TestSubmit_RunsTaskAndProvidesValidCtx 验证正常 Submit：任务被执行且 ctx 有效。
func TestSubmit_RunsTaskAndProvidesValidCtx(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()
	defer r.Stop()
	defer r.Wait()

	type result struct {
		ctxErr error
	}
	done := make(chan result, 1)
	if err := r.Submit("normal_task", 0, func(ctx context.Context) {
		// 必须在 task 内部检查 ctx.Err()：task 返回后 deferred cancel 会取消 ctx。
		done <- result{ctxErr: ctx.Err()}
	}); err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	select {
	case res := <-done:
		if res.ctxErr != nil {
			t.Fatalf("ctx should be valid during task execution, got err: %v", res.ctxErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task did not execute within 2s")
	}
}

// TestSubmit_DefaultTTLUsedWhenTTLZero 验证 ttl=0 时使用 defaultTTL。
func TestSubmit_DefaultTTLUsedWhenTTLZero(t *testing.T) {
	r, rootCancel := newRunner(t, 50*time.Millisecond)
	defer rootCancel()

	done := make(chan struct{})
	if err := r.Submit("default_ttl_task", 0, func(ctx context.Context) {
		<-ctx.Done()
		close(done)
	}); err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not observe ctx.Done() within 2s; defaultTTL likely not applied")
	}

	r.Stop()
	r.Wait()
}

// TestSubmit_PanicRecovery 验证 panic 被恢复，runner 不崩溃，Wait 正常返回。
func TestSubmit_PanicRecovery(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()

	panicTaskDone := make(chan struct{})
	if err := r.Submit("panic_task", 0, func(ctx context.Context) {
		defer close(panicTaskDone)
		panic("boom")
	}); err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	// 等到 panic 任务执行完毕（panic 已被 recover）。
	select {
	case <-panicTaskDone:
	case <-time.After(2 * time.Second):
		t.Fatal("panic task did not complete within 2s")
	}

	// 后续 Submit 仍应可用——证明 runner 未崩溃。
	nextDone := make(chan struct{})
	if err := r.Submit("after_panic", 0, func(ctx context.Context) {
		close(nextDone)
	}); err != nil {
		t.Fatalf("Submit after panic returned error: %v", err)
	}
	select {
	case <-nextDone:
	case <-time.After(2 * time.Second):
		t.Fatal("task after panic did not execute within 2s")
	}

	r.Stop()
	// Wait 必须返回——证明 wg.Done 在 panic 路径也执行了。
	waitDone := make(chan struct{})
	go func() {
		r.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after panic task; wg.Done likely not called")
	}
}

// TestSubmit_PerTaskTimeout 验证 per-task 超时：短 ttl 阻塞任务会收到 ctx.Done()，
// 且触发时延接近 ttl。
func TestSubmit_PerTaskTimeout(t *testing.T) {
	r, rootCancel := newRunner(t, 10*time.Second) // defaultTTL 较大，确保用不上
	defer rootCancel()
	defer r.Stop()
	defer r.Wait()

	ttl := 50 * time.Millisecond
	measuredDone := make(chan time.Duration, 1)
	if err := r.Submit("timeout_task", ttl, func(ctx context.Context) {
		start := time.Now()
		<-ctx.Done()
		measuredDone <- time.Since(start)
	}); err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	select {
	case d := <-measuredDone:
		if d < ttl {
			t.Fatalf("ctx.Done fired before ttl: got %v, want >= %v", d, ttl)
		}
		if d > ttl+1*time.Second {
			t.Fatalf("ctx.Done fired too late: got %v, want <= %v", d, ttl+1*time.Second)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task did not observe ctx.Done() within 2s")
	}
}

// TestSubmit_AfterStopReturnsError 验证 Stop 后 Submit 返回 error。
func TestSubmit_AfterStopReturnsError(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()

	r.Stop()

	err := r.Submit("rejected_task", 0, func(ctx context.Context) {})
	if err == nil {
		t.Fatal("expected error when Submit after Stop, got nil")
	}

	// 即使 error 已返回，仍确保不会泄漏 goroutine（任务不应被执行）。
	r.Wait()
}

// TestStop_Idempotent 验证多次调用 Stop 不会 panic。
func TestStop_Idempotent(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()

	// 多次调用必须不 panic、不阻塞。
	for i := 0; i < 5; i++ {
		r.Stop()
	}
}

// TestWait_BlocksUntilAllTasksExit 验证 Wait 等待所有任务退出。
func TestWait_BlocksUntilAllTasksExit(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()

	const n = 5
	var completed int32
	started := make(chan struct{}, n)
	release := make(chan struct{})

	for i := 0; i < n; i++ {
		if err := r.Submit("blocking_task", 0, func(ctx context.Context) {
			started <- struct{}{}
			<-release
			atomic.AddInt32(&completed, 1)
		}); err != nil {
			t.Fatalf("Submit[%d] returned error: %v", i, err)
		}
	}

	// 等待所有任务进入运行态。
	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("task %d did not start within 2s", i)
		}
	}

	// 此时 Wait 应阻塞——因为任务都卡在 release 上。
	waitStarted := make(chan struct{})
	waitReturned := make(chan struct{})
	go func() {
		close(waitStarted)
		r.Wait()
		close(waitReturned)
	}()
	<-waitStarted

	select {
	case <-waitReturned:
		t.Fatal("Wait returned before tasks completed")
	case <-time.After(50 * time.Millisecond):
		// 预期 Wait 仍阻塞——good。
	}

	// 释放所有任务，再 Stop。
	close(release)
	r.Stop()

	select {
	case <-waitReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return within 2s after tasks released")
	}

	if got := atomic.LoadInt32(&completed); got != n {
		t.Fatalf("completed count = %d, want %d", got, n)
	}
}

// TestSubmit_RootCtxCancelPropagates 验证 Stop 取消 rootCtx 后，在飞任务收到信号。
func TestSubmit_RootCtxCancelPropagates(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()

	done := make(chan struct{})
	if err := r.Submit("long_task", 0, func(ctx context.Context) {
		<-ctx.Done()
		close(done)
	}); err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}

	// 短暂等待确保任务已进入阻塞。
	time.Sleep(20 * time.Millisecond)

	r.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not observe rootCtx cancellation within 2s")
	}
	r.Wait()
}

// TestSubmit_ConcurrentSafe 验证 Submit 并发调用安全（无数据竞争 / panic）。
func TestSubmit_ConcurrentSafe(t *testing.T) {
	r, rootCancel := newRunner(t, 5*time.Second)
	defer rootCancel()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = r.Submit("concurrent_task", 0, func(ctx context.Context) {})
		}()
	}
	wg.Wait()

	r.Stop()
	r.Wait()
}
