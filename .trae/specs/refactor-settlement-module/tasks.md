# Tasks

> 所有 Phase 严格遵循"不改变业务逻辑"原则。每个 SubTask 完成后必须 `go build ./...` + `go test ./settlement/... ./game/...` 通过。Phase 间依赖：Phase 1 → Phase 2 → Phase 5；Phase 3 与 Phase 4 可与 Phase 2 并行；Phase 6 依赖 Phase 2 + Phase 5；Phase 7 依赖 Phase 6。

## Phase 1：死代码与一致性清理（低风险）

- [x] Task 1.1: 删除 RefundAppService 死代码
  - [x] SubTask 1.1.1: grep 确认 `RefundAppService` / `NewRefundAppService` 在整个 backend 中除 `refund_app_service.go` 与 `doc.go` 外无调用方（已确认）
  - [x] SubTask 1.1.2: 删除 `settlement/application/refund_app_service.go` 整个文件
  - [x] SubTask 1.1.3: 更新 `settlement/application/doc.go` 移除 RefundAppService 相关描述（保留 SettleAppService / SchedulerAppService 描述）
  - [x] SubTask 1.1.4: `go build ./...` 验证编译通过

- [x] Task 1.2: 重命名 platform_settle_log.go 文件
  - [x] SubTask 1.2.1: 将 `settlement/model/platform_settle_log.go` 重命名为 `settlement/model/platform_call_log.go`（内容不变，结构体名 PlatformCallLog、表名 platform_call_log 已一致）
  - [x] SubTask 1.2.2: `go build ./...` 验证编译通过

- [x] Task 1.3: 统一 BalanceService / BalanceQueryService 字段命名
  - [x] SubTask 1.3.1: `settlement/service/balance_service.go` 字段 `virtualBalanceSvc` 重命名为 `virtualBalance`（含构造函数参数与赋值）
  - [x] SubTask 1.3.2: 检查 `settlement/service/balance_query_service.go` 字段命名是否已是 `virtualBalance`（若是则无操作）
  - [x] SubTask 1.3.3: grep 确认全模块无 `virtualBalanceSvc` 残留
  - [x] SubTask 1.3.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 1.4: 统一 NewExceptionRepository / NewPlatformCallLogRepository 返回接口类型
  - [x] SubTask 1.4.1: `settlement/infrastructure/persistence/mysql/exception_repository.go` `NewExceptionRepository` 返回类型改为 `repository.ExceptionRepository`
  - [x] SubTask 1.4.2: `settlement/infrastructure/persistence/mysql/platform_call_log_repository.go`（重命名后）`NewPlatformCallLogRepository` 返回类型改为 `repository.PlatformCallLogRepository`
  - [x] SubTask 1.4.3: 更新 `db_repository.go` 中 `dbRepositoryImpl` 与 `gormTransactionImpl` 字段类型从 `*ExceptionRepositoryImpl` / `*PlatformCallLogRepositoryImpl` 改为 `repository.ExceptionRepository` / `repository.PlatformCallLogRepository`
  - [x] SubTask 1.4.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 1.5: 补全 dto/response.go json tag
  - [x] SubTask 1.5.1: 为 `FirstRoundDeductResult` / `FailedPlayerInfo` / `RefundResult` / `BillQueryResult` 结构体字段补全 json tag（遵循已有命名风格，snake_case）
  - [x] SubTask 1.5.2: `go build ./...` 通过

- [x] Task 1.6: Multiplier 常量化
  - [x] SubTask 1.6.1: `settlement/service/game_settle_reporting_service.go` 在文件顶部 const 块定义 `defaultMultiplier = "1"`（或更具语义的命名如 `gameSettleDefaultMultiplier`）
  - [x] SubTask 1.6.2: 第 265 行 `Multiplier: "1"` 改为 `Multiplier: defaultMultiplier`
  - [x] SubTask 1.6.3: `go build ./...` + `go test ./settlement/...` 通过

## Phase 2：枚举统一与兼容垫片移除（中风险）

> 依赖：Phase 1 完成（doc.go 已不含 RefundAppService）

