# Round 罚款关联重构 Spec

## Why

罚款扣款（`bill_type=8`）与罚款分发（`bill_type=10`）的 `round_id` 大量为 `0`，无法关联到具体 round。根因是罚款发生在 inter-round 过渡期（上一轮已结算、下一轮未创建），`meta.CurrentRoundID` 已被清除。同时 `PenaltyRecord` 仅存 Redis（TTL 过期丢失），`PenaltyDistribution` 完全未持久化，导致后期无法按 round 维度分析罚款、无法对账。此外 `PenaltyTypeLeaveDuringGame` 和 `PenaltyTypeDisconnectTimeout` 两个常量属于死代码。

## What Changes

### 核心方案：预创建 Pending Round + 复用
- `OnSendTimeout` / `OnReplaceTimeout` 触发罚款时，预创建下一轮 Pending round，罚款关联到该 roundID
- `createRoundRecord` 增加查询复用逻辑：已有 Pending round 则复用，无则创建
- `RoundDBRepository` 新增 `GetRoundBySessionAndRoundNo` 方法

### 数据持久化
- **BREAKING** 新增 `penalty_records` 表：持久化 PenaltyRecord（含 roundID 关联）
- **BREAKING** 新增 `penalty_distributions` 表：持久化 PenaltyDistribution（含 roundID 关联）
- **BREAKING** 新增 `penalty_distribution_recipients` 表：持久化分发接收方明细
- `BillRecord` 新增 4 个关联字段：`PenaltyType` / `PenaltyRecordID` / `SpecialRewardID` / `PenaltyDistributionID`
- `rounds` 表 `idx_session_roundno` 升级为唯一索引（兜底防并发重复创建）

### DTO 与 TraceID 增强
- `PenaltyDistributeRequest` 新增 `RoundNo` 和 `TriggerPhase` 字段
- `GeneratePenaltyDistTraceID` 增加 `roundNo` 参数（可追溯性改进，非 bug 修复——当前每个 session 仅一次分发，无冲突）

### 死代码清理
- 删除 `PenaltyTypeLeaveDuringGame` 和 `PenaltyTypeDisconnectTimeout` 常量及其 `String()` / `ParsePenaltyType()` 分支

## Impact

### 受影响的代码
- `game/domain/round/penalty.go` — 删除死代码常量
- `game/domain/repository/db_repository.go` — RoundDBRepository 新增方法，Transaction/DBRepository 新增 PenaltyRecordRepo
- `game/infrastructure/persistence/mysql/round_repository.go` — 实现 GetRoundBySessionAndRoundNo
- `game/application/game_lifecycle_timeout.go` — OnSendTimeout/OnReplaceTimeout 改用 ensureNextRound
- `game/application/game_lifecycle_service.go` — 新增 ensureNextRound 辅助方法
- `game/application/packet_round_init.go` — createRoundRecord 增加复用逻辑
- `game/application/penalty_service.go` — ApplyPenalty 增加 DB 持久化
- `game/model/round.go` — idx_session_roundno 升级为唯一索引
- `game/model/penalty_record.go` — 新增 GORM 模型
- `game/infrastructure/persistence/mysql/penalty_record_repository.go` — 新增仓储实现
- `settlement/domain/repository/transaction.go` — Transaction/DBRepository 新增 PenaltyDistributionRepo
- `settlement/service/penalty_settlement_service.go` — DistributePenaltyFromPlatform 增加持久化
- `settlement/service/trace_id_generator.go` — GeneratePenaltyDistTraceID 增加 roundNo 参数
- `settlement/dto/request.go` — PenaltyDistributeRequest 新增字段
- `settlement/model/bill.go` — BillRecord 新增关联字段
- `settlement/model/penalty_distribution.go` — 新增 GORM 模型
- `settlement/infrastructure/persistence/mysql/penalty_distribution_repository.go` — 新增仓储实现
- `common/rediskeys/keys.go` — 无新增（TraceID 改用 roundNo，无需 Redis seq）

### 不受影响
- Lua 脚本不修改
- stats 模块不在本次重构范围
- 历史数据不回填（接受 round_id=0 的历史记录缺失）

## ADDED Requirements

### Requirement: 预创建 Pending Round

系统在 inter-round 罚款场景下 SHALL 预创建下一轮 Pending round，使罚款账目关联到正确的 roundID。

#### Scenario: OnSendTimeout 预创建 round
- **WHEN** OnSendTimeout 触发且 `meta.CurrentRoundID` 为空
- **THEN** 系统通过 `ensureNextRound` 查询或创建下一轮（roundNo = CurrentRound + 1）Pending round
- **AND** 罚款扣款 bill 的 `round_id` 设为该 roundID
- **AND** 若 ensureNextRound 失败，降级为 roundID=0，不阻塞罚款流程

#### Scenario: OnReplaceTimeout 预创建 round
- **WHEN** OnReplaceTimeout 触发
- **THEN** 系统通过 `ensureNextRound` 查询或创建下一轮 Pending round
- **AND** 罚款分发 bill 的 `round_id` 设为该 roundID
- **AND** `TriggerPhase` 设为 `inter_round`

#### Scenario: createRoundRecord 复用预创建 round
- **WHEN** SendPacket 调用 createRoundRecord 且该 roundNo 已存在 Pending round
- **THEN** 复用已有 round，保留原 roundID 和 created_at
- **AND** 不创建重复 round 记录

