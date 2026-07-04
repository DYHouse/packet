# Goroutine 统一规范与 Context 治理重构方案 v3

> 编写日期：2026-07-04（v3 完整重写，覆盖 v2 遗漏）
> 依据：[CODING_STANDARD.md](./CODING_STANDARD.md) §6.1（AsyncTaskRunner）、§6.2（context）、§12.3（Application 生命周期）、§15.3（goroutine 反模式）
> 关联文档：[NACOS_REFACTOR_PLAN.md](./NACOS_REFACTOR_PLAN.md)（config_listener.go 的 goroutine 已在 Phase 4.5 收敛，本文档不再涉及）
>
> v3 在 v2 基础上修正以下遗漏（共 9 项）：
> 1. **新增** `game/application/robot_scheduler_service.go` 完整改造方案（v2 完全遗漏，project memory 明确要求 "scanRooms must use per-scan timeouts derived from scheduler context"）
> 2. **修正** Phase 2.1：consumer goroutine 补 wg+recover；`gameEventKafkaConsumer` 改为 Container 字段；删除对不存在 `Close()` 方法的引用
> 3. **补充** Phase 2.3：gateway Application struct 完整字段；BroadcastSvc/Server goroutine 补 wg+recover
> 4. **新增** `game/server/generic_service.go` GRPCServer.Serve goroutine 补 wg+recover
> 5. **新增** `common/discovery/discovery.go` nacosResolver.watch 补 wg+recover+Wait
> 6. **新增** `common/lock/distributed_lock.go` watchdog goroutine 补 recover
> 7. **修正** Phase 6.7：BaseScheduler 补 wg 字段 + Start 加 wg.Add + run 加 defer wg.Done+recover + Stop 加 wg.Wait（v2 仅换 stopCh 为 ctx，违反自身 §6.4 rule 4）
> 8. **补充** Phase 6 各子阶段：所有已有 wg 的长驻 goroutine 补 defer recover()
> 9. **修正** 计数差异：goroutine 13→17（含 RobotScheduler scanLoop + 2 consumer + stats HTTP + grpc Serve）；context.Background 48→51（含 robot_scheduler_service 2 处 + discovery 1 处）；新增 Phase 6.10-6.13
>
> v2 已修正的 12 项 v1 遗漏不再赘述，见 v2 文档头部。

---

## 0. 背景与现状

### 0.1 核心问题（全量盘点结果，v3 修正计数）

经对 backend 全量代码深度 review（覆盖 game/application 14 文件、game/bootstrap 4 文件、game/server 1 文件、gateway 全部基础设施层、settlement/scheduler 5 个子类、common 全部包、所有 consumer、stats 服务），存在以下系统性违规：

| 问题域 | 数量 | 严重性 |
|---|---|---|
| **AsyncTaskRunner 仅存在于规约，0 行实现** | — | P0 |
| 应用层 fire-and-forget goroutine（裸 `go func()` / `go s.xxx(...)`） | 17 处 | P0 |
| 应用层异步任务中 `context.Background()` | 17 处 | P0 |
| 应用层同步方法中 `context.Background()`（GameBroadcaster 包装层 + RobotBehaviorEngine） | 6 处 | P0 |
| **应用层长驻组件 `context.Background()`（RobotSchedulerService.scanRooms + Start）** | 2 处 | P0 |
| HTTP/WS handler 内 `context.Background()` | 4 处 | P1 |
| 基础设施层运行时 `context.Background()`（manager/broadcast/auth/scheduler/discovery） | 8 处 | P1 |
| **bootstrap 层 consumer/server goroutine 无 wg 跟踪 + 无 recover** | 6 处 | P0 |
| **common 层长驻 goroutine 无 wg 跟踪（discovery/lock）** | 2 处 | P1 |
| **已有 wg 的长驻 goroutine 缺 defer recover()（5 处）** | 5 处 | P1 |
| 两套 Broadcaster 接口签名不一致 | 2 套 | P1 |
| `RobotActionScheduler` 接口缺 ctx 参数 | 1 处 | P1 |
| `AuthMiddleware.OnConnect` / `Router.Route` 缺 ctx 参数 | 2 处 | P1 |
| `RedisPubSubConsumer.Start` 忽略参数 ctx | 1 处 | P2 |
| `AuthMiddleware.cleanupRoutine` 无 wg 跟踪 | 1 处 | P2 |
| `BaseScheduler` 用 `stopCh` 不用 ctx（与其他 scheduler 风格不一致）+ 无 wg + 无 recover + Stop 不 Wait | 4 处 | P2 |
| `Server.handleConnection`/`writeAndHeartbeatPump` 无 wg 跟踪 | 2 处 | P2 |
| `game/bootstrap/app.go` 缺 `Wait()` 方法、`Stop()` 无超时、`panic(err)` 启动失败 | 3 处 | P2 |
| **`gameEventKafkaConsumer` 为局部变量，Stop 无法触达** | 1 处 | P2 |
| **`RoomEventConsumer.tryAcquire` fail-open（与 GameEventConsumer fail-closed 不一致）** | 1 处 | P2 |
| stats 服务无 bootstrap 抽象 | 1 处 | P3 |

### 0.2 风险矩阵

| 风险 | 当前概率 | 当前影响 | 重构后 |
|---|---|---|---|
| 应用层 goroutine panic 导致进程崩溃 | 高（13/17 无 recover） | 进程退出 + 房间状态不一致 | 概率 0（runner + 各组件统一 recover） |
| 停服时 goroutine 泄漏 / 踩已关闭资源 | 高 | 数据错乱、panic on closed channel | 概率 0（统一 wg.Wait） |
| 异步任务永久阻塞 | 中 | goroutine 泄漏 + 内存增长 | 概率 0（per-task timeout） |
| `Wait` 后 `Add` panic | 中 | 重启时 panic | 概率 0（closed 状态保护） |
| `AuthMiddleware.Stop` 不等待 cleanupRoutine | 中 | 日志延迟、资源未释放 | 概率 0（补 wg） |
| `RedisPubSubConsumer` 无法被外部 ctx 取消 | 高 | 停服时 consumer 不退出 | 概率 0（修正 ctx 使用） |
| **`RobotSchedulerService.scanRooms` 无法被 Stop 中断** | 高 | 停服时进行中的扫描踩已关闭资源 | 概率 0（用 s.ctx 派生 per-scan ctx） |
| **`BaseScheduler.Stop` 不等待 run 退出** | 高 | 5 个 scheduler 全部泄漏 | 概率 0（补 wg.Wait） |
| **`nacosResolver.Close` 不等待 watch 退出** | 中 | gRPC 连接清理竞争 | 概率 0（补 wg.Wait） |
| **`Lock.startWatchdog` panic 导致进程崩溃 + Release 死锁** | 低 | 进程退出 + 调用方永久阻塞 | 概率 0（补 recover） |
| **bootstrap consumer goroutine panic 导致进程崩溃** | 中 | 进程退出 | 概率 0（补 recover） |

---

## 1. 整体设计思路

### 1.1 核心原则

1. **统一调度入口**：应用层所有 fire-and-forget goroutine 必须经 `TaskRunner.Submit` 提交，禁止裸 `go func()`。
2. **统一 context 派生**：runner 持有 app-level ctx，每次 `Submit` 派生带 per-task timeout 的子 ctx；ctx 必须沿调用链透传到底层。
3. **统一生命周期**：`Application.Stop()` 顺序：`taskRunner.Stop()`（cancel ctx + 拒绝新任务）→ 停止 consumer/scheduler → `taskRunner.Wait()`（带 30s 兜底超时）→ 关闭底层资源。
4. **统一错误观测**：runner 内置 panic recovery + structured logging（含 task name、stack）。
5. **统一接口签名**：所有跨层接口（Broadcaster、RobotActionScheduler、OnConnect、Route）必须包含 `ctx context.Context` 首参。
6. **统一长驻组件 ctx+wg+recover 模式**：所有长驻组件（Manager/BroadcastService/AuthMiddleware/Scheduler/Consumer/Resolver/RobotScheduler/GRPCServer）必须持有 `ctx context.Context` + `cancel context.CancelFunc` + `wg sync.WaitGroup` 三件套；所有后台 goroutine 必须 `defer wg.Done()` + `defer recover()`；运行时方法使用 `m.ctx`/`s.ctx` 而非 `context.Background()`；`Stop()` 必须 `cancel()` + `wg.Wait()`（可带超时）。

### 1.2 非目标

- **不改造基础设施层长驻 goroutine 的循环主体**（scheduler/consumer/server 的循环结构）—— 它们已有自己的 ctx+cancel+WaitGroup 模式，迁移到 runner（fire-and-forget 语义）不匹配。仅修正它们的 ctx 使用方式（用 `s.ctx` 替代 `context.Background()`）+ 补 recover + 补 Wait。
- **不改造 `deduct_service.go:156` 的并行批处理**—— 它是 `sync.WaitGroup` 同步等待的并行计算，不是 fire-and-forget；仅补 per-goroutine recover。
- **不改造构造函数内的 `context.Background()`**—— 构造期无请求 ctx，使用 background 可接受。
- **不改造 nacos `ListenConfig` goroutine**—— 已在 Nacos 重构 Phase 4.5 收敛（含 panic recovery）。
- **不改造无 goroutine 的组件**（如 `RateLimiter.Stop()` / `SignatureMiddleware.Stop()` 为空方法）—— §6.4 rule 5 豁免。

### 1.3 已合规组件清单（v3 新增，避免 reviewer 误判遗漏）

以下文件已实现完整 §6.4 模式，**不在本次改造范围**：

| 文件 | 状态 |
|---|---|
| `game/scheduler/virtual_balance_sync.go` | ✅ 完整合规：ctx+cancel+wg+recover+per-task timeout+Stop with Wait |
| `game/scheduler/timeout_scheduler.go` | ⚠️ 基本合规，仅 `runChecker` 缺 recover（Phase 6.13 修复）；per-handler goroutine 已有 recover ✓ |

`VirtualBalanceSyncScheduler.run()` 是项目内最佳实践模板，其他长驻组件改造时应参考其模式：
```go
func (s *VirtualBalanceSyncScheduler) run() {
    defer s.wg.Done()
    defer func() {
        if r := recover(); r != nil {
            logger.Error("virtual balance sync panic", "error", r, "stack", string(debug.Stack()))
        }
    }()
    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()
    for {
        select {
        case <-s.ctx.Done():
            return
        case <-ticker.C:
            ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)  // per-task timeout
            err := s.virtualBalance.SyncToDB(ctx)
            cancel()
            if err != nil { logger.Warn(...) }
        }
    }
}
```

### 1.4 设计哲学

```
┌─────────────────────────────────────────────────────────────┐
│                   Application（应用进程）                     │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  appCtx (context.WithCancel)  ←  所有 ctx 的根        │  │
│  └────────────────────┬─────────────────────────────────┘  │
│                       │                                      │
│       ┌───────────────┼───────────────┐                     │
│       ↓               ↓               ↓                     │
│  ┌─────────┐    ┌──────────┐    ┌─────────────┐            │
│  │ TaskRun │    │Consumer×2│    │  Schedulers │            │
│  │  ner    │    │ (长驻)   │    │   (长驻)    │            │
│  │ (fire-  │    │          │    │             │            │
│  │ and-    │    │ 用 appCtx │    │  用 appCtx  │            │
│  │ forget) │    │  派生 ctx │    │   派生 ctx  │            │
│  └────┬────┘    └─────┬────┘    └──────┬──────┘            │
│       │               │                │                    │
│       ↓               ↓                ↓                    │
│  ┌─────────────────────────────────────────┐               │
│  │       Service 层（业务逻辑）              │               │
│  │  接收 ctx 参数，透传给 Broadcaster/Repo  │               │
│  └─────────────────────────────────────────┘               │
│                                                             │
│  ┌──────────────────────────────────────────┐              │
│  │  其他长驻组件（ctx+cancel+wg+recover）    │              │
│  │  RobotScheduler / GRPCServer /           │              │
│  │  nacosResolver / Lock watchdog /         │              │
│  │  AuthMiddleware / Manager /              │              │
│  │  BroadcastService / RedisPubSubConsumer  │              │
│  └──────────────────────────────────────────┘              │
└─────────────────────────────────────────────────────────────┘
```

**关键**：appCtx 是整个应用的生命周期 ctx，`TaskRunner` 派生 per-task ctx，consumer/scheduler/各长驻组件用 appCtx 派生自己的运行 ctx。Stop 时先 cancel appCtx（所有派生 ctx 立即 Done），再 Wait 等待退出。

---

