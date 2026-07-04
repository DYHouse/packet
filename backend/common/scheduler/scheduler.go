package scheduler

import "context"

// Scheduler 是所有调度器的统一接口。
type Scheduler interface {
	// Name 返回调度器名称，用于日志、metrics、健康检查。
	Name() string
	// Start 启动调度器，ctx MUST 为 appCtx 派生的 context。
	Start(ctx context.Context) error
	// Stop 停止调度器，阻塞等待 goroutine 退出，带超时兜底。
	Stop()
}
