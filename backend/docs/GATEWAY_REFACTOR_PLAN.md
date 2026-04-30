# Gateway 重构方案：连接管理优化 + 单连接互踢 + 掉线重连

## 一、现状分析

### 1.1 当前架构

```
客户端 → WebSocket → Gateway(认证→注册→读写循环) → gRPC → Game Service
                                    ↓
                            内存 sync.Map 管理连接
                            (localConnections + userConnections)
                                    ↓
                            Kafka 消费广播消息
```

### 1.2 现有连接管理结构

```go
// connection/connection.go
type ConnStatus int

const (
    StatusConnecting ConnStatus = iota  // 连接中
    StatusAuthed                         // 已认证
    StatusClosed                         // 已关闭
)

type Connection struct {
    ConnID       string
    UserID       string
    Token        string
    IP           string
    DeviceID     string
    Platform     string
    UserAgent    string
    Nickname     string
    Avatar       string
    Balance      int64

    conn          *websocket.Conn
    status        ConnStatus
    lastHeartbeat time.Time
    sendChan      chan []byte
    closeChan     chan struct{}
    closeOnce     sync.Once
    mu            sync.RWMutex
    sendQueueSize int
}
```

```go
// connection/manager.go
type userConnMap struct {
    mu    sync.RWMutex
    conns map[string]struct{}   // connID 集合（一对多）
}

type Manager struct {
    localConnections sync.Map   // connID -> *Connection
    userConnections  sync.Map   // userID -> *userConnMap（支持多连接）
    config           *ManagerConfig
    connectionCount  int64
    ctx              context.Context
    cancel           context.CancelFunc
    wg               sync.WaitGroup
}

type ManagerConfig struct {
    MaxConnections int
}
```

### 1.3 现有问题清单

| # | 问题 | 位置 | 影响 |
|---|------|------|------|
| 1 | **`userConnMap` 过度设计** | `manager.go:21-85` | 支持一对多映射（`map[string]struct{}`），但需求是单连接互踢，整个结构体及其 7 个方法都是多余的 |
| 2 | **`Manager` 缺少 Redis 依赖** | `manager.go:87-97` | 没有注入 Redis 和 nodeID，无法做跨节点互踢和断线重连 |
| 3 | **`Manager` 缺少事件回调** | `manager.go` | 踢旧、断线、重连等事件没有回调机制，Server 层无法感知并做后续处理 |
| 4 | **`Connection` 缺少断线时间字段** | `connection.go:24-45` | 没有 `DisconnectedAt`，无法支持断线缓冲超时判断 |
| 5 | **`ManagerConfig` 过于简单** | `manager.go:17-19` | 缺少断线缓冲超时、Redis Key 前缀等配置 |
| 6 | **心跳检测重复** | `manager.go:251-276` + `server.go:262-264` | `cleanupStaleConnections` 和 `writeAndHeartbeatPump` 都在检测心跳超时，职责不清 |
| 7 | **`cleanupConnection` 直接断开** | `server.go:274-277` | 断线时直接 `Unregister`，无缓冲期，无法支持重连 |
| 8 | **无 Redis 连接映射** | 全局 | 连接信息仅存内存 `sync.Map`，多节点部署时无法跨节点踢旧 |
| 9 | **`BroadcastService` 盲推** | `broadcast.go:144-171` | 通过 `IsUserConnectedLocally` 判断本节点是否有连接，但不知道用户在哪个节点，跨节点推送完全依赖 Kafka 消费 |
| 10 | **`GetConnectionsByUserID` 返回多连接** | `manager.go:181-202` | 返回 `[]*Connection`，与单连接需求矛盾 |

---

## 二、重构目标

1. **连接管理结构优化**：简化 `userConnMap`，`Manager` 注入 Redis + nodeID，增加事件回调机制
2. **单连接互踢**：同一用户只保留最新连接，新连接登录时踢掉旧连接
3. **跨节点互踢**：多 Gateway 节点部署时，通过 Redis 实现跨节点踢旧
4. **掉线自动重连**：断线后 30s 缓冲期内，同一用户再次登录自动重连恢复会话，重连后获取房间状态继续游戏
5. **断线超时处理**：缓冲期超时后真正断开，通知 game 服务处理断线逻辑

---

## 三、连接管理结构优化

### 3.1 Connection 优化

#### 问题

- 缺少 `DisconnectedAt` 字段，无法支持断线缓冲超时判断
- 缺少 `StatusDisconnected` 状态，断线与关闭无法区分
- `Token` 字段在认证后无用途，保留有安全风险

#### 优化方案

```go
// connection/connection.go

const (
    StatusConnecting   ConnStatus = iota  // 连接中
    StatusAuthed                           // 已认证，正常在线
    StatusDisconnected                     // 断线缓冲中（新增）
    StatusClosed                           // 已关闭
)

type Connection struct {
    ConnID       string
    UserID       string
    IP           string
    DeviceID     string
    Platform     string
    UserAgent    string
    Nickname     string
    Avatar       string
    Balance      int64

    DisconnectedAt time.Time    // 新增：断线时间

    conn          *websocket.Conn
    status        ConnStatus
    lastHeartbeat time.Time
    sendChan      chan []byte
    closeChan     chan struct{}
    closeOnce     sync.Once
    mu            sync.RWMutex
    sendQueueSize int
}
```

变更点：
- 删除 `Token` 字段（认证后不再需要，避免内存中残留敏感信息）
- ~~新增 `RoomID`~~ — **不需要本地存储**，直接从 Redis `cashparty:player:room:{userID}` 读取
- 新增 `DisconnectedAt` — 记录断线时间，用于缓冲期超时判断
- 新增 `StatusDisconnected` — 区分"断线缓冲"和"真正关闭"

#### 设计说明：RoomID 从 Redis 获取

