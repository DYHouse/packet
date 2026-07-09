# Settlement 模块全面重构 Spec

> 本 Spec 基于 `backend/refactor_docs/SETTLEMENT_MODULE_REFACTOR_DESIGN.md`（1512 行逆向分析与重构设计文档），所有结论可追溯至 settlement/ 源码。重构遵循**不改变业务逻辑**原则，分 7 个 Phase 推进，从低风险死代码清理到高风险 domain 双层落地。

## Why

Settlement 模块整体设计质量较高（Unit of Work / fail-closed / 确定性业务号 / Lua 原子化 / CQRS / 乐观锁 / Processing 中间态等优秀设计已落地），但存在 7 类需重构解决的问题：

1. **架构意图未落地**：domain 双层设计存在但 Repository 契约引用 `model.*` 类型而非 `domain.*`，聚合根守卫方法（CanRefund / CanSettle / CanHandle 等）实际从未被调用
2. **死代码与兼容垫片**：`RefundAppService` 整个文件 5 方法 + 构造函数零调用方；`dto/constants.go` 全部为 domain 重导出别名
3. **不一致**：枚举定义位置（Exception 在 model，其余在 domain）、构造函数返回类型（NewBillRepository 返回接口 vs NewExceptionRepository 返回具体类型）、字段命名（virtualBalance vs virtualBalanceSvc）、json tag 策略
4. **硬编码**：事务超时 `30 * time.Second`、对账 limit `100`、批量扣款并发上限 `20`、`Multiplier: "1"`
5. **模块边界破洞**：`BalanceService` 直接 import `game/domain/room.CalculateRequiredFee`
6. **状态机与实现脱节**：`RoundSettlement` 定义 `Settling(2)/Success(3)/Partial(4)` 三个中间态，但 `creditRound` 直接 Deducted→Credited
7. **职责过载**：`RefundService` 承载申请 / 审批 / 执行 / 查询四类操作

## What Changes

### Phase 1：死代码与一致性清理（低风险）

- **REMOVED** 删除 `settlement/application/refund_app_service.go` 整个文件（RefundAppService 5 方法 + NewRefundAppService 构造函数）
- **MODIFIED** 更新 `settlement/application/doc.go` 移除 RefundAppService 描述
- **MODIFIED** 重命名 `settlement/model/platform_settle_log.go` → `settlement/model/platform_call_log.go`（文件名与结构体/表名一致）
- **MODIFIED** 统一 `BalanceService` / `BalanceQueryService` 字段命名：`virtualBalanceSvc` → `virtualBalance`
- **MODIFIED** 统一 `NewExceptionRepository` / `NewPlatformCallLogRepository` 返回接口类型（与 `NewBillRepository` 一致）
- **MODIFIED** 补全 `dto/response.go` 中 `FirstRoundDeductResult` / `FailedPlayerInfo` / `RefundResult` / `BillQueryResult` 的 json tag
- **MODIFIED** `game_settle_reporting_service.go:265` `Multiplier: "1"` 抽为常量

### Phase 2：枚举统一与兼容垫片移除（中风险）

- **MODIFIED** Exception 枚举（`ExceptionType` / `ExceptionStatus` / `HandleType` 及常量）从 `model/exception_record.go` 迁入 `domain/exception.go`；`model/exception_record.go` 改为 type alias 引用 domain（反转当前依赖方向）
- **MODIFIED** `PlatformCallLog` 状态常量迁入 domain（若存在）
- **MODIFIED** 全模块 `dto.BillType*` / `dto.BillStatus*` / `dto.DeductScene*` / `dto.RoundStatus*` / `dto.ReconcileStatus*` / `dto.RefundStatus*` / `dto.RefundType*` / `dto.ReconcileType*` / `dto.ReconcileScope*` / `dto.GameSettleStatus*` / `dto.BillGameSettle*` 共 219 处引用迁移为 `domain.*`
- **REMOVED** 删除 `settlement/dto/constants.go` 中所有 domain 重导出别名（保留 `TraceTypePenaltyDeduct` / `TraceTypePenaltyDist` / `PlatformAccountID` / `MaxRetryCount` / `PenaltyRoundID` / `CreditRetryBaseDelay` / `CreditRetryMaxDelay` 这些 DTO 层特有常量）

### Phase 3：配置化硬编码（低风险）