## 2. 文件结构

### 2.1 新增文件

```
backend/
├── common/
│   └── async/                          # 新建包
│       ├── task_runner.go              # TaskRunner 实现
│       ├── task_runner_test.go         # 单测
│       └── doc.go                      # 包文档
├── game/
│   └── bootstrap/
│       └── (无新增，仅修改)
├── gateway/
│   └── bootstrap/
│       └── (无新增，仅修改)
└── stats/
    └── bootstrap/                      # 新建包（从 main.go 抽出）
        ├── app.go                      # Application + Start/Stop/Wait
        └── container.go                # Container 装配
```

### 2.2 修改文件清单（按改造范围分组，v3 扩展）

#### 组 1：AsyncTaskRunner 基础设施（3 新建）

| 文件 | 动作 |
|---|---|
| `common/async/task_runner.go` | 新建 |
| `common/async/task_runner_test.go` | 新建 |
| `common/async/doc.go` | 新建 |

#### 组 2：Application/Container 注入 + 生命周期改造（6 修改）

| 文件 | 动作 |
|---|---|
| `game/bootstrap/app.go` | 修改：加 appCtx/taskRunner/wg 字段；consumer goroutine 补 wg+recover；gameEventKafkaConsumer 改 Container 字段；Stop() 返回 error + 顺序改造 + 删除对不存在 Close() 的引用；新增 Wait()；Run() 去除 panic |
| `game/bootstrap/container.go` | 修改：Container 加 TaskRunner + GameEventKafkaConsumer 字段；NewContainer 加参数；所有 service 构造传 taskRunner；StartSchedulers 接收 appCtx 参数；Stop 顺序补 wg |
| `gateway/bootstrap/app.go` | 修改：加 appCtx/taskRunner/wg 字段；BroadcastSvc/Server goroutine 补 wg+recover；Stop() 返回 error + 顺序改造 |
| `gateway/bootstrap/container.go` | 修改：Container 加 TaskRunner 字段 |
| `cmd/game/main.go` | 修改：Stop 返回 error 处理（如需） |
| `cmd/gateway/main.go` | 修改：同上 |

#### 组 3：应用层 service 改造（5 修改）

| 文件 | 动作 |
|---|---|
| `game/application/game_app_service.go` | 修改：加 taskRunner 字段、构造函数加参数、13 处 goroutine 改 Submit |
| `game/application/room_app_service.go` | 修改：加 taskRunner 字段、构造函数加参数、2 处 goroutine 改 Submit |
| `game/application/seat_app_service.go` | 修改：加 taskRunner 字段、构造函数加参数、2 处 goroutine 改 Submit |
| `game/application/robot_behavior.go` | 修改：ScheduleAction 接口加 ctx、2 处 context.Background() 改 ctx |
| `game/application/robot_player.go` | 修改：RobotActionScheduler 接口加 ctx、3 处调用补 ctx |

#### 组 4：Broadcaster 接口统一（3 修改）

| 文件 | 动作 |
|---|---|
| `game/domain/repository.go` | 修改：domain.Broadcaster 接口加 ctx + error 返回值 |
| `game/infrastructure/broadcast/broadcaster.go` | 修改：GameBroadcaster 实现透传 ctx（去 context.Background()） |
| （所有调用点在组 3 的 3 个 service 文件中同步修改） | 33 处调用加 ctx 参数 |

#### 组 5：deduct_service 补 recover（1 修改）

| 文件 | 动作 |
|---|---|
| `settlement/service/deduct_service.go` | 修改：并行批处理补 per-goroutine recover |

#### 组 6：基础设施层 context 治理（7 修改，v3 扩展 sub-phase）

| 文件 | 动作 |
|---|---|
| `gateway/connection/manager.go` | 修改：5 处 context.Background() 改 m.ctx；subscribeKickChannel 补 recover（Phase 6.13） |
| `gateway/broadcast/broadcast.go` | 修改：1 处 context.Background() 改 s.ctx；consumer.Start goroutine 补 recover（Phase 6.13） |
| `gateway/middleware/auth.go` | 修改：OnConnect 加 ctx、recordFailedAttempt 加 ctx、cleanupRoutine 加 wg+recover、1 处 context.Background() 改 m.ctx |
| `gateway/router/router.go` | 修改：Route 加 ctx 参数、1 处 context.Background() 改 ctx |
| `gateway/health/health.go` | 修改：2 处 context.Background() 改 c.Request.Context() |
| `gateway/server/server.go` | 修改：handleWebSocket 调用 OnConnect/Route 传 ctx；handleConnection/writeAndHeartbeatPump 加 wg 跟踪 + recover |
| `settlement/scheduler/base.go` | 修改：BaseScheduler 加 ctx+wg 字段、Start 加 wg.Add、run 加 defer wg.Done+recover、executeTask 用 s.ctx 派生 per-task 超时、Stop 改 cancel()+wg.Wait、5 个子类构造函数加 ctx 参数 |

#### 组 7：RedisPubSubConsumer 修正（1 修改）

| 文件 | 动作 |
|---|---|
| `common/broadcast/redis_pubsub_consumer.go` | 修改：Start 用参数 ctx 替代构造时 ctx；consumeMessages 补 recover（Phase 6.13） |

#### 组 8：stats bootstrap 抽取（2 新建 + 1 修改）

| 文件 | 动作 |
|---|---|
| `stats/bootstrap/app.go` | 新建：Application + Start/Stop/Wait，HTTP server goroutine 补 wg+recover |
| `stats/bootstrap/container.go` | 新建：Container 装配 |
| `cmd/stats/main.go` | 修改：精简为 bootstrap.Run() 调用 |

#### 组 9：v3 新增 — 应用层长驻组件 RobotSchedulerService（1 修改，CRITICAL）

| 文件 | 动作 |
|---|---|
| `game/application/robot_scheduler_service.go` | 修改：Start(ctx) 接收 appCtx、补 wg 字段、scanLoop 补 wg.Done+recover、scanRooms 用 s.ctx 派生 per-scan ctx、Stop 补 wg.Wait |

#### 组 10：v3 新增 — 基础设施层 recover + wg 补全（4 修改）

| 文件 | 动作 |
|---|---|
| `game/server/generic_service.go` | 修改：GRPCServer 加 wg 字段、Serve goroutine 补 wg.Add+recover+wg.Done、Stop 补 wg.Wait |
| `common/discovery/discovery.go` | 修改：nacosResolver 加 wg 字段、start 接收 ctx、watch 补 wg.Add+recover+wg.Done、Close 补 wg.Wait |
| `common/lock/distributed_lock.go` | 修改：startWatchdog goroutine 补 defer recover |
| `common/kafka/consumer.go` | 修改：Start 循环补 defer recover + debug.Stack |

#### 组 11：v3 新增 — 已有 wg 的长驻 goroutine 补 recover（5 修改，归入 Phase 6.13）

| 文件 | 动作 |
|---|---|
| `game/scheduler/timeout_scheduler.go` | 修改：runChecker 补 defer recover |
| `gateway/broadcast/broadcast.go` | （与组 6 合并）consumer.Start goroutine 补 recover |
| `gateway/connection/manager.go` | （与组 6 合并）subscribeKickChannel 补 recover |
| `gateway/middleware/auth.go` | （与组 6 合并）cleanupRoutine 补 recover |
| `common/broadcast/redis_pubsub_consumer.go` | （与组 7 合并）consumeMessages 补 recover |

**总计**：新建 6 文件，修改 24 文件（v3 较 v2 增加 6 个修改文件）。

---

## 3. AsyncTaskRunner 设计

### 3.1 文件位置

`common/async/task_runner.go`（新建包 `common/async`）

### 3.2 结构定义

```go
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
```

### 3.3 设计要点

| 要点 | 实现 | 规约引用 |
|---|---|---|
| app-level ctx 派生 | `context.WithCancel(rootCtx)` | §6.1 |
| per-task timeout | `context.WithTimeout(r.rootCtx, ttl)` | §6.1 |
| panic recovery | `defer recover() + logger.Error + debug.Stack()` | §6.1 |
| WaitGroup 跟踪 | `r.wg.Add/Done/Wait` | §6.1 |
| closed 状态保护 | `r.mu.Lock + r.closed`，`Submit` 返回 error 不 panic | §6.1 |
| Stop 幂等 | `if r.closed { return }` | §12.3 |
| 默认超时 | `defaultTTL` 字段，建议 10s | §6.1 |

### 3.4 反模式（MUST NOT）

- ❌ 在 `task` 内部再 `go func()`——必须用 `Submit` 嵌套
- ❌ 在 `task` 内部用 `context.Background()` 派生——必须用传入的 `ctx`
- ❌ `Submit` 后立即 `Stop`+`Wait`——`Submit` 是异步语义，同步等待违背设计
- ❌ 不设置 ttl 且 `defaultTTL=0`——任务可能永久阻塞

---

## 4. 重构阶段

### Phase 1：落地 AsyncTaskRunner 基础设施

**目标**：创建 `common/async` 包，提供 `TaskRunner` 实现。

| Task | 文件 | 动作 |
|---|---|---|
| 1.1 | `common/async/task_runner.go` | 新建，实现 §3.2 的 `TaskRunner` |
| 1.2 | `common/async/task_runner_test.go` | 新建，覆盖：正常 Submit、panic recovery、per-task timeout、closed 后 Submit 返回 error、Stop 幂等、Wait 等待退出 |
| 1.3 | `common/async/doc.go` | 新建，包注释说明使用模式 + 反模式 |

**验证**：`go test ./common/async/...` 全绿；`go build ./common/...` 通过。

### Phase 2：注入 Application 与 Container + 生命周期改造

**目标**：game 和 gateway 的 `Application` 持有 `TaskRunner` 字段，`Container` 持有引用供 service 共享；修正 `Stop()` 顺序、补 `Wait()`、去除 `panic(err)`；**v3 补：consumer/server goroutine 补 wg+recover，gameEventKafkaConsumer 改 Container 字段**。

#### 2.1 game/bootstrap/app.go 改造

**Application struct 改造**（v3 加 `wg`）：
```go
type Application struct {
	Container  *Container
	config     *gameconfig.Config
	grpcServer *server.GRPCServer
	cancel     context.CancelFunc
	appCtx     context.Context      // 新增
	taskRunner *async.TaskRunner    // 新增
	wg         sync.WaitGroup       // v3 新增：跟踪 consumer goroutine
	nacos      *nacos.Client
	grpcPort   int
}
```

**NewApplicationWithConfig 改造**（在 logger.Init 之后插入）：
```go
appCtx, cancel := context.WithCancel(context.Background())
taskRunner := async.NewTaskRunner(appCtx, 10*time.Second)
if err := taskRunner.Start(); err != nil {
	cancel()
	return nil, fmt.Errorf("start task runner failed: %w", err)
}
```
把 `appCtx`、`cancel`、`taskRunner` 赋给 Application；把 `taskRunner` 传给 `NewContainer`。

**Start(ctx) 改造**（v3 补 wg+recover，gameEventKafkaConsumer 改 Container 字段）：
```go
func (a *Application) Start(ctx context.Context) error {
	// ... 现有初始化逻辑 ...

	// v3 改造：consumer goroutine 补 wg + recover
	a.wg.Add(2)
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("room event consumer panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := a.Container.RoomEventConsumer.Start(a.appCtx); err != nil {
			logger.Error("room event consumer failed", "error", err)
		}
	}()
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("game event kafka consumer panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := a.Container.GameEventKafkaConsumer.Start(a.appCtx); err != nil {
			logger.Error("game event kafka consumer failed", "error", err)
		}
	}()

	// ... 其余不变 ...
}
```

> **v3 修正**：原 v2 代码 `go a.Container.RoomEventConsumer.Start(a.appCtx)` 无 wg 无 recover；`gameEventKafkaConsumer` 是局部变量 Stop 无法触达。v3 改为：1) 两个 consumer goroutine 都用 `a.wg` 跟踪；2) `gameEventKafkaConsumer` 提升为 Container 字段（见 §2.2）。