Game 服务已在 Redis 中维护用户-房间映射：

```
Key:   cashparty:player:room:{userID}
Value: roomID
TTL:   24h
```

断线重连时，直接从此 Key 读取用户所在房间，无需在 Gateway 本地维护 `RoomID` 字段。

**优点**：
1. **避免数据冗余**：不重复存储已有数据
2. **数据一致性**：无需同步本地状态与 Redis
3. **简化逻辑**：不需要在加入/离开房间时同步更新本地 `RoomID`

### 3.2 ManagerConfig 优化

#### 问题

- 仅有 `MaxConnections` 一个配置项
- 缺少断线缓冲超时、Redis Key 前缀、心跳续期间隔等配置

#### 优化方案

```go
// connection/manager.go

type ManagerConfig struct {
    MaxConnections       int           // 最大连接数
    DisconnectTimeout    time.Duration // 断线缓冲超时（默认 30s）
    ConnRedisTTL         time.Duration // Redis 连接映射 TTL（默认 24h）
    HeartbeatRenewalTick time.Duration // 心跳续期间隔（默认 30s）
    KeyPrefix            string        // Redis Key 前缀（默认 "cashparty"）
}
```

### 3.3 Manager 优化

#### 问题

| 问题 | 说明 |
|------|------|
| `userConnMap` 过度设计 | 支持一对多映射，7 个方法全部多余，应简化为 1:1 |
| 缺少 Redis 依赖 | 无法做跨节点互踢和断线重连 |
| 缺少 nodeID | 无法标识本节点，无法做跨节点 Pub/Sub |
| 缺少事件回调 | Server 层无法感知踢旧、断线、重连事件 |
| 缺少断线缓冲列表 | 断线连接与活跃连接混在一起 |
| 心跳清理与 writePump 重复 | 两个地方检测心跳超时 |

#### 优化方案

```go
// connection/manager.go

type EventCallback func(conn *Connection, event string, roomID string)

type Manager struct {
    localConnections        sync.Map   // connID -> *Connection
    userConnections         sync.Map   // userID -> connID（改为 1:1 映射）
    disconnectedConnections sync.Map   // connID -> *Connection（断线缓冲，新增）
    config                  *ManagerConfig
    redis                   *cRedis.Client   // 新增
    nodeID                  string           // 新增
    eventCallback           EventCallback    // 新增：事件回调
    connectionCount         int64
    ctx                     context.Context
    cancel                  context.CancelFunc
    wg                      sync.WaitGroup
}
```

**关键变更：**

1. **删除 `userConnMap`** — 整个结构体及其 7 个方法（`Add`/`Remove`/`Contains`/`Count`/`ConnIDs`/`IsEmpty`/`Range`）全部删除
2. **`userConnections` 改为 1:1 映射** — `sync.Map` 中 `userID -> connID`（string），直接用 `Load`/`Store`/`Delete`
3. **新增 `disconnectedConnections`** — 断线缓冲列表，`connID -> *Connection`
4. **新增 `redis` + `nodeID`** — 支持跨节点互踢和断线重连
5. **新增 `eventCallback`** — 事件回调，Server 层可感知踢旧、断线、重连事件

#### 删除 `userConnMap` 后的方法变更

| 原方法 | 变更 |
|--------|------|
| `GetConnectionsByUserID` → `GetConnectionByUserID` | 返回 `*Connection`（单个），不再返回 `[]*Connection` |
| `BroadcastToUser` | 内部调用 `GetConnectionByUserID`，逻辑不变 |
| `IsUserConnectedLocally` | 内部改为 `userConnections.Load(userID)` |
| `Register` | 改为 `userConnections.Store(userID, conn.ConnID)` |
| `Unregister` | 改为 `userConnections.Delete(userID)` |

#### 新增事件回调机制

```go
type EventType string

const (
    EventKicked       EventType = "kicked"        // 被踢旧
    EventDisconnected EventType = "disconnected"   // 断线进入缓冲
    EventReconnected  EventType = "reconnected"    // 重连恢复
    EventTimeout      EventType = "timeout"        // 断线超时真正关闭
)

func (m *Manager) SetEventCallback(cb EventCallback) {
    m.eventCallback = cb
}

func (m *Manager) emitEvent(conn *Connection, event string, roomID string) {
    if m.eventCallback != nil {
        m.eventCallback(conn, event, roomID)
    }
}
```

Server 层注册回调，统一处理踢旧/断线/重连/超时的后续逻辑（如通知 game 服务）。

#### 心跳清理职责明确

- **`writeAndHeartbeatPump`**：负责检测写端心跳超时，关闭写循环 → 触发 `readPump` 退出 → 进入断线缓冲
- **`cleanupStaleConnections`**：仅清理 `disconnectedConnections` 中超时的断线连接（不再清理活跃连接）

```go
func (m *Manager) cleanupStaleConnections() {
    defer m.wg.Done()
    ticker := time.NewTicker(CleanupInterval)
    defer ticker.Stop()

    for {
        select {
        case <-m.ctx.Done():
            return
        case <-ticker.C:
            m.disconnectedConnections.Range(func(key, value interface{}) bool {
                conn := value.(*Connection)
                if time.Since(conn.DisconnectedAt) > m.config.DisconnectTimeout {
                    m.expireDisconnected(conn)
                }
                return true
            })
        }
    }
}
```

---

## 四、单连接互踢

### 4.1 Redis 连接映射

用 Redis Hash 存储用户当前连接信息：

```
Key:   cashparty:gateway:conn:{userID}
Value: Hash {
    conn_id:      "conn_abc123"
    node_id:      "gateway-1"
    platform:     "ios"
    device_id:    "device_xxx"
    connected_at: 1700000000
}
TTL:   24h（心跳续期）
```

### 4.2 注册时踢旧流程