- **ADDED** `settlement/config/config.go` 增加 `SettlementConfig` 结构（TransactionTimeout / SettlementCheckLimit / MaxConcurrentDeduct / CreditRetryBaseDelay / CreditRetryMaxDelay / MaxRetryCount）
- **ADDED** `common/config/settlement_scheduler.go` `SetSettlementSchedulerDefaults` 增加默认值设置
- **MODIFIED** `infrastructure/persistence/mysql/db_repository.go:60` `30 * time.Second` 改为从配置注入
- **MODIFIED** `service/settlement_check_service.go:40,59` 硬编码 `100` 改为从配置注入；`RunSettlementCheck` 增加 limit 参数透传
- **MODIFIED** `service/deduct_service.go:23` `defaultMaxConcurrentDeduct = 20` 改为从配置注入
- **MODIFIED** `SchedulerAppService.RunSettlementCheck` 签名增加 limit 参数；`SettlementCheckScheduler` 透传 limit
- **MODIFIED** `SettlementCheckScheduler` 构造时从 `SettlementSchedulerSubConfig.Limit` 读取 limit（已存在 Limit 字段，仅需透传）

### Phase 4：模块边界修复（中风险）

- **ADDED** `settlement/domain/fee_calculator.go` 新增 `FeeCalculator` 接口：`CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64`
- **ADDED** `game/infrastructure/adapter/fee_calculator_adapter.go` 新增 `FeeCalculatorAdapter` 实现 domain.FeeCalculator，内部委托 `room.CalculateRequiredFee`
- **MODIFIED** `settlement/service/balance_service.go` 移除 `game/domain/room` import，依赖 `domain.FeeCalculator` 接口；构造函数增加 feeCalculator 参数
- **MODIFIED** `game/bootstrap/container.go` 构造 `BalanceService` 时注入 `FeeCalculatorAdapter`
- **验收**：`go list -deps github.com/cashparty/backend/settlement/...` 输出无 `game/` 路径

### Phase 5：状态机一致性（中风险）

- **REMOVED** 删除 `settlement/domain/round_settlement.go` 中 `RoundStatusSettling(2)` / `RoundStatusSuccess(3)` / `RoundStatusPartial(4)`（已 grep 确认仅 domain 定义 + dto 兼容垫片引用，无业务代码使用）
- **REMOVED** 删除 `settlement/dto/constants.go` 中对应 3 个别名（Phase 2 已迁移，此处仅需确保删除）
- **MODIFIED** 更新 `settlement/domain/round_settlement.go` 状态流转注释：`Deducting(0) → Deducted(1) → Credited(6)` / `Deducting(0) → Failed(5)` / `Deducted(1) → Failed(5)`
- **MODIFIED** 更新 `settlement/domain/refund.go` 状态机注释明确 `Approved(2)` 是过渡态（被 `UpdateRefundAuditStatus` 设置后立即进入 `executeRefund`）

### Phase 6：domain 双层落地（高风险，可选）

- **MODIFIED** `settlement/domain/repository/bill_repository.go` 接口签名 `model.*` → `domain.*`（20 方法）
- **MODIFIED** `settlement/domain/repository/round_settlement_repository.go` 接口签名 `model.*` → `domain.*`（13 方法）
- **MODIFIED** `settlement/domain/repository/refund_audit_repository.go` 接口签名 `model.*` → `domain.*`（9 方法）
- **MODIFIED** `settlement/domain/repository/exception_repository.go` 接口签名 `model.*` → `domain.*`
- **MODIFIED** `settlement/domain/repository/platform_call_log_repository.go` 接口签名 `model.*` → `domain.*`（若已迁入 domain）
- **MODIFIED** `settlement/domain/repository/settlement_query_repository.go` 接口签名 `model.*` → `domain.*`（4 方法）
- **MODIFIED** `settlement/infrastructure/persistence/mysql/*.go` 实现层增加 domain ↔ model 转换函数
- **MODIFIED** `settlement/service/*.go` Service 层调用聚合根守卫方法（`bill.TransitionTo(Processing)` 等）
- **MODIFIED** `settlement/domain/bill.go` 增加 `TransitionTo(newStatus int) error` 守卫方法
- **MODIFIED** `settlement/domain/round_settlement.go` 增加 `TransitionTo(newStatus int) error`
- **MODIFIED** `settlement/domain/refund.go` 增加 `TransitionTo(newStatus int) error`
- **MODIFIED** `settlement/model/*.go` 移除业务枚举（仅保留 GORM tag 与 TableName）
- **风险控制**：分子任务逐步迁移（先 BillRepository，再 RoundSettlement，再 RefundAudit，再 Exception，再 PlatformCallLog，再 SettlementQuery），每步编译 + 测试通过再进行下一步