- [x] Task 2.1: Exception 枚举从 model 迁入 domain（反转依赖方向）
  - [x] SubTask 2.1.1: `settlement/domain/exception.go` 将 `ExceptionType` / `ExceptionStatus` / `HandleType` 从 type alias 改为原生类型定义（`type ExceptionType int` 等），常量定义保持 `ExceptionType` 类型
  - [x] SubTask 2.1.2: `settlement/model/exception_record.go` 将 `ExceptionType` / `ExceptionStatus` / `HandleType` 改为 type alias 引用 domain（`type ExceptionType = domain.ExceptionType`），移除常量定义（改引用 `domain.ExceptionTypeDebitFailed` 等）；保留 `ExceptionRecord` struct 与 `TableName()` 不变
  - [x] SubTask 2.1.3: `model/exception_record.go` import `settlement/domain`
  - [x] SubTask 2.1.4: `go build ./...` 通过

- [x] Task 2.2: 迁移 dto/constants.go 重导出别名引用到 domain（219 处，19 个文件）
  - [x] SubTask 2.2.1: 用 grep 列出所有 `dto\.(BillType|BillStatus|DeductScene|RoundStatus|ReconcileStatus|RefundStatus|RefundType|ReconcileType|ReconcileScope|GameSettleStatus|BillGameSettle)` 引用位置
  - [x] SubTask 2.2.2: 逐文件替换 `dto.X` → `domain.X`，确保 import `settlement/domain`（若文件未 import）
  - [x] SubTask 2.2.3: 涉及文件：`settlement/service/*.go`（12 文件）、`settlement/application/scheduler_app_service.go`、`settlement/infrastructure/persistence/mysql/*.go`（4 文件）、`game/integration/flow_test.go`、`game/application/packet_round_init.go`（别名引用）
  - [x] SubTask 2.2.4: 移除不再需要的 `settlement/dto` import（若文件仅引用常量）
  - [x] SubTask 2.2.5: `go build ./...` + `go test ./settlement/... ./game/...` 通过

- [x] Task 2.3: 删除 dto/constants.go 中 domain 重导出别名
  - [x] SubTask 2.3.1: 确认 Task 2.2 完成后 grep 无 `dto.BillType*` / `dto.BillStatus*` / 等残留
  - [x] SubTask 2.3.2: 删除 `settlement/dto/constants.go` 中所有 domain 重导出别名 const 块（第 27-107 行）
  - [x] SubTask 2.3.3: 保留 DTO 层特有常量：`TraceTypePenaltyDeduct` / `TraceTypePenaltyDist` / `PlatformAccountID` / `MaxRetryCount` / `PenaltyRoundID` / `CreditRetryBaseDelay` / `CreditRetryMaxDelay`
  - [x] SubTask 2.3.4: 移除不再需要的 `settlement/domain` import
  - [x] SubTask 2.3.5: `go build ./...` 通过

- [x] Task 2.4: PlatformCallLog 状态常量迁入 domain（若存在状态常量定义在 model）
  - [x] SubTask 2.4.1: 检查 `settlement/model/platform_call_log.go`（重命名后）是否有状态/类型常量定义
  - [x] SubTask 2.4.2: 新增 `settlement/domain/platform_call_log.go`，将 `CallLogStatusPending/Success/Failed` 和 `CallTypeDebit/Credit/Settle` 迁入 domain
  - [x] SubTask 2.4.3: model 改为 type alias 引用 domain，迁移 31 处引用
  - [x] SubTask 2.4.4: `go build ./...` 通过

## Phase 3：配置化硬编码（低风险）

> 可与 Phase 2 并行

- [x] Task 3.1: 新增 SettlementConfig 结构
  - [x] SubTask 3.1.1: `settlement/config/config.go` 新增 `SettlementConfig` struct：`TransactionTimeout time.Duration` / `SettlementCheckLimit int` / `MaxConcurrentDeduct int` / `CreditRetryBaseDelay time.Duration` / `CreditRetryMaxDelay time.Duration` / `MaxRetryCount int`
  - [x] SubTask 3.1.2: 新增 `DefaultSettlementConfig()` 返回与原硬编码一致的默认值（30s / 100 / 20 / 5s / 5min / 3）
  - [x] SubTask 3.1.3: `go build ./...` 通过

