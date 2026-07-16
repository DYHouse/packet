# Checklist

## 核心修复验收

- [ ] `gateway/server/server.go` 的 `handleConnection` 不再有 if/else 分支，统一先调用 `Register(conn)`
- [ ] `Register` 成功后，通过 `GetPlayerRoom` 判断是否需要恢复房间状态
- [ ] `handleReconnect` 已改名为 `restoreRoomState`（或保留原名但移除清理/注册逻辑），只负责发送 `reconnect` 命令
- [ ] `restoreRoomState` 不调用 `CleanupOldConnection`、不删除 `GatewayConnKey`、不删除 `PlayerRoomKey`
- [ ] `gateway/connection/manager.go` 的 `CleanupOldConnection` 方法已删除
- [ ] `gateway/connection/manager.go` 的 `deleteConnectionMapping` 方法已删除
- [ ] 全局搜索无 `CleanupOldConnection` 残留引用
- [ ] 全局搜索无 `deleteConnectionMapping` 残留引用

## 踢人逻辑验收

- [ ] 用户不在房间时重复登录：旧连接收到 `kicked` 推送（reason: `login_elsewhere`）后关闭
- [ ] 用户在房间时重复登录（同节点）：旧连接收到 `kicked` 推送后关闭，新连接恢复房间状态
- [ ] 用户在房间时重复登录（跨节点）：旧节点收到 Redis pubsub 通知后关闭旧连接并发送 `kicked` 推送，新连接恢复房间状态
- [ ] 旧连接已断线时重复登录：新连接注册成功，如果在房间则恢复状态（踢人 no-op 不报错）

## 房间状态恢复验收

- [ ] `PlayerRoomKey` 在登录流程中不被删除
- [ ] `GatewayConnKey` 在 `Register` 之前不被删除
- [ ] `Register` 成功后 `GetPlayerRoom` 返回非空时，发送 `reconnect` 命令到 game-service
- [ ] `reconnect` 命令包含正确的 `roomID` 和 `userID`

## 构建与测试验收

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 无警告
- [ ] 现有 `register_connection_test.go` 测试通过
- [ ] 未引入新的 import 循环