```
新连接认证成功
    ↓
调用 Redis Lua 脚本（原子操作）：
  1. 查询该 userID 的旧连接信息
  2. 写入新连接信息
  3. 返回旧连接信息（如有）
    ↓
判断是否需要踢旧
    ↓ 需要踢旧
判断旧连接是否在本节点
    ↓ 本节点                    ↓ 跨节点
直接关闭旧连接              通过 Redis Pub/Sub
发送 kicked 推送            通知目标节点踢旧
```

### 4.3 注册踢旧 Lua 脚本

```lua
-- LuaRegisterConnection
-- KEYS: [userConnKey]
-- ARGV: [connID, nodeID, platform, deviceID, connectedAt]
-- 返回: {needKick, oldConnID, oldNodeID}

local userConnKey = KEYS[1]
local newConnID = ARGV[1]
local newNodeID = ARGV[2]
local platform = ARGV[3]
local deviceID = ARGV[4]
local connectedAt = tonumber(ARGV[5])

local oldConnID = ''
local oldNodeID = ''

local oldData = redis.call('HGETALL', userConnKey)
if #oldData > 0 then
    for i = 1, #oldData, 2 do
        if oldData[i] == 'conn_id' then oldConnID = oldData[i+1] end
        if oldData[i] == 'node_id' then oldNodeID = oldData[i+1] end
    end
end

redis.call('HMSET', userConnKey,
    'conn_id', newConnID,
    'node_id', newNodeID,
    'platform', platform,
    'device_id', deviceID,
    'connected_at', connectedAt
)
redis.call('EXPIRE', userConnKey, 86400)

if oldConnID ~= '' and oldConnID ~= newConnID then
    return {1, oldConnID, oldNodeID}
end
return {0, '', ''}
```

### 4.4 跨节点踢旧：Redis Pub/Sub

```
Channel: cashparty:gateway:kick:{nodeID}
Message: JSON {
    "user_id":  "xxx",
    "conn_id":  "yyy",
    "reason":   "login_elsewhere"
}
```

每个 Gateway 节点启动时订阅自己 nodeID 的 kick channel，收到消息后：
1. 从 `localConnections` 中找到对应连接
2. 发送 `kicked` 推送
3. 关闭连接

### 4.5 踢旧推送协议

向被踢的旧连接发送：

```json
{
  "cmd": "push",
  "event": "kicked",
  "data": {
    "user_id": "xxx",
    "reason": "login_elsewhere",
    "message": "您的账号在其他设备登录"
  }
}
```

### 4.6 心跳续期

在 `writeAndHeartbeatPump` 的心跳逻辑中，每隔 30s 续期 Redis 连接映射的 TTL：

```go
case <-heartbeatTicker.C:
    // ... 原有 ping 逻辑
    m.renewConnectionTTL(conn.UserID)  // EXPIRE cashparty:gateway:conn:{userID} 86400
```

---

## 五、掉线自动重连

### 5.1 核心设计：基于 user_id 自动判断

重连无需客户端携带任何额外标识。用户认证成功后，Gateway 自动检查 Redis 中该 `user_id` 是否有断线记录，有则自动走重连流程，无则走正常新连接流程。

**客户端无感知**：客户端只需正常发起 WebSocket 连接 + 认证，Gateway 自动判断是重连还是新连接。

### 5.2 断线缓冲流程

```
1. 客户端断线（readPump 退出）
    ↓
2. 不立即 Unregister，而是将连接状态改为 StatusDisconnected
    ↓
3. 将连接从活跃列表移到断线缓冲列表（disconnectedConnections）
    ↓
4. 在 Redis 中设置断线标记：
   Key:   cashparty:gateway:disconnect:{userID}
   Value: Hash {
       conn_id:        "conn_abc123"
       node_id:        "gateway-1"
       disconnected_at: 1700000000
   }
   TTL: 30s（等于缓冲期时长）
    ↓
5. 启动缓冲期倒计时（默认 30s）
    ↓
6. 缓冲期内：
   a. 同一 userID 再次登录 → 自动重连恢复会话
   b. 超时 → 真正断开，通知 game 服务处理断线
```

**注意**：断线记录中**不存储 `room_id`**，重连时从 `cashparty:player:room:{userID}` 读取。

### 5.3 重连流程

```
客户端正常登录（WebSocket 连接 + 认证）
    ↓
Gateway 认证成功，获得 userID
    ↓
检查 Redis 中是否有该 userID 的断线记录
    ↓ 有断线记录（30s 缓冲期内）
自动重连：
  1. 从断线缓冲列表移回活跃列表
  2. 连接状态改为 StatusAuthed
  3. 删除 Redis 断线标记
  4. 写入新的 Redis 连接映射
  5. 从 Redis 读取用户所在房间：
     roomID = GET cashparty:player:room:{userID}
  6. 向 game 服务发送 reconnect 命令（携带 roomID）
  7. game 服务调用 HandleReconnect：
     - 清除断线超时
     - 广播 player_reconnected
  8. game 服务推送完整房间状态（room_state）给重连玩家
  9. 向客户端发送 reconnect_success 推送
    ↓ 无断线记录（缓冲期超时或首次登录）
走正常新连接流程（踢旧 + 注册）
```

### 5.4 断线超时处理

缓冲期超时后：

```
1. 从断线缓冲列表移除
2. 删除 Redis 断线标记
3. 删除 Redis 连接映射
4. 从 Redis 读取用户所在房间：
   roomID = GET cashparty:player:room:{userID}
5. 通知 game 服务处理断线（携带 roomID）：
   - 如果玩家在游戏中：设置断线超时定时器
   - 广播 player_disconnected
   - 等待替换或超时踢出
```

### 5.5 重连 Redis Lua 脚本

