# Checklist

## connection.go 改动验收

- [ ] 新增 `StatusKicking` 常量(在 `StatusDisconnected` 和 `StatusClosed` 之间)
- [ ] 新增 `MarkKicking()` 方法:加锁置状态为 `StatusKicking`,不关闭 `sendChan`/`closeChan`/`wsConn`
- [ ] 新增 `IsKicking() bool` 方法:读锁返回 `status == StatusKicking`
- [ ] `Send` 方法对 `StatusKicking` 仍允许写入(不拒绝,让 kicked 推送能塞入)
- [ ] `Close` 方法不变(`closeOnce` 保证幂等,可被 writePump 或 cleanupConnection 调用)

## writeAndHeartbeatPump 改动验收

- [ ] `sendChan` 读取分支(`case msgData, ok := <-conn.SendChan()`):成功 `WriteMessage` 后检查 `conn.IsKicking() && len(sendChan) == 0`
- [ ] 满足 drain 条件时调用 `conn.Close()` 并 return(发送 CloseMessage 优雅关闭)
- [ ] `sendChan` 关闭分支(`!ok`)保持不变
- [ ] `closeChan` 分支保持不变(其他路径主动关闭仍立即生效)
- [ ] `WriteMessage` 失败分支保持不变(直接 return,由 cleanupConnection 兜底)
- [ ] 心跳超时分支保持不变

## kickLocalConnection 改动验收

- [ ] `c.Close()` 替换为 `c.MarkKicking()`
- [ ] `localConnections.Delete` / `userConnections.Delete` / `connectionCount--` 在 `MarkKicking` 之后立即执行
- [ ] `emitEvent(EventKicked)` 立即触发(不等 drain)
- [ ] kicked 推送 `c.Send(data)` 在 `MarkKicking` 之前调用(确保数据在 sendChan 里)

## 踢人推送投递验收

- [ ] 同节点踢人:被踢客户端收到 `{"type":"kicked","data":{"user_id":"...","reason":"login_elsewhere","message":"..."}}` 推送
- [ ] 跨节点踢人:旧节点通过 pubsub 触发 `kickLocalConnection`,被踢客户端收到 kicked 推送
- [ ] drain 期间 sendChan 有积压:kicked 排在已有消息之后按顺序发出
- [ ] drain 期间 WriteMessage 失败:writePump 直接 return,不阻塞新连接注册,不 panic
- [ ] drain 期间 readPump 退出触发 cleanupConnection:Close 幂等,不产生双重关闭 panic

## 本地映射清理验收

- [ ] `MarkKicking` 后 `userConnections` 立即不指向旧 connID
- [ ] 新连接 `Register` 在 `kickLocalConnection` 后能立即成功(无冲突)
- [ ] 旧连接的 drain 在后台独立完成,不受新连接影响

## 构建与测试验收

- [ ] `go build ./gateway/...` 通过
- [ ] `go vet ./gateway/...` 无警告
- [ ] `go test ./gateway/connection/scripts/...` 通过
- [ ] 未引入新的 import 循环
- [ ] 未引入并发写(goroutine 仍单线程写 WebSocket)
