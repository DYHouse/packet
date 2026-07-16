# 移除 session_players.total_profit 字段 Spec

## Why
`session_players.total_profit` 是一个只写不读的冗余字段：唯一的写入点在 session 结束事务中将 Kafka 消息中的 `FinalResult.TotalProfit` 持久化到 DB，但全代码库无任何读取路径。玩家历史统计的 `total_profit` 实际由 `bill_record` 聚合计算（`SUM(profit)`），不依赖该字段。保留该字段会增加无谓的 DB 写入、事务耗时与维护成本。

## What Changes
- 删除 `model.SessionPlayer.TotalProfit` 字段
- 删除 `SessionDBRepository.UpdateSessionPlayerProfit` 接口方法及实现
- 删除 dead code `repository.PlayerStatsUpdate` 结构体（全代码库无构造使用）
- 删除 `game_event_handler.go` 中 session 结束事务里更新 `total_profit` 的循环
- DB schema 变更：`ALTER TABLE session_players DROP COLUMN total_profit`（由 GORM struct tag 作为单一真相源，删除字段后通过 AutoMigrate 或迁移脚本同步）

## Impact
- Affected specs: 无（不影响其他已实现 spec 的语义）
- Affected code:
  - `backend/game/model/session.go`（字段定义）
  - `backend/game/domain/repository/db_repository.go`（接口方法 + dead struct）
  - `backend/game/infrastructure/persistence/mysql/session_repository.go`（方法实现）
  - `backend/game/application/game_event_handler.go`（调用点）

## ADDED Requirements
无新增需求。

## MODIFIED Requirements
### Requirement: Session 结束事务处理
`handleSessionEnd` 在 session 结束事务中仅更新 `game_sessions` 表（`UpdateSessionEnded`），不再循环更新 `session_players.total_profit`。FinalResults 中的盈亏数据仍通过 `bill_record` 聚合查询获得，不受影响。

#### Scenario: Session 正常结束
- **WHEN** Kafka 投递 `session_end` 事件
- **THEN** 事务内仅调用 `UpdateSessionEnded` 更新 `game_sessions` 的 `actual_rounds` / `ended_at` / `end_reason`
- **AND** 不再调用 `UpdateSessionPlayerProfit`
- **AND** 事务正常提交后继续调用 `SettleGame`

#### Scenario: 玩家查询累计统计
- **WHEN** 调用 `GetPlayerStats`
- **THEN** 通过 `AggregatePlayerStatsFromBill` 从 `bill_record` 聚合得到 `total_profit`
- **AND** 返回结果与删除字段前一致

## REMOVED Requirements
### Requirement: session_players.total_profit 字段持久化
**Reason**: 该字段只写不读，盈亏统计实际由 `bill_record` 聚合提供。保留该字段造成无意义的 DB 写入和事务耗时。
**Migration**: 
- 代码层：删除字段定义、接口方法、实现及调用点
- DB 层：执行 `ALTER TABLE session_players DROP COLUMN total_profit`
- 无数据迁移需求（字段本身不被读取，删除不影响任何查询结果）