```lua
-- LuaTryReconnect
-- KEYS: [disconnectKey, userConnKey, playerRoomKey]
-- ARGV: [userID, newConnID, newNodeID, platform, deviceID, now]
-- 返回: {code, roomID, oldConnID, oldNodeID}

local disconnectKey = KEYS[1]
local userConnKey = KEYS[2]
local playerRoomKey = KEYS[3]
local userID = ARGV[1]
local newConnID = ARGV[2]
local newNodeID = ARGV[3]
local platform = ARGV[4]
local deviceID = ARGV[5]
local now = tonumber(ARGV[6])

local disconnectData = redis.call('HGETALL', disconnectKey)
if #disconnectData == 0 then
    return {0, '', '', ''}
end

local oldConnID = ''
local oldNodeID = ''
for i = 1, #disconnectData, 2 do
    if disconnectData[i] == 'conn_id' then oldConnID = disconnectData[i+1] end
    if disconnectData[i] == 'node_id' then oldNodeID = disconnectData[i+1] end
end

redis.call('DEL', disconnectKey)

redis.call('HMSET', userConnKey,
    'conn_id', newConnID,
    'node_id', newNodeID,
    'platform', platform,
    'device_id', deviceID,
    'connected_at', now
)
redis.call('EXPIRE', userConnKey, 86400)

local roomID = redis.call('GET', playerRoomKey) or ''

return {1, roomID, oldConnID, oldNodeID}
```

返回值：`{0, '', '', ''}` 表示无断线记录，`{1, roomID, oldConnID, oldNodeID}` 表示重连成功。

**关键改进**：`roomID` 从 `cashparty:player:room:{userID}` 读取，而非存储在断线记录中。

---

## 六、Manager 完整改造

### 6.1 新增字段

```go
type Manager struct {
    localConnections        sync.Map   // connID -> *Connection
    userConnections         sync.Map   // userID -> connID（1:1 映射）
    disconnectedConnections sync.Map   // connID -> *Connection（断线缓冲）
    config                  *ManagerConfig
    redis                   *cRedis.Client
    nodeID                  string
    eventCallback           EventCallback
    connectionCount         int64
    ctx                     context.Context
    cancel                  context.CancelFunc
    wg                      sync.WaitGroup
}
```

### 6.2 构造函数改造

```go
func NewManager(config *ManagerConfig, redis *cRedis.Client, nodeID string) *Manager {
    ctx, cancel := context.WithCancel(context.Background())

    if config == nil {
        config = &ManagerConfig{
            MaxConnections:       10000,
            DisconnectTimeout:    30 * time.Second,
            ConnRedisTTL:         24 * time.Hour,
            HeartbeatRenewalTick: 30 * time.Second,
            KeyPrefix:            "cashparty",
        }
    }

    m := &Manager{
        config:  config,
        redis:   redis,
        nodeID:  nodeID,
        ctx:     ctx,
        cancel:  cancel,
    }

    m.wg.Add(2)
    go m.cleanupStaleConnections()
    go m.subscribeKickChannel()

    return m
}
```

### 6.3 Register 改造

```go
func (m *Manager) Register(conn *Connection) error {
    // 1. 连接数限制检查
    if !m.tryIncrementCount() {
        return fmt.Errorf("connection limit reached")
    }

    // 2. 调用 LuaRegisterConnection 写入 Redis 连接映射（含踢旧）
    needKick, oldConnID, oldNodeID := m.registerInRedis(conn)

    // 3. 如果需要踢旧
    if needKick {
        m.kickExistingConnection(conn.UserID, oldConnID, oldNodeID)
    }

    // 4. 内存注册（1:1 映射）
    m.localConnections.Store(conn.ConnID, conn)
    m.userConnections.Store(conn.UserID, conn.ConnID)

    logger.Info("connection registered",
        "conn_id", conn.ConnID,
        "user_id", conn.UserID,
        "current_connections", atomic.LoadInt64(&m.connectionCount))

    return nil
}
```

### 6.4 踢旧逻辑

```go
func (m *Manager) kickExistingConnection(userID, oldConnID, oldNodeID string) {
    if oldNodeID == m.nodeID {
        m.kickLocalConnection(oldConnID)
    } else {
        m.publishKickNotification(userID, oldConnID, oldNodeID)
    }
}

func (m *Manager) kickLocalConnection(connID string) {
    conn, ok := m.localConnections.Load(connID)
    if !ok {
        return
    }
    c := conn.(*Connection)

    // 发送 kicked 推送
    pushMsg := message.NewPushMessage(message.PushKicked, &message.KickedPush{
        UserID:  c.UserID,
        Reason:  message.ReasonLoginElsewhere,
        Message: "您的账号在其他设备登录",
    })
    data, _ := pushMsg.ToJSON()
    c.Send(data)

    // 关闭连接
    c.Close()
    m.localConnections.Delete(connID)
    m.userConnections.Delete(c.UserID)
    atomic.AddInt64(&m.connectionCount, -1)

    m.emitEvent(c, string(EventKicked), "")
}
```

### 6.5 断线缓冲与重连