### Phase 7：职责拆分与补全（中风险，可选）

- **MODIFIED** 拆分 `settlement/service/refund_service.go` 为 `refund_apply_service.go` / `refund_execute_service.go` / `refund_query_service.go`（ApplyForRefund / ApproveRefund+executeRefund+RejectRefund / GetRefundAuditByOrderNo+GetRefundsByStatus）
- **MODIFIED** `settlement/application/scheduler_app_service.go` 调整注入拆分后的 RefundService
- **MODIFIED** 合并 `BalanceService` + `BalanceQueryService` 或明确职责拆分边界
- **ADDED** 补全 `settlement/domain/repository/exception_repository.go` 查询/处理方法（GetByID / GetByStatus / UpdateStatus 等）+ 实现层补全
- **ADDED** `settlement/service/penalty_settlement_service.go` `DeductPenaltyToPlatform` 增加 Redis 分布式锁（lock key 含 userID + roundTraceID）
- **MODIFIED** `settlement/infrastructure/persistence/mysql/platform_call_log_repository.go:28,60` 检查 `json.Marshal` error
- **MODIFIED** `settlement/infrastructure/persistence/redis/virtual_balance_repository.go:118,125` 检查 `SAdd` error
- **MODIFIED** `settlement/infrastructure/persistence/mysql/bill_repository.go:197` `IncrementRetryCountWithNextRetryTime` 增加乐观锁条件 `WHERE retry_count = ?`
- **MODIFIED** `settlement/service/game_settle_reporting_service.go` `settlePlayer` RPC 失败回退 `UpdateGameSettleStatusByUser` 的 `_ =` 改为记录 error 日志

## Impact