- [x] Task 3.2: 事务超时配置化
  - [x] SubTask 3.2.1: `settlement/infrastructure/persistence/mysql/db_repository.go` `dbRepositoryImpl` 增加 `transactionTimeout time.Duration` 字段
  - [x] SubTask 3.2.2: `NewDBRepository` 增加 `transactionTimeout time.Duration` 参数（或 `*config.SettlementConfig`）
  - [x] SubTask 3.2.3: `WithTransaction` 第 60 行 `30 * time.Second` 改为 `r.transactionTimeout`；若零值则用默认 30s 兜底
  - [x] SubTask 3.2.4: `gormTransactionImpl` 同步增加字段（事务内无需超时，可仅保留 dbRepositoryImpl 层）
  - [x] SubTask 3.2.5: `game/bootstrap/app.go` 构造 `NewDBRepository` 时传入配置（从 SettlementConfig 读取）
  - [x] SubTask 3.2.6: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 3.3: 对账 limit 配置化
  - [x] SubTask 3.3.1: `settlement/service/settlement_check_service.go` `SettlementCheckService` 增加 `limit int` 字段；`NewSettlementCheckService` 增加 limit 参数
  - [x] SubTask 3.3.2: 第 40 行 `s.roundSettlementRepo.GetFailedFirstRoundSettlements(ctx, since, 100)` 改为 `s.limit`
  - [x] SubTask 3.3.3: 第 59 行 `s.roundSettlementRepo.GetDeductedButNotSettled(ctx, since, 100)` 改为 `s.limit`
  - [x] SubTask 3.3.4: `settlement/application/scheduler_app_service.go` `RunSettlementCheck` 增加 `limit int` 参数，透传到 `CheckFirstRoundDeductFailure` / `CheckDeductedButNotSettled`
  - [x] SubTask 3.3.5: `settlement/scheduler/settlement_check_scheduler.go` 增加 `limit int` 字段；`NewSettlementCheckScheduler` 从 `cfg.Limit` 读取（已存在 Limit 字段）；`execute` 调用 `RunSettlementCheck` 时透传 limit
  - [x] SubTask 3.3.6: `game/bootstrap/app.go` 构造 `NewSettlementCheckService` 时传入 limit（从配置读取，默认 100）
  - [x] SubTask 3.3.7: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 3.4: 批量扣款并发上限配置化
  - [x] SubTask 3.4.1: `settlement/service/deduct_service.go` `DeductService` 保留 `maxConcurrentDeduct` 字段；`NewDeductService` 增加并发上限参数（或从 SettlementConfig 读取）
  - [x] SubTask 3.4.2: 保留 `defaultMaxConcurrentDeduct = 20` 作为兜底默认值（当传入 0 时使用）
  - [x] SubTask 3.4.3: `game/bootstrap/app.go` 构造 `NewDeductService` 时传入配置值
  - [x] SubTask 3.4.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 3.5: CreditRetry 退避参数配置化
  - [x] SubTask 3.5.1: 检查 `settlement/service/credit_retry_service.go` 是否已使用 `dto.CreditRetryBaseDelay` / `dto.CreditRetryMaxDelay` / `dto.MaxRetryCount`
  - [x] SubTask 3.5.2: 将这些常量改为从 `SettlementConfig` 字段读取（构造函数注入）
  - [x] SubTask 3.5.3: 保留 `dto/constants.go` 中的常量定义作为默认值兜底（或移除，由 SettlementConfig.DefaultSettlementConfig 提供）
  - [x] SubTask 3.5.4: `game/bootstrap/app.go` 构造 `NewCreditRetryService` 时传入配置
  - [x] SubTask 3.5.5: `go build ./...` + `go test ./settlement/...` 通过

## Phase 4：模块边界修复（中风险）

> 可与 Phase 2/3 并行

- [x] Task 4.1: 新增 domain.FeeCalculator 接口
  - [x] SubTask 4.1.1: 新建 `settlement/domain/fee_calculator.go`，定义 `FeeCalculator` interface：`CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64`
  - [x] SubTask 4.1.2: 添加 godoc 注释说明用途（解耦 settlement 对 game/domain/room 的依赖）
  - [x] SubTask 4.1.3: `go build ./...` 通过