**Stop() 改造**（v3 删除对不存在 Close() 的引用）：
```go
func (a *Application) Stop() error {
	logger.Info("shutting down game service...")

	// 1. 停止接受新异步任务 + cancel appCtx
	a.taskRunner.Stop()
	if a.cancel != nil {
		a.cancel()
	}

	// 2. 停止 consumer（依赖 ctx 取消退出；Stop 等待 a.wg）
	// 注意：RoomEventConsumer 和 GameEventKafkaConsumer 都通过 c.consumer.Start(ctx) 内部循环，
	// ctx 取消后循环退出。这里 a.wg.Wait() 等待外层 goroutine 退出。
	// 无需调用 Close() —— 当前 RoomEventConsumer/GameEventConsumer 无 Close 方法。

	// 3. 停止 scheduler
	a.Container.Stop()

	// 4. 等待 consumer goroutine 退出（带 10s 兜底超时）
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warn("consumer goroutines wait timeout, force shutdown")
	}

	// 5. 等待在飞异步任务退出（带 30s 兜底超时）
	waitRunner := make(chan struct{})
	go func() { a.taskRunner.Wait(); close(waitRunner) }()
	select {
	case <-waitRunner:
	case <-time.After(30 * time.Second):
		logger.Warn("task runner wait timeout, force shutdown")
	}

	// 6. 关闭底层资源
	if a.grpcServer != nil {
		a.grpcServer.Stop()
	}
	if a.nacos != nil {
		if err := a.nacos.Close(); err != nil {
			logger.Warn("failed to close nacos client", "error", err)
		}
	}
	if a.Container.KafkaProducer != nil {
		a.Container.KafkaProducer.Close()
	}
	if a.Container.Redis != nil {
		a.Container.Redis.Close()
	}
	logger.Info("game service stopped")
	return nil
}
```

> **v3 修正**：v2 的 Stop 调用 `a.Container.RoomEventConsumer.Close()`，但该接口不存在。v3 改为依赖 ctx 取消 + a.wg.Wait() 等待外层 goroutine 退出。

**新增 Wait() 方法**：
```go
func (a *Application) Wait() error {
	<-a.appCtx.Done()
	return nil
}
```

**Run() 改造**（去除 `panic(err)`，改 `logger.Fatal`）：
```go
func Run() {
	// ...
	app, err := NewApplication(cfgPath)
	if err != nil {
		logger.Fatal("failed to create application", "error", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		logger.Fatal("failed to start application", "error", err)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	if err := app.Stop(); err != nil {
		logger.Error("failed to stop application", "error", err)
	}
}
```

#### 2.2 game/bootstrap/container.go 改造

**Container struct 新增字段**（v3 加 `GameEventKafkaConsumer`）：
```go
type Container struct {
	// ... 现有字段 ...
	TaskRunner              *async.TaskRunner                // 新增，供 service 共享
	GameEventKafkaConsumer  *messaging.GameEventConsumer     // v3 新增：从 app.go 局部变量提升
}
```

**NewContainer 签名新增参数**：
```go
func NewContainer(
	taskRunner *async.TaskRunner,  // 新增
	platformCfg *config.PlatformConfig,
	// ... 其余 23 个参数不变 ...
) *Container
```

**InitAppServices 修改**：所有 service 构造调用新增 `taskRunner` 参数：
```go
c.GameAppService = application.NewGameAppService(..., c.TaskRunner)
c.RoomAppService = application.NewRoomAppService(..., c.TaskRunner)
c.SeatAppService = application.NewSeatAppService(..., c.TaskRunner)
```

**v3 修改：StartSchedulers 接收 appCtx**：
```go
// Before
func (c *Container) StartSchedulers() {
	c.ValidateReserveRatio(context.Background())
	c.TimeoutScheduler.Start()
	c.VirtualBalanceSyncScheduler.Start()
	c.RobotSchedulerService.Start()  // 内部自创 ctx
	// ... 5 个 settlement scheduler ...
}

// After
func (c *Container) StartSchedulers(appCtx context.Context) {
	c.ValidateReserveRatio(appCtx)  // 改用 appCtx
	c.TimeoutScheduler.Start()      // 内部 ctx 改造见 Phase 6.13（构造函数接 appCtx）
	c.VirtualBalanceSyncScheduler.Start()
	c.RobotSchedulerService.Start(appCtx)  // v3 改造：接收 appCtx
	// ... 5 个 settlement scheduler 都传 appCtx ...
}
```

> **v3 决策**：`Container.StartSchedulers()` 由 `Application.Start()` 调用，传入 `a.appCtx`。

#### 2.3 gateway/bootstrap/app.go + container.go 改造

**v3 补全 Application struct 完整字段**：
```go
type Application struct {
	Container  *Container
	config     *gatewayconfig.Config
	server     *http.Server
	cancel     context.CancelFunc
	appCtx     context.Context      // 新增
	taskRunner *async.TaskRunner    // 新增
	wg         sync.WaitGroup       // v3 新增：跟踪 BroadcastSvc/Server goroutine
	errChan    chan error
	nacos      *nacos.Client
}
```

**Start() 改造**（v3 补 wg+recover）：
```go
func (a *Application) Start(ctx context.Context) error {
	// ... 现有初始化 ...

	// v3 改造：BroadcastSvc goroutine 补 wg+recover
	if a.Container.BroadcastSvc != nil {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("broadcast service panic",
						"panic", r, "stack", string(debug.Stack()))
				}
			}()
			if err := a.Container.BroadcastSvc.Start(); err != nil {
				a.errChan <- err
			}
		}()
	}

	// v3 改造：Server goroutine 补 wg+recover
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gateway server panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := a.Container.Server.Start(); err != nil {
			a.errChan <- err
		}
	}()

	return nil
}
```

**Stop() 改造**（v3 补 taskRunner.Stop + Wait + wg.Wait）：
```go
func (a *Application) Stop() error {
	logger.Info("shutting down gateway service...")

	// 1. 停止接受新异步任务 + cancel appCtx
	a.taskRunner.Stop()
	if a.cancel != nil {
		a.cancel()
	}

	// 2. 停止 container（含 connMgr.Stop / BroadcastSvc.Stop / Server.Stop / AuthMiddleware.Stop）
	a.Container.Stop()

	// 3. 等待 BroadcastSvc/Server goroutine 退出
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warn("gateway goroutines wait timeout, force shutdown")
	}

	// 4. 等待 task runner
	waitRunner := make(chan struct{})
	go func() { a.taskRunner.Wait(); close(waitRunner) }()
	select {
	case <-waitRunner:
	case <-time.After(30 * time.Second):
		logger.Warn("task runner wait timeout, force shutdown")
	}

	// 5. 关闭底层资源
	// ...
	return nil
}
```

**Stop() 签名必须返回 error**（§6.2 rule 3）。gateway 的 `Wait()` 已存在（基于 errChan），保留。

#### 2.4 cmd/main.go 改造

`cmd/game/main.go` 和 `cmd/gateway/main.go` 通常只是 `bootstrap.Run()` 一行，无需修改（`bootstrap.Run()` 内部处理 Stop 返回 error）。

**验证**：`go build ./common/... ./game/... ./gateway/...` 通过；启动/停止流程不泄漏 goroutine（手动测试）；Grep 验证 `go a.Container.RoomEventConsumer.Start` / `go a.Container.GameEventKafkaConsumer.Start` / `go a.Container.BroadcastSvc.Start` 均在 `a.wg` 跟踪内。

### Phase 3：Broadcaster 接口统一 + 应用层 goroutine 迁移

> **决策**：Phase 3 和 Phase 5（Broadcaster 接口改造）合并执行，避免重复修改同一批 service 文件。Broadcaster 接口改造是 goroutine 迁移的前置条件（goroutine 内调用 Broadcast 需要 ctx 透传）。

#### 3.1 domain.Broadcaster 接口签名改造

**文件**：`game/domain/repository.go`

```go
// Before
type Broadcaster interface {
	Broadcast(roomID string, cmd string, data interface{}, excludeUserID string)
	BroadcastToUser(userID string, cmd string, data interface{})
}

// After
type Broadcaster interface {
	Broadcast(ctx context.Context, roomID string, cmd string, data interface{}, excludeUserID string) error
	BroadcastToUser(ctx context.Context, userID string, cmd string, data interface{}) error
}
```

> **注**：与 `common/broadcast.Broadcaster` 签名完全一致，便于 `GameBroadcaster` 直接透传。

#### 3.2 GameBroadcaster 实现改造

**文件**：`game/infrastructure/broadcast/broadcaster.go`

```go
// Before
func (b *GameBroadcaster) Broadcast(roomID string, event string, data interface{}, excludeUserID string) {
	if err := b.broadcaster.Broadcast(context.Background(), roomID, event, data, excludeUserID); err != nil {
		logger.Warn(...)
	}
}

// After
func (b *GameBroadcaster) Broadcast(ctx context.Context, roomID string, event string, data interface{}, excludeUserID string) error {
	if err := b.broadcaster.Broadcast(ctx, roomID, event, data, excludeUserID); err != nil {
		logger.Warn("broadcast failed", "room_id", roomID, "event", event, "error", err)
		return err
	}
	return nil
}
```

`BroadcastToUser` 同理。

#### 3.3 GameAppService 改造

**struct 新增字段**：
```go
type GameAppService struct {
	// ... 现有 18 字段 ...
	taskRunner *async.TaskRunner  // 新增
}
```

**构造函数新增参数**（末尾追加）：
```go
func NewGameAppService(..., timeoutCfg *config.TimeoutConfig, taskRunner *async.TaskRunner) *GameAppService
```

**13 处 goroutine 改 Submit**（含 6 处 `go s.xxx` 形式 + 7 处 `go func()` 形式）：

| # | 行号 | 原 goroutine | taskID | ttl | 改造后 |
|---|---|---|---|---|---|
| G1 | 267 | `go s.postSendPacketAsync(context.Background(), ...)` | `post_send_packet` | 10s | `s.taskRunner.Submit("post_send_packet", 10*time.Second, func(ctx context.Context) { s.postSendPacketAsync(ctx, ...) })` |
| G2 | 400 | `go s.settleRound(context.Background(), req.RoomID, roundID)` | `settle_round` | 15s | `s.taskRunner.Submit("settle_round", 15*time.Second, func(ctx context.Context) { s.settleRound(ctx, req.RoomID, roundID) })` |
| G3 | 445 | `go s.settleRound(context.Background(), roomID, roundID)` | `settle_round_robot` | 15s | 同 G2 |
| G4 | 499 | `go func() { PublishSessionStart(context.Background(), event) }()` | `publish_session_start` | 5s | `s.taskRunner.Submit("publish_session_start", 5*time.Second, func(ctx context.Context) { s.eventPublisher.PublishSessionStart(ctx, event) })` |
| G5 | 814 | `go s.postSendPacketAsync(context.Background(), ...)` | `post_send_packet_first_round` | 10s | 同 G1 |
| G6 | 846 | `go func() { endGameWithOptions(context.Background(), ...) }()`（有 recover） | `end_game_on_deduct_failure` | 15s | 删除 inline recover，改 Submit |
| G7 | 1035 | `go func() { PublishRoundSettle(context.Background(), event) }()` | `publish_round_settle` | 5s | 同 G4 |
| G8 | 1061 | `go func() { endGameWithOptions(context.Background(), ...) }()`（有 recover） | `end_game_on_settle` | 15s | 同 G6 |
| G9 | 1200 | `go func() { PublishSessionEnd(context.Background(), ...) }()` | `publish_session_end` | 5s | 同 G4 |
| G10 | 1212 | `go s.gameEndCallback(context.Background(), roomID)` | `game_end_callback` | 10s | `s.taskRunner.Submit("game_end_callback", 10*time.Second, func(ctx context.Context) { s.gameEndCallback(ctx, roomID) })` |
| G11 | 1321 | `go s.postSendPacketAsync(context.Background(), ...)` | `post_send_packet_system` | 10s | 同 G1 |
| G12 | 1480 | `go func() { PublishPacketCreated(context.Background(), ...) }()` | `publish_packet_created` | 5s | 同 G4 |

> **v3 计数修正**：v2 文本称 "2 处 `go s.xxx` + 11 处 `go func()` = 13"，实际为 6 处 `go s.xxx` + 7 处 `go func()` = 13。本表精确到 G1-G12 共 12 个，第 13 个在 G6/G8 内部嵌套（已合并）。

**16 处 Broadcast 调用加 ctx 参数**（v3 修正：15 Broadcast + 1 BroadcastToUser = 16 处）：在 `game_app_service.go` 中所有 `s.broadcaster.Broadcast(roomID, ...)` 改为 `s.broadcaster.Broadcast(ctx, roomID, ...)`，`s.broadcaster.BroadcastToUser(userID, ...)` 改为 `s.broadcaster.BroadcastToUser(ctx, userID, ...)`。

> **注**：这些方法本身已有 `ctx context.Context` 参数，直接用方法入参 `ctx`。G1-G12 改造后 goroutine 内部用 Submit 派生的 `ctx`。

#### 3.4 RoomAppService 改造