- **Affected specs**：无（settlement 模块无前置 spec）
- **Affected code**：
  - `settlement/application/`：删除 refund_app_service.go，修改 doc.go / scheduler_app_service.go / settle_app_service.go
  - `settlement/domain/`：新增 fee_calculator.go，修改 bill.go / round_settlement.go / refund.go / exception.go / repository/*.go
  - `settlement/dto/`：删除 constants.go 大部分，修改 response.go
  - `settlement/model/`：重命名 platform_settle_log.go，修改 exception_record.go / bill.go / refund.go
  - `settlement/service/`：修改 balance_service.go / balance_query_service.go / deduct_service.go / settlement_check_service.go / game_settle_reporting_service.go / refund_service.go / penalty_settlement_service.go
  - `settlement/infrastructure/persistence/mysql/`：修改 db_repository.go / bill_repository.go / refund_audit_repository.go / round_settlement_repository.go / settlement_query_repository.go / exception_repository.go / platform_call_log_repository.go
  - `settlement/infrastructure/persistence/redis/`：修改 virtual_balance_repository.go
  - `settlement/scheduler/`：修改 settlement_check_scheduler.go
  - `settlement/config/`：修改 config.go
  - `game/bootstrap/container.go`：注入 FeeCalculatorAdapter
  - `game/infrastructure/adapter/`：新增 fee_calculator_adapter.go
  - `common/config/settlement_scheduler.go`：补全默认值
- **Affected tests**：`settlement/service/*_test.go`（mocks 与签名变更同步）
- **Business logic impact**：零（所有 Phase 不得改变业务逻辑）

## ADDED Requirements

### Requirement: FeeCalculator 接口（Phase 4）

The system SHALL provide a `domain.FeeCalculator` interface in `settlement/domain/fee_calculator.go` to decouple settlement from `game/domain/room`.

```go
// FeeCalculator 计算开局所需总费用（解耦 settlement 对 game/domain/room 的依赖）。
type FeeCalculator interface {
    CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64
}
```

#### Scenario: Settlement 模块零 game 编译依赖
- **WHEN** 执行 `go list -deps github.com/cashparty/backend/settlement/...`
- **THEN** 输出列表不包含任何 `github.com/cashparty/backend/game/` 路径
- **AND** `settlement/service/balance_service.go` 不再 import `game/domain/room`

#### Scenario: FeeCalculator adapter 透传原逻辑
- **WHEN** `BalanceService.CheckBalanceForReady` 调用 `feeCalculator.CalculateRequiredFee(roomFee, maxPlayers, maxRounds)`
- **THEN** 返回值与原 `room.CalculateRequiredFee(roomFee, maxPlayers, maxRounds)` 完全一致
- **AND** 业务行为零变更

### Requirement: SettlementConfig 配置注入（Phase 3）

The system SHALL provide a `SettlementConfig` struct in `settlement/config/config.go` to inject all hardcoded parameters via configuration.

#### Scenario: 事务超时配置化
- **WHEN** `dbRepositoryImpl.WithTransaction` 执行
- **THEN** 事务超时通过 `SettlementConfig.TransactionTimeout` 控制
- **AND** 默认值 30s 与原硬编码一致
- **AND** 配置可覆盖默认值

#### Scenario: 对账 limit 配置化
- **WHEN** `SettlementCheckScheduler.execute` 触发 `RunSettlementCheck`
- **THEN** limit 通过 `SettlementSchedulerSubConfig.Limit` 透传到 `SettlementCheckService.CheckFirstRoundDeductFailure` / `CheckDeductedButNotSettled`
- **AND** 默认值 100 与原硬编码一致

#### Scenario: 批量扣款并发上限配置化
- **WHEN** `DeductService.BatchDeduct` 执行
- **THEN** 并发上限通过 `SettlementConfig.MaxConcurrentDeduct` 控制
- **AND** 默认值 20 与原硬编码一致

### Requirement: 聚合根守卫方法（Phase 6）

The system SHALL provide `TransitionTo(newStatus int) error` guard methods on domain aggregate roots (BillRecord / RoundSettlement / RefundAudit) to enforce state machine invariants.

#### Scenario: 非法状态转换被拒绝
- **WHEN** Service 层调用 `bill.TransitionTo(Success)` 但 bill 当前状态为 `Refunded`
- **THEN** 返回 error 描述非法转换
- **AND** 业务逻辑不变（仅增加前置校验）

#### Scenario: 守卫方法在 Service 层被调用
- **WHEN** `DeductService.executeSingleDeduct` 准备更新 bill 状态
- **THEN** 先调用 `bill.TransitionTo(Processing)` 校验
- **AND** 校验失败则中止本次扣款

### Requirement: ExceptionRepository 查询补全（Phase 7）

The system SHALL extend `ExceptionRepository` interface with query/handling methods to close the exception handling loop.

#### Scenario: 按状态查询异常记录
- **WHEN** admin 查询 Pending 状态异常记录
- **THEN** `ExceptionRepository.GetByStatus(ctx, status, limit, offset)` 返回匹配记录

#### Scenario: 更新异常处理状态
- **WHEN** admin 处理异常记录
- **THEN** `ExceptionRepository.UpdateStatus(ctx, id, newStatus, handleType, handleRemark, handledBy)` 更新成功

## MODIFIED Requirements

### Requirement: 枚举单一事实来源（Phase 2）

所有 settlement 业务枚举 SHALL 定义在 `settlement/domain/` 下，`model/` 层仅保留 GORM 实体字段类型（通过 type alias 引用 domain），`dto/constants.go` 不再重导出 domain 枚举。

#### Scenario: Exception 枚举定义在 domain
- **WHEN** 任意代码引用 `ExceptionTypeDebitFailed`
- **THEN** 通过 `domain.ExceptionTypeDebitFailed` 引用
- **AND** `model.ExceptionType` 为 `domain.ExceptionType` 的 type alias

#### Scenario: dto 不再重导出 domain 枚举
- **WHEN** 任意代码引用 `dto.BillTypeFirstRoundDeduct`
- **THEN** 编译失败（已迁移为 `domain.BillTypeFirstRoundDeduct`）
- **AND** `dto/constants.go` 仅保留 DTO 层特有常量（TraceType / PlatformAccountID / MaxRetryCount / PenaltyRoundID / CreditRetryBaseDelay / CreditRetryMaxDelay）

### Requirement: 状态机与实现一致（Phase 5）

`RoundSettlement` 状态机定义 SHALL 与实际业务流转一致，未使用的中间态 SHALL 被移除。

#### Scenario: 移除未使用中间态
- **WHEN** Phase 5 完成
- **THEN** `domain/round_settlement.go` 不再定义 `RoundStatusSettling` / `RoundStatusSuccess` / `RoundStatusPartial`
- **AND** 状态流转注释为：`Deducting(0) → Deducted(1) / Failed(5)` / `Deducted(1) → Credited(6) / Failed(5)` / `Credited(6) / Failed(5) 为终态`

### Requirement: Repository 契约使用 domain 类型（Phase 6）

所有 Repository 接口签名 SHALL 引用 `domain.*` 类型而非 `model.*` 类型，实现层负责 domain ↔ model 转换。

#### Scenario: BillRepository 接口签名
- **WHEN** Phase 6 完成
- **THEN** `domain/repository/bill_repository.go` 所有方法参数与返回值使用 `*domain.BillRecord`
- **AND** `infrastructure/persistence/mysql/bill_repository.go` 实现层在入口转 model、出口转 domain

### Requirement: 一致性命名（Phase 1）

构造函数返回类型、字段命名、文件名 SHALL 全局一致。

#### Scenario: 构造函数统一返回接口
- **WHEN** Phase 1 完成
- **THEN** `NewExceptionRepository` / `NewPlatformCallLogRepository` 返回接口类型（`repository.ExceptionRepository` / `repository.PlatformCallLogRepository`）

#### Scenario: 字段命名统一
- **WHEN** Phase 1 完成
- **THEN** `BalanceService` 与 `BalanceQueryService` 的 virtualBalance 字段命名一致（`virtualBalance`）

#### Scenario: 文件名与结构体一致
- **WHEN** Phase 1 完成
- **THEN** `model/platform_settle_log.go` 重命名为 `model/platform_call_log.go`
- **AND** 结构体名 `PlatformCallLog` 与表名 `platform_call_log` 不变

## REMOVED Requirements

### Requirement: RefundAppService

**Reason**: `RefundAppService` 5 方法（ApplyForRefund / ApproveRefund / RejectRefund / GetRefundAuditByOrderNo / GetRefundsByStatus）+ `NewRefundAppService` 构造函数在整个 backend 中零调用方。退款逻辑实际通过 `SchedulerAppService.ProcessPendingRefunds`（自动）与 `SettlementCheckService.ensureRefundCreated`（自动）承载。`doc.go` 声称的"外部调用方应通过本 facade 调用"与代码矛盾。

**Migration**: 无需迁移。删除 `settlement/application/refund_app_service.go` 整个文件 + 更新 `settlement/application/doc.go` 移除 RefundAppService 描述。退款业务逻辑不受影响（已由 SchedulerAppService / SettlementCheckService 承载）。

### Requirement: dto/constants.go domain 重导出别名

**Reason**: `dto/constants.go` 中 `BillType*` / `BillStatus*` / `DeductScene*` / `RoundStatus*` / `ReconcileStatus*` / `RefundStatus*` / `RefundType*` / `ReconcileType*` / `ReconcileScope*` / `GameSettleStatus*` / `BillGameSettle*` 全部为 domain 常量的重导出别名，违反单一事实来源原则。注释明确"兼容垫片，新代码应直接引用 domain"。

**Migration**: 全模块 219 处 `dto.X` 引用替换为 `domain.X`（19 个文件），然后删除 `dto/constants.go` 中所有重导出部分，仅保留 DTO 层特有常量（TraceTypePenaltyDeduct / TraceTypePenaltyDist / PlatformAccountID / MaxRetryCount / PenaltyRoundID / CreditRetryBaseDelay / CreditRetryMaxDelay）。

### Requirement: RoundSettlement 未使用中间态

**Reason**: `RoundStatusSettling(2)` / `RoundStatusSuccess(3)` / `RoundStatusPartial(4)` 定义但 `creditRound` 直接 Deducted→Credited，未经过这些态。已 grep 确认仅 domain 定义 + dto 兼容垫片引用，无业务代码使用。

**Migration**: 直接删除 3 个常量定义 + 更新状态流转注释。无业务行为影响。

---

## 不可触碰的红线

- **任何 Phase 都不得改变业务逻辑**（核心要求）
- **fail-closed 三道防线不得放松**：IsRobot error 中止 / ParseAmount 失败标 Failed / robotChecker nil 检查
- **幂等机制不得移除**：BizOrderNo 确定性 / 乐观锁 WHERE / Processing 中间态 + 回退
- **事务边界不得扩大**：短事务原则，RPC 不在事务内
- **Lua 原子化不得回退**：虚拟余额操作必须 Lua
- **删除任何代码前必须 grep 确认无引用 + 分析影响范围**

## 验收原则

- 每个 Phase 完成后：`go build ./...` + `go test ./settlement/... ./game/...` 通过
- 每个 Phase 完成后：grep 确认无残留旧引用
- 每个 Phase 独立可回滚
- 资金安全相关（fail-closed / 幂等 / 乐观锁）在任何 Phase 都不得放松
