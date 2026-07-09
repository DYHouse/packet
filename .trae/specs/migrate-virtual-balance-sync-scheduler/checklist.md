# Checklist

## Phase 1：迁移 VirtualBalanceSyncScheduler

- [x] `settlement/scheduler/virtual_balance_sync.go` 文件已创建
- [x] `VirtualBalanceSyncScheduler` 的 Name 为 `virtual_balance_sync`
- [x] `VirtualBalanceSyncScheduler` 通过 `schedulerApp.RunVirtualBalanceSync(ctx)` 调用
- [x] LockKey 仍为 `rediskeys.KeySchedulerVirtualBalanceSyncLock`
- [x] LockTTL 默认值与原实现一致（`int(interval.Seconds())+5`）
- [x] `SchedulerAppService` 含 `virtualBalanceSvc` 字段
- [x] `NewSchedulerAppService` 接收 `virtualBalanceSvc` 参数
- [x] `RunVirtualBalanceSync` 方法调用 `s.virtualBalanceSvc.SyncToDB(ctx)`
- [x] `SettlementSchedulerConfig` 含 `VirtualBalanceSync` 子配置
- [x] `SetSettlementSchedulerDefaults` 设置 VirtualBalanceSync 默认值（Interval=30s, LockTTL=35）
- [x] `initSettlementSchedulers()` 注册 `VirtualBalanceSyncScheduler`
- [x] `initGameSchedulers()` 不再注册 `VirtualBalanceSyncScheduler`
- [x] `SchedulerAppService` 构造调用传入 `c.settlementVirtualBalance`
- [x] `game/scheduler/virtual_balance_sync.go` 文件已删除

## Phase 2：验证

- [x] `go build ./settlement/... ./game/...` 编译通过
- [x] `go test ./settlement/... ./game/...` 全部测试通过
- [x] `gofmt -l settlement/ game/` 无输出
- [x] grep 确认 `game/scheduler/virtual_balance_sync.go` 不存在
- [x] grep 确认 `VirtualBalanceSyncScheduler` 仅在 settlement/scheduler/ 和 game/bootstrap/container.go 中出现
- [x] 业务逻辑零变更（SyncToDB 行为不变，Interval/LockKey/LockTTL 默认值不变）