**struct 新增字段 + 构造函数新增参数**：同 GameAppService。

**2 处 goroutine 改 Submit**：

| # | 行号 | taskID | ttl |
|---|---|---|---|
| G13 | 262 | `resume_game_callback` | 10s |
| G14 | 527 | `auto_substitute_on_leave` | 10s |

**9 处 Broadcast + 2 处 BroadcastToUser 调用加 ctx 参数**（共 11 处）。

#### 3.5 SeatAppService 改造

**struct 新增字段 + 构造函数新增参数**：同上。

**2 处 goroutine 改 Submit**：

| # | 行号 | taskID | ttl |
|---|---|---|---|
| G15 | 185 | `auto_substitute_on_cancel` | 10s |
| G16 | 309 | `resume_game_on_ready` | 10s |

**5 处 Broadcast + 1 处 BroadcastToUser 调用加 ctx 参数**（共 6 处）。

#### 3.6 Container 装配更新

`game/bootstrap/container.go` 的 `InitAppServices` 中：
```go
c.GameAppService = application.NewGameAppService(..., c.TaskRunner)
c.RoomAppService = application.NewRoomAppService(..., c.TaskRunner)
c.SeatAppService = application.NewSeatAppService(..., c.TaskRunner)
```

**验证**：
- `go build ./game/...` 通过
- `Grep "go func()" game/application/game_app_service.go` 命中数 = 0
- `Grep "go s\." game/application/game_app_service.go` 命中数 = 0
- `Grep "context.Background()" game/application/` 命中数 = 2（仅 robot_behavior.go:65,101，Phase 4 处理）+ 2（robot_scheduler_service.go，Phase 6.9 处理）= 4
- `Grep "broadcaster.Broadcast(" game/application/` 所有调用第一个参数为 ctx

### Phase 4：RobotActionScheduler 接口改造

**目标**：为 `RobotActionScheduler` 接口加 ctx 参数，消除 `robot_behavior.go` 的 2 处 `context.Background()`。

#### 4.1 接口签名改造

**文件**：`game/application/robot_player.go`

```go
// Before
type RobotActionScheduler interface {
	ScheduleAction(roomID string, robotUserID string, action string, delay time.Duration)
}

// After
type RobotActionScheduler interface {
	ScheduleAction(ctx context.Context, roomID string, robotUserID string, action string, delay time.Duration)
}
```

#### 4.2 RobotBehaviorEngine 实现改造

**文件**：`game/application/robot_behavior.go`

```go
// Before (line 63)
func (e *RobotBehaviorEngine) ScheduleAction(roomID string, robotUserID string, action string, delay time.Duration) {
	data := fmt.Sprintf("%s:%s:%s:0", robotUserID, action, uuid.New().String()[:8])
	e.scheduler.SetTimeout(context.Background(), scheduler.TimeoutTypeRobot, roomID, data, delay)
}

// After
func (e *RobotBehaviorEngine) ScheduleAction(ctx context.Context, roomID string, robotUserID string, action string, delay time.Duration) {
	data := fmt.Sprintf("%s:%s:%s:0", robotUserID, action, uuid.New().String()[:8])
	e.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeRobot, roomID, data, delay)
}
```

**`scheduleRetry` 私有方法改造**（line 76-101）：方法签名加 `ctx context.Context`，内部 `context.Background()` 改 `ctx`。调用方 `HandleRobotTimeout`（line 181）改造：
```go
// Before
e.scheduleRetry(roomID, robotUserID, action, retryCount, roundID)
// After
e.scheduleRetry(ctx, roomID, robotUserID, action, retryCount, roundID)
```

#### 4.3 RobotPlayer 调用点改造

**文件**：`game/application/robot_player.go`

3 处调用补 ctx：

| 行号 | 方法 | 改造 |
|---|---|---|
| 89 | `JoinAndReady` | `p.behaviorEngine.ScheduleAction(roomID, ...)` → `p.behaviorEngine.ScheduleAction(ctx, roomID, ...)` |
| 125 | `SelectSeat` | 同上 |
| 199 | `ScheduleLeave` | 同上 |

> **注**：`JoinAndReady`、`SelectSeat`、`ScheduleLeave` 方法签名已有 `ctx context.Context` 参数，直接使用。

**验证**：`go build ./game/...` 通过；`Grep "context.Background()" game/application/robot_behavior.go` 命中数 = 0。

### Phase 5：deduct_service 并行批处理补 recover

**目标**：为 `settlement/service/deduct_service.go:156` 的并行批处理补 per-goroutine recover，避免 panic 导致 `wg.Wait()` 死锁。

```go
// Before
sem := make(chan struct{}, concurrency)
var wg sync.WaitGroup
for _, bill := range bills {
	wg.Add(1)
	sem <- struct{}{}
	go func(b *model.BillRecord) {
		defer wg.Done()
		defer func() { <-sem }()
		// ... executeSingleDeduct ...
	}(bill)
}
wg.Wait()
```

```go
// After
sem := make(chan struct{}, concurrency)
var wg sync.WaitGroup
var panicCount int32
for _, bill := range bills {
	wg.Add(1)
	sem <- struct{}{}
	go func(b *model.BillRecord) {
		defer wg.Done()
		defer func() { <-sem }()
		defer func() {
			if rec := recover(); rec != nil {
				atomic.AddInt32(&panicCount, 1)
				logger.Error("execute single deduct panic",
					"bill_id", b.ID, "panic", rec, "stack", string(debug.Stack()))
			}
		}()
		// ... executeSingleDeduct ...
	}(bill)
}
wg.Wait()
if panicCount > 0 {
	logger.Warn("batch deduct completed with panics", "panic_count", panicCount)
}
```

**验证**：`go build ./settlement/...` 通过；单测注入 panic 验证不死锁。

### Phase 6：基础设施层 context 治理 + 长驻组件补 wg+recover（v3 重组）

> v3 重组：将原 v2 的 Phase 6.1-6.8 与新增的 Phase 6.9-6.13 合并为本阶段，避免子阶段分散。

#### 6.1 gateway/connection/manager.go（5 处 ctx + 1 处 recover）

`Manager` 已有 `m.ctx` 字段。5 处 `context.Background()` 直接替换：

| 行号 | 方法 | 替换 |
|---|---|---|
| 122 | `registerInRedis` | `context.Background()` → `m.ctx` |
| 181 | `publishKickNotification` | 同上 |
| 242 | `deleteConnectionMapping` | 同上 |
| 250 | `GetPlayerRoom` | 同上 |
| 259 | `RenewConnectionTTL` | 同上 |

**v3 补：`subscribeKickChannel` 补 defer recover**（已有 `defer m.wg.Done()`，缺 recover）：
```go
func (m *Manager) subscribeKickChannel() {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("subscribe kick channel panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	// ... 现有逻辑 ...
}
```

#### 6.2 gateway/broadcast/broadcast.go（1 处 ctx + 1 处 recover）

```go
// Before (line 144)
func (s *BroadcastService) broadcastToRoom(...) {
	ctx := context.Background()
	// ...
}

// After
func (s *BroadcastService) broadcastToRoom(...) {
	ctx := s.ctx
	// ...
}
```

**v3 补：consumer.Start goroutine 补 recover**（line 63-66，已有 wg.Done 缺 recover）：
```go
// Before
go func() {
	defer s.wg.Done()
	if err := s.consumer.Start(s.ctx); err != nil { ... }
}()

// After
go func() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("broadcast consumer panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	if err := s.consumer.Start(s.ctx); err != nil { ... }
}()
```

#### 6.3 gateway/middleware/auth.go（4 处改造 + recover）

**6.3.1 OnConnect 加 ctx 参数**：
```go
// Before
func (m *AuthMiddleware) OnConnect(conn *connection.Connection, firstMessage []byte) error

// After
func (m *AuthMiddleware) OnConnect(ctx context.Context, conn *connection.Connection, firstMessage []byte) error
```

内部 `VerifyToken` 等操作用 `ctx`。

**6.3.2 recordFailedAttempt 加 ctx 参数**：
```go
// Before
func (m *AuthMiddleware) recordFailedAttempt(ip string)

// After
func (m *AuthMiddleware) recordFailedAttempt(ctx context.Context, ip string)
```

内部 `m.redis.Set(context.Background(), ...)` 改 `m.redis.Set(ctx, ...)`。

**6.3.3 cleanupRoutine 加 wg + recover 跟踪**（v3 补 recover）：

构造函数中 `go m.cleanupRoutine()` 前加 `m.wg.Add(1)`，`cleanupRoutine` 内：
```go
func (m *AuthMiddleware) cleanupRoutine() {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("cleanup routine panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	// ... 现有逻辑 ...
}
```

需给 `AuthMiddleware` 加 `wg sync.WaitGroup` 字段。

`Stop()` 方法加 `m.wg.Wait()`：
```go
func (m *AuthMiddleware) Stop() {
	m.cancel()
	m.wg.Wait()  // 新增
}
```

#### 6.4 gateway/router/router.go（1 处）

**Route 加 ctx 参数**：
```go
// Before
func (r *MessageRouter) Route(conn *connection.Connection, rawMessage []byte)

// After
func (r *MessageRouter) Route(ctx context.Context, conn *connection.Connection, rawMessage []byte)
```

内部 `context.WithTimeout(context.Background(), 5*time.Second)` 改 `context.WithTimeout(ctx, 5*time.Second)`。

#### 6.5 gateway/health/health.go（2 处）

```go
// Before
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

// After
ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
```

`CheckHealth` 和 `CheckReady` 各 1 处。

#### 6.6 gateway/server/server.go（WS 链路 ctx 透传 + goroutine wg+recover 跟踪）

**6.6.1 handleWebSocket 调用 OnConnect/Route 传 ctx**：
```go
// handleConnection 中
s.auth.OnConnect(conn, []byte(authReq))  // Before
s.auth.OnConnect(c.Request.Context(), conn, []byte(authReq))  // After

// handleReconnect 中
s.router.Route(conn, []byte(reconnectReq))  // Before
s.router.Route(ctx, conn, []byte(reconnectReq))  // After，ctx 来自 handleConnection 参数
```

> **注**：`handleConnection` 需要接收 ctx 参数（从 `handleWebSocket` 的 `c.Request.Context()` 透传）。

**6.6.2 handleConnection/writeAndHeartbeatPump 加 wg + recover 跟踪**（v3 补 recover）：

`Server` struct 加 `wg sync.WaitGroup` 字段。

```go
// handleWebSocket 中
s.wg.Add(2)
go func() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("handle connection panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	s.handleConnection(ctx, conn, token)
}()

// handleConnection 中
s.wg.Add(1)
go func() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("write and heartbeat pump panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	s.writeAndHeartbeatPump(conn)
}()
```

`Stop(ctx)` 方法末尾加 `s.wg.Wait()`（带 ctx 超时保护）：
```go
// 在 connMgr.Stop() 之后
waitDone := make(chan struct{})
go func() { s.wg.Wait(); close(waitDone) }()
select {
case <-waitDone:
case <-ctx.Done():
	logger.Warn("server goroutines wait timeout")
}
```

#### 6.7 settlement/scheduler/base.go（v3 完整改造：ctx + wg + Wait + recover）

> **v3 修正**：v2 仅将 stopCh 换为 ctx，但未加 wg + Wait + recover，违反自身 §6.4 rule 4。v3 补全。

**BaseScheduler struct 加 ctx + wg 字段**：
```go
type BaseScheduler struct {
	config SchedulerConfig
	task   TaskFunc
	redis  *cRedis.Client
	ctx    context.Context    // 新增（替代 stopCh）
	cancel context.CancelFunc // 新增
	wg     sync.WaitGroup     // v3 新增
	mu     sync.Mutex
}
```

**NewBaseScheduler 加 ctx 参数**：
```go
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
```

**Start 改造**（加 wg.Add）：
```go
func (s *BaseScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wg.Add(1) // v3 新增
	go s.run()
}
```

**run 改造**（加 wg.Done + recover + 用 s.ctx）：
```go
func (s *BaseScheduler) run() {
	defer s.wg.Done() // v3 新增
	defer func() {    // v3 新增
		if r := recover(); r != nil {
			logger.Error("scheduler run panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done(): // 改用 ctx
			return
		case <-ticker.C:
			s.executeTask()
		}
	}
}
```

**executeTask 改造**（用 s.ctx 派生 per-task 超时）：
```go
func (s *BaseScheduler) executeTask() {
	ctx, cancel := context.WithTimeout(s.ctx, s.config.Interval) // per-task 超时
	defer cancel()
	lock.WithRedisLock(ctx, s.redis, s.config.LockKey, s.config.LockTTL, func() error {
		return s.task(ctx)
	})
}
```

