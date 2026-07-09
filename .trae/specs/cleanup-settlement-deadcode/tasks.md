# Tasks

> 遵循"不改变业务逻辑"原则。每个 SubTask 完成后必须 `go build ./settlement/... ./game/...` + `go test ./settlement/... ./game/...` 通过。

## Phase 1：高优先级死代码清理（11 项）

- [x] Task 1.1: 删除 RefundQueryService 整个 Service
  - [x] SubTask 1.1.1: 删除 `settlement/service/refund_query_service.go` 整个文件
  - [x] SubTask 1.1.2: `game/bootstrap/container.go` 移除 `RefundQuerySvc` 字段 + `NewContainer` 的 `refundQuerySvc` 参数 + 赋值
  - [x] SubTask 1.1.3: `game/bootstrap/app.go` 移除 `refundQuerySvc := settlementService.NewRefundQueryService(...)` 构造 + `NewContainer` 调用参数
  - [x] SubTask 1.1.4: `settlement/service/doc.go` 移除 `RefundQueryService` 描述
  - [x] SubTask 1.1.5: `go build ./settlement/... ./game/...` 编译通过

- [x] Task 1.2: 删除 RefundExecuteService.RejectRefund 方法
  - [x] SubTask 1.2.1: `settlement/service/refund_execute_service.go` 删除 `RejectRefund` 方法
  - [x] SubTask 1.2.2: `go build ./settlement/...` 编译通过

- [x] Task 1.3: 删除 DeductService.getBatchDeductResult 私有方法
  - [x] SubTask 1.3.1: `settlement/service/deduct_service.go` 删除 `getBatchDeductResult` 方法
  - [x] SubTask 1.3.2: `go build ./settlement/...` 编译通过

- [x] Task 1.4: 删除 domain/errors.go 整个文件
  - [x] SubTask 1.4.1: 删除 `settlement/domain/errors.go` 整个文件（8 个 error 变量无引用）
  - [x] SubTask 1.4.2: `go build ./settlement/...` 编译通过

- [x] Task 1.5: 删除 domain 层未使用常量
  - [x] SubTask 1.5.1: `settlement/domain/bill.go` 删除 9 个 Reconcile 常量
  - [x] SubTask 1.5.2: `settlement/domain/refund.go` 删除 `RefundTypeOther` 常量
  - [x] SubTask 1.5.3: `settlement/domain/exception.go` 删除 7 个未使用常量，保留 `ExceptionStatusPending`
  - [x] SubTask 1.5.4: `go build ./settlement/...` 编译通过

- [x] Task 1.6: 删除 9 个守卫方法（已被 TransitionTo 取代）
  - [x] SubTask 1.6.1: `settlement/domain/bill.go` 删除 `BillRecord.CanRefund` / `BillRecord.IsTerminalStatus`
  - [x] SubTask 1.6.2: `settlement/domain/round_settlement.go` 删除 `RoundSettlement.CanSettle` / `RoundSettlement.IsTerminalStatus`
  - [x] SubTask 1.6.3: `settlement/domain/refund.go` 删除 `RefundAudit.CanApprove` / `RefundAudit.CanRetry` / `RefundAudit.IsTerminalStatus`
  - [x] SubTask 1.6.4: `settlement/domain/exception.go` 删除 `ExceptionRecord.CanHandle` / `ExceptionRecord.IsTerminalStatus`
  - [x] SubTask 1.6.5: `go build ./settlement/...` 编译通过

- [x] Task 1.7: 删除 dto 层未使用类型和常量
  - [x] SubTask 1.7.1: `settlement/dto/constants.go` 删除 3 个常量
  - [x] SubTask 1.7.2: `settlement/dto/request.go` 删除 `GameSettleRequest` 类型
  - [x] SubTask 1.7.3: `settlement/dto/response.go` 删除 8 个未使用 DTO 类型
  - [x] SubTask 1.7.4: `go build ./settlement/...` 编译通过

## Phase 2：中优先级死代码清理（5 项）

- [x] Task 2.1: 删除 TraceIDGenerator 的 3 个方法
  - [x] SubTask 2.1.1: `settlement/service/trace_id_generator.go` 删除 `GenerateReconcileNo` / `ParseRoundTraceID` / `ExtractSessionIDFromRoundTraceID`
  - [x] SubTask 2.1.2: `settlement/service/trace_id_generator_test.go` 删除对应测试
  - [x] SubTask 2.1.3: `go build ./settlement/...` + `go test ./settlement/...` 通过