```go
func (m *Manager) MarkDisconnected(conn *Connection) {
    conn.SetStatus(StatusDisconnected)
    conn.DisconnectedAt = time.Now()

    // 从活跃列表移到断线缓冲列表
    m.localConnections.Delete(conn.ConnID)
    m.userConnections.Delete(conn.UserID)
    atomic.AddInt64(&m.connectionCount, -1)

    m.disconnectedConnections.Store(conn.ConnID, conn)

    // 写入 Redis 断线标记（不存储 roomID）
    m.setDisconnectRecord(conn)

    // 从 Redis 读取 roomID 用于事件回调
    roomID := m.getPlayerRoom(conn.UserID)
    m.emitEvent(conn, string(EventDisconnected), roomID)
}

func (m *Manager) Reconnect(oldConnID string, newConn *Connection, roomID string) {
    // 从断线缓冲列表移除旧连接
    m.disconnectedConnections.Delete(oldConnID)

    // 删除 Redis 断线标记
    m.deleteDisconnectRecord(newConn.UserID)

    // 注册新连接
    newConn.SetStatus(StatusAuthed)
    m.localConnections.Store(newConn.ConnID, newConn)
    m.userConnections.Store(newConn.UserID, newConn.ConnID)
    atomic.AddInt64(&m.connectionCount, 1)

    m.emitEvent(newConn, string(EventReconnected), roomID)
}

func (m *Manager) expireDisconnected(conn *Connection) {
    m.disconnectedConnections.Delete(conn.ConnID)

    // 删除 Redis 断线标记和连接映射
    m.deleteDisconnectRecord(conn.UserID)
    m.deleteConnectionMapping(conn.UserID)

    conn.Close()

    // 从 Redis 读取 roomID 用于事件回调
    roomID := m.getPlayerRoom(conn.UserID)
    m.emitEvent(conn, string(EventTimeout), roomID)
}

func (m *Manager) getPlayerRoom(userID string) string {
    key := fmt.Sprintf("%s:player:room:%s", m.config.KeyPrefix, userID)
    roomID, _ := m.redis.Get(context.Background(), key).Result()
    return roomID
}
```

### 6.6 方法签名变更

| 原方法 | 新方法 | 变更说明 |
|--------|--------|----------|
| `GetConnectionsByUserID(userID) []*Connection` | `GetConnectionByUserID(userID) (*Connection, bool)` | 返回单个连接 |
| `IsUserConnectedLocally(userID) bool` | `IsUserConnectedLocally(userID) bool` | 内部改为 `userConnections.Load` |
| `BroadcastToUser(userID, msgData)` | `BroadcastToUser(userID, msgData)` | 内部改为 `GetConnectionByUserID` |
| — | `GetDisconnectedConnection(connID) (*Connection, bool)` | 新增：获取断线缓冲连接 |
| — | `SetEventCallback(cb)` | 新增：注册事件回调 |
| — | `MarkDisconnected(conn)` | 新增：标记断线 |
| — | `Reconnect(oldConnID, newConn, roomID)` | 新增：重连恢复 |
| — | `TryReconnectInRedis(conn) (roomID, oldConnID string)` | 新增：查询 Redis 断线记录 |
| — | `RenewConnectionTTL(userID)` | 新增：心跳续期 Redis TTL |
| — | `GetPlayerRoom(userID) string` | 新增：从 Redis 读取用户所在房间 |

### 6.7 Pub/Sub 订阅

```go
func (m *Manager) subscribeKickChannel() {
    defer m.wg.Done()

    channel := fmt.Sprintf("%s:gateway:kick:%s", m.config.KeyPrefix, m.nodeID)
    sub := m.redis.Subscribe(m.ctx, channel)
    defer sub.Close()

    ch := sub.Channel()
    for {
        select {
        case <-m.ctx.Done():
            return
        case msg, ok := <-ch:
            if !ok {
                return
            }
            var kickMsg KickMessage
            json.Unmarshal([]byte(msg.Payload), &kickMsg)
            m.kickLocalConnection(kickMsg.ConnID)
        }
    }
}
```

---

## 七、Server 改造

### 7.1 handleConnection 改造

认证成功后，自动检查 Redis 中该 user_id 是否有断线记录，有则重连，无则正常注册：

```go
func (s *Server) handleConnection(conn *connection.Connection, token string) {
    defer s.cleanupConnection(conn)

    // 认证
    if err := s.auth.OnConnect(conn, []byte(authReq)); err != nil {
        return
    }

    // 认证成功后，自动检查是否可重连（基于 user_id）
    if s.tryReconnect(conn) {
        go s.writeAndHeartbeatPump(conn)
        s.readPump(conn)
        return
    }

    // 正常注册（含踢旧）
    if err := s.connMgr.Register(conn); err != nil {
        s.sendError(conn, "", "", message.CodeConnectionLimit)
        return
    }

    go s.writeAndHeartbeatPump(conn)
    s.readPump(conn)
}
```

### 7.2 cleanupConnection 改造

```go
func (s *Server) cleanupConnection(conn *connection.Connection) {
    switch conn.GetStatus() {
    case connection.StatusAuthed:
        // 正常在线状态断线 → 进入断线缓冲
        s.connMgr.MarkDisconnected(conn)
    case connection.StatusDisconnected:
        // 已经在断线缓冲状态（重连失败等） → 真正断开
        s.connMgr.Unregister(conn.ConnID)
    }
    conn.Close()
}
```

### 7.3 tryReconnect

基于 `user_id` 自动判断，无需客户端携带任何额外参数：

```go
func (s *Server) tryReconnect(conn *connection.Connection) bool {
    // 调用 LuaTryReconnect，基于 userID 查询 Redis 断线记录
    // roomID 从 cashparty:player:room:{userID} 读取
    roomID, oldConnID := s.connMgr.TryReconnectInRedis(conn)
    if roomID == "" && oldConnID == "" {
        return false
    }

    // 恢复会话
    s.connMgr.Reconnect(oldConnID, conn, roomID)

    // 通知 game 服务重连
    if roomID != "" {
        s.router.Route(conn, []byte(fmt.Sprintf(
            `{"cmd":"reconnect","request_id":"rc_%s","data":{"room_id":"%s"}}`,
            conn.ConnID, roomID,
        )))
    }

    return true
}
```

### 7.4 handleWebSocket（无需改动）

WebSocket 连接参数保持不变，无需新增 `reconnect_key` 参数：

