# 复合索引迁移至 struct tag 并清理 migrations 目录 Spec

## Why
项目尚未上线，migrations 目录下 5 个 SQL 脚本不会被自动执行（项目无迁移框架），其中 3 个脚本承载的复合索引未声明在 GORM model 的 struct tag 中，导致 `AutoMigrate` 无法重建这些索引。应将所有索引统一收口到 struct tag（单一事实来源），再删除 SQL 脚本，避免上线后索引丢失。

## What Changes
- 在 `SessionPlayer`、`RoundGrabRecord`、`Round`、`BillRecord`、`PlatformCallLog` 五个 model 的 struct tag 中补充 12 个复合索引声明
- 将原单列 `gorm:"index"` 替换为复合索引（使用同名 `index:idx_xxx,priority:N` 跨字段声明）
- 删除 5 个 SQL 迁移文件：
  - `backend/migrations/20260625_add_player_history_indexes.sql`
  - `backend/migrations/20260626_add_bill_history_indexes.sql`
  - `backend/migrations/20260709_upgrade_platform_call_log.sql`
  - `backend/settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index.sql`
  - `backend/settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index_v2.sql`
- 删除空目录（若删除后目录无文件）

## Impact
- Affected specs: 无（纯 DDL 收口）
- Affected code:
  - `backend/game/model/session.go`
  - `backend/game/model/round.go`
  - `backend/settlement/model/bill.go`
  - `backend/settlement/model/platform_call_log.go`
- 业务逻辑零变更，仅影响索引声明
- 现有数据库不受影响（上线前可重建库）；若已存在数据库，需重新执行 `scripts/init_rooms.go` 的 `AutoMigrate` 或手动 DROP 旧索引

## ADDED Requirements

### Requirement: 复合索引声明收口至 struct tag

系统 SHALL 将所有多列复合索引通过 GORM struct tag 的 `index:<name>,priority:<N>` 语法声明，使得 `AutoMigrate` 能够一次性创建全部索引，无需额外执行 SQL 脚本。

#### Scenario: 初始化数据库
- **WHEN** 执行 `scripts/init_rooms.go` 的 `AutoMigrate`
- **THEN** 以下 12 个复合索引被自动创建：
  - `session_players.idx_user_joined (user_id, joined_at)`
  - `round_grab_records.idx_session_user (session_id, user_id, grabbed_at)`
  - `rounds.idx_session_roundno (session_id, round_no)`
  - `rounds.idx_session_sender (session_id, sender_id, round_no)`
  - `bill_record.idx_user_status_session (user_id, status, session_id)`
  - `bill_record.idx_session_user_type (session_id, user_id, bill_type, status)`
  - `bill_record.idx_user_session (user_id, session_id, created_at)`
  - `platform_call_log.idx_biz_call_time (biz_order_no, call_type, request_time)`
  - `platform_call_log.idx_status_time (status, request_time)`
  - `platform_call_log.idx_user_time (user_id, request_time)`
  - `platform_call_log.idx_session_time (session_id, request_time)`
  - `platform_call_log.idx_trace_id (trace_id)` （已存在，保留）

#### Scenario: migrations 目录清理
- **WHEN** 完成索引迁移后
- **THEN** `backend/migrations/` 目录下的 3 个 SQL 文件被删除
- **AND** `backend/settlement/infrastructure/persistence/mysql/migrations/` 目录下的 2 个 SQL 文件被删除
- **AND** 若目录变空，则删除空目录

### Requirement: 索引名称与原 SQL 脚本保持一致

系统 SHALL 保留原 SQL 脚本中定义的索引名称，便于运维识别和 EXPLAIN 验证。

#### Scenario: 索引名称一致性
- **WHEN** `AutoMigrate` 创建索引
- **THEN** 索引名称与原 SQL 脚本中的名称完全一致（如 `idx_user_joined`、`idx_session_user` 等）

## MODIFIED Requirements

### Requirement: SessionPlayer 索引声明

`SessionPlayer` struct 的 `UserID` 和 `JoinedAt` 字段 SHALL 声明复合索引 `idx_user_joined`，priority 分别为 1 和 2。

### Requirement: RoundGrabRecord 索引声明

`RoundGrabRecord` struct 的 `SessionID`、`UserID`、`GrabbedAt` 字段 SHALL 声明复合索引 `idx_session_user`，priority 分别为 1、2、3。

### Requirement: Round 索引声明

`Round` struct SHALL 调整索引声明：
- `SessionID` 和 `RoundNo` 声明复合索引 `idx_session_roundno`，priority 分别为 1 和 2（替换原单字段 `idx_session_round`）
- `SessionID`、`SenderID`、`RoundNo` 声明复合索引 `idx_session_sender`，priority 分别为 1、2、3

### Requirement: BillRecord 索引声明

`BillRecord` struct SHALL 补充 3 个复合索引：
- `idx_user_status_session (user_id, status, session_id)`
- `idx_session_user_type (session_id, user_id, bill_type, status)`
- `idx_user_session (user_id, session_id, created_at)`

### Requirement: PlatformCallLog 索引声明

`PlatformCallLog` struct SHALL 补充 4 个复合索引：
- `idx_biz_call_time (biz_order_no, call_type, request_time)`
- `idx_status_time (status, request_time)`
- `idx_user_time (user_id, request_time)`
- `idx_session_time (session_id, request_time)`

## REMOVED Requirements

### Requirement: 手动执行 SQL 迁移脚本

**Reason**: 项目未接入迁移框架，SQL 脚本不会被自动执行；索引已收口至 struct tag，`AutoMigrate` 可直接创建。
**Migration**: 上线前重新执行 `scripts/init_rooms.go` 即可，无需保留 SQL 文件。
