# 定时任务（Scheduler）重构方案

> **版本**: v1
> **创建日期**: 2026-07-04
> **基于**: backend/ 全量代码审查（8 个调度器 + 4 个非正式定时器）
> **遵循**: CODING_STANDARD.md、GOROUTINE_REFACTOR_PLAN.md v3

---

## 目录

- [一、现状全量清单](#一现状全量清单)
- [二、问题分类汇总](#二问题分类汇总)
- [三、整体设计思路](#三整体设计思路)
- [四、文件结构](#四文件结构)
- [五、规约（SCH-1 ~ SCH-12）](#五规约sch-1--sch-12)
- [六、阶段划分与详细任务](#六阶段划分与详细任务)
- [七、关键修复细节](#七关键修复细节)
- [八、决策记录](#八决策记录)
- [九、风险与回滚](#九风险与回滚)
- [十、验证清单](#十验证清单)

---

## 一、现状全量清单

### 1.1 业务调度器（8 个）

| # | 调度器 | 文件 | 基类 | 分布式锁 | ctx 来源 | 配置化 |
|---|---|---|---|---|---|---|
| 1 | CreditRetryScheduler | settlement/scheduler/credit_retry_scheduler.go | BaseScheduler | ✅ | ❌ context.Background() | ❌ 硬编码 |
| 2 | SettlementCheckScheduler | settlement/scheduler/settlement_check_scheduler.go | BaseScheduler | ✅ | ❌ context.Background() | ❌ 硬编码 |
| 3 | RefundProcessScheduler | settlement/scheduler/refund_process_scheduler.go | BaseScheduler | ✅ | ❌ context.Background() | ❌ 硬编码 |
| 4 | GameSettleRetryScheduler | settlement/scheduler/game_settle_retry_scheduler.go | BaseScheduler | ✅ | ❌ context.Background() | ❌ 硬编码 |
| 5 | GameSettleTimeoutScheduler | settlement/scheduler/game_settle_timeout_scheduler.go | BaseScheduler | ✅ | ❌ context.Background() | ❌ 硬编码 |
| 6 | TimeoutScheduler | game/scheduler/timeout_scheduler.go | ❌ 独立 | ❌ 无锁（依赖 ZRem 原子性） | ❌ context.Background() | ⚠️ 部分 |
| 7 | VirtualBalanceSyncScheduler | game/scheduler/virtual_balance_sync.go | ❌ 独立 | ❌ 无锁 | ❌ context.Background() | ✅ |
| 8 | RobotSchedulerService | game/application/robot_scheduler_service.go | ❌ 独立 | ❌ 无全局锁 | ✅ appCtx | ✅ |

#### BaseScheduler 现状

**文件**: settlement/scheduler/base.go（102 行）

**核心机制**:
- `run()` 中先 `time.Sleep(InitialDelay)`，再 `time.NewTicker(Interval)` 循环
- `executeTask()` 中通过 `lock.WithRedisLock` 获取分布式 Redis 锁后执行 `task(ctx)`
- 每次执行使用 `context.WithTimeout(s.ctx, s.config.Interval)` 创建 per-task ctx
- `Stop()` 调 `cancel()` + 等 `wg.Wait()`，带 10s 超时兜底
- `run()` 内有 `recover()` 防 panic 退出

**SchedulerConfig 字段**（base.go:16-22）:
```go
type SchedulerConfig struct {
    Name         string
    Interval     time.Duration
    InitialDelay time.Duration
    LockKey      string
    LockTTL      int
}
```

**已知缺陷**:
- 构造时传 `nil` 作为 task，在 `Start()` 中才赋值 `s.base.task = s.execute`（nil task 风险）
- `executeTask` 丢弃 `WithRedisLock` 返回值（锁获取失败与 task 错误均不可观测）
- `time.Sleep(InitialDelay)` 阻塞，期间无法响应 `Stop()`

### 1.2 非正式定时器（4 个，不在 scheduler/ 目录）

| # | 名称 | 文件 | 用途 | 处理方式 |
|---|---|---|---|---|
| 9 | AuthMiddleware.cleanupRoutine | gateway/middleware/auth.go:140-175 | 每小时清理失败登录尝试 | 豁免（已有良好模式） |
| 10 | Server.writeAndHeartbeatPump | gateway/server/server.go:347-379 | per-connection WebSocket 心跳 | 豁免（per-conn 生命周期） |
| 11 | Lock.startWatchdog | common/lock/distributed_lock.go:115-141 | 分布式锁自动续期 | 豁免（锁内部机制） |
| 12 | nacosResolver.watch | common/discovery/discovery.go:65-85 | Nacos 服务发现轮询 | 豁免（resolver 内部机制） |

> 9-12 已有良好的 ctx+cancel+wg+recover 模式，本次不重构，仅在规约中明确豁免。

### 1.3 装配与生命周期

- **唯一注册中心**: `game/bootstrap/container.go` 的 `Container` 持有 8 个 scheduler 字段
- **启动入口**: `Container.StartSchedulers(appCtx)`（container.go:341-373），按顺序 Start
- **停止入口**: `Container.Stop()`（container.go:375-399），串行调用 8 个 `.Stop()`
- **shutdown 顺序**: `taskRunner.Stop()` → `cancel(appCtx)` → `Container.Stop()` → 等 consumer → 等 taskRunner → Kafka → gRPC → Nacos → Redis
- **gateway/stats 无 scheduler**

### 1.4 配置现状

| 调度器 | 配置来源 | 默认值 |
|---|---|---|
| TimeoutScheduler | game.yaml `timeout:` 段 | seat=30s, ready=3s, grab=20s, send=30s, replace=30s |
| TimeoutScheduler.Robot | ❌ 未配置 | 5s（硬编码） |
| TimeoutScheduler.CheckInterval | ❌ 未配置 | 500ms/1s（硬编码 defaultCheckIntervals） |
| RobotSchedulerService | game.yaml `robot.scheduler:` 段 | ScanInterval=5s, MinRealPlayers=2, MaxRobotsPerRoom=3 |
| VirtualBalanceSyncScheduler | game.yaml `robot.account.sync_interval` | 30s |
| 5 个 settlement scheduler | ❌ 全部硬编码 | 见下表 |

**Settlement scheduler 硬编码值**:

| 调度器 | Interval | InitialDelay | LockTTL | LockKey |
|---|---|---|---|---|
| CreditRetryScheduler | 30s | 10s | 60s | rediskeys.KeySchedulerCreditRetryLock |
| SettlementCheckScheduler | 5min | 1min | 300s | rediskeys.KeySchedulerSettlementCheckLock |
| RefundProcessScheduler | 1min | 30s | 120s | rediskeys.KeySchedulerRefundProcessLock |
| GameSettleRetryScheduler | 30s | 15s | 60s | rediskeys.KeySchedulerGameSettleRetryLock |
| GameSettleTimeoutScheduler | 5min | 1min | 300s | rediskeys.KeySchedulerGameSettleTimeoutLock |

### 1.5 死代码

- `RateLimiter.CleanupInterval`（common/config/types.go）配置字段存在，但 `RateLimiter.Stop()`（ratelimit.go:65-66）是空函数，无任何 goroutine 使用此 interval

---

## 二、问题分类汇总

### 2.1 P0 — 正确性/安全（6 项）

| ID | 文件:行号 | 问题 | 影响 |
|---|---|---|---|
| P0-1 | game/bootstrap/container.go:319-323 | 5 个 settlement scheduler 构造时传 `context.Background()`（代码 TODO 承认） | appCtx cancel 不会取消这些 scheduler，只能靠显式 Stop() |
| P0-2 | game/scheduler/timeout_scheduler.go:63 | `context.WithCancel(context.Background())` | 同上 |
| P0-3 | game/scheduler/virtual_balance_sync.go:29 | `context.WithCancel(context.Background())` | 同上 |
| P0-4 | settlement/scheduler/base.go:85-87 | `executeTask` 丢弃 `WithRedisLock` 返回值 | 锁获取失败与 task 错误均不可观测 |
| P0-5 | settlement/scheduler/base.go:62 | `time.Sleep(InitialDelay)` 阻塞 | 期间无法响应 Stop()，最长阻塞 InitialDelay |
| P0-6 | settlement/scheduler/settlement_check_scheduler.go:37-41 | `execute` 不检查两个 service 调用返回值 | 完全吞错误，运维不可见 |

### 2.2 P1 — 架构/一致性（8 项）

| ID | 文件:行号 | 问题 |
|---|---|---|
| P1-1 | 全局 | 无统一 `Scheduler` 接口（settlement 用 BaseScheduler，game 三个各自实现） |
| P1-2 | game/scheduler/virtual_balance_sync.go | 不使用 BaseScheduler，重复 ctx+wg+ticker+recover+Stop 模式 |
| P1-3 | game/scheduler/timeout_scheduler.go:260-269 | handler 用 `s.ctx` 而非派生 per-handler ctx（无隔离，Stop 时无法独立取消） |
| P1-4 | settlement/scheduler/base.go:16 vs common/config/types.go:144 | 两个同名 `SchedulerConfig` 结构体（语义不同，易混淆） |
| P1-5 | settlement/scheduler/*.go | 5 个调度器的 Interval/InitialDelay/LockTTL 全部硬编码 |
| P1-6 | game/scheduler/timeout_scheduler.go:41-48 | `defaultCheckIntervals` 硬编码（500ms/1s） |
| P1-7 | settlement/scheduler/*.go | `TaskFunc` 在 `Start()` 中赋值而非构造函数（nil task 风险） |
| P1-8 | game/bootstrap/container.go:375-399 | `Stop()` 串行调用 8 个调度器，每个 10s 超时，最坏 80s |

### 2.3 P2 — 可观测性/增强（4 项）

| ID | 文件 | 问题 |
|---|---|---|
| P2-1 | 全局 | 无 metrics（执行次数、耗时、错误数、锁竞争失败数、panic 数） |
| P2-2 | 全局 | 无调度器健康检查（无法 HTTP 探测运行状态、最近执行时间） |
| P2-3 | 全局 | 无 panic 计数聚合（所有 recover 只记日志） |
| P2-4 | gateway/middleware/auth.go 等 | 4 处独立实现指数退避+jitter，未抽象公共 helper |

---

## 三、整体设计思路

### 3.1 核心原则

1. **统一框架**: 建立 `common/scheduler/` 公共包，提供 `Scheduler` 接口 + `BaseScheduler` 基类 + `SchedulerRegistry` 注册中心
2. **单一真相源**: `BaseScheduler` 只在 `common/scheduler/base.go` 定义一份，settlement/game 共享
3. **Context 链路完整**: 所有调度器 `Start(ctx)` 接收 appCtx，禁止 `context.Background()`
4. **错误可观测**: `WithRedisLock` 返回值必须检查；task error 必须记录；service 调用返回值必须检查
5. **配置外部化**: 所有间隔/超时/初始延迟通过 yaml 配置，禁止硬编码
6. **并行关停**: `SchedulerRegistry.Stop()` 并行停止所有调度器，带全局预算（默认 30s）

### 3.2 架构分层

```
Application (appCtx)
    │
    ▼
SchedulerRegistry ──── 注册 ──── [Scheduler1, Scheduler2, ..., SchedulerN]
    │                                    │
    │ StartAll(appCtx)                   │ 各自 Start(ctx)
    │ StopAll(timeout=30s)               │ 各自 Stop()
    │                                    │
    ▼                                    ▼
BaseScheduler (common/scheduler/)    独立实现（TimeoutScheduler）
    │
    ├── WithRedisLock（分布式互斥）
    ├── per-task ctx（WithTimeout）
    ├── InitialDelay（select 实现，可取消）
    ├── panic recovery + metrics
    └── task error 记录
```

### 3.3 调度器分类处理

| 类别 | 调度器 | 处理方式 | 理由 |
|---|---|---|---|
| **A: 改用 BaseScheduler** | 5 个 settlement + VirtualBalanceSync | 迁移到 `common/scheduler.BaseScheduler`，消除重复实现 | 单 ticker + 分布式锁模式适合周期性任务 |
| **B: 实现接口保持独立** | TimeoutScheduler | 实现 `Scheduler` 接口，保持 ZSET 逻辑 | 多 handler + 精确到期回调不适合 BaseScheduler 的单 ticker 模式 |
| **C: 实现接口保持独立** | RobotSchedulerService | 实现 `Scheduler` 接口，保持 scanLoop 逻辑 | 复杂扫描 + 业务锁不适合 BaseScheduler |

---

## 四、文件结构

### 4.1 新建文件

```
backend/common/scheduler/
├── scheduler.go         # Scheduler 接口（Name, Start(ctx), Stop）
├── base.go              # BaseScheduler（从 settlement/scheduler/base.go 迁移 + 修复 P0-4/P0-5/P1-7）
├── registry.go          # SchedulerRegistry（注册、StartAll、StopAll 并行+全局预算）
└── metrics.go           # Metrics 收集（执行次数、耗时、错误数、panic 数）
```

### 4.2 删除文件

```
backend/settlement/scheduler/base.go   # 迁移到 common/scheduler/base.go
```

### 4.3 修改文件

| 文件 | 修改内容 |
|---|---|
| settlement/scheduler/credit_retry_scheduler.go | import common/scheduler；构造不传 ctx；Start(ctx) 接收 appCtx |
| settlement/scheduler/settlement_check_scheduler.go | 同上 + 检查 service 返回值（P0-6） |
| settlement/scheduler/refund_process_scheduler.go | import common/scheduler；构造不传 ctx；Start(ctx) |
| settlement/scheduler/game_settle_retry_scheduler.go | 同上 + 检查 UpdateGameSettleStatusBySession |
| settlement/scheduler/game_settle_timeout_scheduler.go | import common/scheduler；构造不传 ctx；Start(ctx) |
| game/scheduler/timeout_scheduler.go | 实现 Scheduler 接口；Start(ctx) 接收 appCtx；handler 派生 per-handler ctx（P1-3） |
| game/scheduler/virtual_balance_sync.go | 改用 BaseScheduler（P1-2）；接收 appCtx（P0-3） |
| game/application/robot_scheduler_service.go | 实现 Scheduler 接口（Name() 方法） |
| game/bootstrap/container.go | 用 SchedulerRegistry 替代 8 个手动字段（P1-1, P1-8） |
| game/bootstrap/app.go | StartSchedulers(appCtx) 传 registry |
| common/config/types.go | 新增 SettlementSchedulerConfig；重命名 SchedulerConfig→RobotSchedulerConfig（P1-4）；删除 RateLimiter.CleanupInterval |
| settlement/service/settlement_check_service.go | CheckFirstRoundDeductFailure / CheckDeductedButNotSettled 方法返回 error（P0-6 前置） |
| config/game.yaml | 新增 settlement_scheduler 配置段；新增 timeout.check_interval |
| CODING_STANDARD.md | 新增 §17 调度器规约 |

---

## 五、规约（SCH-1 ~ SCH-12）

| 规约 ID | 内容 | 级别 |
|---|---|---|
| SCH-1 | 调度器 MUST 实现 `common/scheduler.Scheduler` 接口（`Name() string`, `Start(ctx context.Context) error`, `Stop()`） | MUST |
| SCH-2 | 调度器 MUST 注册到 `SchedulerRegistry`，禁止散落在 Container 中的 ad-hoc 字段 | MUST |
| SCH-3 | `Start(ctx)` MUST 接收 appCtx 作为父 ctx，禁止 `context.Background()` | MUST |
| SCH-4 | `InitialDelay` MUST 用 `select { case <-ctx.Done(): return; case <-time.After(d): }` 实现，禁止 `time.Sleep` | MUST |
| SCH-5 | `WithRedisLock` 返回值 MUST 被检查并记录（禁止丢弃） | MUST |
| SCH-6 | task 返回的 error MUST 被记录；service 调用返回值 MUST 被检查 | MUST |
| SCH-7 | 调度器间隔/超时/InitialDelay MUST 通过配置文件设置，禁止硬编码 | MUST |
| SCH-8 | `SchedulerRegistry.Stop()` MUST 并行停止所有调度器，带全局预算（默认 30s） | MUST |
| SCH-9 | 调度器 SHOULD 收集 metrics（执行次数、耗时、错误数、panic 数） | SHOULD |
| SCH-10 | `BaseScheduler` 定义在 `common/scheduler/base.go`，跨服务共享；`settlement/scheduler/base.go` MUST 删除 | MUST |
| SCH-11 | `TaskFunc` MUST 在构造函数中传入，禁止在 `Start()` 中赋值（避免 nil task 风险） | MUST |
| SCH-12 | ZSET-based 调度器（TimeoutScheduler）依赖 `ZRem` 原子性去重，可豁免分布式锁要求；但 handler MUST 派生 per-handler ctx | MUST |

---

## 六、阶段划分与详细任务

### Phase 1: 新建 common/scheduler/ 框架（4 个任务）

| 任务 | 内容 | 修复问题 |
|---|---|---|
| 1.1 | 新建 `common/scheduler/scheduler.go`：定义 `Scheduler` 接口（`Name() string`, `Start(ctx context.Context) error`, `Stop()`） | P1-1 |
| 1.2 | 迁移 `settlement/scheduler/base.go` → `common/scheduler/base.go`；修复 P0-4（检查 WithRedisLock 返回值）+ P0-5（InitialDelay 改 select）+ P1-7（TaskFunc 构造时传入）+ P1-11（metrics 集成） | P0-4, P0-5, P1-7 |
| 1.3 | 新建 `common/scheduler/registry.go`：`SchedulerRegistry`（Register、StartAll(appCtx)、StopAll 并行+全局预算 30s） | P1-1, P1-8 |
| 1.4 | 新建 `common/scheduler/metrics.go`：执行次数、耗时、错误数、panic 数收集（基于 logger，无 prometheus 依赖） | P2-1, P2-3 |

**验证**: `go build ./common/scheduler/...` 通过

### Phase 2: 修复 P0 正确性问题（6 个任务）

| 任务 | 内容 | 修复问题 |
|---|---|---|
| 2.1 | 修改 5 个 settlement scheduler：构造不传 ctx，`Start(ctx)` 接收 appCtx 传给 base | P0-1 |
| 2.2 | 修改 TimeoutScheduler：`Start(ctx)` 接收 appCtx 作为父 ctx | P0-2 |
| 2.3 | 修改 VirtualBalanceSyncScheduler：改用 BaseScheduler，接收 appCtx | P0-3, P1-2 |
| 2.4 | BaseScheduler.executeTask 检查并记录 WithRedisLock 返回值（Phase 1 迁移时已修复，此处仅验证） | P0-4 |
| 2.5 | BaseScheduler.run InitialDelay 改 select 实现（Phase 1 迁移时已修复，此处仅验证） | P0-5 |
| 2.6 | 修改 SettlementCheckScheduler.execute：检查 service 返回值；修改 SettlementCheckService 方法签名返回 error | P0-6 |

**验证**: `go build ./...` 通过；grep `context.Background()` 在 scheduler 上下文为 0

### Phase 3: 统一 game/scheduler（3 个任务）

| 任务 | 内容 | 修复问题 |
|---|---|---|
| 3.1 | TimeoutScheduler 实现 Scheduler 接口（Name() 方法） | P1-1 |
| 3.2 | TimeoutScheduler handler 派生 per-handler ctx（带超时，默认 30s） | P1-3 |
| 3.3 | RobotSchedulerService 实现 Scheduler 接口（Name() 方法） | P1-1 |

**验证**: `go build ./game/...` 通过

### Phase 4: 配置外部化（3 个任务）

| 任务 | 内容 | 修复问题 |
|---|---|---|
| 4.1 | 新增 `SettlementSchedulerConfig` 到 common/config/types.go（5 个调度器的 interval/initial_delay/lock_ttl） | P1-5 |
| 4.2 | 5 个 settlement scheduler 从配置读取间隔 | P1-5 |
| 4.3 | TimeoutScheduler CheckInterval 与 Robot duration 可配；重命名 `SchedulerConfig`→`RobotSchedulerConfig`（P1-4） | P1-4, P1-6 |

**验证**: grep 硬编码间隔为 0；config/game.yaml 有新配置段

### Phase 5: 统一注册与关停（2 个任务）

| 任务 | 内容 | 修复问题 |
|---|---|---|
| 5.1 | game/bootstrap/container.go 用 SchedulerRegistry 替代 8 个手动字段 | P1-1, P1-8 |
| 5.2 | game/bootstrap/app.go 传 appCtx 给 registry.StartAll | P0-1（彻底修复） |

**验证**: `go build ./...` 通过；Container 无 8 个 scheduler 字段

### Phase 6: 删除死代码（1 个任务）

| 任务 | 内容 | 修复问题 |
|---|---|---|
| 6.1 | 删除 `RateLimiter.CleanupInterval` 配置字段与默认值 | 死代码 |

**验证**: grep `CleanupInterval` 为 0

### Phase 7: 更新规约 + 全局验证（2 个任务）

| 任务 | 内容 |
|---|---|
| 7.1 | CODING_STANDARD.md 新增 §17 调度器规约（SCH-1~SCH-12） |
| 7.2 | 全局验证：go build、go vet、gofmt、grep 检查 |

---

## 七、关键修复细节

### 7.1 P0-1 修复：settlement scheduler ctx 断链

**现状**（container.go:319-323）:
```go
// TODO Phase 6.7: pass appCtx to settlement scheduler constructors
c.CreditRetryScheduler = settlementScheduler.NewCreditRetryScheduler(ctx, ...)
// ctx 是 context.Background()
```

**修复后**:
```go
// 构造时不传 ctx
c.CreditRetryScheduler = settlementScheduler.NewCreditRetryScheduler(...)
// 注册到 registry
c.registry.Register(c.CreditRetryScheduler)
// Start 时传 appCtx
c.registry.StartAll(appCtx)
```

### 7.2 P0-4 修复：WithRedisLock 返回值丢弃

**现状**（base.go:85-87）:
```go
lock.WithRedisLock(ctx, s.redis, s.config.LockKey, s.config.LockTTL, func() error {
    return s.task(ctx)
})
// 返回值被丢弃
```

**修复后**:
```go
err := lock.WithRedisLock(ctx, s.redis, s.config.LockKey, s.config.LockTTL, func() error {
    return s.task(ctx)
})
if err != nil {
    logger.Warn("scheduler task failed or lock acquire failed",
        "name", s.config.Name, "lock_key", s.config.LockKey, "error", err)
    s.metrics.RecordError(s.config.Name)
}
```

### 7.3 P0-5 修复：InitialDelay 阻塞 Stop

**现状**（base.go:62）:
```go
if s.config.InitialDelay > 0 {
    time.Sleep(s.config.InitialDelay)  // 期间无法响应 Stop()
}
```

**修复后**:
```go
if s.config.InitialDelay > 0 {
    select {
    case <-s.ctx.Done():
        return
    case <-time.After(s.config.InitialDelay):
    }
}
```

### 7.4 P0-6 修复：SettlementCheckScheduler 吞错误

**现状**（settlement_check_scheduler.go:37-41）:
```go
s.settlementCheck.CheckFirstRoundDeductFailure(ctx)
s.settlementCheck.CheckDeductedButNotSettled(ctx, time.Now().Add(-5*time.Minute))
return nil
```

**修复后**（需 service 层方法返回 error）:
```go
if err := s.settlementCheck.CheckFirstRoundDeductFailure(ctx); err != nil {
    logger.Error("check first round deduct failure failed", "error", err)
    return err
}
if err := s.settlementCheck.CheckDeductedButNotSettled(ctx, time.Now().Add(-5*time.Minute)); err != nil {
    logger.Error("check deducted but not settled failed", "error", err)
    return err
}
return nil
```

> **前置改动**: `SettlementCheckService` 的 `CheckFirstRoundDeductFailure` 与 `CheckDeductedButNotSettled` 方法签名需改为返回 error。

### 7.5 P1-1 修复：统一 Scheduler 接口

**新建**（common/scheduler/scheduler.go）:
```go
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
```

### 7.6 P1-2 修复：VirtualBalanceSyncScheduler 改用 BaseScheduler

**现状**: 独立实现 ctx+wg+ticker+recover+Stop，per-task 超时硬编码 10s。

**修复后**:
```go
type VirtualBalanceSyncScheduler struct {
    base           *scheduler.BaseScheduler
    virtualBalance *redis.VirtualBalanceService
}

func NewVirtualBalanceSyncScheduler(virtualBalance *redis.VirtualBalanceService, cfg scheduler.SchedulerConfig, redis *cRedis.Client) *VirtualBalanceSyncScheduler {
    return &VirtualBalanceSyncScheduler{
        base: scheduler.NewBaseScheduler(cfg, func(ctx context.Context) error {
            return virtualBalance.SyncToDB(ctx)
        }, redis),
        virtualBalance: virtualBalance,
    }
}

func (s *VirtualBalanceSyncScheduler) Name() string { return s.base.Name() }
func (s *VirtualBalanceSyncScheduler) Start(ctx context.Context) error { return s.base.Start(ctx) }
func (s *VirtualBalanceSyncScheduler) Stop() { s.base.Stop() }
```

> **行为变更**: 多实例部署时会加分布式锁互斥（之前无锁并发）。SyncToDB 依赖 SPOP 原子性，加锁后单实例执行，不影响正确性，减少空跑开销。

### 7.7 P1-3 修复：TimeoutScheduler handler 派生 per-handler ctx

**现状**（timeout_scheduler.go:260-269）:
```go
s.handlerWg.Add(1)
go func(handler TimeoutHandler, roomID, data string) {
    defer s.handlerWg.Done()
    defer func() { if r := recover(); ... }()
    handler(s.ctx, roomID, data)  // 用 s.ctx，无超时
}(handler, roomID, data)
```

**修复后**:
```go
s.handlerWg.Add(1)
go func(handler TimeoutHandler, roomID, data string) {
    defer s.handlerWg.Done()
    defer func() {
        if r := recover(); r != nil {
            logger.Error("timeout handler panic", "type", timeoutType, "room_id", roomID, "panic", r)
            s.metrics.RecordPanic(string(timeoutType))
        }
    }()
    // 派生 per-handler ctx，带 30s 超时
    ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
    defer cancel()
    handler(ctx, roomID, data)
}(handler, roomID, data)
```

### 7.8 P1-7 修复：TaskFunc 在构造函数中传入

**现状**: `NewBaseScheduler(ctx, config, nil, redis)` 传 nil，子类 `Start()` 中赋值 `s.base.task = s.execute`。

**修复后**: `NewBaseScheduler(config, task, redis)` 在构造时传入 task，`Start(ctx)` 只负责启动。

### 7.9 P1-8 修复：串行 Stop 最坏 80s

**现状**（container.go:375-399）: 串行调用 8 个 `.Stop()`，每个 10s 超时。

**修复后**（registry.go）:
```go
func (r *Registry) StopAll(timeout time.Duration) {
    var wg sync.WaitGroup
    for _, s := range r.schedulers {
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
    case <-time.After(timeout):  // 全局预算 30s
        logger.Warn("scheduler registry stop timeout", "timeout", timeout)
    }
}
```

---

## 八、决策记录

| # | 决策 | 理由 | 影响 |
|---|---|---|---|
| D1 | VirtualBalanceSyncScheduler 改用 BaseScheduler | 消除重复实现；多实例加锁互斥不影响正确性（SPOP 原子性） | 多实例从并发空跑改为单实例执行，减少开销 |
| D2 | TimeoutScheduler 保持独立实现 | ZSET + 多 handler + 精确到期回调不适合 BaseScheduler 的单 ticker 模式 | 仅实现接口 + 修复 per-handler ctx |
| D3 | RobotSchedulerService 保持独立实现 | 复杂扫描 + 业务锁不适合 BaseScheduler | 仅实现接口 |
| D4 | SettlementCheckService 方法签名改为返回 error | P0-6 修复的必要前置改动 | 接口变更，调用方需适配 |
| D5 | SchedulerRegistry 替代 Container 手动字段 | 统一注册/启动/停止，并行关停 | Container 结构变更 |
| D6 | 配置外部化范围：settlement 5 个 + TimeoutScheduler CheckInterval | 消除硬编码，支持运维调整 | 新增 yaml 配置段 |
| D7 | Metrics 基于 logger 而非 prometheus | 当前无 prometheus 依赖，避免引入新依赖 | 可观测性有限但满足基本需求 |
| D8 | 全局关停预算 30s | 8 个调度器并行 Stop，每个内部仍有 10s 超时 | 最坏 30s（并行）而非 80s（串行） |

---

## 九、风险与回滚

### 9.1 风险

| 风险 | 级别 | 缓解措施 |
|---|---|---|
| BaseScheduler 迁移引入编译错误 | 中 | Phase 1 完成后立即 `go build ./...` 验证 |
| SettlementCheckService 签名变更影响调用方 | 中 | grep 所有调用点，逐一适配 |
| VirtualBalanceSyncScheduler 加锁后行为变化 | 低 | SyncToDB 依赖 SPOP 原子性，加锁不影响正确性 |
| SchedulerRegistry 并行 Stop 竞态 | 低 | 每个 Stop 内部有 mu 锁保护，幂等 |
| 配置缺失导致调度器不启动 | 中 | SetDefaults 函数提供默认值，Load 时校验 |

### 9.2 回滚

每个 Phase 独立提交，可单独回滚：
- Phase 1-2 失败：回滚 common/scheduler/ 新建文件 + settlement/scheduler/ 改动
- Phase 3 失败：回滚 game/scheduler/ 改动
- Phase 4 失败：回滚 config 改动
- Phase 5 失败：回滚 bootstrap 改动

---

## 十、验证清单

### 10.1 构建与静态检查

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `gofmt -l` 无输出
- [ ] `go test ./common/scheduler/...` 通过（如有测试）

### 10.2 Grep 检查

- [ ] `context.Background()` 不出现在 scheduler 相关文件（common/scheduler/、settlement/scheduler/、game/scheduler/、game/application/robot_scheduler_service.go）
- [ ] `time.Sleep` 不出现在 scheduler InitialDelay 上下文
- [ ] `WithRedisLock(` 返回值不被丢弃（无裸调用）
- [ ] `settlement/scheduler/base.go` 已删除
- [ ] `"scheduler:.*:lock"` 硬编码字符串为 0（用 rediskeys 常量）
- [ ] settlement scheduler 间隔无硬编码（从配置读取）
- [ ] `CleanupInterval` 在 RateLimiter 上下文为 0
- [ ] 两个同名 `SchedulerConfig` 已重命名（一个为 `BaseSchedulerConfig` 或删除，一个为 `RobotSchedulerConfig`）

### 10.3 行为验证

- [ ] 8 个调度器均实现 `Scheduler` 接口
- [ ] 8 个调度器均注册到 `SchedulerRegistry`
- [ ] `Start(ctx)` 接收 appCtx
- [ ] `InitialDelay` 用 select 实现
- [ ] `WithRedisLock` 返回值被检查
- [ ] `SettlementCheckScheduler.execute` 检查 service 返回值
- [ ] `TimeoutScheduler` handler 派生 per-handler ctx
- [ ] `SchedulerRegistry.StopAll` 并行停止
- [ ] CODING_STANDARD.md 包含 §17 调度器规约
