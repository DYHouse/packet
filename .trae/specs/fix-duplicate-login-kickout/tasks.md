# Tasks

## 修复阶段

- [ ] Task 1: 统一 `handleConnection` 登录路径
  - [ ] SubTask 1.1: 移除 `roomID := s.connMgr.GetPlayerRoom(conn.UserID)` 和后续的 if/else 分支
  - [ ] SubTask 1.2: 统一调用 `s.connMgr.Register(conn)`，失败时发送 `CodeConnectionLimit` 并 return
  - [ ] SubTask 1.3: `Register` 成功后调用 `s.connMgr.GetPlayerRoom(conn.UserID)` 判断 roomID
  - [ ] SubTask 1.4: 如果 `roomID != ""` 调用 `s.restoreRoomState(ctx, conn, roomID)`，否则继续启动 `writeAndHeartbeatPump` 和 `readPump`

- [ ] Task 2: 精简 `handleReconnect` 为 `restoreRoomState`
  - [ ] SubTask 2.1: 将 `handleReconnect` 改名为 `restoreRoomState`（更准确反映职责）
  - [ ] SubTask 2.2: 移除 `s.connMgr.CleanupOldConnection(conn.UserID)` 调用
  - [ ] SubTask 2.3: 移除 `s.connMgr.Register(conn)` 调用及其错误处理（已移到 `handleConnection`）
  - [ ] SubTask 2.4: 保留构建 `reconnectRequest` 和 `s.router.Route(ctx, conn, reconnectReqBytes)` 逻辑不变

- [ ] Task 3: 删除 `CleanupOldConnection` 及相关方法
  - [ ] SubTask 3.1: 删除 `gateway/connection/manager.go` 中的 `CleanupOldConnection` 方法
  - [ ] SubTask 3.2: 删除 `gateway/connection/manager.go` 中的 `deleteConnectionMapping` 方法
  - [ ] SubTask 3.3: 全局搜索确认无残留引用（`CleanupOldConnection` / `deleteConnectionMapping`）

- [ ] Task 4: 验证与构建
  - [ ] SubTask 4.1: `go build ./...` 通过
  - [ ] SubTask 4.2: `go vet ./...` 无警告
  - [ ] SubTask 4.3: 现有 `register_connection_test.go` 测试通过
  - [ ] SubTask 4.4: 代码审查确认 `Register` 的踢人逻辑覆盖同节点和跨节点场景

## Task Dependencies

- Task 1 和 Task 2 可并行修改（都在 server.go），但需协调 `handleReconnect` → `restoreRoomState` 的改名
- Task 3 依赖 Task 2 完成（确认 `CleanupOldConnection` 无调用点后才能删除）
- Task 4 依赖 Task 1 + Task 2 + Task 3 全部完成

## 验证策略

1. **代码审查**：确认 `handleConnection` 流程为 `Register` → 判断 roomID → `restoreRoomState`
2. **构建验证**：`go build ./...` 和 `go vet ./...` 通过
3. **单元测试**：`register_connection_test.go` 的三个场景（首次注册、同 connID 重复、不同 connID 触发踢旧）全部通过
4. **手动验证**（可选）：
   - 同一用户在房间中重复登录，确认旧连接收到 `kicked` 推送并关闭，新连接恢复房间状态
   - 跨节点场景：旧连接在节点 A，新连接在节点 B，确认节点 A 的旧连接被 pubsub 通知踢掉
