# Tasks

- [x] Task 1: 为 SessionPlayer 补充复合索引 idx_user_joined
  - [x] SubTask 1.1: 修改 `backend/game/model/session.go` 中 `SessionPlayer` 结构，为 `UserID` 添加 `index:idx_user_joined,priority:1`，为 `JoinedAt` 添加 `index:idx_user_joined,priority:2`
  - [x] SubTask 1.2: 验证 `go build ./...` 通过

- [x] Task 2: 为 RoundGrabRecord 和 Round 补充复合索引
  - [x] SubTask 2.1: 修改 `backend/game/model/round.go` 中 `RoundGrabRecord`，为 `SessionID` 添加 `index:idx_session_user,priority:1`，为 `UserID` 添加 `index:idx_session_user,priority:2`，为 `GrabbedAt` 添加 `index:idx_session_user,priority:3`
  - [x] SubTask 2.2: 修改 `backend/game/model/round.go` 中 `Round`，将 `SessionID` 的 `index` 替换为 `index:idx_session_roundno,priority:1;index:idx_session_sender,priority:1`，将 `RoundNo` 的 `index:idx_session_round` 替换为 `index:idx_session_roundno,priority:2;index:idx_session_sender,priority:3`，为 `SenderID` 添加 `index:idx_session_sender,priority:2`
  - [x] SubTask 2.3: 验证 `go build ./...` 通过

- [x] Task 3: 为 BillRecord 补充 3 个复合索引
  - [x] SubTask 3.1: 修改 `backend/settlement/model/bill.go` 中 `BillRecord`，补充 `idx_user_status_session`、`idx_session_user_type`、`idx_user_session` 三个复合索引的 tag 声明（注意 `UserID`、`SessionID`、`BillType`、`Status`、`CreatedAt` 字段需附加多个 index 优先级）
  - [x] SubTask 3.2: 验证 `go build ./...` 通过

- [x] Task 4: 为 PlatformCallLog 补充 4 个复合索引
  - [x] SubTask 4.1: 修改 `backend/settlement/model/platform_call_log.go`，补充 `idx_biz_call_time`、`idx_status_time`、`idx_user_time`、`idx_session_time` 四个复合索引的 tag 声明（注意 `BizOrderNo`、`CallType`、`Status`、`UserID`、`SessionID`、`RequestTime` 字段需附加多个 index 优先级）
  - [x] SubTask 4.2: 验证 `go build ./...` 通过

- [x] Task 5: 删除 5 个 SQL 迁移文件及空目录
  - [x] SubTask 5.1: 删除 `backend/migrations/20260625_add_player_history_indexes.sql`
  - [x] SubTask 5.2: 删除 `backend/migrations/20260626_add_bill_history_indexes.sql`
  - [x] SubTask 5.3: 删除 `backend/migrations/20260709_upgrade_platform_call_log.sql`
  - [x] SubTask 5.4: 删除 `backend/migrations/` 空目录
  - [x] SubTask 5.5: 删除 `backend/settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index.sql`
  - [x] SubTask 5.6: 删除 `backend/settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index_v2.sql`
  - [x] SubTask 5.7: 删除 `backend/settlement/infrastructure/persistence/mysql/migrations/` 空目录

- [x] Task 6: 全量验证
  - [x] SubTask 6.1: 执行 `go build ./...` 通过（注：scripts/ 目录有预先存在的 main 重复声明问题，与本次修改无关；本次涉及的 game/settlement/common/gateway/stats 包均编译通过）
  - [x] SubTask 6.2: 执行 `go vet ./...` 通过（game/... 和 settlement/...）
  - [x] SubTask 6.3: 执行 `gofmt -l .` 无输出（platform_call_log.go 已 gofmt -w 修复）
  - [x] SubTask 6.4: 执行 `go test ./...` 通过（game/... 和 settlement/... 全部 ok）

# Task Dependencies
- Task 1、2、3、4 相互独立，可并行执行
- Task 5 依赖 Task 1-4 完成（确认索引已迁移后再删 SQL）
- Task 6 依赖 Task 1-5 全部完成
