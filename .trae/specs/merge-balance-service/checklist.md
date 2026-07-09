# Checklist

## Phase 1：合并 BalanceService

- [x] `BalanceService` 含 `CheckBalance` 方法（从 BalanceQueryService 迁移）
- [x] `BalanceService` 的 `CheckBalance` 行为与原 `BalanceQueryService.CheckBalance` 完全一致（含机器人虚拟通道）
- [x] `BalanceService` 的 `CheckBalanceForReady` 行为不变
- [x] `BalanceService` 的 `CheckUserBalance` 行为不变
- [x] `SettleAppService` 不再持有 `balanceQueryService` 字段
- [x] `SettleAppService.CheckBalance` 委派到 `balanceService.CheckBalance`
- [x] `SettleAppService.CheckBalanceForReady` 委派不变
- [x] `balance_query_service.go` 文件已删除
- [x] `doc.go` 不再描述 `BalanceQueryService`
- [x] `container.go` 不再持有 `BalanceQuerySvc` 字段
- [x] `app.go` 不再构造 `balanceQuerySvc`
- [x] `NewContainer` 参数不再含 `balanceQuerySvc`
- [x] `generic_service.go` 直接持有 `BalanceService` 的调用路径不变

## 全局验收

- [x] `go build ./settlement/... ./game/...` 编译通过
- [x] `go test ./settlement/... ./game/...` 全部测试通过
- [x] `gofmt -l settlement/ game/` 无输出
- [x] grep `BalanceQueryService` / `balanceQueryService` / `BalanceQuerySvc` / `balanceQuerySvc` 在整个 backend 中无残留
- [x] 业务逻辑零变更（CheckBalanceForReady / CheckUserBalance / CheckBalance 行为不变）