```go
func (s *Server) handleWebSocket(c *gin.Context) {
    token := c.Query("token")
    deviceID := c.Query("device_id")
    platform := c.Query("platform")

    // ... 原有校验和升级逻辑不变
    // 客户端只需正常连接，Gateway 自动判断重连
}
```

---

## 八、Auth 改造

### 8.1 认证成功时返回重连超时信息

认证成功后，在 auth 响应中告知客户端断线重连的超时时间，客户端可据此决定是否尝试重连：

```go
func (m *AuthMiddleware) OnConnect(conn *connection.Connection, firstMessage []byte) error {
    // ... 原有认证逻辑

    // 认证成功后
    m.sendSuccess(conn, req.Cmd, req.RequestID, map[string]interface{}{
        "user_id":           internalUserID,
        "nickname":          userInfo.Nickname,
        "avatar":            userInfo.Avatar,
        "balance":           userInfo.Balance,
        "reconnect_timeout": 30,   // 断线重连缓冲期（秒）
    })

    return nil
}
```

**说明**：`reconnect_timeout` 仅用于告知客户端重连窗口期，客户端无需携带任何重连标识，Gateway 基于 `user_id` 自动判断。

---

## 九、Router 改造

### 9.1 sendResponse 改造

支持服务端主动通知场景（`conn=nil`）：

```go
func (r *MessageRouter) sendResponse(conn *connection.Connection, resp *message.Response) {
    // conn 为 nil 表示服务端主动通知，不需要发送响应给客户端
    if conn == nil {
        logger.Debug("server-initiated notification, skip sending response", "cmd", resp.Cmd)
        return
    }
    
    data, err := resp.ToJSON()
    if err != nil {
        logger.Error("failed to marshal response", "error", err, "conn_id", conn.ConnID)
        return
    }

    if !conn.Send(data) {
        logger.Warn("failed to send response, send queue full", "conn_id", conn.ConnID)
    }
}
```

### 9.2 本地命令扩展

```go
func (r *MessageRouter) handleLocalCommand(conn *connection.Connection, req *message.Request) *message.Response {
    switch req.Cmd {
    case message.CmdPing:
        conn.UpdateHeartbeat()
        return message.NewSuccessResponse("pong", req.RequestID, &message.PingResponse{
            ServerTime: time.Now().UnixMilli(),
        })
    case message.CmdReconnect:
        return r.handleReconnect(conn, req)
    default:
        return message.NewErrorResponse(req.Cmd, req.RequestID, message.CodeUnknownCommand)
    }
}
```

### 9.3 reconnect 命令转发

`reconnect` 命令路由到 game-service，由 game 服务调用 `HandleReconnect`：

```
1. 调用 RoomAppService.HandleReconnect
2. 清除断线超时
3. 广播 player_reconnected
4. 返回完整 room_state
```

### 9.4 disconnect 命令处理（Game 服务）

Game 服务新增 `disconnect` 命令处理：

```go
// game/server/generic_service.go
case message.CmdDisconnect:
    resp, err = s.handleDisconnect(ctx, req)

func (s *GenericServiceServer) handleDisconnect(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    var data struct {
        RoomID string `json:"room_id"`
        UserID string `json:"user_id"`
    }
    if err := json.Unmarshal(req.Data, &data); err != nil {
        return s.errorResponse(req, message.CodeInvalidParams, "invalid data"), nil
    }

    // 调用断线超时处理
    go s.roomAppService.HandleDisconnectTimeout(context.Background(), data.RoomID, data.UserID)

    return s.successResponse(req, message.CodeSuccess, message.GetErrorMsg(message.CodeSuccess), nil), nil
}
```

---

## 十、Bootstrap 改造

### 10.1 Container 初始化

```go
func NewContainer(cfg *gatewayConfig.Config, redis *cRedis.Client, kafkaProducer *kafka.Producer, nodeID string) *Container {
    connMgr := connection.NewManager(&connection.ManagerConfig{
        MaxConnections:       cfg.Gateway.MaxConnections,
        DisconnectTimeout:    30 * time.Second,
        ConnRedisTTL:         24 * time.Hour,
        HeartbeatRenewalTick: 30 * time.Second,
        KeyPrefix:            "cashparty",
    }, redis, nodeID)  // 注入 Redis + nodeID

    broadcastSvc := broadcast.NewBroadcastService(connMgr, redis)

    return &Container{
        Config:        cfg,
        Redis:         redis,
        KafkaProducer: kafkaProducer,
        ConnMgr:       connMgr,
        BroadcastSvc:  broadcastSvc,
        nodeID:        nodeID,
    }
}
```

### 10.2 事件回调注册

在 `InitServices` 中注册 Manager 事件回调：

```go
func (c *Container) InitServices(serviceDiscovery *discovery.ServiceDiscovery, routerConfig *router.RouterConfig) {
    // ... 原有初始化

    c.ConnMgr.SetEventCallback(func(conn *connection.Connection, event string, roomID string) {
        switch event {
        case "kicked":
            logger.Info("connection kicked", "user_id", conn.UserID)
        case "disconnected":
            logger.Info("connection disconnected, entering buffer", "user_id", conn.UserID, "room_id", roomID)
        case "reconnected":
            logger.Info("connection reconnected", "user_id", conn.UserID, "room_id", roomID)
        case "timeout":
            logger.Info("disconnect timeout, closing", "user_id", conn.UserID, "room_id", roomID)
            // 通知 game 服务处理断线超时（通过 Route 发送 disconnect 命令）
            if roomID != "" {
                c.notifyGameDisconnect(roomID, conn.UserID)
            }
        }
    })
}

// notifyGameDisconnect 通过 Route 发送 disconnect 命令通知 game 服务
func (c *Container) notifyGameDisconnect(roomID, userID string) {
    msg := fmt.Sprintf(`{"cmd":"disconnect","request_id":"dc_%s","data":{"room_id":"%s","user_id":"%s"}}`,
        uuid.New().String()[:8], roomID, userID)
    // conn 为 nil，表示服务端主动通知，不需要发送响应给客户端
    c.MessageRouter.Route(nil, []byte(msg))
}
```