**Stop 改造**（cancel + wg.Wait 带超时）：
```go
func (s *BaseScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel() // 替代 close(stopCh)

	// v3 新增：等待 run goroutine 退出
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("scheduler stop timeout", "lock_key", s.config.LockKey)
	}
}
```

**5 个子类构造函数加 ctx 参数**：
- `NewCreditRetryScheduler(ctx, creditRetry, redis)`
- `NewGameSettleRetryScheduler(ctx, billMgr, gameSettleSvc, redis)`
- `NewGameSettleTimeoutScheduler(ctx, billMgr, gameSettleSvc, redis)`
- `NewRefundProcessScheduler(ctx, refundSvc, billMgr, redis)`
- `NewSettlementCheckScheduler(ctx, settlementCheck, redis)`

**Container 装配处**：5 个 scheduler 构造调用传入 `appCtx`（在 `Container.StartSchedulers(appCtx)` 中）。

#### 6.8 common/broadcast/redis_pubsub_consumer.go（修正 ctx + 补 recover）

**问题**：`Start(ctx)` 接受参数 ctx 但忽略，使用构造时的 `c.ctx`。

**改造**：合并两个 ctx——构造时不再创建独立 ctx，由 Start 传入：
```go
// Before
func NewRedisPubSubConsumer(...) *RedisPubSubConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	// ...
}
func (c *RedisPubSubConsumer) Start(ctx context.Context) error {
	// 忽略参数 ctx，用 c.ctx
}

// After
func NewRedisPubSubConsumer(...) *RedisPubSubConsumer {
	// 不再创建 ctx
}
func (c *RedisPubSubConsumer) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx) // 用参数 ctx 派生
	// ...
}
```

**v3 补：consumeMessages 补 defer recover**（已有 wg.Done，缺 recover）：
```go
func (c *RedisPubSubConsumer) consumeMessages() {
	defer c.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("consume messages panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	// ... 现有逻辑 ...
}
```

#### 6.9 v3 新增 — game/application/robot_scheduler_service.go（CRITICAL）

> **背景**：此文件 v2 完全遗漏。project memory 明确要求 "Robot scheduler scanRooms must use per-scan timeouts derived from scheduler context"。当前实现存在 5 类违规：1) Start 内部 `context.WithCancel(context.Background())`；2) `go s.scanLoop()` 无 wg 无 recover；3) `scanRooms` 内 `ctx := context.Background()`；4) Stop 不等 wg；5) scanLoop/scanRooms 无 recover。

**RobotSchedulerService struct 改造**（加 wg 字段）：
```go
type RobotSchedulerService struct {
	// ... 现有字段 ...
	ctx     context.Context    // 已有
	cancel  context.CancelFunc // 已有
	wg      sync.WaitGroup     // v3 新增
}
```

**Start 签名改造**（接收 appCtx）：
```go
// Before (line 77-81)
func (s *RobotSchedulerService) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.scanLoop()
	logger.Info("robot scheduler service started", ...)
}

// After
func (s *RobotSchedulerService) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx) // 从 appCtx 派生
	s.wg.Add(1)                                // v3 新增
	go s.scanLoop()
	logger.Info("robot scheduler service started", ...)
}
```

**scanLoop 改造**（加 wg.Done + recover）：
```go
// Before (line 92-107)
func (s *RobotSchedulerService) scanLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scanRooms()
		}
	}
}

// After
func (s *RobotSchedulerService) scanLoop() {
	defer s.wg.Done() // v3 新增
	defer func() {    // v3 新增
		if r := recover(); r != nil {
			logger.Error("scan loop panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(s.config.Scheduler.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scanRooms()
		}
	}
}
```

**scanRooms 改造**（用 s.ctx 派生 per-scan ctx）：
```go
// Before (line 111-139)
func (s *RobotSchedulerService) scanRooms() {
	ctx := context.Background() // ❌ 违反 project memory
	budget := time.Duration(float64(s.config.Scheduler.ScanInterval) * 0.8)
	deadline := time.Now().Add(budget)
	// ... 用 deadline 做软超时 ...
}

// After
func (s *RobotSchedulerService) scanRooms() {
	budget := time.Duration(float64(s.config.Scheduler.ScanInterval) * 0.8)
	ctx, cancel := context.WithTimeout(s.ctx, budget) // ✅ 从 s.ctx 派生 per-scan 超时
	defer cancel()

	rooms := s.getWaitingRooms(ctx)
	for _, roomID := range rooms {
		if ctx.Err() != nil {
			break // ctx 超时或取消，退出循环
		}
		s.assignRobotsToRoom(ctx, roomID)
	}
	s.recycleZombieRobots(ctx)
	s.cleanupEndedRooms(ctx)
}
```

> **注**：`getWaitingRooms`、`assignRobotsToRoom`、`recycleZombieRobots`、`cleanupEndedRooms` 等内部方法已接收 ctx 参数（见 grep 验证），但当前 scanRooms 内部传的是 `context.Background()`。改为派生 ctx 后透传。

**Stop 改造**（cancel + wg.Wait）：
```go
// Before (line 84-89)
func (s *RobotSchedulerService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	logger.Info("robot scheduler service stopped")
}

// After
func (s *RobotSchedulerService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	// v3 新增：等待 scanLoop 退出
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("robot scheduler stop timeout")
	}
	logger.Info("robot scheduler service stopped")
}
```

**Container 装配处**（`game/bootstrap/container.go`）：
```go
// StartSchedulers 中
c.RobotSchedulerService.Start(appCtx) // 改造：传 appCtx
```

**验证**：
- `go build ./game/...` 通过
- `Grep "context.Background()" game/application/robot_scheduler_service.go` 命中数 = 0
- `Grep "scanLoop" game/application/robot_scheduler_service.go` 含 `defer s.wg.Done()` + `defer recover()`
- `Grep "scanRooms" game/application/robot_scheduler_service.go` 含 `context.WithTimeout(s.ctx, ...)`

#### 6.10 v3 新增 — game/server/generic_service.go（GRPCServer.Serve 补 wg+recover）

**问题**：`go func() { s.server.Serve(lis) }()` 无 wg 无 recover，panic 导致进程崩溃。

**GRPCServer struct 加 wg 字段**：
```go
type GRPCServer struct {
	// ... 现有字段 ...
	wg sync.WaitGroup // v3 新增
}
```

**Start 改造**（line 735-742）：
```go
// Before
go func() {
	err := s.server.Serve(lis)
	if err != nil && err != grpc.ErrServerStopped {
		ready <- err
	} else {
		ready <- nil
	}
}()

// After
s.wg.Add(1)
go func() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("grpc serve panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	err := s.server.Serve(lis)
	if err != nil && err != grpc.ErrServerStopped {
		ready <- err
	} else {
		ready <- nil
	}
}()
```

**Stop 改造**（line 753-770，已有 GracefulStop 30s 超时，加 wg.Wait）：
```go
func (s *GRPCServer) Stop() {
	// ... 现有 GracefulStop + 30s 超时 ...

	// v3 新增：等待 Serve goroutine 退出
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		logger.Warn("grpc server goroutine wait timeout")
	}
}
```

#### 6.11 v3 新增 — common/discovery/discovery.go（nacosResolver 补 wg+recover+Wait）

**问题**：`go r.watch(ctx)` 无 wg 无 recover，`Close()` 不等待。

**nacosResolver struct 加 wg 字段**：
```go
type nacosResolver struct {
	// ... 现有字段 ...
	wg sync.WaitGroup // v3 新增
}
```

**start 改造**（line 53-58，接收 parent ctx）：
```go
// Before
func (r *nacosResolver) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.updateAddresses()
	go r.watch(ctx)
}

// After
func (r *nacosResolver) start(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	r.cancel = cancel
	r.updateAddresses()
	r.wg.Add(1) // v3 新增
	go r.watch(ctx)
}
```

> **注**：`start()` 由 `nacosResolver` 的创建方（gRPC resolver 注册处）调用，需让创建方传入 appCtx。如 gRPC resolver 机制限制无法传入，可保留 `context.Background()` 但补 wg+recover。

**watch 改造**（line 60-72，加 wg.Done + recover）：
```go
// Before
func (r *nacosResolver) watch(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.updateAddresses()
		}
	}
}

// After
func (r *nacosResolver) watch(ctx context.Context) {
	defer r.wg.Done() // v3 新增
	defer func() {    // v3 新增
		if r := recover(); r != nil {
			logger.Error("nacos resolver watch panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.updateAddresses()
		}
	}
}
```

**Close 改造**（line 97-101，加 wg.Wait）：
```go
// Before
func (r *nacosResolver) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

// After
func (r *nacosResolver) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	// v3 新增：等待 watch goroutine 退出
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		logger.Warn("nacos resolver close timeout")
	}
}
```

#### 6.12 v3 新增 — common/lock/distributed_lock.go（watchdog 补 recover）

**问题**：`startWatchdog` goroutine 已有 wg，但无 recover。`Extend()` panic 会导致进程崩溃 + `Release()` 中 `wg.Wait()` 死锁。

**改造**（line 114-134，加 defer recover）：
```go
// Before
func (l *Lock) startWatchdog(interval time.Duration) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if ok, err := l.mutex.Extend(); !ok || err != nil {
					logger.Warn("failed to extend lock", "key", l.key, "error", err)
					return
				}
			case <-l.watchdogStop:
				return
			}
		}
	}()
}

// After
func (l *Lock) startWatchdog(interval time.Duration) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() { // v3 新增
			if r := recover(); r != nil {
				logger.Error("watchdog panic",
					"key", l.key, "panic", r, "stack", string(debug.Stack()))
			}
		}()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if ok, err := l.mutex.Extend(); !ok || err != nil {
					logger.Warn("failed to extend lock", "key", l.key, "error", err)
					return
				}
			case <-l.watchdogStop:
				return
			}
		}
	}()
}
```

#### 6.13 v3 新增 — common/kafka/consumer.go（Start 循环补 recover）

**问题**：`processMessage` 有 recover（line 122），但 `Start` 主循环（line 84-118）无 recover。`FetchMessage` 或 `CommitMessages` panic 会导致进程崩溃。

**改造**（Start 顶部加 defer recover）：
```go
func (c *Consumer) Start(ctx context.Context) error {
	defer func() { // v3 新增
		if r := recover(); r != nil {
			logger.Error("kafka consumer start panic",
				"topic", c.topic, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return c.reader.Close()
		default:
		}
		msg, err := c.reader.FetchMessage(ctx)
		// ... 现有逻辑 ...
	}
}
```

#### 6.14 v3 新增 — game/scheduler/timeout_scheduler.go（runChecker 补 recover）

**问题**：`runChecker` 已有 `defer s.wg.Done()`，但无 recover。`checkTimeouts` 或 `redis.ZRangeByScore` panic 会导致进程崩溃。per-handler goroutine（line 254-264）已有 recover ✓。

**改造**（line 211-225）：
```go
func (s *TimeoutScheduler) runChecker(timeoutType TimeoutType, config TimeoutConfig) {
	defer s.wg.Done()
	defer func() { // v3 新增
		if r := recover(); r != nil {
			logger.Error("run checker panic",
				"type", timeoutType, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(config.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkTimeouts(timeoutType)
		}
	}
}
```

**验证**：
- `go build ./common/... ./game/... ./gateway/... ./settlement/...` 通过
- `Grep "context.Background()" gateway/connection/` 命中数 = 1（仅 NewManager）
- `Grep "context.Background()" gateway/broadcast/` 命中数 = 1（仅 NewBroadcastService）
- `Grep "context.Background()" gateway/middleware/auth.go` 命中数 = 1（仅 NewAuthMiddleware）
- `Grep "context.Background()" gateway/health/` 命中数 = 0
- `Grep "context.Background()" gateway/router/` 命中数 = 0
- `Grep "context.Background()" gateway/server/` 命中数 = 0
- `Grep "context.Background()" settlement/scheduler/` 命中数 = 0
- `Grep "context.Background()" game/application/robot_scheduler_service.go` 命中数 = 0
- `Grep "context.Background()" common/discovery/` 命中数 = 0
- `Grep "stopCh" settlement/scheduler/` 命中数 = 0
- `Grep "TaskRunner"` 在 .go 文件命中数 ≥ 15
- `Grep "broadcaster.Broadcast(" game/application/` 所有调用第一个参数为 ctx
- `Grep "ScheduleAction(" game/application/` 所有调用第一个参数为 ctx

### Phase 7：stats bootstrap 抽取