- [x] Task 4.2: 新增 game 层 FeeCalculatorAdapter 实现
  - [x] SubTask 4.2.1: 新建 `game/infrastructure/adapter/fee_calculator_adapter.go`，定义 `FeeCalculatorAdapter struct{}`
  - [x] SubTask 4.2.2: 实现 `CalculateRequiredFee` 方法，内部委托 `room.CalculateRequiredFee(roomFee, maxPlayers, maxRounds)`
  - [x] SubTask 4.2.3: 新增 `NewFeeCalculatorAdapter() *FeeCalculatorAdapter` 构造函数
  - [x] SubTask 4.2.4: `go build ./...` 通过

- [x] Task 4.3: BalanceService 依赖倒置
  - [x] SubTask 4.3.1: `settlement/service/balance_service.go` 移除 `"github.com/cashparty/backend/game/domain/room"` import
  - [x] SubTask 4.3.2: `BalanceService` struct 增加 `feeCalculator domain.FeeCalculator` 字段
  - [x] SubTask 4.3.3: `NewBalanceService` 构造函数增加 `feeCalculator domain.FeeCalculator` 参数
  - [x] SubTask 4.3.4: 第 37 行 `room.CalculateRequiredFee(req.RoomFee, req.MaxPlayers, req.MaxRounds)` 改为 `s.feeCalculator.CalculateRequiredFee(req.RoomFee, req.MaxPlayers, req.MaxRounds)`
  - [x] SubTask 4.3.5: `go build ./...` 通过

- [x] Task 4.4: game/bootstrap 注入 FeeCalculatorAdapter
  - [x] SubTask 4.4.1: `game/bootstrap/container.go` 构造 BalanceService 处，先构造 `NewFeeCalculatorAdapter()`，再传入 `NewBalanceService`
  - [x] SubTask 4.4.2: `go list -deps` 仍输出 `game/domain/reward`（Phase 4 范围外预存依赖，来自 game_settle_reporting_service.go 的 reward.DetermineGameResult 调用）
  - [x] SubTask 4.4.3: `go build ./...` + `go test ./settlement/... ./game/...` 通过

## Phase 5：状态机一致性（中风险）

> 依赖：Phase 2 完成（dto/constants.go 已清理）

- [x] Task 5.1: 删除 RoundSettlement 未使用中间态
  - [x] SubTask 5.1.1: grep 确认 `RoundStatusSettling` / `RoundStatusSuccess` / `RoundStatusPartial` 在 Phase 2 完成后仅 domain 定义处引用（dto 兼容垫片已删）
  - [x] SubTask 5.1.2: `settlement/domain/round_settlement.go` 删除 `RoundStatusSettling = 2` / `RoundStatusSuccess = 3` / `RoundStatusPartial = 4` 三行
  - [x] SubTask 5.1.3: 更新状态流转注释为：`Deducting(0) → Deducted(1) / Failed(5)` / `Deducted(1) → Credited(6) / Failed(5)` / `Credited(6) / Failed(5) 为终态`
  - [x] SubTask 5.1.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 5.2: RefundAudit Approved 状态注释明确
  - [x] SubTask 5.2.1: `settlement/domain/refund.go` 状态机注释明确 `Approved(2)` 是过渡态：被 `UpdateRefundAuditStatus` 设置后立即进入 `executeRefund`，不会持久停留
  - [x] SubTask 5.2.2: `go build ./...` 通过

## Phase 6：domain 双层落地（高风险，可选）

> 依赖：Phase 2 + Phase 5 完成。分子任务逐步迁移，每步编译 + 测试通过再进行下一步。

