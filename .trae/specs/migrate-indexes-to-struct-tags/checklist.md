# Checklist

## 索引声明验证

- [x] `SessionPlayer.UserID` 和 `SessionPlayer.JoinedAt` 声明了复合索引 `idx_user_joined`（priority 1/2）
- [x] `RoundGrabRecord.SessionID`、`UserID`、`GrabbedAt` 声明了复合索引 `idx_session_user`（priority 1/2/3）
- [x] `Round.SessionID` 和 `Round.RoundNo` 声明了复合索引 `idx_session_roundno`（priority 1/2）
- [x] `Round.SessionID`、`SenderID`、`RoundNo` 声明了复合索引 `idx_session_sender`（priority 1/2/3）
- [x] `BillRecord` 声明了复合索引 `idx_user_status_session (user_id, status, session_id)`
- [x] `BillRecord` 声明了复合索引 `idx_session_user_type (session_id, user_id, bill_type, status)`
- [x] `BillRecord` 声明了复合索引 `idx_user_session (user_id, session_id, created_at)`
- [x] `BillRecord` 原有的 `idx_round_trace_bill_user` 复合唯一索引未被破坏
- [x] `PlatformCallLog` 声明了复合索引 `idx_biz_call_time (biz_order_no, call_type, request_time)`
- [x] `PlatformCallLog` 声明了复合索引 `idx_status_time (status, request_time)`
- [x] `PlatformCallLog` 声明了复合索引 `idx_user_time (user_id, request_time)`
- [x] `PlatformCallLog` 声明了复合索引 `idx_session_time (session_id, request_time)`
- [x] `PlatformCallLog.TraceID` 的单列索引保留

## SQL 文件清理验证

- [x] `backend/migrations/20260625_add_player_history_indexes.sql` 已删除
- [x] `backend/migrations/20260626_add_bill_history_indexes.sql` 已删除
- [x] `backend/migrations/20260709_upgrade_platform_call_log.sql` 已删除
- [x] `backend/migrations/` 目录已删除（若为空）
- [x] `backend/settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index.sql` 已删除
- [x] `backend/settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index_v2.sql` 已删除
- [x] `backend/settlement/infrastructure/persistence/mysql/migrations/` 目录已删除（若为空）

## 索引名称一致性验证

- [x] 所有新增复合索引名称与原 SQL 脚本完全一致（idx_user_joined、idx_session_user、idx_session_roundno、idx_session_sender、idx_user_status_session、idx_session_user_type、idx_user_session、idx_biz_call_time、idx_status_time、idx_user_time、idx_session_time）

## 构建与测试验证

- [x] `go build ./...` 通过（注：scripts/ 目录有预先存在的 main 重复声明问题，与本次修改无关；本次涉及的 game/settlement/common/gateway/stats 包均编译通过）
- [x] `go vet ./...` 通过（game/... 和 settlement/...）
- [x] `gofmt -l .` 无输出（所有文件格式正确）
- [x] `go test ./...` 通过（game/... 和 settlement/... 全部 ok）