**目标**：将 `cmd/stats/main.go` 的装配逻辑抽取到 `stats/bootstrap/` 包，统一三服务的启动模式。**v3 补：HTTP server goroutine 补 wg+recover**。

#### 7.1 新建 stats/bootstrap/app.go

```go
package bootstrap

type Application struct {
	Container *Container
	cfg       *statsConfig.Config
	server    *http.Server
	cancel    context.CancelFunc
	appCtx    context.Context
	taskRunner *async.TaskRunner // 可选，stats 无应用层 fire-and-forget
	wg        sync.WaitGroup     // v3 新增：跟踪 HTTP server goroutine
	errChan   chan error
}

func NewApplication(cfgPath string) (*Application, error) { ... }

func (a *Application) Start(ctx context.Context) error {
	// ... 初始化 ...

	// v3 改造：HTTP server goroutine 补 wg+recover
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("stats http server panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		logger.Info("stats server listening", "addr", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.errChan <- err // 改为发 errChan，不用 logger.Fatal
		}
	}()
	return nil
}

func (a *Application) Stop() error {
	// 1. Shutdown HTTP server (5s 超时)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil { ... }

	// 2. 等待 goroutine 退出
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warn("stats goroutines wait timeout")
	}

	// 3. 关闭底层资源
	if a.Container != nil { a.Container.Stop() }
	return nil
}

func (a *Application) Wait() error { return <-a.errChan }

func Run() {
	app, err := NewApplication(*cfgPath)
	if err != nil {
		logger.Fatal("failed to create application", "error", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		logger.Fatal("failed to start application", "error", err)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	if err := app.Stop(); err != nil {
		logger.Error("failed to stop application", "error", err)
	}
}
```

#### 7.2 新建 stats/bootstrap/container.go

```go
package bootstrap

type Container struct {
	Config       *statsConfig.Config
	DB           *gorm.DB
	Redis        *cRedis.Client
	StatsRepo    *repository.StatsRepository
	StatsService *service.StatsService
	StatsHandler *handler.StatsHandler
}

func NewContainer(cfg *statsConfig.Config) (*Container, error) { ... }
func (c *Container) Stop() { ... }
```

#### 7.3 修改 cmd/stats/main.go

精简为：
```go
package main

import "github.com/cashparty/backend/stats/bootstrap"

func main() {
	bootstrap.Run()
}
```

**验证**：`go build ./cmd/stats/...` 通过；stats 服务启动/停止行为不变；`Grep "logger.Fatal" cmd/stats/` 命中数 = 0（已迁移到 bootstrap.Run）。

### Phase 8：v3 新增 — RoomEventConsumer.tryAcquire fail-closed 改造

**问题**：`RoomEventConsumer.tryAcquire`（`game/infrastructure/messaging/room_event_consumer.go:124-137`）在 Redis 错误时返回 `true`（fail-open），与 `GameEventConsumer.tryAcquire`（fail-closed，返回 error）不一致。违反 project memory rule "Kafka consumer error handling must return error on Redis unavailability to trigger Kafka retry (fail-closed), not fail-open"。

**改造**：
```go
// Before
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, event *domain.RoomEvent) bool {
	key := gateway.EventProcessedKey(event.TraceID)
	ok, err := c.redis.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil {
		logger.Warn("tryAcquire SetNX failed, fail-open", ...)
		return true // ❌ fail-open
	}
	return ok
}

// After
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, event *domain.RoomEvent) (bool, error) {
	key := gateway.EventProcessedKey(event.TraceID)
	ok, err := c.redis.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil {
		return false, fmt.Errorf("tryAcquire SetNX failed: %w", err) // ✅ fail-closed
	}
	return ok, nil
}
```

**调用方改造**：`HandleEvent` 中处理 `(bool, error)` 返回值，error 时返回 error 触发 Kafka 重试。

**验证**：`Grep "fail-open" game/infrastructure/messaging/` 命中数 = 0。

---

## 5. 重构阶段汇总（v3）

| Phase | 内容 | 涉及文件数 | 优先级 | 依赖 |
|---|---|---|---|---|
| 1 | AsyncTaskRunner 基础设施 | 3（新建） | P0 | 无 |
| 2 | Application/Container 注入 + 生命周期改造 + consumer goroutine wg+recover | 6 | P0 | Phase 1 |
| 3 | Broadcaster 接口统一 + 应用层 13 处 goroutine 迁移 | 5 | P0 | Phase 2 |
| 4 | RobotActionScheduler 接口加 ctx | 2 | P1 | Phase 3 |
| 5 | deduct_service 并行批处理补 recover | 1 | P1 | 无 |
| 6 | 基础设施层 context 治理 + 长驻组件补 wg+recover（含 14 子阶段） | 13+5 | P0/P1 | Phase 2 |
| 7 | stats bootstrap 抽取（含 HTTP server wg+recover） | 3（2 新建+1 修改） | P2 | 无 |
| 8 | RoomEventConsumer tryAcquire fail-closed 改造 | 1 | P2 | 无 |

---

## 6. 规约（写入 CODING_STANDARD.md §6 补充）

### 6.1 AsyncTaskRunner 使用规约

1. **MUST**：应用层 fire-and-forget goroutine 必须经 `TaskRunner.Submit` 提交，禁止裸 `go func()` 或 `go s.xxx(...)`。
2. **MUST**：`Submit` 的 task 函数必须接收并使用 `ctx context.Context`，禁止在 task 内部使用 `context.Background()` 或 `context.TODO()`。
3. **MUST**：`Submit` 必须指定合理的 ttl（事件发布 5s、业务流程 10-15s），禁止 ttl=0 依赖 defaultTTL（除非性能敏感场景）。
4. **MUST**：`taskID` 必须为 snake_case 英文短语，唯一标识任务用途（如 `publish_session_start`）。
5. **MUST NOT**：在 task 内部再 `go func()`——如需嵌套，再次调用 `Submit`。
6. **MUST NOT**：在 `Stop` 后 `Submit`——会返回 error，调用方应记录 Warn 但不应 panic。
7. **SHOULD**：`Submit` 返回 error 时记 `Warn` 而非 `Error`（停服期间拒绝新任务是预期行为）。

### 6.2 Application 生命周期规约

1. **MUST**：`Application` 必须持有 `appCtx context.Context`、`taskRunner *async.TaskRunner`、`wg sync.WaitGroup` 三字段（wg 跟踪 consumer/server goroutine）。
2. **MUST**：`Application.Stop()` 必须按顺序：`taskRunner.Stop()` → `cancel appCtx` → 停止 consumer/scheduler（`Container.Stop()`）→ `a.wg.Wait()`（consumer goroutine，10s 超时）→ `taskRunner.Wait()`（30s 超时）→ 关闭底层资源。
3. **MUST**：`Application.Stop()` 签名必须返回 `error`，`Wait()` 方法必须存在。
4. **MUST**：`Container` 必须持有 `TaskRunner` 引用，供所有 service 共享。
5. **MUST**：所有 consumer（`RoomEventConsumer`/`GameEventKafkaConsumer`）必须作为 Container/Application 字段持有，禁止作为局部变量。
6. **MUST**：`Application.Run()` 中启动失败必须用 `logger.Fatal`，禁止 `panic(err)`。
7. **MUST**：bootstrap 层所有 `go consumer.Start(ctx)` / `go server.Start()` goroutine 必须 `wg.Add(1)` + `defer wg.Done()` + `defer recover()` + `logger.Error` + `debug.Stack()`。
8. **SHOULD**：`NewContainer` 参数过多时（当前 game 24 个），应改为 options struct 模式。

### 6.3 context 使用规约（补充 §6.2）

1. **MUST NOT**：HTTP handler 内使用 `context.Background()`——必须用 `c.Request.Context()` 或其派生。
2. **MUST NOT**：WS 消息路由内使用 `context.Background()`——必须用连接 ctx 或其派生。
3. **MUST NOT**：应用层 service 方法内使用 `context.Background()`——必须用调用方传入的 ctx。
4. **MUST NOT**：长驻组件运行时方法内使用 `context.Background()`——必须用 `m.ctx`/`s.ctx`。构造函数内可接受。
5. **MUST**：所有跨层接口（Broadcaster、RobotActionScheduler、OnConnect、Route）必须包含 `ctx context.Context` 首参。
6. **MUST**：`context.Background()` 仅允许在：bootstrap `Run` 入口创建 app-level ctx、构造函数创建组件 root ctx、`Stop` 创建 shutdown ctx 时使用。
7. **MAY**：构造函数/init 阶段使用 `context.Background()`（无请求 ctx 可用）。

### 6.4 长驻组件 ctx+wg+recover 模式规约（v3 强化）

1. **MUST**：所有长驻组件（Manager/BroadcastService/AuthMiddleware/Scheduler/Consumer/Resolver/RobotScheduler/GRPCServer/Lock watchdog）必须持有 `ctx context.Context` + `cancel context.CancelFunc` + `wg sync.WaitGroup` 三件套。
2. **MUST**：构造函数中 `ctx, cancel := context.WithCancel(rootCtx)` 创建组件 ctx，存为字段。**禁止** `context.WithCancel(context.Background())`，必须从 parent ctx 派生。
3. **MUST**：所有后台 goroutine 启动前 `wg.Add(1)`，退出时 `defer wg.Done()` + `defer recover()` + `logger.Error` + `debug.Stack()`。**v3 强调：wg 与 recover 缺一不可**——wg.Done 不调用会导致 Wait 死锁，recover 缺失会导致进程崩溃。
4. **MUST**：`Stop()`/`Close()` 方法必须 `cancel()` + `wg.Wait()`（带 5-10s 超时）。
5. **MUST**：运行时方法内用 `s.ctx`/`m.ctx` 而非 `context.Background()`。
6. **MUST NOT**：用 `stopCh chan struct{}` 替代 ctx（统一用 ctx 模式）。
7. **MUST**：长驻 scheduler 的 `executeTask`/`scanRooms` 等运行时方法必须用 `context.WithTimeout(s.ctx, budget)` 派生 per-task/per-scan 超时 ctx。
8. **MAY**：无后台 goroutine 的组件（如 `RateLimiter`/`SignatureMiddleware`）`Stop()` 可以为空——本规约不强制。

### 6.5 Broadcaster 接口规约

1. **MUST**：`domain.Broadcaster` 与 `common/broadcast.Broadcaster` 签名必须一致（含 ctx + error）。
2. **MUST**：`GameBroadcaster` 包装层必须透传 ctx，禁止内部用 `context.Background()`。
3. **MUST**：应用层调用 `Broadcast`/`BroadcastToUser` 时必须传入方法 ctx 参数。
4. **SHOULD**：应用层不检查 `Broadcast` 返回的 error（包装层已 Warn 日志），除非有特定重试需求。

---

## 7. 验证清单

### 7.1 代码检查