- [x] Task 6.1: 聚合根增加 TransitionTo 守卫方法
  - [x] SubTask 6.1.1: `settlement/domain/bill.go` `BillRecord` 增加 `TransitionTo(newStatus int) error` 方法，内部校验状态转换合法性（Processing→Success/Failed、Success→Refunded），非法转换返回 error
  - [x] SubTask 6.1.2: `settlement/domain/round_settlement.go` `RoundSettlement` 增加 `TransitionTo(newStatus int) error`（Deducting→Deducted/Failed、Deducted→Credited/Failed）
  - [x] SubTask 6.1.3: `settlement/domain/refund.go` `RefundAudit` 增加 `TransitionTo(newStatus int) error`（None→Pending、Pending→Approved/Rejected、Approved→Refunded/Processing）
  - [x] SubTask 6.1.4: 新增单元测试覆盖合法与非法转换
  - [x] SubTask 6.1.5: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 6.2: BillRepository 接口签名 model → domain
  - [x] SubTask 6.2.1: `settlement/domain/repository/bill_repository.go` 所有 20 方法签名 `*model.BillRecord` → `*domain.BillRecord`
  - [x] SubTask 6.2.2: `settlement/infrastructure/persistence/mysql/bill_repository.go` 实现层增加 `billModelToDomain` / `billDomainToModel` 转换函数
  - [x] SubTask 6.2.3: 实现层每个方法入口转 model、出口转 domain
  - [x] SubTask 6.2.4: `settlement/service/*.go` 中调用 BillRepository 的代码：调用聚合根守卫方法（`bill.TransitionTo(Success)`）后再传给 Repository
  - [x] SubTask 6.2.5: 更新 `settlement/service/mocks_test.go` 中 BillRepository mock 的签名
  - [x] SubTask 6.2.6: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 6.3: RoundSettlementRepository 接口签名 model → domain
  - [x] SubTask 6.3.1: `settlement/domain/repository/round_settlement_repository.go` 所有 13 方法签名 `*model.RoundSettlement` → `*domain.RoundSettlement`
  - [x] SubTask 6.3.2: `settlement/infrastructure/persistence/mysql/round_settlement_repository.go` 实现层增加转换函数
  - [x] SubTask 6.3.3: 实现层每个方法入口转 model、出口转 domain
  - [x] SubTask 6.3.4: Service 层调用守卫方法（`roundSettlement.TransitionTo(Credited)`）
  - [x] SubTask 6.3.5: 更新 mocks
  - [x] SubTask 6.3.6: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 6.4: RefundAuditRepository 接口签名 model → domain
  - [x] SubTask 6.4.1: `settlement/domain/repository/refund_audit_repository.go` 所有 9 方法签名 `*model.RefundAudit` → `*domain.RefundAudit`
  - [x] SubTask 6.4.2: 实现层增加转换函数
  - [x] SubTask 6.4.3: Service 层调用守卫方法
  - [x] SubTask 6.4.4: 更新 mocks
  - [x] SubTask 6.4.5: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 6.5: ExceptionRepository 接口签名 model → domain
  - [x] SubTask 6.5.1: `settlement/domain/repository/exception_repository.go` 签名 `*model.ExceptionRecord` → `*domain.ExceptionRecord`
  - [x] SubTask 6.5.2: 实现层增加转换函数
  - [x] SubTask 6.5.3: Service 层调用守卫方法（`exception.CanHandle()` 校验）
  - [x] SubTask 6.5.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 6.6: SettlementQueryRepository 接口签名 model → domain（no-op：接口仅使用基础类型 map[int64]int64 / bool / []int64，无 model 引用，无需迁移）
  - [x] SubTask 6.6.1: `settlement/domain/repository/settlement_query_repository.go` 4 方法签名 `*model.*` → `*domain.*`（确认接口无 model 类型引用，无需修改）
  - [x] SubTask 6.6.2: 实现层增加转换函数（实现层使用 model.BillRecord 进行 GORM 查询是预期行为，无需转换）
  - [x] SubTask 6.6.3: `go build ./...` + `go test ./settlement/...` 通过（已在前序 Task 中验证）

