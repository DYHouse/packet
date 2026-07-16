# 踢人推送可靠发送(优雅关闭)Spec

## Why

`kickLocalConnection` 当前的实现存在竞态条件,导致 kicked 推送**永远到达不了前端**:

```go
c.Send(data)    // 数据塞进 sendChan,还没发到 WebSocket
c.Close()       // 立即 close(closeChan) + close(sendChan) + conn.Close()
```

`Connection.Send` 只是把数据塞进 `sendChan` channel([connection.go:99-113](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/connection/connection.go#L99-L113)),真正发送由 `writeAndHeartbeatPump` goroutine 异步完成。但 `Close()` 紧接着 `close(sendChan)` 并 `wsConn.Close()`(见 [connection.go:127-140](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/connection/connection.go#L127-L140)),导致 `writeAndHeartbeatPump` 从 `sendChan` 读到 `ok=false` 直接 return,kicked 数据滞留在 channel 里永远丢失。

**用户实测现象**:同一用户重复登录,被踢的旧连接的 WebSocket 流量里**看不到任何 kicked 推送**,只看到 TCP 断开,于是前端走自动重连流程,而不是踢人弹窗流程。

## What Changes

### 核心方案:优雅关闭(Drain 模式)

引入 `StatusKicking` 中间状态,让 `writeAndHeartbeatPump` 负责"flush 完 sendChan 再关闭连接",避免 kicked 推送滞留丢失。

### 流程对比

**当前(有 bug)**:
```
kickLocalConnection:
  c.Send(kickedData)       ← 塞进 sendChan
  c.Close()                ← 立即 close(sendChan) + wsConn.Close()
                             → writePump 读到 ok=false 直接 return,kicked 丢失
```

**修复后**:
```
kickLocalConnection:
  c.Send(kickedData)       ← 塞进 sendChan
  c.MarkKicking()          ← 状态置为 Kicking,通知 writePump 进入 drain 模式
                             (不 close sendChan,不 close wsConn)

writeAndHeartbeatPump:
  select {
  case msgData, ok := <-sendChan:
    if !ok { return }      ← 正常情况
    wsConn.WriteMessage(msgData)
    // Kicking 状态下:sendChan 已排空 → 主动 close 自己
    if c.IsKicking() && len(sendChan) == 0 {
      c.Close()            ← writePump 自己关闭,保证 kicked 已发出去
      return
    }
  case <-closeChan: return ← 其他路径主动关闭(心跳超时、readPump 退出等)
  }
```

### 具体改动

1. **`gateway/connection/connection.go`**:
   - 新增 `StatusKicking` 状态常量
   - 新增 `MarkKicking()` 方法:原子置状态为 `StatusKicking`,**不**关闭 `sendChan`/`closeChan`/`wsConn`
   - 新增 `IsKicking() bool` 方法
   - `Close()` 保持不变(仍由 `writePump` 在 drain 完成后调用,或由其他路径如 `cleanupConnection` 调用)

2. **`gateway/server/server.go` — `writeAndHeartbeatPump`**:
   - 每次成功 `WriteMessage` 后,检查 `IsKicking() && sendChan 是否已排空`,满足则调 `c.Close()` 并 return
   - `sendChan` 关闭(`ok=false`)分支保持不变
   - `closeChan` 分支保持不变(其他路径的主动关闭仍立即生效)

3. **`gateway/connection/manager.go` — `kickLocalConnection`**:
   - 把 `c.Close()` 改为 `c.MarkKicking()`
   - 本地映射清理(`localConnections.Delete` / `userConnections.Delete` / `connectionCount--`)移到 `MarkKicking` 之后立即执行,不等待 drain 完成
   - `emitEvent(EventKicked)` 立即触发(不等待 drain)

4. **`gateway/connection/manager.go` — `consumeKickMessages`**:
   - 跨节点踢人通过 pubsub 调 `kickLocalConnection`,同样受益于 drain 模式

### 不在范围

- **`writeAndHeartbeatPump` 加写锁并发写** — 方案 A 涉及的并发写安全改造不引入,drain 模式仍由单 goroutine 写,无并发风险
- **心跳超时/读错误路径的关闭逻辑** — 这些路径仍走 `Close()` 立即关闭,无需 drain(它们没有待发送的关键推送)
- **`cleanupConnection`** — 保持不变,仍调用 `conn.Close()` 兜底(若 `writePump` 已因 drain 退出则 no-op)
- **sendChan 容量调整** — 不改 `SendQueueSize`(默认 256),drain 通常在毫秒级完成

## Impact

- **Affected code**:
  - `gateway/connection/connection.go`:新增 `StatusKicking` 常量、`MarkKicking()`、`IsKicking()` 方法
  - `gateway/server/server.go`:`writeAndHeartbeatPump` 增加 drain 检查
  - `gateway/connection/manager.go`:`kickLocalConnection` 用 `MarkKicking` 替换 `Close`
- **不受影响**:
  - `Register` / `kickExistingConnection` / `publishKickNotification` / `subscribeKickChannel`:逻辑不变
  - `cleanupConnection` / `MarkDisconnected` / `Unregister`:不变
  - `Send` / `SendChan` / `CloseChan` / `Close`:语义不变
  - 前端代码:无需改动,kicked 推送能正常收到后,现有弹窗链路自动生效

## ADDED Requirements

### Requirement:踢人推送可靠投递

系统 SHALL 在踢人时通过 drain 模式保证 kicked 推送**真正写入 WebSocket** 后再关闭连接,禁止在 `sendChan` 还有待发送数据时直接 `close(sendChan)` 或 `wsConn.Close()`。

#### Scenario: 同节点踢人,kicked 推送成功投递

- **WHEN** 同一用户重复登录,`kickLocalConnection` 被调用
- **THEN** kicked 推送数据被塞入 `sendChan`
- **AND** 连接状态置为 `StatusKicking`,`sendChan` 和 `wsConn` 不立即关闭
- **AND** `writeAndHeartbeatPump` 继续从 `sendChan` 读取并 `WriteMessage` 发送
- **AND** 当 `sendChan` 排空且状态为 `Kicking` 时,`writeAndHeartbeatPump` 调用 `c.Close()` 关闭连接并退出
- **AND** 被踢客户端收到 `{"type":"kicked","data":{...}}` 推送

#### Scenario: 跨节点踢人,kicked 推送成功投递

- **WHEN** 跨节点踢人,旧节点通过 pubsub 收到通知调用 `kickLocalConnection`
- **THEN** 旧节点执行与同节点相同的 drain 流程
- **AND** 被踢客户端收到 kicked 推送

#### Scenario: drain 期间 writePump 写失败

- **WHEN** `StatusKicking` 状态下 `writeAndHeartbeatPump` 调用 `WriteMessage` 失败(客户端网络故障)
- **THEN** `writeAndHeartbeatPump` 直接 return(不调 `c.Close()`,由 `cleanupConnection` 兜底)
- **AND** 不阻塞新连接注册流程

#### Scenario: drain 期间 sendChan 有积压

- **WHEN** `StatusKicking` 时 `sendChan` 还有 N 条消息(例如前面有 room_state 推送)
- **THEN** `writeAndHeartbeatPump` 按顺序全部发送完
- **AND** kicked 推送排在其后发出(顺序保证)
- **AND** 全部发完后才 `c.Close()`

### Requirement: Kicking 状态语义

`StatusKicking` SHALL 表示"连接已被标记为踢出,等待 `writeAndHeartbeatPump` drain 完 sendChan 后自行关闭",此状态下 `Send` 仍可工作(允许 kicked 推送塞入),`Close` 仍可被外部调用(兜底)。

#### Scenario: Kicking 状态下其他路径触发 Close

- **WHEN** `StatusKicking` 期间 `readPump` 因读错误退出,触发 `cleanupConnection` → `conn.Close()`
- **THEN** `Close()` 正常执行(`closeOnce` 保证幂等)
- **AND** `writeAndHeartbeatPump` 通过 `closeChan` 分支退出
- **AND** 不产生 panic 或双重关闭

### Requirement: 本地映射立即清理

`kickLocalConnection` 调用 `MarkKicking` 后 SHALL 立即清理 `localConnections` / `userConnections` / `connectionCount`,不等 drain 完成,确保新连接能立即注册(避免 `userConnections` 仍指向旧 connID)。

#### Scenario: 踢人后新连接立即注册

- **WHEN** `kickLocalConnection` 执行 `MarkKicking` 后,新连接调用 `Register`
- **THEN** `userConnections.Load(userID)` 不返回旧 connID(已清理)
- **AND** 新连接注册成功,写入新的 connID
- **AND** 旧连接的 drain 在后台独立完成,不受新连接影响

## MODIFIED Requirements

### Requirement: writeAndHeartbeatPump 退出条件

`writeAndHeartbeatPump` SHALL 在以下任一条件退出:1) `sendChan` 关闭(`ok=false`);2) `closeChan` 触发;3) `WriteMessage` 失败;4) 心跳超时;5) **新增:`StatusKicking` 且 `sendChan` 排空**。

#### Scenario: Kicking 状态下 sendChan 排空

- **WHEN** 连接状态为 `StatusKicking`
- **AND** `writeAndHeartbeatPump` 刚成功 `WriteMessage` 一条消息
- **AND** `len(sendChan) == 0`
- **THEN** `writeAndHeartbeatPump` 调用 `c.Close()` 关闭连接
- **AND** 发送 WebSocket CloseMessage(优雅关闭)
- **AND** goroutine 退出