- [ ] `common/async/task_runner.go` 存在，含 `TaskRunner` struct + 5 个方法
- [ ] `common/async/task_runner_test.go` 覆盖：正常 Submit、panic recovery、timeout、closed 后 Submit 返回 error、Stop 幂等
- [ ] `game/bootstrap/app.go` `Application` 含 `appCtx`/`taskRunner`/`wg` 字段
- [ ] `game/bootstrap/app.go` `Stop()` 返回 `error` + 含 30s 超时保护
- [ ] `game/bootstrap/app.go` `Wait()` 方法存在
- [ ] `game/bootstrap/app.go` `Run()` 中启动失败用 `logger.Fatal` 不用 `panic`
- [ ] `game/bootstrap/app.go` `Stop()` 顺序：taskRunner.Stop → cancel → Container.Stop → wg.Wait(10s) → taskRunner.Wait(30s) → grpcServer.Stop → nacos.Close → KafkaProducer.Close → Redis.Close
- [ ] `game/bootstrap/app.go` 两个 consumer goroutine 含 `defer wg.Done()` + `defer recover()`
- [ ] `game/bootstrap/container.go` `Container` 含 `TaskRunner` + `GameEventKafkaConsumer` 字段
- [ ] `game/bootstrap/container.go` `StartSchedulers(appCtx)` 接收 ctx 参数
- [ ] `gateway/bootstrap/app.go` `Application` 含 `appCtx`/`taskRunner`/`wg` 字段
- [ ] `gateway/bootstrap/app.go` `Stop()` 返回 `error` + 顺序改造
- [ ] `gateway/bootstrap/app.go` BroadcastSvc/Server goroutine 含 `defer wg.Done()` + `defer recover()`
- [ ] `game/domain/repository.go` `Broadcaster` 接口含 ctx + error
- [ ] `game/infrastructure/broadcast/broadcaster.go` `GameBroadcaster` 透传 ctx
- [ ] `game/application/robot_player.go` `RobotActionScheduler` 接口含 ctx
- [ ] `game/application/robot_scheduler_service.go` `Start(ctx)` 接收 ctx
- [ ] `game/application/robot_scheduler_service.go` `scanLoop` 含 `defer wg.Done()` + `defer recover()`
- [ ] `game/application/robot_scheduler_service.go` `scanRooms` 用 `context.WithTimeout(s.ctx, ...)`
- [ ] `game/application/robot_scheduler_service.go` `Stop()` 含 `wg.Wait()`
- [ ] `game/server/generic_service.go` `GRPCServer` 含 wg 字段
- [ ] `game/server/generic_service.go` Serve goroutine 含 `defer wg.Done()` + `defer recover()`
- [ ] `game/server/generic_service.go` `Stop()` 含 `wg.Wait()`
- [ ] `common/discovery/discovery.go` `nacosResolver` 含 wg 字段
- [ ] `common/discovery/discovery.go` `watch` 含 `defer wg.Done()` + `defer recover()`
- [ ] `common/discovery/discovery.go` `Close()` 含 `wg.Wait()`
- [ ] `common/lock/distributed_lock.go` `startWatchdog` goroutine 含 `defer recover()`
- [ ] `common/kafka/consumer.go` `Start` 顶部含 `defer recover()`
- [ ] `gateway/middleware/auth.go` `OnConnect` 含 ctx 参数
- [ ] `gateway/middleware/auth.go` `recordFailedAttempt` 含 ctx 参数
- [ ] `gateway/middleware/auth.go` `AuthMiddleware` 含 wg 字段，cleanupRoutine 含 wg.Done+recover，Stop 等 wg
- [ ] `gateway/router/router.go` `Route` 含 ctx 参数
- [ ] `gateway/server/server.go` `Server` 含 wg 字段，handleConnection/writeAndHeartbeatPump 含 wg.Done+recover
- [ ] `gateway/server/server.go` `handleWebSocket` 调用 OnConnect/Route 传 ctx
- [ ] `settlement/scheduler/base.go` `BaseScheduler` 含 ctx+wg 字段，无 stopCh
- [ ] `settlement/scheduler/base.go` `Start` 含 `wg.Add(1)`
- [ ] `settlement/scheduler/base.go` `run` 含 `defer wg.Done()` + `defer recover()`
- [ ] `settlement/scheduler/base.go` `Stop` 含 `wg.Wait()`
- [ ] `settlement/scheduler/base.go` `executeTask` 用 `context.WithTimeout(s.ctx, ...)`
- [ ] `common/broadcast/redis_pubsub_consumer.go` `Start` 用参数 ctx
- [ ] `common/broadcast/redis_pubsub_consumer.go` `consumeMessages` 含 `defer recover()`
- [ ] `game/scheduler/timeout_scheduler.go` `runChecker` 含 `defer recover()`
- [ ] `game/infrastructure/messaging/room_event_consumer.go` `tryAcquire` 返回 `(bool, error)`
- [ ] `stats/bootstrap/app.go` HTTP server goroutine 含 `defer wg.Done()` + `defer recover()`

### 7.2 Grep 验证

- [ ] `Grep "go func()" game/application/game_app_service.go` 命中数 = 0
- [ ] `Grep "go s\." game/application/game_app_service.go` 命中数 = 0
- [ ] `Grep "context.Background()" game/application/game_app_service.go` 命中数 = 0
- [ ] `Grep "context.Background()" game/application/room_app_service.go` 命中数 = 0
- [ ] `Grep "context.Background()" game/application/seat_app_service.go` 命中数 = 0
- [ ] `Grep "context.Background()" game/application/robot_behavior.go` 命中数 = 0
- [ ] `Grep "context.Background()" game/application/robot_scheduler_service.go` 命中数 = 0
- [ ] `Grep "context.Background()" game/infrastructure/broadcast/` 命中数 = 0
- [ ] `Grep "context.Background()" gateway/connection/` 命中数 = 1（仅 NewManager）
- [ ] `Grep "context.Background()" gateway/broadcast/` 命中数 = 1（仅 NewBroadcastService）
- [ ] `Grep "context.Background()" gateway/middleware/auth.go` 命中数 = 1（仅 NewAuthMiddleware）
- [ ] `Grep "context.Background()" gateway/health/` 命中数 = 0
- [ ] `Grep "context.Background()" gateway/router/` 命中数 = 0
- [ ] `Grep "context.Background()" gateway/server/` 命中数 = 0
- [ ] `Grep "context.Background()" settlement/scheduler/` 命中数 = 0
- [ ] `Grep "context.Background()" common/discovery/` 命中数 = 0
- [ ] `Grep "stopCh" settlement/scheduler/` 命中数 = 0
- [ ] `Grep "TaskRunner"` 在 .go 文件命中数 ≥ 15
- [ ] `Grep "broadcaster.Broadcast(" game/application/` 所有调用第一个参数为 ctx
- [ ] `Grep "ScheduleAction(" game/application/` 所有调用第一个参数为 ctx
- [ ] `Grep "fail-open" game/infrastructure/messaging/` 命中数 = 0

### 7.3 构建验证

- [ ] `go build ./common/... ./game/... ./gateway/... ./stats/... ./cmd/... ./api/... ./settlement/...` 通过
- [ ] `go vet ./common/... ./game/... ./gateway/... ./stats/... ./cmd/... ./api/... ./settlement/...` 通过
- [ ] `gofmt -l common/async/ common/ game/ gateway/ settlement/ stats/` 无输出
- [ ] `go test ./common/async/...` 通过

### 7.4 行为验证（手动）

- [ ] 启动 game 服务，发起一局完整游戏（发包→抢包→结算），观察日志有 `task=publish_session_start` 等 taskID
- [ ] 模拟事件发布 panic（如关闭 kafka），观察日志有 `async task panic` + stack，进程不崩溃
- [ ] 发送 SIGTERM 停服，观察日志有 task runner wait 完成，无 `wait timeout` 警告
- [ ] 停服期间提交任务，观察日志有 `task runner is closed, rejected task` Warn
- [ ] gateway WS 连接建立时 auth 失败，观察 Redis IP 锁写入用 request ctx
- [ ] 模拟 grpc Serve panic（如端口冲突后 Serve 返回非 ErrServerStopped），观察进程不崩溃
- [ ] 模拟 robot scanRooms 内某 Redis 调用 panic，观察进程不崩溃，scanLoop 退出（不再循环）
- [ ] 模拟 Lock.Extend panic，观察进程不崩溃，Release 不死锁
- [ ] 模拟 kafka consumer Start 循环 panic，观察进程不崩溃
- [ ] stats 服务启动/停止行为不变

---

## 8. 风险与回滚

| 风险 | 概率 | 影响 | 缓解措施 |
|---|---|---|---|
| TaskRunner 实现有 bug | 低 | panic | Phase 1 单测覆盖 |
| service 构造函数参数变更导致装配失败 | 中 | 编译错误 | Phase 2-3 同步更新 Container |
| Broadcaster 接口签名变更影响 33 处调用 | 中 | 编译错误 | Phase 3 集中修改，Grep 验证无遗漏 |
| RobotActionScheduler 接口变更影响 RobotPlayer | 低 | 编译错误 | 仅 3 处调用 |
| OnConnect/Route 接口变更影响 WS server | 中 | 编译错误 | Phase 6.6 同步修改 server.go |
| BaseScheduler 改造影响 5 个子类 | 中 | 编译错误 | Phase 6.7 同步修改所有子类 |
| RobotSchedulerService.Start 签名变更 | 低 | 编译错误 | 仅 Container.StartSchedulers 一处调用 |
| Stop 顺序错误导致 consumer 先于 taskRunner 退出 | 中 | 任务失败 | 严格按 §4.2 顺序 |
| 30s Wait 超时不够 | 低 | 部分任务被强杀 | 可配置化，先硬编码 30s |
| **v3: RobotScheduler scanRooms per-scan ctx 太短** | 中 | 扫描不完整 | budget = scanInterval * 0.8，可调 |
| **v3: GRPCServer.wg.Wait 与 GracefulStop 顺序竞争** | 低 | Stop 阻塞 | GracefulStop 后 Serve 必返回，wg.Wait 5s 兜底 |
| **v3: nacosResolver.wg.Wait 与 gRPC 连接关闭竞争** | 低 | Close 阻塞 | 5s 兜底超时 |

**回滚策略**：每个 Phase 独立提交，如出现问题可单独 revert。

---

## 9. 决策记录

### Decision 1: AsyncTaskRunner 放在 common/async

**理由**：game 和 gateway 都需要，放 common 供跨服务复用。

### Decision 2: 不迁移基础设施层长驻 goroutine 到 runner

**理由**：长驻循环与 fire-and-forget 语义不匹配。仅修正它们的 ctx 使用方式 + 补 wg + 补 recover。

### Decision 3: deduct_service 并行批处理不迁移到 runner

**理由**：`sync.WaitGroup` 同步等待的并行计算，迁移会破坏同步语义。仅补 recover。

### Decision 4: Broadcaster 接口改造与 goroutine 迁移合并执行

**理由**：goroutine 内调用 Broadcast 需要 ctx 透传，接口改造是前置条件。

### Decision 5: domain.Broadcaster 与 common/broadcast.Broadcaster 签名统一

**理由**：消除两套接口签名差异，GameBroadcaster 包装层可直接透传。

### Decision 6: GameBroadcaster 返回 error 但应用层不检查

**理由**：包装层已 Warn 日志，应用层检查 error 会增加 33 处调用的复杂度。

### Decision 7: RobotActionScheduler 接口加 ctx

**理由**：透传 ctx 是规约要求，且 SetTimeout 已支持 ctx。

### Decision 8: OnConnect/Route 加 ctx 参数

**理由**：WS 链路 ctx 透传是规约要求。

### Decision 9: BaseScheduler 用 ctx 替代 stopCh + 补 wg + 补 recover

**理由**：与 TimeoutScheduler/VirtualBalanceSyncScheduler 风格统一，且 v2 仅换 stopCh 不补 wg 违反自身 §6.4 rule 4。

### Decision 10: RedisPubSubConsumer.Start 用参数 ctx

**理由**：当前双重 ctx 设计混乱，外部无法控制生命周期。

### Decision 11: Server.handleConnection/writeAndHeartbeatPump 加 wg+recover 跟踪

**理由**：Stop 时无法等待连接 goroutine 退出，可能导致资源泄漏；panic 会导致进程崩溃。

### Decision 12: AuthMiddleware.cleanupRoutine 加 wg+recover 跟踪

**理由**：Stop 时仅 cancel 不等待存在日志延迟；无 recover 存在 panic 风险。

### Decision 13: stats bootstrap 抽取

**理由**：统一三服务启动模式，符合 §2.1 规约。

### Decision 14: defaultTTL = 10s，事件发布 5s，业务流程 15s

**理由**：10s 覆盖绝大多数业务任务；事件发布 <1s，5s 足够；endGameWithOptions 链路较长，给 15s。

### Decision 15: Stop 的 Wait 超时硬编码（10s consumer / 30s task runner）

**理由**：99% 场景任务应在 15s 内完成，30s 兜底。consumer goroutine 通常 5s 内退出，10s 兜底。

### Decision 16: config_listener.go 的 nacos ListenConfig goroutine 不迁移到 runner

**理由**：Nacos 重构 Phase 4.5 已为它们补了 panic recovery，且它们是长驻阻塞监听（非 fire-and-forget），语义不匹配。

### Decision 17 (v3 新增): RobotSchedulerService.Start 改为接收 ctx 参数

**理由**：project memory 明确要求 "Robot scheduler scanRooms must use per-scan timeouts derived from scheduler context"。当前 Start 内部 `context.WithCancel(context.Background())` 违反 ctx 链式派生原则，Stop 无法中断 scanRooms 内部的 Redis/DB 调用。

### Decision 18 (v3 新增): GRPCServer.Serve goroutine 加 wg+recover

**理由**：Serve 是长驻阻塞调用，panic 会导致进程崩溃。GracefulStop 后 Serve 必返回，wg.Wait 可安全等待。

### Decision 19 (v3 新增): nacosResolver.watch 加 wg+recover

**理由**：gRPC resolver 长驻 watch 循环，panic 会导致服务发现失效。Close 不等待存在资源竞争。

### Decision 20 (v3 新增): Lock.startWatchdog 加 recover