- [x] Task 6.7: PlatformCallLogRepository 接口签名 model → domain（若已迁入 domain）
  - [x] SubTask 6.7.1: 检查 Phase 2 是否已将 PlatformCallLog 状态常量迁入 domain；若是，新增 `domain/platform_call_log.go` 定义 `PlatformCallLog` 聚合根
  - [x] SubTask 6.7.2: `settlement/domain/repository/platform_call_log_repository.go` 签名改用 domain 类型
  - [x] SubTask 6.7.3: 实现层增加转换函数
  - [x] SubTask 6.7.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 6.8: model 层移除业务枚举（仅保留 GORM tag 与 TableName）（no-op：Phase 2 已完成迁移，model 层无常量定义）
  - [x] SubTask 6.8.1: `settlement/model/bill.go` 移除业务状态常量定义（已迁入 domain），保留 struct + GORM tag + TableName（确认无 const 块）
  - [x] SubTask 6.8.2: `settlement/model/round_settlement.go` 同上（RoundSettlement 与 BillRecord 同在 bill.go，无 const 块）
  - [x] SubTask 6.8.3: `settlement/model/refund.go` 同上（确认无 const 块）
  - [x] SubTask 6.8.4: `settlement/model/exception_record.go` 已在 Phase 2 改为 type alias，确认无冗余
  - [x] SubTask 6.8.5: grep 确认全模块业务枚举引用均通过 `domain.*`（grep `model\.(BillStatus|BillType|...)` 无输出）
  - [x] SubTask 6.8.6: `go build ./...` + `go test ./settlement/... ./game/...` 通过（已在前序 Task 中验证）

## Phase 7：职责拆分与补全（中风险，可选）

> 依赖：Phase 6 完成

- [x] Task 7.1: 拆分 RefundService 为 Apply / Execute / Query 三 Service
  - [x] SubTask 7.1.1: 新建 `settlement/service/refund_apply_service.go`，迁移 `ApplyForRefund` 方法
  - [x] SubTask 7.1.2: 新建 `settlement/service/refund_execute_service.go`，迁移 `ApproveRefund` / `executeRefund` / `RejectRefund` 方法
  - [x] SubTask 7.1.3: 新建 `settlement/service/refund_query_service.go`，迁移 `GetRefundAuditByOrderNo` / `GetRefundsByStatus` 方法
  - [x] SubTask 7.1.4: 删除原 `settlement/service/refund_service.go`（或保留为兼容入口，按需）
  - [x] SubTask 7.1.5: `settlement/application/scheduler_app_service.go` 调整注入：`refundExecuteSvc *RefundExecuteService` 替代 `refundSvc *RefundService`
  - [x] SubTask 7.1.6: `settlement/service/settlement_check_service.go` `refundSvc` 字段类型改为 `*RefundApplyService`（仅用 ApplyForRefund）
  - [x] SubTask 7.1.7: `game/bootstrap/container.go` 调整 RefundService 构造与注入
  - [x] SubTask 7.1.8: `go build ./...` + `go test ./settlement/... ./game/...` 通过