**说明**：`notifyGameDisconnect` 复用 `Route` 机制，通过 `Forward` 接口发送 `disconnect` 命令。传入 `conn=nil` 表示这是服务端主动通知，`sendResponse` 会跳过发送响应给客户端的步骤。

---

## 十一、message 包新增

### 11.1 types.go

```go
CmdReconnect          = "reconnect"
CmdDisconnect         = "disconnect"          // 断线超时通知
PushReconnectSuccess  = "reconnect_success"
```

### 11.2 payload.go

```go
type ReconnectRequest struct {
    RoomID string `json:"room_id"`
}

type ReconnectSuccessPush struct {
    RoomID string      `json:"room_id"`
    State  interface{} `json:"state"`
}
```

### 11.3 errors.go

```go
ReasonLoginElsewhere = "login_elsewhere"

CodeReconnectExpired = 3010  // 重连已过期（断线缓冲期超时）
```

---

## 十二、Redis Key 汇总

| Key | 类型 | 用途 | TTL | 来源 |
|-----|------|------|-----|------|
| `cashparty:gateway:conn:{userID}` | Hash | 用户当前连接映射 | 24h（心跳续期） | Gateway 新增 |
| `cashparty:gateway:disconnect:{userID}` | Hash | 断线缓冲记录 | 30s | Gateway 新增 |
| `cashparty:gateway:kick:{nodeID}` | Pub/Sub Channel | 跨节点踢旧通知 | - | Gateway 新增 |
| `cashparty:player:room:{userID}` | String | 用户所在房间 | 24h | **Game 服务已有** |

---

## 十三、修改文件清单

| 文件 | 变更内容 |
|------|----------|
| `gateway/connection/connection.go` | 删除 `Token` 字段；新增 `StatusDisconnected` 状态、`DisconnectedAt` 字段 |
| `gateway/connection/manager.go` | 删除 `userConnMap` 及其 7 个方法；`userConnections` 改为 1:1 映射；新增 Redis + nodeID + 事件回调；Register 踢旧逻辑；断线缓冲管理；跨节点 Pub/Sub；重连恢复；Lua 脚本；`GetConnectionsByUserID` → `GetConnectionByUserID`；新增 `GetPlayerRoom` 方法 |
| `gateway/server/server.go` | `handleConnection` 认证后自动检查重连；`cleanupConnection` 改为断线缓冲 |
| `gateway/middleware/auth.go` | 认证成功时返回 `reconnect_timeout` |
| `gateway/router/router.go` | `sendResponse` 支持 `conn=nil`（服务端主动通知）；`handleLocalCommand` 增加 `reconnect` 命令处理 |
| `gateway/bootstrap/container.go` | Manager 初始化注入 Redis + nodeID；注册事件回调；新增 `notifyGameDisconnect` 方法 |
| `gateway/broadcast/broadcast.go` | `BroadcastToUser` 内部调用 `GetConnectionByUserID`（方法签名变更适配） |
| `game/server/generic_service.go` | 新增 `disconnect` 命令处理（`handleDisconnect`） |
| `common/message/types.go` | 新增 `CmdReconnect`、`CmdDisconnect`、`PushReconnectSuccess` |
| `common/message/payload.go` | 新增 `ReconnectRequest`、`ReconnectSuccessPush` |
| `common/message/errors.go` | 新增 `ReasonLoginElsewhere`、`CodeReconnectExpired` |

---

## 十四、实施步骤

### 阶段一：连接管理结构优化

1. `Connection` 新增 `StatusDisconnected` 状态和 `DisconnectedAt` 字段
2. 删除 `Token` 字段
3. 删除 `userConnMap` 结构体及其 7 个方法
4. `userConnections` 改为 `userID -> connID`（1:1 映射）
5. `GetConnectionsByUserID` → `GetConnectionByUserID`
6. `ManagerConfig` 增加 `DisconnectTimeout`/`ConnRedisTTL`/`HeartbeatRenewalTick`/`KeyPrefix`
7. 适配 `BroadcastService` 调用

### 阶段二：单连接互踢（本节点）

1. `Manager` 注入 Redis + nodeID
2. 注册前检查内存中是否已有该用户的连接，有则踢旧
3. 踢旧时发送 `kicked` 推送（reason: `login_elsewhere`）
4. 新增 `ReasonLoginElsewhere` 常量
5. 新增事件回调机制

### 阶段三：单连接互踢（跨节点）

1. 实现 `LuaRegisterConnection` Lua 脚本
2. `Manager.Register` 中调用 Lua 脚本写入 Redis 连接映射
3. 实现 Redis Pub/Sub 订阅/发布踢旧通知
4. `Manager` 启动时订阅本节点的 kick channel
5. 心跳续期 Redis 连接映射 TTL

### 阶段四：掉线自动重连

1. `Manager` 新增 `disconnectedConnections` 断线缓冲列表
2. `cleanupConnection` 改为断线缓冲逻辑
3. `cleanupStaleConnections` 改为仅清理断线超时连接
4. 实现 `LuaTryReconnect` Lua 脚本（基于 user_id 查询断线记录，从 `player:room:{userID}` 读取 roomID）
5. 新增 `GetPlayerRoom` 方法从 Redis 读取用户所在房间
6. `handleConnection` 认证成功后自动检查重连
7. 新增 `CmdReconnect` 命令和路由
8. 重连成功后向 game 服务发送 `reconnect` 命令

### 阶段五：端到端测试

