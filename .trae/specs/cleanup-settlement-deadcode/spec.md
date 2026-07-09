# 清理 Settlement 模块死代码 Spec

## Why

对 settlement 模块进行全面死代码扫描后，发现 **18 项**无任何外部调用方的导出符号（高优先级 11 项 + 中优先级 5 项 + 低优先级 2 项）。这些死代码源于：
- 历史拆分预留但从未消费的方法（如 `RefundQueryService` 整个 Service）
- 枚举常量定义但业务未使用（如 9 个 Reconcile 常量、7 个 Exception 状态常量）
- 守卫方法被 `TransitionTo` 取代后未清理（如 `CanRefund` / `CanSettle` 等 9 个方法）
- Phase 7.3 补全的 ExceptionRepository 方法无生产调用方（`GetByID` / `GetByStatus` / `UpdateStatus`）

死代码增加维护成本、误导开发者、污染代码索引，应统一清理。

## What Changes

### 高优先级删除（11 项）

1. **删除 `RefundQueryService` 整个 Service**（`refund_query_service.go` 文件 + `container.go` 字段 + `app.go` 构造）
2. **删除 `RefundExecuteService.RejectRefund` 方法**（无外部调用方）
3. **删除 `DeductService.getBatchDeductResult` 私有方法**（定义但从未调用）
4. **删除 `domain/errors.go` 整个文件**（8 个 error 变量无引用）
5. **删除 `domain/bill.go` 中 9 个 Reconcile 常量**（`ReconcileStatusPending/Success/Abnormal` / `ReconcileTypeScheduled/Abnormal/Manual` / `ReconcileScopeSession/Round/Bill`）
6. **删除 `domain/refund.go` 中 `RefundTypeOther` 常量**
7. **删除 `domain/exception.go` 中 7 个未使用常量**（`ExceptionStatusProcessing/Resolved/Ignored` / `HandleTypeManual/Refund/Retry/Ignore`），保留 `ExceptionStatusPending`
8. **删除 9 个守卫方法**（`CanRefund` / `CanSettle` / `CanApprove` / `CanRetry` / `CanHandle` / 4 个 `IsTerminalStatus`），已被 `TransitionTo` 取代
9. **删除 `dto/constants.go` 中 3 个常量**（`TraceTypePenaltyDeduct` / `TraceTypePenaltyDist` / `PenaltyRoundID`）
10. **删除 `dto/request.go` 中 `GameSettleRequest` 类型**
11. **删除 `dto/response.go` 中 8 个未使用 DTO 类型**（`RefundResult` / `BillQueryResult` / `BillInfo` / `RoundSettlementInfo` / `RefundAuditInfo` / `BatchBalanceCheckResult` / `GameSettleInfo` / `GamePlayerSettleInfo`）

### 中优先级删除（5 项）

12. **删除 `TraceIDGenerator` 的 3 个方法**（`GenerateReconcileNo` / `ParseRoundTraceID` / `ExtractSessionIDFromRoundTraceID`），仅测试调用
13. **删除 `ExceptionRepository` 的 3 个接口方法**（`GetByID` / `GetByStatus` / `UpdateStatus`）+ MySQL 实现对应方法 + 单元测试，仅测试调用
14. **删除 `PlatformCallLogRepository` 的 2 个接口方法**（`GetLogByID` / `GetFailedLogs`）+ MySQL 实现对应方法
15. **删除 `BillRepository` 的 2 个接口方法**（`GetBillByTraceID` / `GetBillsByUserID`）+ MySQL 实现对应方法
16. **删除 `RefundAuditRepository.RejectRefund` 接口方法** + MySQL 实现对应方法（仅被死代码 #2 调用）

### 低优先级删除（2 项）

17. **删除 `VirtualBalanceService.IsRobot` 接口方法**（与 `RobotChecker.IsRobot` 同名但无调用方）
18. **删除 `VirtualBalanceRepository.IsRobot` 实现方法**

### 不删除的内容