- [x] Task 7.2: BalanceService 与 BalanceQueryService 职责明确
  - [x] SubTask 7.2.1: 评估两者职责：BalanceService.CheckBalanceForReady / CheckUserBalance vs BalanceQueryService 的查询方法
  - [x] SubTask 7.2.2: 若职责重叠严重，合并为单一 BalanceService；若边界清晰，保留拆分但在 godoc 明确职责
  - [x] SubTask 7.2.3: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 7.3: 补全 ExceptionRepository 查询/处理方法
  - [x] SubTask 7.3.1: `settlement/domain/repository/exception_repository.go` 增加 `GetByID(ctx, id) (*domain.ExceptionRecord, error)` / `GetByStatus(ctx, status, limit, offset) ([]*domain.ExceptionRecord, error)` / `UpdateStatus(ctx, id, newStatus, handleType, handleRemark, handledBy) error`
  - [x] SubTask 7.3.2: `settlement/infrastructure/persistence/mysql/exception_repository.go` 实现新增方法
  - [x] SubTask 7.3.3: 新增单元测试
  - [x] SubTask 7.3.4: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 7.4: PenaltySettlementService.DeductPenaltyToPlatform 增加 Redis 锁
  - [x] SubTask 7.4.1: `settlement/service/penalty_settlement_service.go` `PenaltySettlementService` 增加 `distributedLock lock.DistributedLock` 字段（或 `cRedis.RedisClient`）
  - [x] SubTask 7.4.2: `NewPenaltySettlementService` 增加 lock 参数
  - [x] SubTask 7.4.3: `DeductPenaltyToPlatform` 入口处获取分布式锁（lock key 含 `userID + roundTraceID`，使用随机 token + Lua 释放）
  - [x] SubTask 7.4.4: 锁获取失败返回 error（避免重复扣款）
  - [x] SubTask 7.4.5: `game/bootstrap/container.go` 注入 lock 依赖
  - [x] SubTask 7.4.6: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 7.5: platform_call_log_repository json.Marshal error 检查
  - [x] SubTask 7.5.1: `settlement/infrastructure/persistence/mysql/platform_call_log_repository.go` 第 28 行 `reqBody, _ := json.Marshal(...)` 改为 `reqBody, err := json.Marshal(...)`，err 非 nil 时 logger.Warn 并用空 body 兜底
  - [x] SubTask 7.5.2: 第 60 行 `respBody, _` 同上
  - [x] SubTask 7.5.3: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 7.6: virtual_balance_repository SAdd error 检查
  - [x] SubTask 7.6.1: `settlement/infrastructure/persistence/redis/virtual_balance_repository.go` 第 118 行 `s.redis.SAdd(ctx, dirtyKey, member)` 改为检查 `.Err()`，失败时 logger.Error
  - [x] SubTask 7.6.2: 第 125 行同上
  - [x] SubTask 7.6.3: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 7.7: IncrementRetryCountWithNextRetryTime 乐观锁
  - [x] SubTask 7.7.1: `settlement/infrastructure/persistence/mysql/bill_repository.go` 第 197 行 `IncrementRetryCountWithNextRetryTime` 增加 `WHERE id = ? AND retry_count = ?` 条件（传入当前 retry_count）
  - [x] SubTask 7.7.2: 方法签名增加 `currentRetryCount int` 参数
  - [x] SubTask 7.7.3: `RowsAffected == 0` 时返回 error（或特定 sentinel error 表示并发冲突）
  - [x] SubTask 7.7.4: `settlement/service/credit_retry_service.go` 调用处传入当前 retry_count
  - [x] SubTask 7.7.5: `settlement/domain/repository/bill_repository.go` 接口签名同步
  - [x] SubTask 7.7.6: `go build ./...` + `go test ./settlement/...` 通过

- [x] Task 7.8: settlePlayer 回退日志补全
  - [x] SubTask 7.8.1: `settlement/service/game_settle_reporting_service.go` `settlePlayer` 方法 RPC 失败回退 `UpdateGameSettleStatusByUser` 的 `_ =` 改为 `if rollbackErr := ...; rollbackErr != nil { logger.Error("rollback game settle status failed", ...) }`
  - [x] SubTask 7.8.2: `go build ./...` + `go test ./settlement/...` 通过

# Task Dependencies

- **Phase 1**（Task 1.1-1.6）：相互独立，可并行
- **Phase 2**（Task 2.1-2.4）：依赖 Phase 1.1 完成（doc.go 已清理）；Task 2.1 → Task 2.2 → Task 2.3 串行；Task 2.4 可与 Task 2.1 并行
- **Phase 3**（Task 3.1-3.5）：依赖 Task 3.1 完成（SettlementConfig 结构）；Task 3.2-3.5 可并行；可与 Phase 2 并行
- **Phase 4**（Task 4.1-4.4）：Task 4.1 → Task 4.2 → Task 4.3 → Task 4.4 串行；可与 Phase 2/3 并行
- **Phase 5**（Task 5.1-5.2）：依赖 Phase 2 完成（dto 兼容垫片已删）；Task 5.1 → Task 5.2 串行
- **Phase 6**（Task 6.1-6.8）：依赖 Phase 2 + Phase 5 完成；Task 6.1 先行（守卫方法）；Task 6.2-6.7 按 Repository 逐个串行（每步测试通过再下一步）；Task 6.8 依赖 Task 6.2-6.7 完成
- **Phase 7**（Task 7.1-7.8）：依赖 Phase 6 完成；Task 7.1-7.8 大部分可并行（除 Task 7.1 影响多处注入需先完成）

# 执行顺序建议

1. **第一波（并行）**：Phase 1 全部 + Phase 4 全部 + Phase 3.1
2. **第二波（并行）**：Phase 2 + Phase 3.2-3.5
3. **第三波**：Phase 5
4. **第四波**：Phase 6（分子任务串行）
5. **第五波**：Phase 7
