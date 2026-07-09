# 合并 BalanceService 与 BalanceQueryService Spec

## Why

当前 settlement/service 存在两个职责高度重叠的 Service：

- `BalanceService`（balance_service.go）：业务校验语义，含 `CheckBalanceForReady` / `CheckUserBalance`
- `BalanceQueryService`（balance_query_service.go）：通用只读查询，含 `CheckBalance` / `GetUserBalance` / `GetBillByTraceID` / `GetBillsByUserID` / `GetBillsByRoundID` / `GetRoundSettlement`

### 现状问题（基于代码调研）

1. **字段重叠严重**：两者共享 5 个相同依赖（`platform` / `cfg` / `userIDConvert` / `robotChecker` / `virtualBalance`），构造函数参数高度重复
2. **方法语义重叠**：
   - `BalanceService.CheckUserBalance`（仅真实玩家）vs `BalanceQueryService.GetUserBalance`（含机器人虚拟通道）—— 行为差异容易混淆
   - `BalanceService.CheckBalanceForReady`（含 FeeCalculator）vs `BalanceQueryService.CheckBalance`（不含 FeeCalculator）—— 调用方需理解差异
3. **BalanceQueryService 大量死代码**：6 个方法中 5 个（`GetUserBalance` / `GetBillByTraceID` / `GetBillsByUserID` / `GetBillsByRoundID` / `GetRoundSettlement`）**无任何外部调用方**，属于 P0-10 拆分时预留但从未消费的方法
4. **DI 容器冗余**：`container.go` 同时持有 `BalanceQuerySvc` 和 `BalanceService` 两个字段，增加装配复杂度

### 调用方分布

| Service | 被调用方法 | 外部调用点 |
|---------|-----------|-----------|
| BalanceService | `CheckBalanceForReady` | settle_app_service.go（facade）→ room_app_service.go (2 处) / seat_app_service.go (1 处) |
| BalanceService | `CheckUserBalance` | generic_service.go:511（gRPC handleGetUserBalance，**直接持有，不经 facade**） |
| BalanceQueryService | `CheckBalance` | settle_app_service.go（facade）→ room_app_service.go:504 (1 处) |
| BalanceQueryService | 其余 5 个方法 | **无外部调用方（死代码）** |

## What Changes

- **合并** `BalanceService` 与 `BalanceQueryService` 为单一 `BalanceService`
- **删除** `BalanceQueryService` 及其死代码方法（`GetUserBalance` / `GetBillByTraceID` / `GetBillsByUserID` / `GetBillsByRoundID` / `GetRoundSettlement`）
- **统一** 余额查询语义：合并后 `CheckUserBalance` 保持仅真实玩家语义（被 gRPC 直接调用），`CheckBalance` 保持含机器人虚拟通道语义
- **简化** DI 容器：`container.go` 仅保留 `BalanceService` 一个字段
- **保持** 所有外部调用方接口不变（facade 方法签名不变）

### 不改变的内容（核心红线）

- `CheckBalanceForReady` 行为不变（含 FeeCalculator + 机器人虚拟通道 + 真实玩家平台余额）
- `CheckUserBalance` 行为不变（仅真实玩家平台余额，被 gRPC 直接调用）
- `CheckBalance` 行为不变（含机器人虚拟通道 + 真实玩家平台余额）
- 所有 facade 方法签名不变（`SettleAppService.CheckBalanceForReady` / `SettleAppService.CheckBalance`）
- `generic_service.go` 直接持有 `BalanceService` 的调用路径不变

## Impact

- Affected code:
  - `settlement/service/balance_service.go`（合并目标，保留）
  - `settlement/service/balance_query_service.go`（**删除**）
  - `settlement/service/doc.go`（更新注释）
  - `settlement/application/settle_app_service.go`（移除 `balanceQueryService` 字段，`CheckBalance` 改为委派 `balanceService`）
  - `game/bootstrap/container.go`（移除 `BalanceQuerySvc` 字段）
  - `game/bootstrap/app.go`（移除 `balanceQuerySvc` 构造，`NewContainer` 参数调整）

## ADDED Requirements

### Requirement: 合并后的 BalanceService

合并后的 `BalanceService` SHALL 提供以下方法：

#### Scenario: CheckBalanceForReady 开局余额校验
- **WHEN** 调用 `CheckBalanceForReady(ctx, req)`
- **THEN** 使用 `FeeCalculator` 计算所需费用，透明处理机器人虚拟通道，返回 `BalanceCheckResult`

#### Scenario: CheckUserBalance 真实玩家余额查询
- **WHEN** 调用 `CheckUserBalance(ctx, userID)`
- **THEN** 仅查询真实玩家平台余额（不处理机器人虚拟通道），被 gRPC `handleGetUserBalance` 直接调用

#### Scenario: CheckBalance 通用余额校验
- **WHEN** 调用 `CheckBalance(ctx, userID, requiredAmount)`
- **THEN** 透明处理机器人虚拟通道，校验余额是否满足所需金额，返回 `(balance, sufficient, error)`

### Requirement: 删除死代码

合并后 SHALL 删除以下无外部调用方的方法：

- `GetUserBalance`（BalanceQueryService 方法，无调用方）
- `GetBillByTraceID`（BalanceQueryService 方法，无调用方）
- `GetBillsByUserID`（BalanceQueryService 方法，无调用方）
- `GetBillsByRoundID`（BalanceQueryService 方法，无调用方）
- `GetRoundSettlement`（BalanceQueryService 方法，无调用方）

#### Scenario: 死代码删除后编译通过
- **WHEN** 删除上述 5 个方法
- **THEN** `go build ./settlement/... ./game/...` 编译通过（因无外部调用方）

## MODIFIED Requirements

### Requirement: SettleAppService facade

`SettleAppService` SHALL 移除 `balanceQueryService` 字段，`CheckBalance` 方法改为委派 `balanceService`：

#### Scenario: CheckBalance facade 委派
- **WHEN** 调用 `SettleAppService.CheckBalance(ctx, userID, requiredAmount)`
- **THEN** 委派到 `balanceService.CheckBalance(ctx, userID, requiredAmount)`，行为不变

### Requirement: DI 容器简化

`Container` SHALL 移除 `BalanceQuerySvc` 字段，仅保留 `BalanceService`：

#### Scenario: 容器装配
- **WHEN** 构造 `Container`
- **THEN** 仅注入 `BalanceService`（合并后含原 `BalanceQueryService` 的 `CheckBalance` 方法），`BalanceQuerySvc` 字段删除

## REMOVED Requirements

### Requirement: BalanceQueryService

**Reason**: 与 `BalanceService` 职责重叠，6 个方法中 5 个为死代码，合并后消除冗余
**Migration**: `CheckBalance` 方法迁移到 `BalanceService`，其余 5 个死代码方法直接删除