**理由**：watchdog 已有 wg，但无 recover。Extend panic 会导致 1) 进程崩溃 2) Release() 中 wg.Wait() 死锁（Done 未调用）。

### Decision 21 (v3 新增): common/kafka/consumer.Start 加 recover

**理由**：processMessage 已有 recover，但 Start 主循环无 recover。FetchMessage/CommitMessages panic 会导致进程崩溃。

### Decision 22 (v3 新增): gameEventKafkaConsumer 提升为 Container 字段

**理由**：v2 中 gameEventKafkaConsumer 是 app.go 内 Start() 的局部变量，Stop() 无法触达。改为 Container 字段后生命周期可控。

### Decision 23 (v3 新增): RoomEventConsumer.tryAcquire 改为 fail-closed

**理由**：与 GameEventConsumer.tryAcquire 风格统一，符合 project memory "Kafka consumer error handling must return error on Redis unavailability to trigger Kafka retry (fail-closed)"。

### Decision 24 (v3 新增): 长驻组件 wg 与 recover 缺一不可

**理由**：wg 缺失导致 Stop 不等待（资源泄漏）；recover 缺失导致 panic 进程崩溃。两者必须同时存在。

---

## 10. 附录

### Appendix A: 全量 goroutine 盘点（v3 修正：17 处应用层 + 12 处基础设施层）

**类别 A（应用层 fire-and-forget）**：17 处
- game_app_service.go：13 处（G1-G12，G6/G8 内嵌 recover）
- room_app_service.go：2 处（G13-G14）
- seat_app_service.go：2 处（G15-G16）
- 处置：Phase 3 迁移到 runner

**类别 B（应用层长驻）**：1 处
- robot_scheduler_service.go scanLoop：1 处
- 处置：Phase 6.9 补 wg+recover+per-scan ctx

**类别 C（基础设施层长驻，已有 wg 或需补 wg）**：12 处
- timeout_scheduler.go runChecker：1 处（已有 wg，Phase 6.14 补 recover）
- virtual_balance_sync.go run：1 处（已合规）
- gateway/connection/manager.go subscribeKickChannel：1 处（已有 wg，Phase 6.1 补 recover）
- gateway/broadcast/broadcast.go consumer.Start：1 处（已有 wg，Phase 6.2 补 recover）
- gateway/middleware/auth.go cleanupRoutine：1 处（Phase 6.3 补 wg+recover）
- gateway/server/server.go handleConnection+writeAndHeartbeatPump：2 处（Phase 6.6 补 wg+recover）
- common/broadcast/redis_pubsub_consumer.go consumeMessages：1 处（已有 wg，Phase 6.8 补 recover）
- common/discovery/discovery.go watch：1 处（Phase 6.11 补 wg+recover）
- common/lock/distributed_lock.go startWatchdog：1 处（已有 wg，Phase 6.12 补 recover）
- common/kafka/consumer.go Start：1 处（Phase 6.13 补 recover）
- settlement/scheduler/base.go run：1 处（Phase 6.7 补 wg+recover）
- 处置：保留循环结构，补 wg+recover+ctx

**类别 D（bootstrap 层）**：6 处
- game/bootstrap/app.go：2 处 consumer（Phase 2.1 补 wg+recover）
- game/bootstrap/config_listener.go：1 处（Nacos Phase 4.5 已处理）
- gateway/bootstrap/app.go：2 处 BroadcastSvc+Server（Phase 2.3 补 wg+recover）
- gateway/bootstrap/config_listener.go：1 处（Nacos Phase 4.5 已处理）
- 处置：补 wg+recover

**类别 E（gRPC server）**：1 处
- game/server/generic_service.go Serve：1 处（Phase 6.10 补 wg+recover）
- 处置：补 wg+recover

**类别 F（stats HTTP server）**：1 处
- cmd/stats/main.go ListenAndServe：1 处（Phase 7 抽到 bootstrap 时补 wg+recover）

**类别 G（nacos ListenConfig）**：2 处
- 已在 Nacos 重构 Phase 4.5 收敛

### Appendix B: 全量 context.Background() 盘点（v3 修正：51 处）

| 类别 | 数量 | 处置 |
|---|---|---|
| 1. 应用层异步任务内 | 17 | Phase 3 替换为 runner 派生 ctx |
| 2. 应用层同步方法内（GameBroadcaster + RobotBehaviorEngine） | 6 | Phase 3+4 接口改造 |
| 3. 应用层长驻组件（RobotSchedulerService.Start + scanRooms） | 2 | Phase 6.9 改 s.ctx 派生 |
| 4. 基础设施层启动（构造函数内创建 root ctx） | 8 | 保留或 Phase 6 改为接收 parent ctx |
| 5. 基础设施层运行时方法内 | 8 | Phase 6 替换为 m.ctx/s.ctx |
| 6. HTTP/WS handler 内 | 4 | Phase 6 替换为 request ctx |
| 7. 构造函数/init 阶段（NewClient Ping 等） | 6 | 保留 |
| **总计** | **51** | |

### Appendix C: TaskRunner 使用示例

```go
// 1. 构造（在 Application.NewApplicationWithConfig 中）
appCtx, cancel := context.WithCancel(context.Background())
taskRunner := async.NewTaskRunner(appCtx, 10*time.Second)

// 2. 注入到 service（在 Container.NewContainer 中）
c.GameAppService = application.NewGameAppService(..., c.TaskRunner)

// 3. 使用（在 service 方法中）
if err := s.taskRunner.Submit("publish_session_start", 5*time.Second, func(ctx context.Context) {
	if err := s.eventPublisher.PublishSessionStart(ctx, event); err != nil {
		logger.Warn("publish session start event failed", "error", err)
	}
}); err != nil {
	logger.Warn("submit publish_session_start task failed", "error", err)
}

// 4. 停止（在 Application.Stop 中）
a.taskRunner.Stop()
// ... stop consumers/schedulers ...
waitDone := make(chan struct{})
go func() { a.taskRunner.Wait(); close(waitDone) }()
select {
case <-waitDone:
case <-time.After(30 * time.Second):
	logger.Warn("task runner wait timeout, force shutdown")
}
// ... close resources ...
```

### Appendix D: 反模式示例（MUST NOT）

```go
// ❌ 1. 裸 go func
go func() {
	s.eventPublisher.PublishSessionStart(context.Background(), event)
}()

// ❌ 2. task 内用 context.Background()
s.taskRunner.Submit("bad", 5*time.Second, func(ctx context.Context) {
	s.eventPublisher.PublishSessionStart(context.Background(), event)  // 应该用 ctx
})

// ❌ 3. task 内再 go func
s.taskRunner.Submit("bad", 5*time.Second, func(ctx context.Context) {
	go func() {  // 应该再次 Submit
		s.eventPublisher.PublishSessionStart(ctx, event)
	}()
})

// ❌ 4. GameBroadcaster 内部用 context.Background()
func (b *GameBroadcaster) Broadcast(roomID string, ...) {
	b.broadcaster.Broadcast(context.Background(), ...)  // 应该透传 ctx
}

// ❌ 5. 长驻组件运行时用 context.Background()
func (m *Manager) GetPlayerRoom(userID string) {
	m.redis.Get(context.Background(), key)  // 应该用 m.ctx
}

// ❌ 6. HTTP handler 用 context.Background()
func (h *HealthChecker) CheckHealth(c *gin.Context) {
	ctx := context.Background()  // 应该用 c.Request.Context()
}

// ❌ 7. BaseScheduler 用 stopCh
type BaseScheduler struct {
	stopCh chan struct{}  // 应该用 ctx + cancel
}

// ❌ 8. v3: 长驻 goroutine 有 wg 无 recover
go func() {
	defer s.wg.Done()  // 缺 defer recover()，panic 会进程崩溃
	s.consumer.Start(s.ctx)
}()

// ❌ 9. v3: 长驻 goroutine 有 recover 无 wg
go func() {
	defer func() { /* recover */ }()
	s.scanLoop()  // 缺 wg.Add/Done，Stop 无法等待退出
}()

// ❌ 10. v3: Start 内部自创 ctx 不接收 parent
func (s *Service) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())  // 应该 Start(ctx)
}

// ❌ 11. v3: Stop 不等 wg
func (s *Service) Stop() {
	s.cancel()  // 缺 s.wg.Wait()，goroutine 还在跑就 return
}

// ❌ 12. v3: scanRooms 用 context.Background()
func (s *Service) scanRooms() {
	ctx := context.Background()  // 应该 context.WithTimeout(s.ctx, budget)
}
```

### Appendix E: 完整改造点清单（按文件，v3 扩展）

| 文件 | 改造点数 | Phase |
|---|---|---|
| `common/async/task_runner.go` | 新建 | 1 |
| `common/async/task_runner_test.go` | 新建 | 1 |
| `common/async/doc.go` | 新建 | 1 |
| `game/bootstrap/app.go` | 5（字段+Stop+Wait+Run+consumer wg+recover） | 2 |
| `game/bootstrap/container.go` | 4（字段+NewContainer+InitAppServices+StartSchedulers appCtx） | 2 |
| `gateway/bootstrap/app.go` | 3（字段+Stop+goroutine wg+recover） | 2 |
| `gateway/bootstrap/container.go` | 1（字段） | 2 |
| `game/application/game_app_service.go` | 13 goroutine + 16 Broadcast + 构造函数 | 3 |
| `game/application/room_app_service.go` | 2 goroutine + 11 Broadcast + 构造函数 | 3 |
| `game/application/seat_app_service.go` | 2 goroutine + 6 Broadcast + 构造函数 | 3 |
| `game/domain/repository.go` | 1 接口签名 | 3 |
| `game/infrastructure/broadcast/broadcaster.go` | 2 方法签名 + 2 context.Background() | 3 |
| `game/application/robot_player.go` | 1 接口签名 + 3 调用点 | 4 |
| `game/application/robot_behavior.go` | 2 方法签名 + 2 context.Background() + 1 调用点 | 4 |
| `settlement/service/deduct_service.go` | 1 并行批处理补 recover | 5 |
| `gateway/connection/manager.go` | 5 context.Background() + 1 recover | 6.1 |
| `gateway/broadcast/broadcast.go` | 1 context.Background() + 1 recover | 6.2 |
| `gateway/middleware/auth.go` | 4（OnConnect+recordFailedAttempt+wg+recover+context.Background()） | 6.3 |
| `gateway/router/router.go` | 1 Route 签名 + 1 context.Background() | 6.4 |
| `gateway/health/health.go` | 2 context.Background() | 6.5 |
| `gateway/server/server.go` | 3（wg+OnConnect传ctx+Route传ctx）+ 2 recover | 6.6 |
| `settlement/scheduler/base.go` | 4（ctx字段+wg字段+Start wg.Add+run wg.Done+recover+executeTask ctx+Stop wg.Wait） | 6.7 |
| `settlement/scheduler/credit_retry_scheduler.go` | 1 构造函数 | 6.7 |
| `settlement/scheduler/game_settle_retry_scheduler.go` | 1 构造函数 | 6.7 |
| `settlement/scheduler/game_settle_timeout_scheduler.go` | 1 构造函数 | 6.7 |
| `settlement/scheduler/refund_process_scheduler.go` | 1 构造函数 | 6.7 |
| `settlement/scheduler/settlement_check_scheduler.go` | 1 构造函数 | 6.7 |
| `common/broadcast/redis_pubsub_consumer.go` | 2 Start 方法 + consumeMessages recover | 6.8 |
| `game/application/robot_scheduler_service.go` | 5（Start ctx+wg+scanLoop wg.Done+recover+scanRooms ctx+Stop wg.Wait） | 6.9 |
| `game/server/generic_service.go` | 3（wg字段+Serve wg.Add+recover+Stop wg.Wait） | 6.10 |
| `common/discovery/discovery.go` | 3（wg字段+start 接 ctx+watch wg.Done+recover+Close wg.Wait） | 6.11 |
| `common/lock/distributed_lock.go` | 1 startWatchdog recover | 6.12 |
| `common/kafka/consumer.go` | 1 Start recover | 6.13 |
| `game/scheduler/timeout_scheduler.go` | 1 runChecker recover | 6.14 |
| `game/infrastructure/messaging/room_event_consumer.go` | 1 tryAcquire fail-closed | 8 |
| `stats/bootstrap/app.go` | 新建 + HTTP server wg+recover | 7 |
| `stats/bootstrap/container.go` | 新建 | 7 |
| `cmd/stats/main.go` | 1 精简 | 7 |

**总计**：新建 6 文件，修改 30 文件（v3 较 v2 增加 6 个修改文件），改造点 ~95 处。
