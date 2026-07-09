# Tasks

> 遵循"不改变业务逻辑"原则。每个 SubTask 完成后必须 `go build ./settlement/... ./game/...` + `go test ./settlement/... ./game/...` 通过。

## Phase 1：迁移 VirtualBalanceSyncScheduler

- [x] Task 1.1: 新增 settlement/scheduler/virtual_balance_sync.go
  - [x] SubTask 1.1.1: 在 `settlement/scheduler/virtual_balance_sync.go` 创建 `VirtualBalanceSyncScheduler`，结构与原 game/scheduler/virtual_balance_sync.go 一致，但 import 改为 settlement 内部路径
  - [x] SubTask 1.1.2: `NewVirtualBalanceSyncScheduler` 参数改为接收 `schedulerApp *application.SchedulerAppService` + `cfg commonconfig.SettlementSchedulerSubConfig` + `redis cRedis.RedisClient`
  - [x] SubTask 1.1.3: execute 方法调用 `s.schedulerApp.RunVirtualBalanceSync(ctx)`
  - [x] SubTask 1.1.4: LockTTL 改为从 `cfg.LockTTL` 读取（若 <=0 则用 `int(cfg.Interval.Seconds())+5` 兜底）
  - [x] SubTask 1.1.5: `go build ./settlement/...` 编译通过

- [x] Task 1.2: SchedulerAppService 新增 RunVirtualBalanceSync facade
  - [x] SubTask 1.2.1: `settlement/application/scheduler_app_service.go` struct 新增 `virtualBalanceSvc domain.VirtualBalanceService` 字段
  - [x] SubTask 1.2.2: `NewSchedulerAppService` 构造函数新增 `virtualBalanceSvc domain.VirtualBalanceService` 参数
  - [x] SubTask 1.2.3: 新增 `RunVirtualBalanceSync(ctx context.Context) error` 方法
  - [x] SubTask 1.2.4: 更新文件顶部注释"5 个 scheduler" → "6 个 scheduler"
  - [x] SubTask 1.2.5: `go build ./settlement/...` 编译通过

- [x] Task 1.3: 新增 VirtualBalanceSync 配置项
  - [x] SubTask 1.3.1: `common/config/settlement_scheduler.go` `SettlementSchedulerConfig` struct 新增 `VirtualBalanceSync SettlementSchedulerSubConfig` 字段
  - [x] SubTask 1.3.2: `SetSettlementSchedulerDefaults` 新增 VirtualBalanceSync 默认值：Interval=30s, LockTTL=35
  - [x] SubTask 1.3.3: 注释从"5 个 settlement schedulers" → "6 个 settlement schedulers"
  - [x] SubTask 1.3.4: `go build ./...` 编译通过

- [x] Task 1.4: 容器装配迁移
  - [x] SubTask 1.4.1: `game/bootstrap/container.go` `initSettlementSchedulers()` 中 `NewSchedulerAppService` 调用新增 `c.settlementVirtualBalance` 参数
  - [x] SubTask 1.4.2: `initSettlementSchedulers()` 末尾新增 `NewVirtualBalanceSyncScheduler` 注册
  - [x] SubTask 1.4.3: `initGameSchedulers()` 移除原 `VirtualBalanceSyncScheduler` 注册
  - [x] SubTask 1.4.4: `go build ./settlement/... ./game/...` 编译通过

- [x] Task 1.5: 删除旧文件
  - [x] SubTask 1.5.1: 删除 `game/scheduler/virtual_balance_sync.go`
  - [x] SubTask 1.5.2: `go build ./settlement/... ./game/...` 编译通过

## Phase 2：验证

- [x] Task 2.1: 全量验证
  - [x] SubTask 2.1.1: `go build ./settlement/... ./game/...` 编译通过
  - [x] SubTask 2.1.2: `go test ./settlement/... ./game/...` 全部测试通过
  - [x] SubTask 2.1.3: `gofmt -l settlement/ game/` 无输出
  - [x] SubTask 2.1.4: grep 确认 `game/scheduler/virtual_balance_sync.go` 不存在
  - [x] SubTask 2.1.5: grep 确认 `VirtualBalanceSyncScheduler` 仅在 settlement/scheduler/ 和 game/bootstrap/container.go 中出现

# Task Dependencies
- Task 1.2 依赖 Task 1.1
- Task 1.3 无依赖，可与 Task 1.1/1.2 并行
- Task 1.4 依赖 Task 1.1 + 1.2 + 1.3
- Task 1.5 依赖 Task 1.4
- Task 2.1 依赖 Task 1.5
