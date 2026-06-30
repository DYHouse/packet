# Gateway 服务设计文档

> 基于代码实现整理，覆盖连接管理、消息路由、认证、限流、广播、服务发现等核心模块
> 代码路径：`backend/gateway/`

---

## 目录

1. [系统架构总览](#1-系统架构总览)
2. [启动与生命周期](#2-启动与生命周期)
3. [配置设计](#3-配置设计)
4. [HTTP/WebSocket 服务器设计](#4-httpwebsocket-服务器设计)
5. [连接管理设计](#5-连接管理设计)
6. [消息路由设计](#6-消息路由设计)
7. [认证中间件设计](#7-认证中间件设计)
8. [签名校验中间件设计](#8-签名校验中间件设计)
9. [限流设计](#9-限流设计)
10. [广播服务设计](#10-广播服务设计)
11. [服务发现与 gRPC 客户端设计](#11-服务发现与-grpc-客户端设计)
12. [Token 服务设计](#12-token-服务设计)
13. [游戏服务与 HTTP 接口设计](#13-游戏服务与-http-接口设计)
14. [健康检查设计](#14-健康检查设计)
15. [Redis Key 设计](#15-redis-key-设计)
16. [跨节点协同机制](#16-跨节点协同机制)
17. [关键流程时序图](#17-关键流程时序图)
18. [并发控制与锁策略汇总](#18-并发控制与锁策略汇总)
19. [关键设计约束附录](#19-关键设计约束附录)

---

## 1. 系统架构总览

### 1.1 分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                   客户端（WebSocket / HTTP）                  │
└───────────────────────────┬─────────────────────────────────┘
┌───────────────────────────▼─────────────────────────────────┐
│                      Gateway 服务                              │
│  server/ (HTTP+WS) → middleware/ → router/ → connection/      │
│  → broadcast/ → discovery/ → service/ → health/ → bootstrap/  │
└───────┬──────────────────┬───────────────────┬──────────────┘
        │ Redis            │ Kafka              │ gRPC (Nacos)
        │ (连接注册/限流/   │ (跨节点广播消费)    │ (Forward/SaveUser
        │  踢人 Pub/Sub)   │                    │  转发至 game-service)
```

### 1.2 核心设计决策

| 决策 | 说明 |
|------|------|
| **职责单一** | Gateway 只负责连接、认证、路由，不含业务逻辑；业务下沉到 game-service 等后端微服务 |
| **首包 JWT 认证** | WebSocket 建连后首条消息必须为 `cmd=auth`，携带 JWT token；认证通过后绑定用户信息并标记 `StatusAuthed` |
| **跨节点单点登录** | Redis Lua 脚本原子读旧值+写新值，发现旧连接在其它节点则通过 Redis Pub/Sub 通知踢人 |
| **跨节点广播** | 每个网关节点独立 Kafka 消费组（`gateway-broadcast-<nodeID>`），本地过滤（`IsUserConnectedLocally`）后推送 |
| **Redis Lua 滑动窗口限流** | ZSET score 为纳秒时间戳，IP/用户/全局/命令四维度，fail-open 策略 |
| **Nacos 自定义 gRPC Resolver** | `nacos:///` scheme + round_robin 负载均衡 + 10s 轮询刷新地址 |
| **配置三层降级** | Nacos 远程配置 → 本地文件 → 默认值 |
| **优雅关闭分层超时** | Application 30s → Server 30s 等连接关闭 → 各组件 Stop |
| **健康探针三分法** | live（进程活）/ ready（依赖就绪）/ health（综合诊断，degraded 也返回 200） |

### 1.3 模块职责矩阵

| 模块 | 职责 | 关键文件 |
|------|------|---------|
| `bootstrap` | 应用启动、依赖装配、生命周期编排 | `app.go`, `container.go` |
| `config` | 配置定义、加载、默认值、热更新 | `config.go` |
| `server` | HTTP/WebSocket 服务器、连接生命周期 | `server.go` |
| `connection` | 连接封装、状态机、双索引管理、跨节点踢人 | `connection.go`, `manager.go` |
| `router` | 消息路由、gRPC Forward 转发、本地命令 | `router.go` |
| `middleware/auth` | 首包 JWT 认证、IP 锁定 | `auth.go` |
| `middleware/signature` | HTTP 签名校验（GET/POST 分流） | `signature.go` |
| `middleware/ratelimit` | Redis Lua 滑动窗口限流（4 维度） | `ratelimit.go` |
| `broadcast` | Kafka/Redis 双后端广播消费、本地过滤 | `broadcast.go` |
| `discovery` | Nacos 服务发现、gRPC 客户端懒加载缓存 | `discovery.go` |
| `service/token` | JWT HS256 生成与校验 | `token.go` |
| `service/game` | StartGame 流程（SaveUser→Token→URL） | `game.go` |
| `service/test` | 测试 Token 生成 | `test.go` |
| `handler/game` | 游戏 HTTP 接口 | `game.go` |
| `health` | 三层健康探针 | `health.go` |
| `store/memory` | 内存游戏存储（三索引） | `memory.go` |
| `model/game` | Game GORM 模型 | `game.go` |

---

## 2. 启动与生命周期

### 2.1 入口与参数

入口：`cmd/gateway/main.go`（仅调 `bootstrap.Run()`）

`bootstrap.Run()`（`app.go:175-207`）参数解析：
- 默认 `cfgPath = "config/gateway.yaml"`、`routerPath = "config/gateway-router.yaml"`
- 命令行参数覆盖：`os.Args[1]` 为 cfgPath、`os.Args[2]` 为 routerPath
- 监听 `SIGINT`/`SIGTERM` 信号

### 2.2 NewApplication 构造流程（11 步）

`NewApplicationWithConfig`（`app.go:40-95`）：

| 步骤 | 操作 | 失败策略 |
|------|------|---------|
| 1 | `initNacos(cfg)` 初始化 Nacos 客户端 | 未启用则跳过 |
| 2 | 若 Nacos 启用且 `ConfigDataID != ""`，`reloadConfigFromNacos` 拉远端配置覆盖本地 | 失败保留本地配置并告警 |
| 3 | `initLogger(cfg)` 初始化日志 | 必须成功 |
| 4 | `idgen.GetNodeIDString()` 获取节点 ID | 必须成功 |
| 5 | 打印启动信息（name/port/max_connections/go_version/cpu_cores/node_id） | — |
| 6 | `initRedis(cfg)` 初始化 Redis + 3s Ping 校验 | 失败返回 error |
| 7 | 若 `cfg.Kafka.Enabled`，`kafka.NewProducerWithBrokers` 创建生产者 | — |
| 8 | `NewContainer(...)` 构造依赖容器 | — |
| 9 | `loadRouterConfig(...)` 加载路由配置（优先 Nacos，回退本地） | Nacos 失败回退本地 |
| 10 | 若 Nacos 启用则 `discovery.NewServiceDiscovery`；否则告警并禁用服务发现 | 禁用则 userSaver 为 nil |
| 11 | `container.InitServices(...)` 初始化业务服务 | — |

### 2.3 Start 阶段（8 步）

`Application.Start`（`app.go:97-145`）：

| 步骤 | 操作 | 失败策略 |
|------|------|---------|
| 1 | 创建可取消上下文，保存 `cancel` 与 `errChan`（缓冲 1） | — |
| 2 | `Container.InitKafkaConsumer()` 初始化 Kafka 消费者 | — |
| 3 | `Container.InitServer()` 创建 HTTP/WS 服务器 | — |
| 4 | 若 `BroadcastSvc != nil` 启动广播服务 goroutine | — |
| 5 | 若 `nacos != nil` 调用 `RegisterService()` 注册到 Nacos | 失败仅记日志不阻断 |
| 6 | 若 Nacos 启用且 `RateLimiterDataID != ""`，启动 goroutine 监听限流配置热更新 | 解析失败保留旧配置 |
| 7 | 启动 goroutine 运行 `Container.Server.Start()`，错误写入 `errChan` | — |
| 8 | 打印启动日志，返回 nil | — |

### 2.4 优雅关闭流程（6 步）

`Application.Stop`（`app.go:151-173`）：

| 步骤 | 操作 | 超时 |
|------|------|------|
| 1 | `cancel()` 取消启动上下文 | — |
| 2 | 创建 30s 超时的 `shutdownCtx` | 30s |
| 3 | `Container.Server.Stop(shutdownCtx)` 停止 HTTP/WS | 30s（内部 WaitForAllConnectionsClose） |
| 4 | `Container.Stop()` 停止各中间件与服务 | — |
| 5 | `nacos.Close()` 关闭 Nacos 客户端 | — |
| 6 | `Redis.Close()` 关闭 Redis | — |

`Container.Stop`（`container.go:142-160`）逆序关闭 6 组件（均做 nil 检查）：
1. `RateLimiter.Stop()`
2. `AuthMiddleware.Stop()`
3. `SignatureMiddleware.Stop()`
4. `BroadcastSvc.Stop()`
5. `ConnMgr.Stop()`
6. `KafkaProducer.Close()`

### 2.5 DI 容器结构

`Container`（`container.go:24-45`）持有 20 个组件引用：
- 基础设施：`Config`、`Redis`、`KafkaProducer`、`KafkaConsumer`、`nodeID`
- 连接层：`ConnMgr *connection.Manager`、`BroadcastSvc *broadcast.BroadcastService`
- 路由层：`RouterConfig`、`MessageRouter *router.MessageRouter`、`ServiceDiscovery`
- 服务层：`TokenService`、`TestService`、`GameService`、`GameStore`、`GameHandler`
- 中间件：`AuthMiddleware`、`RateLimiter`、`SignatureMiddleware`、`HealthChecker`
- 服务器：`Server *server.Server`

装配方法：
- `NewContainer`：仅初始化 `Config/Redis/KafkaProducer/ConnMgr/nodeID`
- `InitServices`（13 步）：装配路由器、限流器、健康检查、Token、Auth、Signature、Store、UserSaverAdapter、GameService、TestService、GameHandler，并注册事件回调
- `InitServer`：构造 `server.Config` 创建 HTTP/WS 服务器
- `InitKafkaConsumer`：`kafkaGroupID = "gateway-broadcast-<nodeID>"`，创建 `BroadcastService`

---

## 3. 配置设计

### 3.1 配置结构（10 个子配置）

`Config`（`config.go:12-23`）聚合：

| 子配置 | 关键字段 | 默认值 |
|--------|---------|--------|
| `ServerConfig` | Name/Port/Mode/ReadTimeout/WriteTimeout | `gateway-service`/`8081`/`debug`/`60s`/`60s` |
| `GatewayConfig` | ReadBufferSize/WriteBufferSize/SendQueueSize/MaxConnections/AllowedOrigins | `4096`/`4096`/`256`/`10000`/— |
| `RedisConfig` | Addr/Password/DB/PoolSize | —/—/—/`100` |
| `NacosConfig` | Enabled/ServerAddr/Namespace/Group + 3 组 DataID/Group | — |
| `KafkaConfig` | Enabled/Brokers | — |
| `BroadcastConfig` | Mode + Kafka{Topic} + RedisPub{Channel} | — |
| `MerchantConfig` | ID/Secret/GameEntryURL/WsURL | — |
| `TokenConfig` | SecretKey/TokenTTL/Issuer | `default-secret-key-please-change-in-production`/`2h`/`gateway-service` |
| `LogConfig` | Level/Filename/MaxSize/MaxBackups/MaxAge/Compress | `info`/—/`100`/`10`/`30`/— |
| `RateLimiterConfig` | IPRequestsPerSecond/IPBurstSize/UserRequestsPerSecond/UserBurstSize/GlobalRequestsPerSec/CleanupInterval | `100`/`200`/`50`/`100`/`10000`/`1min` |

### 3.2 配置加载入口（4 个）

| 方法 | 用途 | 默认值处理 |
|------|------|-----------|
| `Load(configPath)` | 本地文件加载主配置 | 调用 `setDefaults` |
| `LoadFromContent(content)` | Nacos 远端配置加载主配置 | 调用 `setDefaults` |
| `LoadRouterConfig(configPath)` | 本地加载路由配置 | **不设默认值** |
| `LoadRouterConfigFromContent(content)` | Nacos 加载路由配置 | **不设默认值** |
| `LoadRateLimiterFromContent(content)` | 限流配置独立热更新 | 调用 `setRateLimiterDefaults` |

### 3.3 三层降级策略

```
Nacos 远端配置（最高优先级）
       │ 拉取失败/未启用
       ▼
本地 YAML 文件
       │ 字段缺失
       ▼
setDefaults 默认值
```

- `reloadConfigFromNacos`（`app.go:231-246`）：拉取失败则保留本地配置并告警
- `loadRouterConfig`（`app.go:276-297`）：Nacos 启用且有 `RouterDataID` 时优先 Nacos，拉取失败回退本地
- 限流配置热更新：Nacos 监听回调 → `LoadRateLimiterFromContent` 解析 → `RateLimiter.UpdateConfig` 热更新

### 3.4 关键默认值备注

- `Token.SecretKey` 默认值带 `please-change-in-production` 提示，生产必须覆盖
- `Server.Mode` 配置项在 `server.go:61` 被 `gin.SetMode(gin.ReleaseMode)` 覆盖，未生效
- `ConnMgr` 的 `DisconnectTimeout`/`ConnRedisTTL`/`HeartbeatRenewalTick` 在 `container.go:50-52` 硬编码（30s/24h/30s），非配置驱动

---

## 4. HTTP/WebSocket 服务器设计

### 4.1 路由注册表

`setupRoutes`（`server.go:98-116`）：

| 路由 | 方法 | 中间件 | 处理函数 |
|------|------|--------|---------|
| `/health` | GET | gin.Recovery + requestLogger | `health.CheckHealth` |
| `/ready` | GET | 同上 | `health.CheckReady` |
| `/live` | GET | 同上 | `health.CheckLive` |
| `/test/token` | POST | 同上 | `server.handleTestToken` |
| `/ws` | GET | + RateLimitMiddleware | `server.handleWebSocket` |
| `/game/list` | GET | + SignatureMiddleware | `gameHandler.GetGameList` |
| `/game/start` | POST | + SignatureMiddleware | `gameHandler.StartGame` |

### 4.2 WebSocket 连接生命周期

```
handleWebSocket (server.go:171-194)
  ├── 从 query 取 token，空则 400 + CodeInvalidParams
  ├── upgrader.Upgrade 升级为 WS（HandshakeTimeout=10s）
  ├── utils.GenerateConnID() 生成连接 ID
  ├── connection.NewConnection(connID, wsConn, SendQueueSize)
  ├── 设置 IP 与 UserAgent
  └── go handleConnection(conn, token)

handleConnection (server.go:196-221)
  ├── defer cleanupConnection(conn)
  ├── 构造 auth JSON 报文（含 cmd/auth/request_id/timestamp）
  ├── auth.OnConnect(conn, authReq) 首包认证；失败仅记日志返回
  ├── connMgr.GetPlayerRoom(conn.UserID) 查询是否已有房间
  ├── 有房间 → handleReconnect(conn, roomID)
  │     ├── connMgr.CleanupOldConnection(conn.UserID) 清理旧连接
  │     ├── connMgr.Register(conn) 注册新连接，失败发 CodeConnectionLimit
  │     └── router.Route(conn, reconnectReq) 触发重连流程
  ├── 无房间 → connMgr.Register(conn)，失败发 CodeConnectionLimit
  ├── go writeAndHeartbeatPump(conn)  写/心跳泵
  └── readPump(conn)  读泵（阻塞）

readPump (server.go:239-265)
  ├── defer conn.Close()
  ├── SetReadLimit(512KB)  单帧上限
  ├── SetReadDeadline(60s)
  ├── PongHandler: 收到 Pong 续期 60s + UpdateHeartbeat
  └── 循环 ReadMessage:
        ├── IsUnexpectedCloseError 区分日志级别
        ├── 续期 60s + UpdateHeartbeat
        └── router.Route(conn, messageData)

writeAndHeartbeatPump (server.go:267-299)
  ├── heartbeatTicker = 30s
  ├── defer 停止 ticker + conn.Close()
  └── select:
        ├── <-conn.CloseChan(): 写空关闭帧返回
        ├── msgData, ok := <-conn.SendChan(): chan 关闭则写关闭帧返回；否则 10s 写超时写文本消息
        └── <-heartbeatTicker.C: IsHeartbeatTimeout 则返回；否则 10s 写超时写 PingMessage

cleanupConnection (server.go:301-309)
  ├── StatusAuthed → connMgr.MarkDisconnected(conn)
  ├── StatusDisconnected → connMgr.Unregister(conn.ConnID)
  └── conn.Close()
```

### 4.3 优雅关闭

`Server.Stop`（`server.go:132-169`）：

| 步骤 | 操作 | 超时/降级 |
|------|------|----------|
| 1 | `httpServer.Shutdown(ctx)` | 失败 fallback `httpServer.Close()` |
| 2 | 给所有 WS 连接发送 `CloseGoingAway` 关闭帧 | `connMgr.Range` 遍历 |
| 3 | goroutine 调用 `connMgr.WaitForAllConnectionsClose(30s)` | 30s |
| 4 | `select`：done 完成 → 日志；`ctx.Done()` 超时 → `connMgr.CloseAll()` 强制关闭 | — |
| 5 | `connMgr.Stop()`、`broadcast.Stop()`、`auth.Stop()` | — |

### 4.4 关键超时与阈值

| 项 | 值 | 引用 |
|---|---|---|
| WS HandshakeTimeout | 10s | `server.go:77` |
| WS 读 deadline | 60s | `server.go:243,260` |
| WS 写 deadline | 10s | `server.go:284,293` |
| 心跳 ticker | 30s | `server.go:268` |
| WS 单帧大小上限 | 512KB | `server.go:242` |
| 连接优雅关闭等待 | 30s | `server.go:152` |
| HTTP Read/Write Timeout | 60s（默认） | `config.go:213,216` |

### 4.5 Origin 校验

`upgrader.CheckOrigin`（`server.go:78-89`）：
- `AllowedOrigins` 为空或含 `"*"` → 放行
- 否则用 `utils.ContainsString` 校验 Origin 头

---

## 5. 连接管理设计

### 5.1 Connection 状态机

`Connection`（`connection/connection.go:25-43`）4 态状态机：

```
StatusConnecting(0)  ──(认证成功 SetStatus)──>  StatusAuthed(1)
        │                                             │
        └──(Manager.MarkDisconnected)──>  StatusDisconnected(2)
                                                       │
                                       (Close / 超时)   │
                                                       ▼
                                               StatusClosed(3)  终态，幂等
```

| 状态 | 值 | 含义 |
|------|----|----|
| `StatusConnecting` | 0 | 连接刚建立、未认证 |
| `StatusAuthed` | 1 | 已认证 |
| `StatusDisconnected` | 2 | 已断开（临时断线标记） |
| `StatusClosed` | 3 | 已彻底关闭（终态） |

关键字段：
- `sendChan chan []byte`：带缓冲发送队列（默认 256），队列满即丢
- `closeChan chan struct{}`：关闭信号广播通道
- `closeOnce sync.Once`：保证 `Close` 幂等
- `mu sync.RWMutex`：保护 status/lastHeartbeat/用户元信息
- `lastHeartbeat time.Time`：心跳超时判定（2 分钟）

### 5.2 Send 非阻塞写入

`Send`（`connection.go:99-113`）：
- 读锁下检查 `StatusClosed`
- `select default` 向 `sendChan` 投递；队列满返回 `false`
- 避免写 goroutine 阻塞

### 5.3 Close 幂等关闭

`Close`（`connection.go:127-140`）在 `closeOnce.Do` 内：
1. 加写锁置 `StatusClosed`
2. `close(c.closeChan)`（通知监听者）
3. `close(c.sendChan)`（让读端 range 退出）
4. 底层 `c.conn.Close()`

### 5.4 Manager 双索引设计

`Manager`（`connection/manager.go:41-52`）：
- `localConnections sync.Map`：`connID -> *Connection`（主键索引）
- `userConnections sync.Map`：`userID -> connID`（二级索引，再回查 localConnections）
- `connectionCount int64`：原子计数器，CAS 限流

### 5.5 Register 流程

`Manager.Register`（`manager.go:80-99`）：

| 步骤 | 操作 | 失败策略 |
|------|------|---------|
| 1 | `tryIncrementCount` CAS 限流 | 达上限返回 `"connection limit reached"` |
| 2 | `registerInRedis` Lua 注册 Redis | 出错降级返回 `false, "", ""`（不阻塞注册） |
| 3 | 若 needKick，调用 `kickExistingConnection` | — |
| 4 | 写本地双索引：`localConnections[connID]=conn`、`userConnections[userID]=connID` | — |
| 5 | 记录 info 日志 | — |

`tryIncrementCount`（`manager.go:101-114`）：循环 `LoadInt64` → 比较 `MaxConnections` → `CompareAndSwapInt64(current, current+1)`，CAS 失败重试。

### 5.6 LuaRegisterConnection 跨节点踢人

`registerInRedis`（`manager.go:116-136`）调用 Lua 脚本（`manager.go:374-406`）：

```
KEYS[1] = cashparty:gateway:conn:{userID}
ARGV[1..5] = newConnID, newNodeID, platform, deviceID, connectedAt

流程：
1. HGETALL 读旧数据，解析 oldConnID/oldNodeID
2. HMSET 写入新连接元数据
3. EXPIRE 86400s（24h TTL，硬编码）
4. 若旧 connID 存在且 ≠ 新 connID，返回 {1, oldConnID, oldNodeID}；否则 {0, '', ''}

原子性：整个读-改-写在一个 Lua 脚本内完成
```

`kickExistingConnection`（`manager.go:138-144`）路由：
- `oldNodeID == m.nodeID` → `kickLocalConnection`（本地踢）
- 否则 → `publishKickNotification`（Redis Pub/Sub 跨节点踢）

### 5.7 跨节点踢人 Pub/Sub

- `publishKickNotification`（`manager.go:169-182`）：向 `cashparty:gateway:kick:<oldNodeID>` 频道发布 JSON
- `subscribeKickChannel`（`manager.go:184-209`）：长循环监听本节点 kick 频道，收到消息后调用 `kickLocalConnection`
- `KickMessage` 结构：`{UserID, ConnID, Reason}`

`kickLocalConnection`（`manager.go:146-167`）：
1. 从 `localConnections` 查 conn
2. 构造 `PushKicked` 推送（reason = `ReasonLoginElsewhere`）
3. `c.Send(data)` + `c.Close()`
4. 双索引删除 + `connectionCount - 1`
5. 触发 `EventKicked` 事件

### 5.8 事件回调机制

`Manager` 通过 `eventCallback EventCallback`（`manager.go:46`）支持 4 类事件：
- `EventKicked = "kicked"`
- `EventDisconnected = "disconnected"`
- `EventReconnected = "reconnected"`
- `EventTimeout = "timeout"`

`Container.InitServices` 中注册回调（`container.go:95-102`）：仅记日志区分 kicked/disconnected。

### 5.9 其它关键方法

| 方法 | 用途 |
|------|------|
| `MarkDisconnected` | 置 `StatusDisconnected`、删双索引、计数 -1、触发 `EventDisconnected` |
| `CleanupOldConnection` | 先删 Redis 映射，再清理本地旧 conn |
| `Unregister` | 根据 connID 注销，Close + 双索引删 + 计数 -1 |
| `RenewConnectionTTL` | 刷新 Redis 连接 key 的 TTL 为 `ConnRedisTTL` |
| `BroadcastToUser` | 通过 user 索引取 conn，调用 `conn.Send`；失败记 warn |
| `IsUserConnectedLocally` | 查 `userConnections` map |
| `CloseAll` | Range 所有连接调用 `Close()`（不删索引） |
| `WaitForAllConnectionsClose` | 轮询 `connectionCount==0`，每 100ms 检查，最长等 timeout |

---

## 6. 消息路由设计

### 6.1 路由表与精确匹配

`MessageRouter`（`router/router.go:27-31`）：
- `routes map[string]string`：cmd → service 映射表
- `routesMu sync.RWMutex`：读写分离锁

> 注：字段名虽叫 `CmdPrefix`，但实现是**精确 key 匹配**（`r.routes[cmd]`），非前缀匹配。

`RouteConfig`（`router.go:18-21`）：
```yaml
routes:
  - cmd_prefix: "ping"           # 实际为精确匹配的 cmd
    service: "gateway"           # 本地命令
  - cmd_prefix: "join_room"
    service: "game-service"      # 转发到后端
```

### 6.2 Route 流程

`Route`（`router.go:68-124`）：

| 步骤 | 操作 | 失败处理 |
|------|------|---------|
| 1 | `json.Unmarshal(rawMessage, &req)` | 发送 `CodeInvalidMessage` |
| 2 | Cmd 校验：空 cmd | 发送 `CodeMissingCommand` |
| 3 | RequestID 补全：为空则 `generateRequestID()`（格式 `req_{毫秒时间戳}_{uuid前8位}`） | — |
| 4 | 日志记录（区分 conn 存在/不存在） | — |
| 5 | `getServiceName(req.Cmd)` 精确匹配 | 找不到发送 `CodeUnknownCommand` |
| 6 | 若 `serviceName == "gateway"`，调用 `handleLocalCommand` 并直接回包 | — |
| 7 | `context.WithTimeout(context.Background(), 5s)` | 固定 5s |
| 8 | `forwardToService` gRPC 转发 | 失败返回 `CodeSystemError` |
| 9 | `sendResponse` 回包 | — |

### 6.3 forwardToService gRPC 调用

`forwardToService`（`router.go:137-200`）：

| 步骤 | 操作 |
|------|------|
| 1 | `serviceDiscovery.GetClient(serviceName)` 获取 gRPC 客户端，失败返回 `CodeSystemError` |
| 2 | 从 `conn.UserID` 取用户 ID（conn 为 nil 时为空字符串，支持服务端推送） |
| 3 | 构造 `commonPb.ForwardRequest{UserId, Cmd, RequestId, Data, Timestamp}` |
| 4 | 打印 `[Gateway->Server]` 发送日志（含 data 明文） |
| 5 | `client.Forward(ctx, forwardReq)`，失败返回 `CodeSystemError` |
| 6 | 打印 `[Gateway<-Server]` 接收日志 |
| 7 | 响应 Data 解析：尝试 JSON 反序列化为 `interface{}`，失败则保留原始字节 |
| 8 | 组装 `message.Response` 返回（`Code` 由 `int32` 转 `int`） |

### 6.4 本地命令处理

`handleLocalCommand`（`router.go:202-212`）：
- 仅处理 `message.CmdPing`：调用 `conn.UpdateHeartbeat()`，返回 `PingResponse{ServerTime: 毫秒时间戳}`
- 其他命令返回 `CodeUnknownCommand`

### 6.5 动态路由热更新

| 方法 | 用途 |
|------|------|
| `LoadRoutes` | 批量加载路由配置（覆盖式追加，相同 CmdPrefix 覆盖） |
| `AddRoute` | 动态增加单条路由 |
| `RemoveRoute` | 动态删除单条路由 |

均加写锁，支持运行时热更新路由表。

### 6.6 关键设计要点

- **gRPC Forward 超时硬编码 5s**（`router.go:119`），不可配置，不同 cmd 无法差异化超时
- **本地命令短路**：`serviceName == "gateway"` 时不走 gRPC，直接本地处理
- **sendResponse 容错**：conn 为 nil 时跳过（服务端推送无需回包）；`conn.Send` 返回 false 仅 Warn 不阻塞

---

## 7. 认证中间件设计

### 7.1 首包 JWT 认证

`AuthMiddleware.OnConnect`（`auth.go:50-111`）首包认证核心：

| 步骤 | 操作 | 失败处理 |
|------|------|---------|
| 1 | `json.Unmarshal(firstMessage, &req)` | 发送 `CodeInvalidMessage` + 返回 `ErrInvalidToken` |
| 2 | `req.Cmd != "auth"` 强制首包必须是 auth 命令 | 发送 `CodeUnauthorized` + 返回 `ErrUnauthorized` |
| 3 | `req.ParseData(&authData{Token})` | 发送 `CodeInvalidParams` + 返回 `ErrInvalidToken` |
| 4 | 空 Token 校验 | 返回 `ErrInvalidToken` |
| 5 | `isLocked(conn.IP)` IP 锁定检查 | 发送 `CodeForbidden` + 返回 `ErrTooManyFailedAttempts` |
| 6 | `tokenService.VerifyToken(token)` JWT 校验 | 失败：`recordFailedAttempt` + 发送 `CodeAuthFailed` + 返回 `ErrAuthFailed` |
| 7 | 成功：`clearFailedAttempts(conn.IP)` 清零计数 | — |
| 8 | `conn.SetUserInfo(InternalUserID, Nickname, Avatar)` + `conn.SetStatus(StatusAuthed)` | — |
| 9 | 回包成功：返回 `user_id/nickname/avatar` | — |

### 7.2 IP 锁定机制

`AuthMiddleware`（`auth.go:26-34`）：
- `failedAttempts sync.Map`：内存中的失败次数计数（key=ip, value=int）
- `maxAttempts = 5`（`auth.go:42`）
- `lockDuration = 15 * time.Minute`（`auth.go:43`）

| 方法 | 行为 |
|------|------|
| `isLocked` | 从 `sync.Map` 加载 IP 失败次数，`>= maxAttempts(5)` 即锁定（仅查内存） |
| `recordFailedAttempt` | `LoadOrStore` 原子获取 + `+1` + `Store`；若 `>= maxAttempts`，向 Redis 写 `GatewayLockedIPKey(ip)` = "1"，TTL=15min |
| `clearFailedAttempts` | `failedAttempts.Delete(ip)` 仅清内存（未删 Redis 锁键） |
| `cleanupRoutine` | `time.NewTicker(1h)` 每小时遍历 `failedAttempts` 全部 Delete |

### 7.3 错误哨兵

`auth.go:18-24`：
- `ErrUnauthorized`：非 auth 命令
- `ErrInvalidToken`：JSON 解析失败 / 空 token
- `ErrTokenExpired`：token 过期
- `ErrAuthFailed`：JWT 校验失败
- `ErrTooManyFailedAttempts`：IP 被锁定

### 7.4 已知缺陷

- `isLocked` 仅查内存，服务重启后内存丢失，Redis 锁不会生效
- `clearFailedAttempts` 未删 Redis 锁键，成功认证后 Redis 锁仍存在至 TTL 过期
- 内存计数无 TTL，仅靠每小时清理协程重置

---

## 8. 签名校验中间件设计

### 8.1 签名参数

`SignatureMiddleware`（`signature.go:15-17`）基于商户 ID + 商户密钥签名，从 query string 提取三参数：
- `mid`：商户 ID
- `ts`：时间戳
- `sign`：签名

### 8.2 VerifySignature 中间件流程

`VerifySignature()`（`signature.go:25-105`）：

| 步骤 | 操作 | 失败处理 |
|------|------|---------|
| 1 | 提取 `mid`/`ts`/`sign` | 任一为空返回 401 `Missing signature parameters` |
| 2 | `mid != signer.MerchantID()` | 返回 401 `Invalid merchant ID` |
| 3 | `strconv.ParseInt(tsStr, 10, 64)` | 失败返回 401 `Invalid timestamp` |
| 4 | 方法分流：GET → `verifyGETSignature`；POST → `verifyPOSTSignature`；其他 → 405 | POST body 读取失败返回 500 |
| 5 | `sign != expectedSign` | 返回 401 `Invalid signature` |
| 6 | 通过则 `c.Next()` | — |

### 8.3 GET/POST 分流

`verifyGETSignature`（`signature.go:107-124`）：
1. 收集 URL Query 参数，**排除 `mid`/`ts`/`sign` 三个签名参数**
2. 转为 `map[string]string`（仅取每个 key 的第一个值）
3. 调用 `signer.SignGET(paramMap)` 返回签名

`verifyPOSTSignature`（`signature.go:126-136`）：
1. `io.ReadAll(c.Request.Body)` 读取完整 body
2. **关键：body 回填** — `c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))`，保证后续 handler 能再次读取 body
3. 调用 `signer.SignPOST(bodyBytes)` 返回签名

### 8.4 已知风险

- 时间戳 `ts` 未做防重放/过期校验（如 5 分钟窗口），存在重放攻击风险
- 函数签名里的 `ts` 参数实际未使用，可能是设计遗留

---

## 9. 限流设计

### 9.1 Redis Lua 滑动窗口

`slidingWindowAllow`（`ratelimit.go:97-128`）核心：

```lua
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local windowStart = now - window
redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStart)  -- 清除窗口外旧成员
local count = redis.call('ZCARD', key)                    -- 当前窗口计数
if count < limit then
    redis.call('ZADD', key, now, now)                     -- member=now
    redis.call('PEXPIRE', key, window / 1000000)          -- 毫秒 TTL
    return 1
end
return 0
```

- ZSET score 为纳秒时间戳
- `ZREMRANGEBYSCORE` 清理过期 + `ZCARD` 计数 + `ZADD` 入队，原子性强
- **fail-open**：err != nil 时记录 Error 日志并**返回 true**（放行，`ratelimit.go:122-125`）

### 9.2 四维度限流

| 维度 | 方法 | Key | limit | window |
|------|------|-----|-------|--------|
| IP | `AllowIP(ctx, ip)` | `RateLimitIPKey(ip)` | `IPRequestsPerSecond`(100) | 1s |
| User | `AllowUser(ctx, userID)` | `RateLimitUserKey(userID)` | `UserRequestsPerSecond`(50) | 1s |
| Global | `AllowGlobal(ctx)` | `RateLimitGlobalKey()` | `GlobalRequestsPerSec`(10000) | 1s |
| Command | `AllowCommand(ctx, userID, cmd, limit, window)` | `RateLimitCmdKey(cmd, userID)` | 调用方传入 | 调用方传入 |

### 9.3 4 个预设命令限流

| 中间件 | cmd | limit | window |
|--------|-----|-------|--------|
| `GrabRateLimitMiddleware` | `grab` | 10 | 1s |
| `SendPacketRateLimitMiddleware` | `send_packet` | 5 | 1s |
| `JoinRoomRateLimitMiddleware` | `join_room` | 5 | 1min |
| `SelectSeatRateLimitMiddleware` | `select_seat` | 10 | 1s |

### 9.4 Gin 中间件

`RateLimitMiddleware`（`ratelimit.go:130-155`）IP 维度：
- 取 `c.ClientIP()`，调用 `AllowIP`
- Redis 出错 → fail-open，`c.Next()` 放行
- 超限返回 429 `{success:false, code:429, msg:"rate limit exceeded"}`

`UserRateLimitMiddleware` / `CommandRateLimitMiddleware`：从 `c.Get("user_id")` 取用户 ID。

### 9.5 配置热更新

`UpdateConfig`（`ratelimit.go:43-57`）：
- config 为 nil 直接返回
- 加写锁替换 `rl.config`
- 打印新配置日志（5 个字段）

`getConfig`（`ratelimit.go:59-63`）：加读锁返回当前 config。

### 9.6 已知缺陷

- Lua 中 `ZADD key now now` 的 member 也是 `now`，同一纳秒并发请求会覆盖（仅算一次），高并发下限流偏宽松
- `IPBurstSize`/`UserBurstSize`/`CleanupInterval` 字段定义但未使用

---

## 10. 广播服务设计

### 10.1 BroadcastService 结构

`BroadcastService`（`broadcast/broadcast.go:22-33`）：
- `manager *connection.Manager`：依赖注入的连接管理器
- `redis *cRedis.Client`
- `consumer broadcast.Consumer`：抽象消费者接口（Kafka/Redis 双后端）
- `ctx/cancel/wg`：生命周期
- `roomUsersCache sync.Map`：roomID -> `*roomUsersCacheEntry{users, expiresAt}`
- `cacheTTL time.Duration`：构造时硬编码为 5s

### 10.2 双后端消费

`NewBroadcastService`（`broadcast.go:35-59`）：
1. `cacheTTL = 5 * time.Second`（硬编码）
2. `broadcast.NewConsumerFactory(cfg, kafkaBrokers, kafkaGroupID, redis)`：工厂支持 Kafka/Redis 双后端
3. `factory.CreateConsumer(service.handleBroadcastMessage)`：把处理函数注入消费者

Kafka 消费组：`gateway-broadcast-<nodeID>`（每节点独立，避免多节点互相消费）

### 10.3 handleBroadcastMessage 分发

`handleBroadcastMessage`（`broadcast.go:75-103`）：

| 步骤 | 操作 |
|------|------|
| 1 | `msg.Event + msg.Data` 包装为 `PushMessage`，`ToJSON` 序列化 |
| 2 | switch `msg.TargetType`：<br>• `TargetTypeUser`：优先遍历 `msg.UserIDs`；为空则用 `msg.TargetID`。每个 userID 先 `IsUserConnectedLocally` 过滤，再 `BroadcastToUser`<br>• `TargetTypeRoom`：调 `broadcastToRoom`，传 `ExcludeID`<br>• default：warn 未知类型 |
| 3 | 永远返回 nil（消费成功），marshal 失败返回 err 由消费者决定重试 |

### 10.4 GetRoomUsers 缓存

`GetRoomUsers`（`broadcast.go:105-141`）：
1. 先查 `roomUsersCache`，未过期直接返回
2. 过期则 `Delete` 后回源
3. Redis Pipeline 并发 `HGetAll(RoomPlayersKey)` + `HGetAll(RoomSpectatorsKey)`
4. 合并 players + spectators 的 key（userID 列表）
5. 写回缓存（`expiresAt = now + cacheTTL`）

缓存设计要点：
- `sync.Map[roomID] -> *roomUsersCacheEntry{users, expiresAt}`
- TTL = 5s（硬编码）
- 懒删除：读取时若过期则 Delete 并回源；无主动淘汰
- 并发：sync.Map 适合读多写少，5s TTL 保证最终一致性

### 10.5 broadcastToRoom 本地过滤

`broadcastToRoom`（`broadcast.go:143-170`）：
1. 取 room 用户列表
2. 遍历，跳过 `excludeUserID`
3. 仅对 `IsUserConnectedLocally` 的用户 `BroadcastToUser`
4. Debug 日志统计 total_users / local_users

### 10.6 跨节点广播机制

- 每个网关节点独立 Kafka 消费组（`gateway-broadcast-<nodeID>`）
- 本地过滤（`IsUserConnectedLocally`）后推送
- 跨节点用户由各自节点独立消费处理
- 避免广播消息跨节点转发

---

## 11. 服务发现与 gRPC 客户端设计

### 11.1 Nacos 自定义 Resolver

`nacosResolverBuilder`（`discovery/discovery.go:24-26`）：
- `Scheme()` 返回 `"nacos"`（对应 `grpc.Dial("nacos:///service")`）
- `Build` 从 `target.URL.Host` 或 `target.URL.Path` 取 serviceName

`nacosResolver`（`discovery.go:46-51`）：
- `start`：首次同步调用 `updateAddresses()`，启动 `go r.watch(ctx)`
- `watch`：`time.NewTicker(10s)`，每 10s 触发 `updateAddresses()`（纯轮询，无 push 订阅）
- `updateAddresses`：`nacosClient.DiscoverService(serviceName)` 拉取实例列表，出错仅 Error 日志保留旧地址；仅当 `len(addrs) > 0` 时 `cc.UpdateState`

### 11.2 ServiceDiscovery 懒加载缓存

`ServiceDiscovery`（`discovery.go:103-108`）：
- `connections sync.Map`：serviceName → `*grpc.ClientConn`
- `clients sync.Map`：serviceName → `ServiceClient`
- 双重缓存，首次访问才建立连接

`GetClient`（`discovery.go:117-131`）：
1. `clients.Load(serviceName)` 命中直接返回
2. 未命中调用 `getConnection` 获取/建立 gRPC 连接
3. 包装为 `genericServiceClient` 存入 `clients` map
4. 返回 client

`getConnection`（`discovery.go:133-152`）：
- target：`nacos:///<serviceName>`
- `grpc.WithTransportCredentials(insecure.NewCredentials())`（明文，无 TLS）
- `grpc.WithDefaultServiceConfig({"loadBalancingPolicy":"round_robin"})`（round_robin 负载均衡）
- `grpc.WithResolvers(d.builder)`（注册自定义 nacos resolver）

### 11.3 genericServiceClient

`genericServiceClient`（`discovery.go:163-165`）实现 `ServiceClient` 接口：
- `Forward`/`SaveUser`：调用 `commonPb.NewGenericServiceClient(conn).XXX`
- `Close`：关闭底层 conn

### 11.4 UserSaverAdapter

`UserSaverAdapter`（`discovery.go:182-184`）实现 `service.UserSaver` 接口：
- `SaveUser`（`discovery.go:190-206`）：
  1. `discovery.GetClient("game-service")`（**硬编码服务名 "game-service"**）
  2. 构造 `SaveUserRequest{UserId, Nickname, Avatar, Ip, DeviceId}`
  3. 调用 `client.SaveUser(ctx, req)`
  4. 返回 `resp.Id, resp.Avatar, nil`

### 11.5 已知缺陷

- 无 TLS（`insecure.NewCredentials()`），内网可接受，跨网段有风险
- 无连接健康检查/断路器
- `grpc.Dial` 是已废弃 API，应迁移到 `grpc.NewClient`
- `Close` 时未清理 `clients` map

---

## 12. Token 服务设计

### 12.1 GameTokenClaims

`GameTokenClaims`（`service/token.go:11-18`）自定义 Claims：
- `SessionID string` json:`sid`：会话 ID（每次生成 token 时 `uuid.New` 生成）
- `InternalUserID string` json:`uid`：内部用户 ID
- `PlatformUserID string` json:`puid`：平台用户 ID
- `Nickname string` json:`nick`
- `Avatar string` json:`avatar`
- 内嵌 `jwt.RegisteredClaims`：含 iss/iat/exp/nbf

### 12.2 GenerateToken

`GenerateToken`（`token.go:47-76`）：
1. **SecretKey 校验**：`len(SecretKey) < 32` 返回错误 `"secret key must be at least 32 characters"`
2. 取当前时间 `now`
3. 生成 `sessionID = uuid.New().String()`
4. 构造 `GameTokenClaims`：
   - SessionID/InternalUserID/PlatformUserID/Nickname/Avatar
   - `Issuer=config.Issuer`、`IssuedAt=now`、`ExpiresAt=now+TokenTTL`、`NotBefore=now`
5. `jwt.NewWithClaims(jwt.SigningMethodHS256, claims)`（**强制 HS256**）
6. `token.SignedString([]byte(SecretKey))` 签名

### 12.3 VerifyToken 防御

`VerifyToken`（`token.go:78-96`）：
1. `jwt.ParseWithClaims(tokenString, &GameTokenClaims{}, keyFunc)`
2. **keyFunc 内 alg 校验**（`token.go:80-83`）：
   - `if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok` → 返回错误
   - **防 alg=none 攻击**：仅接受 HMAC 类型签名方法，拒绝 `none`/RS256 等
   - 返回 `[]byte(SecretKey)` 作为验证密钥
3. Parse 失败：包装 `"failed to parse token: %w"`
4. 类型断言 `token.Claims.(*GameTokenClaims)`，断言失败或 `!token.Valid` → 返回 `"invalid token claims"`

### 12.4 TokenConfig 默认值

| 字段 | 默认值 |
|------|--------|
| `SecretKey` | `default-secret-key-please-change-in-production` |
| `TokenTTL` | `2h` |
| `Issuer` | `gateway-service` |

### 12.5 已知缺陷

- 无 token 吊销机制（无 blacklist）
- 无 refresh token 机制
- `VerifyToken` 错误信息未区分过期/未生效

---

## 13. 游戏服务与 HTTP 接口设计

### 13.1 GameService StartGame 流程

`StartGame`（`service/game.go:59-120`）：

| 步骤 | 操作 | 失败处理 |
|------|------|---------|
| 1 | 日志：`game_code` + `user_id` | — |
| 2 | `!game.IsActive()` 游戏状态校验 | 返回 `"game is not active"` |
| 3 | `saveUserAndGetInternalID` 保存用户 | 失败包装 `ErrUserSaveFailed` |
| 4 | 头像回填：avatarURL 非空则覆盖 `req.Avatar`（服务端返回优先） | — |
| 5 | `tokenService.GenerateToken` 生成 Token | 失败包装 `"failed to generate token: %w"` |
| 6 | 构造 gameURL 参数（10 个）：currency/gameId/gameName/lang/mid/resource_id/token/userId/version/wsUrl | — |
| 7 | 拼接 gameURL：`{cfg.Merchant.GameEntryURL}/{game.GameCode}?{params.Encode()}` | — |
| 8 | 构造 gameConfig map（4 个）：user_token/currency/lang/ws_url | — |
| 9 | JSON 序列化 gameConfig 为字符串 | 失败包装 |
| 10 | 组装响应：UserToken/GameURL/GameConfig | — |

`saveUserAndGetInternalID`（`game.go:122-135`）：
- `userSaver == nil` → 返回 `ErrUserSaveFailed: user saver not configured`
- 调用 `userSaver.SaveUser(ctx, userID, nickname, avatar, clientIP, "")`（**deviceID 传空字符串**）

### 13.2 GameHandler HTTP 接口

`GameHandler`（`handler/game.go:13-16`）两个接口：

**GetGameList**（`game.go:35-67`）：
1. `c.ShouldBindQuery(&req)`（Page 字段绑定但未使用，未做分页）
2. `gameStore.GetAllGames()`
3. `gameService.GetGameList(ctx, games)`（仅日志 + 原样返回）
4. 响应 `{code:0, msg:"", data:gameList}`

**StartGame**（`game.go:69-133`）：
1. `c.ShouldBindJSON(&req)`（`service.GameStartRequest`）
2. 游戏查找（优先 GameCode，其次 GameID，两者皆空拒绝）：
   - `req.GameCode != ""` → `gameStore.GetGameByCode(req.GameCode)`
   - `req.GameID > 0` → `gameStore.GetGameByID(req.GameID)`
3. 查找失败 → 404 `Game not found`
4. `gameService.StartGame(ctx, &req, game)`
5. 错误映射：
   - `errors.Is(err, service.ErrUserSaveFailed)` → 401
   - 其他 → 500
6. 成功响应 `{code:0, msg:"", data:resp}`

### 13.3 TestService

`TestService.GenerateTestToken`（`service/test.go:34-65`）：简化版 StartGame，仅生成 token 不构造 URL。
- Nickname 默认 `"TestPlayer"`
- 硬编码 IP=`127.0.0.1`，deviceID=`test-device`
- **风险**：应在生产环境关闭此接口

### 13.4 MemoryGameStore

`MemoryGameStore`（`store/memory.go:10-15`）三索引设计：
- `games []*model.Game`：slice（保序，用于 GetAllGames）
- `gameMap map[int]*model.Game`：主键 ID → Game
- `codeMap map[string]*model.Game`：业务键 GameCode → Game
- `mu sync.RWMutex`：单一读写锁保护三个结构

`initSampleData`（`memory.go:69-85`）内置一个样例（redpacket 游戏）。

### 13.5 Game GORM 模型

`Game`（`model/game.go:5-16`）表 `games`：

| 字段 | 类型 | GORM tag | 说明 |
|------|------|---------|------|
| `ID` | `int` | `primaryKey;autoIncrement` | 主键自增 |
| `Name` | `string` | `size:255;not null` | 游戏名 |
| `GameCode` | `string` | `size:100;uniqueIndex;not null` | 业务唯一码 |
| `Category` | `string` | `size:50;not null` | 分类 |
| `Provider` | `string` | `size:100;not null` | 供应商 |
| `ResourceID` | `int` | `default:1` | 资源 ID |
| `CoverURL` | `string` | `size:500` | 封面图 |
| `Status` | `int` | `default:1` | 状态（1=active, 2=inactive） |
| `CreatedAt`/`UpdatedAt` | `time.Time` | — | GORM 自动管理 |

`IsActive()`：`Status == int(GameStatusActive)`

---

## 14. 健康检查设计

### 14.1 三层探针

`HealthChecker`（`health/health.go:15-18`）：

| 探针 | 路由 | 检查项 | 失败响应 |
|------|------|--------|---------|
| `/live` | `CheckLive`（`health.go:113-117`） | 无（纯存活） | 永远 200 `{"status":"alive"}` |
| `/ready` | `CheckReady`（`health.go:95-111`） | Redis Ping（2s 超时） | 503 `{"status":"not_ready","error":"..."}` |
| `/health` | `CheckHealth`（`health.go:38-60`） | 综合诊断（连接数 + Redis 延迟 + 系统资源） | 200 + `status="degraded"` |

### 14.2 CheckHealth 综合诊断

| 步骤 | 操作 |
|------|------|
| 1 | 2 秒超时上下文 |
| 2 | 初始化 `HealthStatus{Status:"ok", Service:"gateway", Timestamp:UnixMilli, Dependencies:make(...)}` |
| 3 | `connMgr.GetConnectionCount()` 获取当前连接数 |
| 4 | `checkRedis(ctx)` 检查 Redis，写入 `Dependencies["redis"]` |
| 5 | 若 Redis 状态非 `ok` 则整体 `Status = "degraded"`（**degraded 也返回 200**） |
| 6 | `getSystemResources()` 收集运行时资源 |
| 7 | 返回 200 + JSON |

### 14.3 checkRedis

`checkRedis`（`health.go:62-79`）：
- 记录开始时间
- `redis.Raw().Ping(ctx).Err()`
- 成功 → `status=ok` + `latency_ms`
- 失败 → `status=error` + `error` 字符串 + 记错误日志

### 14.4 getSystemResources

`getSystemResources`（`health.go:81-93`）通过 `runtime.ReadMemStats` 返回：
- `goroutines`：`runtime.NumGoroutine()`
- `cpu_cores`：`runtime.NumCPU()`
- `memory_mb`：Alloc
- `heap_mb`：HeapAlloc
- `stack_mb`：StackInuse
- `gc_pause_ns`：PauseTotalNs

### 14.5 设计要点

- 三探针分离：`/live` 最轻量（K8s liveness）；`/ready` 检查 Redis（K8s readiness）；`/health` 综合诊断
- 降级策略：Redis 异常时 `/health` 返回 200 但 `status="degraded"`，避免误杀
- 所有外部调用带 2s 超时，防止探针阻塞
- `runtime.ReadMemStats` 是 STW 调用，频繁调用可能有性能影响

### 14.6 已知缺陷

- `HealthStatus.MaxConnections` 与 `Uptime` 字段定义但 `CheckHealth` 未赋值（`health.go:32,35` vs `:42-47`）
- `connections` 字段类型为 `int64`，与 `MaxConnections int` 不一致

---

## 15. Redis Key 设计

### 15.1 Key 常量与构造函数

`keys.go` 统一管理 10 个 Redis Key，前缀 `cashparty`：

| Key 常量 | 模板 | 构造函数 | 用途 |
|---------|------|---------|------|
| `KeyGatewayConn` | `cashparty:gateway:conn:%s` | `GatewayConnKey(userID)` | 用户连接信息（hash） |
| `KeyGatewayKick` | `cashparty:gateway:kick:%s` | `GatewayKickKey(nodeID)` | 按 nodeID 的踢人通知（Pub/Sub 频道） |
| `KeyPlayerRoom` | `cashparty:player:room:%s` | `PlayerRoomKey(userID)` | 玩家当前房间 |
| `KeyRateLimitIP` | `cashparty:ratelimit:ip:%s` | `RateLimitIPKey(ip)` | IP 限流计数（ZSET） |
| `KeyRateLimitUser` | `cashparty:ratelimit:user:%s` | `RateLimitUserKey(userID)` | 用户限流计数（ZSET） |
| `KeyRateLimitGlobal` | `cashparty:ratelimit:global` | `RateLimitGlobalKey()` | 全局限流（无参数，ZSET） |
| `KeyRateLimitCmd` | `cashparty:ratelimit:cmd:%s:%s` | `RateLimitCmdKey(cmd, userID)` | 按 cmd+user 的命令级限流（双参数，ZSET） |
| `KeyGatewayLockedIP` | `cashparty:gateway:locked_ip:%s` | `GatewayLockedIPKey(ip)` | 封禁 IP |
| `KeyRoomPlayers` | `cashparty:room:players:%s` | `RoomPlayersKey(roomID)` | 房间玩家集合（hash） |
| `KeyRoomSpectators` | `cashparty:room:spectators:%s` | `RoomSpectatorsKey(roomID)` | 房间观众集合（hash） |

### 15.2 命名规则

统一 `cashparty:<域>:<子域>:<动态参数>` 三段以上冒号分隔，便于 Redis CLI 模式扫描与按域统计。

### 15.3 测试覆盖

`keys_test.go` 覆盖：
- 10 个独立单测，断言精确字符串
- `TestKeyPrefix`：遍历全部 Key 常量，校验均以 `cashparty` 开头
- `TestKeyFormat`：表驱动测试，验证 `fmt.Sprintf(key, args...)` 格式化

---

## 16. 跨节点协同机制

### 16.1 单点登录踢人

```
用户 A 在节点 1 登录 → 用户 A 在节点 2 登录

节点 2 Register:
  ├── registerInRedis (LuaRegisterConnection)
  │     ├── HGETALL 读旧数据：oldConnID=node1-conn, oldNodeID=node1
  │     ├── HMSET 写入新连接元数据
  │     └── 返回 {1, oldConnID, oldNodeID=node1}
  ├── needKick=true, oldNodeID=node1 ≠ node2
  └── publishKickNotification → cashparty:gateway:kick:node1

节点 1 subscribeKickChannel:
  ├── 收到 KickMessage{UserID, ConnID, Reason}
  └── kickLocalConnection:
        ├── 构造 PushKicked 推送（reason=ReasonLoginElsewhere）
        ├── c.Send(data) + c.Close()
        └── 双索引删除 + 计数 -1 + EventKicked
```

### 16.2 跨节点广播

```
game-service 发布广播消息 → Kafka topic

节点 1 BroadcastService（消费组 gateway-broadcast-node1）:
  ├── handleBroadcastMessage
  ├── TargetTypeRoom → broadcastToRoom
  │     ├── GetRoomUsers（5s 缓存）
  │     └── 仅对 IsUserConnectedLocally 的用户推送
  └── 跨节点用户由各自节点独立消费处理

节点 2 BroadcastService（消费组 gateway-broadcast-node2）:
  └── 同上，独立消费同一 topic
```

关键设计：
- 每个网关节点独立 Kafka 消费组（`gateway-broadcast-<nodeID>`）
- 本地过滤（`IsUserConnectedLocally`）避免无效 Send
- 跨节点用户由各自节点处理，无需跨节点转发

### 16.3 多节点隔离

- 每节点唯一 `nodeID`（雪花算法生成）
- Redis Key 按节点区分：`cashparty:gateway:kick:<nodeID>`
- Kafka 消费组按节点区分：`gateway-broadcast-<nodeID>`
- 连接注册按用户区分：`cashparty:gateway:conn:<userID>`（含 nodeID 字段）

---

## 17. 关键流程时序图

### 17.1 WebSocket 建连 + 认证

```
客户端            Server              AuthMiddleware       ConnMgr            Router
  │  WS Upgrade     │                     │                  │                 │
  │ ──────────────► │                     │                  │                 │
  │  101 Upgrade    │                     │                  │                 │
  │ ◄────────────── │                     │                  │                 │
  │                 │  NewConnection      │                  │                 │
  │                 │  handleConnection   │                  │                 │
  │                 │  构造 auth JSON     │                  │                 │
  │                 │ ───OnConnect──────► │                  │                 │
  │                 │                     │  VerifyToken     │                 │
  │                 │                     │  SetUserInfo     │                 │
  │                 │                     │  SetStatus(Authed)│                │
  │                 │  auth success       │                  │                 │
  │                 │ ◄────────────────── │                  │                 │
  │                 │  GetPlayerRoom ─────────────────────► │                 │
  │                 │  roomID=""          │                  │                 │
  │                 │  Register ────────────────────────────► │                 │
  │                 │                     │                  │  CAS 限流        │
  │                 │                     │                  │  LuaRegister    │
  │                 │                     │                  │  写双索引         │
  │                 │  registered         │                  │                 │
  │                 │ ◄────────────────────────────────────── │                 │
  │                 │  go writeAndHeartbeatPump              │                 │
  │                 │  readPump (阻塞)                       │                 │
  │  auth resp      │                     │                  │                 │
  │ ◄────────────── │                     │                  │                 │
  │  后续消息        │                     │                  │                 │
  │ ──────────────► │ ───Route──────────────────────────────────────────────► │
  │                 │                     │                  │  gRPC Forward   │
  │  推送/响应       │                     │                  │                 │
  │ ◄────────────── │                     │                  │                 │
```

### 17.2 单点登录踢人

```
节点1(旧连接)        Redis                节点2(新连接)

  │  conn1 在线       │                     │
  │                   │  Register           │
  │                   │ ◄──LuaRegister───── │
  │                   │  返回 needKick=1    │
  │                   │   oldNodeID=node1   │
  │                   │  publishKick ─────► │ (cashparty:gateway:kick:node1)
  │                   │                     │
  │  subscribeKickChannel 收到消息           │
  │  kickLocalConnection:                  │
  │   - PushKicked 推送                     │
  │   - conn1.Close()                      │
  │   - 删双索引 + 计数-1                    │
  │   - EventKicked 回调                    │
  │  conn1 关闭       │                     │
  │                   │  新连接注册成功       │
```

### 17.3 消息路由转发

```
客户端            Router              Discovery           game-service

  │  JSON 消息        │                     │                  │
  │ ──────────────► │                     │                  │
  │                 │  json.Unmarshal     │                  │
  │                 │  getServiceName     │                  │
  │                 │  (精确匹配)          │                  │
  │                 │  本地命令? ──是──► handleLocalCommand   │
  │                 │  否                 │                  │
  │                 │  ctx 5s 超时        │                  │
  │                 │ ──GetClient───────► │                  │
  │                 │  gRPC client        │                  │
  │                 │ ──Forward(ctx, req)──────────────────► │
  │                 │                     │                  │  业务处理
  │                 │ ◄──────────────────────────────────── │
  │                 │  ForwardResponse    │                  │
  │                 │  sendResponse       │                  │
  │  响应             │                     │                  │
  │ ◄────────────── │                     │                  │
```

### 17.4 广播消息分发

```
game-service       Kafka              节点1 BroadcastSvc        ConnMgr

  │  发布广播消息     │                     │                       │
  │ ──────────────► │                     │                       │
  │                 │  消费(组=node1)      │                       │
  │                 │ ──────────────────► │                       │
  │                 │                     │  handleBroadcastMessage│
  │                 │                     │  switch TargetType:    │
  │                 │                     │   TargetTypeUser:      │
  │                 │                     │    遍历 UserIDs        │
  │                 │                     │    IsUserConnectedLocally│
  │                 │                     │ ──BroadcastToUser────► │
  │                 │                     │                       │  conn.Send
  │                 │                     │   TargetTypeRoom:      │
  │                 │                     │    broadcastToRoom     │
  │                 │                     │    GetRoomUsers(5s缓存)│
  │                 │                     │    过滤本地在线用户     │
  │                 │                     │ ──BroadcastToUser────► │
  │                 │                     │                       │  conn.Send
```

### 17.5 优雅关闭

```
SIGINT/SIGTERM      Application          Server              ConnMgr          各组件

  │                  │                    │                    │                │
  │ ──────────────► │                    │                    │                │
  │                 │  cancel()          │                    │                │
  │                 │  shutdownCtx 30s   │                    │                │
  │                 │ ──Stop(ctx)──────► │                    │                │
  │                 │                    │  httpServer.Shutdown│                │
  │                 │                    │  发送 CloseGoingAway │                │
  │                 │                    │ ──WaitForAllClose─► │                │
  │                 │                    │   (30s 轮询)        │                │
  │                 │                    │   超时→CloseAll     │                │
  │                 │                    │ ◄────────────────── │                │
  │                 │                    │ ──Stop─────────────────────────────► │
  │                 │                    │                    │   RateLimiter.Stop
  │                 │                    │                    │   AuthMiddleware.Stop
  │                 │                    │                    │   SignatureMiddleware.Stop
  │                 │                    │                    │   BroadcastSvc.Stop
  │                 │                    │                    │   ConnMgr.Stop
  │                 │                    │                    │   KafkaProducer.Close
  │                 │ ◄────────────────────────────────────────────────────── │
  │                 │  nacos.Close       │                    │                │
  │                 │  Redis.Close       │                    │                │
  │  停止完成         │                    │                    │                │
```

---

## 18. 并发控制与锁策略汇总

### 18.1 锁与并发原语一览

| 类型 | 位置 | 用途 |
|------|------|------|
| `sync.RWMutex` | `connection.go:38` | 保护 status/lastHeartbeat/用户元信息 |
| `sync.Once` | `connection.go:37` | `Close` 幂等 |
| `chan []byte` (buffered 256) | `connection.go:34` | 发送队列 + 背压 |
| `chan struct{}` | `connection.go:35` | 关闭广播信号 |
| `sync.Map` × 2 | `manager.go:43-44` | 双索引（connID + userID）无锁读写 |
| `atomic.Int64` | `manager.go:48` | 连接计数 + CAS 限流 |
| `sync.WaitGroup` | `manager.go:49` | kick 订阅 goroutine 同步 |
| `context.CancelFunc` | `manager.go:50` | 优雅停机 |
| `sync.Map` | `broadcast.go:31` | 房间用户缓存 |
| `sync.WaitGroup` + `context` | `broadcast.go:28-29` | 消费者生命周期 |
| `sync.RWMutex` | `router.go:30` | 路由表读写分离 |
| `sync.Map` | `auth.go:29` | IP 失败计数 |
| `context.WithCancel` | `auth.go:37` | 清理协程生命周期 |
| `time.Ticker` | `auth.go:151` | 每小时清理失败计数 |
| `sync.RWMutex` | `ratelimit.go:30` | 限流配置读写分离 |
| `sync.Map` × 2 | `discovery.go:105-106` | 服务连接 + 客户端双缓存 |
| `context.WithCancel` | `discovery.go:54` | resolver watch 协程生命周期 |
| `time.Ticker` | `discovery.go:61` | 10s 轮询 Nacos |
| `sync.RWMutex` | `store/memory.go:14` | 三索引统一保护 |

### 18.2 并发安全设计要点

1. **双索引 sync.Map**：`localConnections` + `userConnections` 无锁并发读，写入时成对更新
2. **CAS 限流**：`atomic.Int64` + `CompareAndSwapInt64` 自旋实现无锁软上限
3. **closeOnce 幂等关闭**：多次调用 `Close` 安全，避免重复 close channel panic
4. **Send 非阻塞**：`select default` 实现背压，队列满返回 false 不阻塞
5. **读写分离锁**：路由表、限流配置用 `sync.RWMutex`，读多写少场景优化
6. **懒加载缓存**：`discovery` 的 `connections` + `clients` 双层 `sync.Map`，首次访问才建立连接

### 18.3 潜在竞态点

1. **Connection.Send 与 Close 并发**：Send 持 RLock 检查 `StatusClosed` 后向 `sendChan` 投递，Close 在 `closeOnce.Do` 内先持 Lock 置状态再 `close(sendChan)`，锁会互斥但 Close 释放 Lock 后 Send 才进入，仍可能写入已 close 的 sendChan（理论竞态窗口）
2. **MemoryGameStore.AddGame 无唯一性校验**：重复 Add 同 ID/Code 会覆盖 map 旧值，但 slice 保留旧指针，导致数据不一致
3. **MemoryGameStore.GetAllGames 浅拷贝**：返回元素指针与存储共享，调用方修改 Game 字段会污染存储
4. **subscribeKickChannel JSON Unmarshal 错误被忽略**：可能导致踢人消息丢失
5. **Lua 脚本 TTL 86400 硬编码**：与 `ManagerConfig.ConnRedisTTL` 不联动，配置改动不影响脚本 TTL
6. **Manager.CloseAll 不删索引不调整计数**：依赖后续 Unregister 清理，若调用顺序不当会留下脏索引

---

## 19. 关键设计约束附录

### 19.1 职责约束

1. Gateway 只负责连接、认证、路由，不含业务逻辑
2. 业务逻辑下沉到 game-service 等后端微服务，通过 gRPC Forward 转发
3. 本地命令（如 `ping`）短路处理，不走 gRPC

### 19.2 连接管理约束

4. 单节点连接数上限通过 CAS 限流（默认 10000），达上限拒绝新连接
5. 单用户单连接：Redis Lua 原子检测旧连接，发现则跨节点踢人
6. Connection 关闭幂等（`closeOnce`），多次调用安全
7. Send 非阻塞：队列满返回 false，不阻塞写 goroutine

### 19.3 认证约束

8. WebSocket 首包必须是 `cmd=auth`，否则拒绝
9. JWT 强制 HS256，SecretKey ≥ 32 字符，防 alg=none 攻击
10. IP 失败 5 次锁定 15 分钟（内存计数 + Redis 持久化双写）

### 19.4 限流约束

11. 四维度限流：IP / User / Global / Command
12. Redis Lua 滑动窗口原子操作，fail-open 策略（Redis 故障放行）

### 19.5 广播约束

13. 每节点独立 Kafka 消费组（`gateway-broadcast-<nodeID>`）
14. 本地过滤（`IsUserConnectedLocally`）避免无效 Send
15. 房间用户列表缓存 5s TTL，懒删除

### 19.6 关闭约束

16. 优雅关闭分层超时：Application 30s → Server 30s 等连接关闭 → 各组件 Stop
17. 关闭超时降级：`WaitForAllConnectionsClose` 超时则 `CloseAll` 强制关闭

### 19.7 可观测性约束

18. 三层健康探针：live（进程活）/ ready（依赖就绪）/ health（综合诊断，degraded 也返回 200）
19. 健康检查外部调用带 2s 超时，防止探针阻塞
