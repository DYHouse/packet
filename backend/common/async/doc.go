// Package async 提供应用层 fire-and-forget 异步任务的统一调度器。
//
// # 使用模式
//
// 1. 在 Application 启动时创建 TaskRunner（rootCtx 为 app-level ctx）：
//
//	appCtx, cancel := context.WithCancel(context.Background())
//	taskRunner := async.NewTaskRunner(appCtx, 10*time.Second)
//
// 2. 注入到需要 fire-and-forget 语义的 service：
//
//	type Service struct {
//		taskRunner *async.TaskRunner
//	}
//
// 3. 在 service 方法中提交任务：
//
//	s.taskRunner.Submit("publish_event", 5*time.Second, func(ctx context.Context) {
//		s.publisher.Publish(ctx, event)
//	})
//
// 4. 在 Application 停止时：
//
//	taskRunner.Stop()  // 拒绝新任务 + cancel ctx
//	// ... 停止其他组件 ...
//	taskRunner.Wait()  // 等待在飞任务退出（建议外层包 select+超时）
//
// # 反模式（MUST NOT）
//
// - 在 task 内部再 go func()——必须用 Submit 嵌套
// - 在 task 内部用 context.Background()——必须用传入的 ctx
// - Submit 后立即 Stop+Wait——Submit 是异步语义
// - 不设置 ttl 且 defaultTTL=0——任务可能永久阻塞
package async
