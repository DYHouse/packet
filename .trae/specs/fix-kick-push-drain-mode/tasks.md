# Tasks

## 修复阶段

- [ ] Task 1: 在 `Connection` 上新增 `StatusKicking` 状态及方法
  - [ ] SubTask 1.1: 在 `gateway/connection/connection.go` 的 `ConnStatus` 常量块中,在 `StatusDisconnected` 和 `StatusClosed` 之间新增 `StatusKicking`
  - [ ] SubTask 1.2: 新增 `MarkKicking()` 方法:加写锁置 `status = StatusKicking`,不关闭 `sendChan`/`closeChan`/`wsConn`
  - [ ] SubTask 1.3: 新增 `IsKicking() bool` 方法:加读锁返回 `status == StatusKicking`
  - [ ] SubTask 1.4: 确认 `Send` 方法对 `StatusKicking` 仍允许写入(检查 `status == StatusClosed` 才拒绝,`Kicking` 不拒绝)

- [ ] Task 2: 修改 `writeAndHeartbeatPump` 支持 drain 模式
  - [ ] SubTask 2.1: 在 `gateway/server/server.go` 的 `writeAndHeartbeatPump` 中,`case msgData, ok := <-conn.SendChan()` 分支成功 `WriteMessage` 后,新增 drain 检查
  - [ ] SubTask 2.2: drain 条件为 `conn.IsKicking() && len(conn.SendChan()) == 0`
  - [ ] SubTask 2.3: 满足条件时发送 `websocket.CloseMessage`,调用 `conn.Close()`,然后 return
  - [ ] SubTask 2.4: 其他分支(`!ok`、`closeChan`、WriteMessage 失败、心跳超时)保持不变

- [ ] Task 3: 修改 `kickLocalConnection` 使用 `MarkKicking`
  - [ ] SubTask 3.1: 在 `gateway/connection/manager.go` 的 `kickLocalConnection` 中,把 `c.Close()` 替换为 `c.MarkKicking()`
  - [ ] SubTask 3.2: 确认 `c.Send(data)` 在 `MarkKicking` 之前调用(kicked 推送先入 sendChan)
  - [ ] SubTask 3.3: 确认 `localConnections.Delete` / `userConnections.Delete` / `connectionCount--` / `emitEvent` 在 `MarkKicking` 之后立即执行,不等 drain

- [ ] Task 4: 验证与构建
  - [ ] SubTask 4.1: `go build ./gateway/...` 通过
  - [ ] SubTask 4.2: `go vet ./gateway/...` 无警告
  - [ ] SubTask 4.3: `go test ./gateway/connection/scripts/...` 通过
  - [ ] SubTask 4.4: 代码审查确认无并发写(仍由 writePump 单 goroutine 写 WebSocket)
  - [ ] SubTask 4.5: 代码审查确认 `closeOnce` 保证 Close 幂等(drain 期间其他路径调用 Close 不 panic)

## Task Dependencies

- Task 1 独立,先行完成
- Task 2 依赖 Task 1(使用 `IsKicking` 方法)
- Task 3 依赖 Task 1(使用 `MarkKicking` 方法)
- Task 2 和 Task 3 可并行
- Task 4 依赖 Task 1 + Task 2 + Task 3

## 验证策略

1. **代码审查**:确认 writePump 的 drain 分支逻辑正确,不影响其他退出路径
2. **构建验证**:`go build ./gateway/...` 和 `go vet ./gateway/...` 通过
3. **单元测试**:`register_connection_test.go` 的踢人场景测试通过
4. **手动验证**(可选):
   - 同一用户重复登录,检查被踢客户端的 WS 流量是否收到 `type:"kicked"` 推送
   - 检查被踢客户端是否弹出"您已不在当前房间座位上"弹窗
   - 检查 drain 期间 sendChan 有积压时,kicked 是否排在已有消息之后发出
