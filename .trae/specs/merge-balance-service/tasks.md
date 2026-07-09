# Tasks

> 遵循"不改变业务逻辑"原则。每个 SubTask 完成后必须 `go build ./settlement/... ./game/...` + `go test ./settlement/... ./game/...` 通过。

## Phase 1：合并 BalanceService（低风险）

- [x] Task 1.1: BalanceService 增加 CheckBalance 方法
  - [x] SubTask 1.1.1: `settlement/service/balance_service.go` 将 `BalanceQueryService.CheckBalance` 方法实现迁移到 `BalanceService`（接收者改为 `*BalanceService`，字段引用保持不变，因两者共享 `platform` / `cfg` / `userIDConvert` / `robotChecker` / `virtualBalance` 5 个字段）
  - [x] SubTask 1.1.2: 更新 `BalanceService` 的 godoc 注释，增加 `CheckBalance` 方法说明
  - [x] SubTask 1.1.3: `go build ./settlement/...` 编译通过

- [x] Task 1.2: SettleAppService 移除 balanceQueryService 字段
  - [x] SubTask 1.2.1: `settlement/application/settle_app_service.go` 移除 `balanceQueryService` 字段
  - [x] SubTask 1.2.2: `NewSettleAppService` 构造函数移除 `balanceQueryService` 参数
  - [x] SubTask 1.2.3: `CheckBalance` 方法委派改为 `s.balanceService.CheckBalance(ctx, userID, requiredAmount)`
  - [x] SubTask 1.2.4: `go build ./settlement/...` 编译通过

- [x] Task 1.3: 删除 BalanceQueryService
  - [x] SubTask 1.3.1: 删除 `settlement/service/balance_query_service.go` 整个文件
  - [x] SubTask 1.3.2: 更新 `settlement/service/doc.go` 移除 `BalanceQueryService` 描述
  - [x] SubTask 1.3.3: `go build ./settlement/...` 编译通过

- [x] Task 1.4: 简化 DI 容器
  - [x] SubTask 1.4.1: `game/bootstrap/container.go` 移除 `BalanceQuerySvc` 字段
  - [x] SubTask 1.4.2: `NewContainer` 构造函数移除 `balanceQuerySvc` 参数
  - [x] SubTask 1.4.3: `game/bootstrap/app.go` 移除 `balanceQuerySvc := settlementService.NewBalanceQueryService(...)` 构造
  - [x] SubTask 1.4.4: `NewContainer` 调用处移除 `balanceQuerySvc` 参数
  - [x] SubTask 1.4.5: `go build ./settlement/... ./game/...` 编译通过

- [x] Task 1.5: 验证
  - [x] SubTask 1.5.1: `go build ./settlement/... ./game/...` 编译通过
  - [x] SubTask 1.5.2: `go test ./settlement/... ./game/...` 全部测试通过
  - [x] SubTask 1.5.3: `gofmt -l settlement/ game/` 无输出
  - [x] SubTask 1.5.4: grep 确认无 `BalanceQueryService` / `balanceQueryService` / `BalanceQuerySvc` / `balanceQuerySvc` 残留

# Task Dependencies
- Task 1.2 依赖 Task 1.1（需先有 CheckBalance 方法可委派）
- Task 1.3 依赖 Task 1.2（需先移除所有 BalanceQueryService 引用）
- Task 1.4 依赖 Task 1.3（需先删除 Service 定义）
- Task 1.5 依赖 Task 1.4