- `ExceptionStatusPending` 常量（4 个 service 文件使用）
- `ExceptionStatus` / `HandleType` 类型定义（作为 struct 字段和接口参数类型使用）
- `ReconcileStatus` 字段定义（model 和 infrastructure 中使用）
- `RefundTypeFirstRoundFail` 常量（service 层使用）
- `FirstRoundDeductResult` / `FailedPlayerInfo` / `BalanceCheckResult` DTO 类型（有调用方）
- `RobotChecker.IsRobot`（高频使用，非死代码）

## Impact

- Affected code:
  - `settlement/service/refund_query_service.go`（**删除**）
  - `settlement/service/refund_execute_service.go`（删除 `RejectRefund` 方法）
  - `settlement/service/deduct_service.go`（删除 `getBatchDeductResult` 方法）
  - `settlement/service/trace_id_generator.go`（删除 3 个方法）
  - `settlement/domain/errors.go`（**删除**）
  - `settlement/domain/bill.go`（删除 9 个常量 + 2 个守卫方法）
  - `settlement/domain/round_settlement.go`（删除 2 个守卫方法）
  - `settlement/domain/refund.go`（删除 1 个常量 + 3 个守卫方法）
  - `settlement/domain/exception.go`（删除 7 个常量 + 2 个守卫方法）
  - `settlement/domain/virtual_balance_service.go`（删除 `IsRobot` 接口方法）
  - `settlement/domain/repository/bill_repository.go`（删除 2 个接口方法）
  - `settlement/domain/repository/refund_audit_repository.go`（删除 1 个接口方法）
  - `settlement/domain/repository/exception_repository.go`（删除 3 个接口方法）
  - `settlement/domain/repository/platform_call_log_repository.go`（删除 2 个接口方法）
  - `settlement/infrastructure/persistence/mysql/bill_repository.go`（删除 2 个方法实现 + 转换函数）
  - `settlement/infrastructure/persistence/mysql/refund_audit_repository.go`（删除 1 个方法实现）
  - `settlement/infrastructure/persistence/mysql/exception_repository.go`（删除 3 个方法实现 + 转换函数）
  - `settlement/infrastructure/persistence/mysql/platform_call_log_repository.go`（删除 2 个方法实现 + 转换函数）
  - `settlement/infrastructure/persistence/redis/virtual_balance_repository.go`（删除 `IsRobot` 实现）
  - `settlement/dto/constants.go`（删除 3 个常量）
  - `settlement/dto/request.go`（删除 `GameSettleRequest`）
  - `settlement/dto/response.go`（删除 8 个 DTO 类型）
  - `settlement/application/settle_app_service.go`（无变化，facade 不受影响）
  - `game/bootstrap/container.go`（移除 `RefundQuerySvc` 字段 + `NewContainer` 参数）
  - `game/bootstrap/app.go`（移除 `refundQuerySvc` 构造 + `NewContainer` 调用参数）
  - `settlement/service/mocks_test.go`（移除对应 mock 方法）
  - `game/integration/mocks_test.go`（移除对应 mock 方法）
  - `settlement/infrastructure/persistence/mysql/exception_repository_test.go`（删除或更新）

## ADDED Requirements

### Requirement: 死代码清理

系统 SHALL 删除所有无外部调用方的导出符号。

#### Scenario: 清理后编译通过
- **WHEN** 删除所有 18 项死代码
- **THEN** `go build ./settlement/... ./game/...` 编译通过

#### Scenario: 清理后测试通过
- **WHEN** 删除死代码 + 更新 mock
- **THEN** `go test ./settlement/... ./game/...` 全部测试通过

#### Scenario: 业务逻辑不变
- **WHEN** 清理完成
- **THEN** 所有保留的 Service / Repository / Domain 方法行为不变

## REMOVED Requirements

### Requirement: RefundQueryService

**Reason**: 整个 Service 无任何外部调用方（container 字段从未被读取）
**Migration**: 无需迁移，直接删除

### Requirement: ExceptionRepository 扩展方法

**Reason**: Phase 7.3 补全的 `GetByID` / `GetByStatus` / `UpdateStatus` 仅测试调用，无生产消费方
**Migration**: 无需迁移，直接删除（如未来需要可重新添加）