#### Scenario: 踢人+无替补+游戏中断
- **WHEN** OnSendTimeout 预创建 round 后 handleKickAndReplace 无替补
- **THEN** EndGameWithOptions 结束游戏
- **AND** round 保持 Pending 状态（语义：预创建但未发包，游戏已结束）

### Requirement: PenaltyRecord DB 持久化

系统 SHALL 将 PenaltyRecord 持久化到 DB，包含 roundID 关联，不再仅依赖 Redis。

#### Scenario: ApplyPenalty 持久化
- **WHEN** ApplyPenalty 的 Lua 脚本执行成功
- **THEN** 同步创建 `penalty_records` 记录，包含 roomID/sessionID/roundID/roundNo/userID/penaltyType/amount/count/kickRequired
- **AND** `deduct_status` 初始为 Processing(0)
- **AND** 持久化失败时记录日志，不阻塞主流程（Redis 已写入，后续对账补偿）

### Requirement: PenaltyDistribution DB 持久化

系统 SHALL 将 PenaltyDistribution 及其接收方明细持久化到 DB，包含 roundID 关联。

#### Scenario: DistributePenaltyFromPlatform 持久化
- **WHEN** DistributePenaltyFromPlatform 成功创建 bills
- **THEN** 在同一事务内创建 `penalty_distributions` 记录和 `penalty_distribution_recipients` 明细
- **AND** 持久化失败时记录日志，不阻塞主流程（bills 已创建）

### Requirement: BillRecord 关联字段

系统 SHALL 在 BillRecord 上新增关联字段，便于跨表查询。

#### Scenario: 罚款 bill 关联
- **WHEN** 创建罚款扣款 bill（BillType=8）
- **THEN** 填充 `penalty_type` 和 `penalty_record_id` 字段
- **WHEN** 创建罚款分发 bill（BillType=10）
- **THEN** 填充 `penalty_distribution_id` 字段

## MODIFIED Requirements

### Requirement: RoundDBRepository

RoundDBRepository 接口新增 `GetRoundBySessionAndRoundNo` 方法，按 sessionID + roundNo 查询 round，利用现有 `idx_session_roundno` 复合索引。

### Requirement: createRoundRecord

createRoundRecord 方法修改为：先查询是否已有 Pending round（罚款时预创建的），有则复用，无则创建新 round。复用时保留原 roundID 和 created_at。

### Requirement: GeneratePenaltyDistTraceID

GeneratePenaltyDistTraceID 方法签名变更，增加 `roundNo int` 参数，输出格式从 `PENALTY_DIST_{roomID}_{sessionID}` 改为 `PENALTY_DIST_{roomID}_{sessionID}_{roundNo}`。提升可追溯性，便于按 round 维度定位分发账目。

### Requirement: PenaltyDistributeRequest

PenaltyDistributeRequest 新增 `RoundNo int` 和 `TriggerPhase string` 字段。`TriggerPhase` 取值：`inter_round`（轮间触发）/ `in_round`（轮内触发）。

### Requirement: rounds 表唯一索引

rounds 表的 `idx_session_roundno`（session_id + round_no）从普通索引升级为唯一索引，作为并发兜底防止重复 round 创建。升级前需检查并清理可能的重复数据。

## REMOVED Requirements

### Requirement: PenaltyTypeLeaveDuringGame 和 PenaltyTypeDisconnectTimeout

**Reason**: 两个罚款类型常量无任何调用点，属于死代码。
**Migration**: 直接删除常量定义及其在 `String()` 和 `ParsePenaltyType()` 中的分支。`PenaltyTypeSendTimeout` 的 iota 值保持为 1 不变（因 iota + 1 起始，删除后续常量不影响已有值）。PenaltyType int 值不直接持久化（仅通过 `.String()` 转为字符串存储），无数据兼容风险。

## 设计决策

1. **TriggerPhase 用 string 类型**：与现有 `PenaltyType.String()` 和 `Reason` 字符串模式一致，DB 查询可读性好。
2. **历史数据不回填**：现有 `bill_record` 中 `round_id=0` 的罚款记录无法恢复真实 roundID，接受历史数据缺失。新代码产生的罚款将有正确的 roundID。
3. **OnReplaceTimeout 并发安全**：OnReplaceTimeout 触发时游戏已 Interrupted，不会有 SendPacket 调用。`ReplaceTimeoutLockKey` 覆盖所有并发路径。
4. **created_at 保留策略**：复用预创建 round 时不更新 `created_at`，反映 round 的真实生命周期（从罚款预创建开始）。
5. **TraceID 非 bug 修复**：经核实，`DistributePenaltyFromPlatform` 每个 session 仅调用一次（OnReplaceTimeout 后 EndGameWithOptions 结束游戏），当前 traceID 无冲突。增加 roundNo 参数是可追溯性改进，非 P0 bug 修复。
6. **PenaltyRecord 仓储位置**：PenaltyRecord 属于 game 域概念，仓储定义在 `game/domain/repository`，持久化在 `game/infrastructure/persistence/mysql`。
7. **PenaltyDistribution 仓储位置**：PenaltyDistribution 在 settlement 域内持久化（DistributePenaltyFromPlatform 事务内），仓储定义在 `settlement/domain/repository`。
8. **降级策略**：ensureNextRound 失败时降级为 roundID=0；PenaltyRecord/Distribution 持久化失败时仅记录日志不阻塞主流程。