- [x] Task 2.2: 删除 ExceptionRepository 的 3 个接口方法
  - [x] SubTask 2.2.1: `settlement/domain/repository/exception_repository.go` 删除 `GetByID` / `GetByStatus` / `UpdateStatus` 方法声明
  - [x] SubTask 2.2.2: `settlement/infrastructure/persistence/mysql/exception_repository.go` 删除 3 个方法实现 + `exceptionModelToDomain` / `exceptionModelSliceToDomain` 辅助函数
  - [x] SubTask 2.2.3: 删除 `settlement/infrastructure/persistence/mysql/exception_repository_test.go`
  - [x] SubTask 2.2.4: `settlement/service/mocks_test.go` 移除 `mockExceptionRepo` 的 3 个方法
  - [x] SubTask 2.2.5: `go build ./settlement/...` + `go test ./settlement/...` 通过

- [x] Task 2.3: 删除 PlatformCallLogRepository 的 2 个接口方法
  - [x] SubTask 2.3.1: `settlement/domain/repository/platform_call_log_repository.go` 删除 `GetLogByID` / `GetFailedLogs` 方法声明
  - [x] SubTask 2.3.2: `settlement/infrastructure/persistence/mysql/platform_call_log_repository.go` 删除 2 个方法实现 + `platformCallLogModelSliceToDomain` 辅助函数
  - [x] SubTask 2.3.3: `settlement/service/mocks_test.go` 移除 `mockPlatformCallLogRepo` 的 2 个方法
  - [x] SubTask 2.3.4: `go build ./settlement/...` + `go test ./settlement/...` 通过

- [x] Task 2.4: 删除 BillRepository 的 2 个接口方法
  - [x] SubTask 2.4.1: `settlement/domain/repository/bill_repository.go` 删除 `GetBillByTraceID` / `GetBillsByUserID` 方法声明
  - [x] SubTask 2.4.2: `settlement/infrastructure/persistence/mysql/bill_repository.go` 删除 2 个方法实现（保留 `billModelToDomain` / `billModelSliceToDomain`，被其他方法使用）
  - [x] SubTask 2.4.3: `settlement/service/mocks_test.go` 移除 `mockBillRepo` 的 2 个方法
  - [x] SubTask 2.4.4: `game/integration/mocks_test.go` 移除 `mockBillRepo` 的 2 个方法
  - [x] SubTask 2.4.5: `go build ./settlement/...` + `go test ./settlement/...` 通过

- [x] Task 2.5: 删除 RefundAuditRepository.RejectRefund 接口方法
  - [x] SubTask 2.5.1: `settlement/domain/repository/refund_audit_repository.go` 删除 `RejectRefund` 方法声明
  - [x] SubTask 2.5.2: `settlement/infrastructure/persistence/mysql/refund_audit_repository.go` 删除 `RejectRefund` 方法实现
  - [x] SubTask 2.5.3: `settlement/service/mocks_test.go` 移除 `mockRefundAuditRepo.RejectRefund`
  - [x] SubTask 2.5.4: `game/integration/mocks_test.go` 移除 `mockRefundAuditRepo.RejectRefund`
  - [x] SubTask 2.5.5: `go build ./settlement/...` + `go test ./settlement/...` 通过

## Phase 3：低优先级死代码清理（2 项）

- [x] Task 3.1: 删除 VirtualBalanceService.IsRobot 接口方法 + 实现
  - [x] SubTask 3.1.1: `settlement/domain/virtual_balance_service.go` 删除 `IsRobot` 方法声明
  - [x] SubTask 3.1.2: `settlement/infrastructure/persistence/redis/virtual_balance_repository.go` 删除 `IsRobot` 方法实现
  - [x] SubTask 3.1.3: `settlement/service/mocks_test.go` 移除 `mockVirtualBalance.IsRobot`
  - [x] SubTask 3.1.4: `go build ./settlement/...` + `go test ./settlement/...` 通过

## Phase 4：最终验证

- [x] Task 4.1: 全量验证
  - [x] SubTask 4.1.1: `go build ./settlement/... ./game/...` 编译通过
  - [x] SubTask 4.1.2: `go test ./settlement/... ./game/...` 全部测试通过
  - [x] SubTask 4.1.3: `gofmt -l settlement/ game/` 无输出
  - [x] SubTask 4.1.4: `go vet ./settlement/... ./game/...` 无 warning

# Task Dependencies
- Phase 2 依赖 Phase 1（先清理高优先级，避免中优先级清理时遇到依赖）
- Phase 3 依赖 Phase 2
- Phase 4 依赖 Phase 3
- Phase 1 内部各 Task 无依赖，可并行
- Phase 2 内部各 Task 无依赖，可并行
