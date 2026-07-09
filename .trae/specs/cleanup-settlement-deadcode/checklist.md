# Checklist

## Phase 1：高优先级死代码清理

- [x] `refund_query_service.go` 文件已删除
- [x] `container.go` 不再持有 `RefundQuerySvc` 字段
- [x] `app.go` 不再构造 `refundQuerySvc`
- [x] `doc.go` 不再描述 `RefundQueryService`
- [x] `refund_execute_service.go` 不再含 `RejectRefund` 方法
- [x] `deduct_service.go` 不再含 `getBatchDeductResult` 方法
- [x] `domain/errors.go` 文件已删除
- [x] `domain/bill.go` 不再含 9 个 Reconcile 常量
- [x] `domain/refund.go` 不再含 `RefundTypeOther` 常量
- [x] `domain/exception.go` 不再含 7 个未使用常量（保留 `ExceptionStatusPending`）
- [x] `domain/bill.go` 不再含 `CanRefund` / `IsTerminalStatus` 方法
- [x] `domain/round_settlement.go` 不再含 `CanSettle` / `IsTerminalStatus` 方法
- [x] `domain/refund.go` 不再含 `CanApprove` / `CanRetry` / `IsTerminalStatus` 方法
- [x] `domain/exception.go` 不再含 `CanHandle` / `IsTerminalStatus` 方法
- [x] `dto/constants.go` 不再含 3 个 Trace/Penalty 常量
- [x] `dto/request.go` 不再含 `GameSettleRequest` 类型
- [x] `dto/response.go` 不再含 8 个未使用 DTO 类型

## Phase 2：中优先级死代码清理

- [x] `trace_id_generator.go` 不再含 `GenerateReconcileNo` / `ParseRoundTraceID` / `ExtractSessionIDFromRoundTraceID`
- [x] `exception_repository.go` 接口不再含 `GetByID` / `GetByStatus` / `UpdateStatus`
- [x] `exception_repository.go` 实现不再含上述 3 个方法
- [x] `platform_call_log_repository.go` 接口不再含 `GetLogByID` / `GetFailedLogs`
- [x] `platform_call_log_repository.go` 实现不再含上述 2 个方法
- [x] `bill_repository.go` 接口不再含 `GetBillByTraceID` / `GetBillsByUserID`
- [x] `bill_repository.go` 实现不再含上述 2 个方法
- [x] `refund_audit_repository.go` 接口不再含 `RejectRefund`
- [x] `refund_audit_repository.go` 实现不再含 `RejectRefund`
- [x] 所有 mock 文件已同步更新

## Phase 3：低优先级死代码清理

- [x] `virtual_balance_service.go` 不再含 `IsRobot` 接口方法
- [x] `virtual_balance_repository.go` 不再含 `IsRobot` 实现方法
- [x] mock 文件已同步更新

## 全局验收

- [x] `go build ./settlement/... ./game/...` 编译通过
- [x] `go test ./settlement/... ./game/...` 全部测试通过
- [x] `gofmt -l settlement/ game/` 无输出
- [x] `go vet ./settlement/... ./game/...` 无 warning
- [x] 业务逻辑零变更（所有保留的 Service / Repository / Domain 方法行为不变）
- [x] 资金安全三道防线未放松（IsRobot error 中止 / ParseAmount 失败标 Failed / robotChecker nil 检查）