1. 单设备：新连接踢旧连接，旧连接收到 `kicked` 推送
2. 多设备：跨节点踢旧，Pub/Sub 通知生效
3. 断线 30s 内重连：同一用户再次登录自动恢复会话，获取房间状态
4. 断线超时：真正断开，game 服务处理断线逻辑
5. 缓冲期外重连：无断线记录，走正常新连接流程（踢旧 + 注册）

---

## 十五、关键时序图

### 15.1 单连接互踢

```
客户端A(旧)                    Gateway                    Redis                 客户端B(新)
   |                            |                         |                       |
   |--------ping/pong---------->|                         |                       |
   |                            |<-----新连接请求---------|-----------------------|
   |                            |                         |                       |
   |                            |---Lua:查询+替换-------->|                       |
   |                            |<--返回旧connID---------|                       |
   |                            |                         |                       |
   |<---push:kicked(elsewhere)--|                         |                       |
   |                            |                         |                       |
   |       (连接关闭)            |---注册新连接----------->|                       |
   |                            |                         |-------auth success--->|
```

### 15.2 跨节点互踢

```
客户端A(旧)          Gateway-1              Redis               Gateway-2          客户端B(新)
   |                    |                     |                     |                   |
   |---ping/pong------->|                     |                     |                   |
   |                    |                     |                     |<---新连接请求-----|
   |                    |                     |                     |                   |
   |                    |                     |<---Lua:查询+替换---|                   |
   |                    |                     |----返回旧信息------>|                   |
   |                    |                     |                     |                   |
   |                    |<--Pub/Sub:kick------|                     |                   |
   |                    |    (nodeID=gw-1)    |                     |                   |
   |<--push:kicked------|                     |                     |---注册新连接----->|
   |   (elsewhere)      |                     |                     |                   |
   |  (连接关闭)        |                     |                     |--auth success--->|
```

### 15.3 掉线自动重连

```
客户端                      Gateway                    Redis                 Game Service
   |                          |                         |                       |
   |---(断线)-----------------|                         |                       |
   |                          |---写入断线记录--------->|                       |
   |                          |   (TTL=30s, 无roomID)   |                       |
   |                          |                         |                       |
   |                    (30s内再次登录)                  |                       |
   |---ws?token(正常登录)---->|                         |                       |
   |                          |---认证成功------------- |                       |
   |                          |                         |                       |
   |                          |---Lua:查询断线记录----->|                       |
   |                          |    + 读取player:room   |                       |
   |                          |<--返回roomID+旧connID--|                       |
   |                          |                         |                       |
   |                          |---删除断线记录--------->|                       |
   |                          |---写入新连接映射------->|                       |
   |                          |                         |                       |
   |                          |---reconnect cmd---------------------------------->|
   |                          |                         |  HandleReconnect:     |
   |                          |                         |  清除断线超时          |
   |                          |                         |  广播player_reconnected|
   |                          |<--room_state--------------------------------------|
   |                          |                         |                       |
   |<--reconnect_success+state-|                         |                       |
   |                          |                         |                       |
   |                    (恢复正常游戏)                    |                       |
```

### 15.4 断线超时

```
客户端                      Gateway                    Redis                 Game Service
   |                          |                         |                       |
   |---(断线)-----------------|                         |                       |
   |                          |---写入断线记录--------->|                       |
   |                          |   (TTL=30s)             |                       |
   |                          |                         |                       |
   |                    (30s超时)                        |                       |
   |                          |---删除断线记录--------->|                       |
   |                          |---删除连接映射--------->|                       |
   |                          |---读取player:room----->|                       |
   |                          |<--返回roomID-----------|                       |
   |                          |                         |                       |
   |                          |---disconnect通知--------------------------------->|
   |                          |                         |  设置断线超时定时器    |
   |                          |                         |  广播player_disconnected|
```

### 15.5 缓冲期外重连（走正常新连接流程）

```
客户端                      Gateway                    Redis                 Game Service
   |                          |                         |                       |
   |---(断线)-----------------|                         |                       |
   |                          |---写入断线记录--------->|                       |
   |                          |   (TTL=30s)             |                       |
   |                          |                         |                       |
   |                    (30s后再次登录)                  |                       |
   |---ws?token(正常登录)---->|                         |                       |
   |                          |---认证成功------------- |                       |
   |                          |                         |                       |
   |                          |---Lua:查询断线记录----->|                       |
   |                          |    (基于user_id)        |                       |
   |                          |<--无断线记录-----------|                       |
   |                          |                         |                       |
   |                          |---走正常新连接流程----->|                       |
   |                          |   (LuaRegisterConn      |                       |
   |                          |    + 踢旧 + 注册)       |                       |
   |                          |                         |                       |
   |<--auth success-----------|                         |                       |
   |                          |                         |                       |
   |                    (作为新连接进入)                  |                       |
```

---

## 十六、优化总结

### 相比原方案的改进

| 项目 | 原方案 | 优化后方案 |
|------|--------|-----------|
| `Connection.RoomID` | 新增字段，本地存储 | **移除**，从 Redis 读取 |
| 断线记录存储 | 存储 `room_id` | **不存储**，重连时从 `player:room:{userID}` 读取 |
| 数据一致性 | 需要同步维护本地 `RoomID` | **无需维护**，直接读取权威数据源 |
| 代码复杂度 | 需要在加入/离开房间时更新本地 `RoomID` | **简化**，无需额外同步逻辑 |
| Lua 脚本 | `LuaTryReconnect` 从断线记录读取 roomID | **改进**，从 `player:room:{userID}` 读取 |

### 设计原则

1. **单一数据源**：`roomID` 以 Game 服务的 `cashparty:player:room:{userID}` 为准
2. **避免冗余**：不在 Gateway 本地重复存储已有数据
3. **最终一致性**：通过 Redis 保证数据一致性，无需额外同步机制
