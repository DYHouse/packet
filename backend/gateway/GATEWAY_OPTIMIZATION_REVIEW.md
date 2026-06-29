# Gateway 代码审查与优化建议

> 审查范围：`packet/backend/gateway/` 全部代码及配套配置（`config/gateway*.yaml`）
> 审查日期：2026-06-29
> 审查目标：识别安全、并发、性能、可维护性等方面的问题并给出优化建议

---

## 一、问题总览

| 严重级别 | 数量 | 说明 |
|---------|------|------|
| P0 严重 | 9 | 安全漏洞、数据竞争、可能 panic 的代码 |
| P1 重要 | 12 | 逻辑错误、资源泄漏、双停问题 |
| P2 一般 | 14 | 性能、可维护性、配置不一致 |
| P3 建议 | 6 | 命名、文档、测试覆盖 |

按类别分布：

| 类别 | 数量 |
|------|------|
| 安全 | 11 |
| 并发/正确性 | 8 |
| 性能 | 8 |
| 资源管理 | 5 |
| 可维护性 | 5 |
| 配置 | 4 |

---

## 二、P0 严重问题（必须优先修复）

### P0-1 签名校验未防重放，时间戳未参与签名

**位置**：[middleware/signature.go](file:///e:/demo/party/packet/backend/gateway/middleware/signature.go)

**问题**：
- `ts` 参数虽被解析，但从未校验新鲜度（无 `now-ts` 与阈值的比较）。
- `verifyGETSignature` / `verifyPOSTSignature` 计算 `sign` 时均未把 `ts` 纳入签名内容，攻击者可随意篡改时间戳。
- 综合效果：截获一次合法签名请求即可无限重放，可冒充商户发起 `/game/start` 等敏感操作。

**修复建议**：
```go
// 1. 时间戳新鲜度校验（建议 5 分钟）
const maxSkew = 5 * time.Minute
if now := time.Now(); ts < now.Add(-maxSkew).UnixMilli() || ts > now.Add(maxSkew).UnixMilli() {
    c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "timestamp expired"})
    c.Abort()
    return
}
// 2. 将 ts 纳入签名计算（Signer 侧需同步调整）
expectedSign = m.signer.SignWithTimestamp(paramMap, bodyBytes, ts)
// 3. 可选：基于 redis SETNX 做 nonce 防重放（key=cashparty:sign:nonce:{sign}, ttl=maxSkew*2）
```

---

### P0-2 `handleConnection` / `handleReconnect` 用 fmt.Sprintf 拼 JSON 存在注入与解析失败风险

**位置**：[server/server.go#L199-L205](file:///e:/demo/party/packet/backend/gateway/server/server.go), [server/server.go#L231-L236](file:///e:/demo/party/packet/backend/gateway/server/server.go)

**问题**：
```go
authReq := fmt.Sprintf(`{"cmd":"auth","request_id":"auth_%s","data":{"token":"%s"},"timestamp":%d}`,
    conn.ConnID, token, time.Now().UnixMilli())
```
- `token` 来自 URL query，若包含 `"`、`\` 等字符会产生非法 JSON，导致后续 `json.Unmarshal` 失败。
- `conn.ConnID` 同理。
- 这是一个安全与稳定性双重隐患。

**修复建议**：改用 `json.Marshal`：
```go
authReq, _ := json.Marshal(message.Request{
    Cmd:       "auth",
    RequestID: "auth_" + conn.ConnID,
    Data:      mustJSONRaw(map[string]string{"token": token}),
    Timestamp: time.Now().UnixMilli(),
})
```

---

### P0-3 `registerInRedis` 类型断言无保护，可导致 panic

**位置**：[connection/manager.go#L130-L134](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**问题**：
```go
if len(result) >= 3 {
    needKick = result[0].(int64) == 1   // 若返回 nil 或其他类型会 panic
    oldConnID = result[1].(string)
    oldNodeID = result[2].(string)
}
```
Redis Lua 返回类型受版本/网络层影响，`go-redis` 在某些场景会把 `int64` 转为 `int`，且 `result[0]` 在异常时可能为 `nil`。生产环境一次返回异常即可使整个 gateway 进程崩溃。

**修复建议**：使用带 `ok` 的安全断言：
```go
if len(result) >= 3 {
    if v, ok := result[0].(int64); ok {
        needKick = v == 1
    }
    if v, ok := result[1].(string); ok {
        oldConnID = v
    }
    if v, ok := result[2].(string); ok {
        oldNodeID = v
    }
}
```
建议同时对 `Eval` 返回用 `assert.GoRedisAssertion` 兼容 `int`/`int64`。

---

### P0-4 测试 Token 接口在生产环境暴露

**位置**：[server/server.go#L106](file:///e:/demo/party/packet/backend/gateway/server/server.go)

**问题**：
```go
s.engine.POST("/test/token", s.handleTestToken)
```
- 该路由无条件注册，任何人均可通过该接口生成有效 JWT 并登录。
- 内部实现 `TestService.saveUserAndGetInternalID` 直接以 `127.0.0.1`、`test-device` 调用 `userSaver.SaveUser`，可创建任意内部用户。

**修复建议**：用环境变量或配置开关限制：
```go
if cfg.Server.Mode != "release" {
    s.engine.POST("/test/token", s.handleTestToken)
}
```
或在 `gateway.yaml` 增加 `enable_test_token: false`，生产强制关闭。

---

### P0-5 默认 Token Secret 兜底导致 fail-open

**位置**：[config/config.go#L251-L253](file:///e:/demo/party/packet/backend/gateway/config/config.go)

**问题**：
```go
if cfg.Token.SecretKey == "" {
    cfg.Token.SecretKey = "default-secret-key-please-change-in-production"
}
```
- 一旦配置缺失或 Nacos 拉取失败，gateway 会静默使用默认密钥启动。
- 攻击者只要知道默认密钥（写在源码里）即可伪造任意用户 JWT。

**修复建议**：改为 fail-fast：
```go
if cfg.Token.SecretKey == "" {
    return nil, fmt.Errorf("token.secret_key must be set")
}
```
并在 `Load` 后增加 `Validate()` 校验所有敏感字段（`Merchant.Secret` 同理）。

---

### P0-6 配置文件中硬编码生产密钥

**位置**：[config/gateway.yaml](file:///e:/demo/party/packet/backend/config/gateway.yaml)

**问题**：仓库内提交了真实密钥：
```yaml
merchant:
  secret: "aca5d11a-e481-4163-9505-194564558ae3"
token:
  secret_key: "your-256-bit-secret-key-here-min-32-characters"
nacos:
  password: "nacos"
```
Git 历史已永久泄露，必须轮换。

**修复建议**：
1. 立即轮换 merchant secret、token secret、nacos 密码。
2. 配置文件改为占位符，真实值从环境变量或 K8s Secret 注入：
   ```yaml
   merchant:
     secret: "${MERCHANT_SECRET}"
   token:
     secret_key: "${TOKEN_SECRET_KEY}"
   ```
3. 在 `.gitignore` 排除真实配置，仅保留 `gateway.yaml.example`。

---

### P0-7 `allowed_origins: ["*"]` 使 WebSocket 可被任意站点连接

**位置**：[config/gateway.yaml#L13-L14](file:///e:/demo/party/packet/backend/config/gateway.yaml), [server/server.go#L78-L89](file:///e:/demo/party/packet/backend/gateway/server/server.go)

**问题**：`CheckOrigin` 命中 `*` 时直接 `return true`，任何网页均可建立 WebSocket 连接，配合 token 泄露可发起 CSRF 式攻击。

**修复建议**：生产环境显式列出允许的域名，删除 `*`；本地开发单独维护 dev 配置。

---

### P0-8 限流器 Redis 异常时 fail-open

**位置**：[middleware/ratelimit.go#L121-L128](file:///e:/demo/party/packet/backend/gateway/middleware/ratelimit.go)

**问题**：
```go
result, err := rl.redis.Eval(ctx, script, ...).Int()
if err != nil {
    logger.Error("rate limiter error", "key", key, "error", err)
    return true, nil  // 直接放行
}
```
Redis 抖动或短时故障期间所有请求绕过限流，给后端 game-service 造成击穿风险。

**修复建议**：
- 增加配置项 `fail_open: bool`，默认 `false`。
- fail-closed 时返回 `(false, err)`，由上层返回 429/503。
- 同时叠加本地 token bucket（如 `golang.org/x/time/rate`）作为兜底，Redis 异常时退化为本地限流。

---

### P0-9 `kickLocalConnection` 与 `cleanupConnection` 之间的连接计数竞争

**位置**：[connection/manager.go](file:///e:/demo/party/packet/backend/gateway/connection/manager.go), [server/server.go#L301-L309](file:///e:/demo/party/packet/backend/gateway/server/server.go)

**问题**：
- `kickLocalConnection`：`Delete` + `atomic.AddInt64(&m.connectionCount, -1)` + `c.Close()`（将状态置为 `StatusClosed`）。
- 被踢连接的 `readPump` 随后退出，`cleanupConnection` 检查状态，`StatusClosed` 不匹配任何分支，不再减计数 → 这一支路 OK。
- 但 `handleReconnect` 路径：`CleanupOldConnection` 会先 `Delete`+`-1`+`Close()` 老连接，紧接着 `Register` 新连接 `+1`。如果老连接的 `readPump` 还没退出，老连接此时状态为 `StatusClosed`，不会重复减计数。
- **但** `MarkDisconnected` 会把状态置为 `StatusDisconnected`，`cleanupConnection` 此后命中 `StatusDisconnected` 分支再次调用 `Unregister` → `atomic.AddInt64(-1)` **再次**减计数，造成计数泄漏为负。

**修复建议**：在 `Manager` 中维护一个状态机，仅允许“已注册→已断开”这一转移触发计数 -1，并使用 CAS 包裹计数变更：
```go
func (m *Manager) unregisterIfRegistered(conn *Connection) bool {
    // 用 conn 自身的状态 CAS 防重复
    // 仅在 localConnections 中存在时才删除并减计数
    _, loaded := m.localConnections.LoadAndDelete(conn.ConnID)
    if loaded {
        atomic.AddInt64(&m.connectionCount, -1)
        m.userConnections.Delete(conn.UserID)
        return true
    }
    return false
}
```
`MarkDisconnected`、`Unregister`、`kickLocalConnection`、`CleanupOldConnection` 全部走该方法，避免重复 -1。

---

## 三、P1 重要问题

### P1-1 `Server.Stop()` 与 `Container.Stop()` 重复停止组件

**位置**：[server/server.go#L132-L168](file:///e:/demo/party/packet/backend/gateway/server/server.go), [bootstrap/container.go#L142-L161](file:///e:/demo/party/packet/backend/gateway/bootstrap/container.go), [bootstrap/app.go#L151-L173](file:///e:/demo/party/packet/backend/gateway/bootstrap/app.go)

**问题**：`app.Stop()` 先调用 `Server.Stop()`（内部已停 `connMgr`/`broadcast`/`auth`），再调用 `Container.Stop()`（再次停 `RateLimiter`/`AuthMiddleware`/`SignatureMiddleware`/`BroadcastSvc`/`ConnMgr`）。`BroadcastService.Close()` 中 `consumer.Close()` 二次调用可能 panic，`AuthMiddleware.Stop()` 的 `cancel()` 二次调用本身安全但仍是冗余。

**修复建议**：明确职责边界——`Server.Stop()` 只负责 HTTP 优雅关闭与连接 drain，组件停止统一由 `Container.Stop()` 完成。从 `Server.Stop()` 中移除 `s.broadcast.Stop()` 与 `s.auth.Stop()`，仅保留 `s.connMgr.Stop()` 由 `Container.Stop()` 统一调度。

---

### P1-2 `AuthMiddleware` 的 IP 锁形同虚设

**位置**：[middleware/auth.go#L79-L83](file:///e:/demo/party/packet/backend/gateway/middleware/auth.go), [middleware/auth.go#L125-L143](file:///e:/demo/party/packet/backend/gateway/middleware/auth.go)

**问题**：
- `recordFailedAttempt` 达到阈值时把 `cashparty:gateway:locked_ip:{ip}` 写入 Redis。
- 但 `isLocked` 只读内存 `failedAttempts`，**从不读 Redis**。
- 进程重启后内存清空，原本应被锁的 IP 立即可继续尝试，安全机制失效。
- 且内存清理逻辑每 1 小时清空全部 `failedAttempts`，等于定时解锁所有 IP。

**修复建议**：
```go
func (m *AuthMiddleware) isLocked(ip string) bool {
    // 1. 先查 Redis（跨节点共享、持久）
    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()
    if n, _ := m.redis.Exists(ctx, gateway.GatewayLockedIPKey(ip)).Result(); n > 0 {
        return true
    }
    // 2. 再查内存（快速路径）
    if attempts, ok := m.failedAttempts.Load(ip); ok && attempts.(int) >= m.maxAttempts {
        return true
    }
    return false
}
```
同时把 `lockDuration`、`maxAttempts` 抽到配置；清理任务改为按 IP 单独过期（用 `sync.Map` + 过期时间字段）。

---

### P1-3 `recordFailedAttempt` 非原子，可漏计数

**位置**：[middleware/auth.go#L133-L144](file:///e:/demo/party/packet/backend/gateway/middleware/auth.go)

**问题**：
```go
attempts, _ := m.failedAttempts.LoadOrStore(ip, 0)
newAttempts := attempts.(int) + 1
m.failedAttempts.Store(ip, newAttempts)
```
`LoadOrStore` + `Store` 之间非原子，并发失败请求会丢失计数，达不到 `maxAttempts`。

**修复建议**：用 `atomic.Int32` 或 `sync.Map` + `atomic`：
```go
type counter struct{ n atomic.Int32 }
v, _ := m.failedAttempts.LoadOrStore(ip, &counter{})
c := v.(*counter)
if c.n.Add(1) == int32(m.maxAttempts) {
    // 写 Redis 锁
}
```

---

### P1-4 `Register` 中 Redis 注册与本地 map 写入之间存在竞态窗口

**位置**：[connection/manager.go#L80-L99](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**问题**：执行顺序为 `tryIncrementCount` → `registerInRedis`（可能触发跨节点 kick）→ `localConnections.Store`。若 kick 通知在本节点，`kickLocalConnection(oldConnID)` 因 `oldConnID != conn.ConnID` 不影响新连接；但若被 kick 的恰好是同一用户的新连接（极端并发），新连接尚未 `Store` 到本地，kick 会丢失，旧连接仍占用 Redis 映射。

**修复建议**：先 `Store` 本地映射，再 `registerInRedis`，并用单一 Lua 脚本完成“检查旧 + 写入新 + 返回旧连接”的原子操作（已有 Lua，但需保证本地 map 先就位）。

---

### P1-5 `RenewConnectionTTL` 从未被调用，长连接 24h 后被 Redis 强制下线

**位置**：[connection/manager.go#L254-L260](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**问题**：连接在 Redis 的 TTL 写死 86400s，方法 `RenewConnectionTTL` 存在但没有任何调用点。用户在线超过 24 小时后，Redis 中映射过期，多节点 kick / 路由失效。

**修复建议**：在 `Manager` 启动一个后台 ticker（如每 1 小时）调用 `RenewConnectionTTL` 对所有本地连接续期，或在每次心跳更新时续期。

---

### P1-6 `ManagerConfig.DisconnectTimeout` 与 `HeartbeatInterval` 死代码

**位置**：[connection/connection.go#L11-L14](file:///e:/demo/party/packet/backend/gateway/connection/connection.go), [connection/manager.go#L17-L22](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**问题**：`HeartbeatInterval`、`HeartbeatTimeout`、`DisconnectTimeout`、`HeartbeatRenewalTick` 等常量/字段散落且未使用——`server.go` 直接硬编码 `30s`/`60s`/`2m`。

**修复建议**：统一抽取到 `ManagerConfig`/`ServerConfig`，删除未使用常量，避免配置与实际行为脱节。

---

### P1-7 `forwardToService` 对响应二次 JSON 解码浪费且可能改变数据

**位置**：[router/router.go#L182-L200](file:///e:/demo/party/packet/backend/gateway/router/router.go)

**问题**：
```go
var data interface{}
if len(resp.Data) > 0 {
    var jsonData interface{}
    if err := json.Unmarshal(resp.Data, &jsonData); err == nil {
        data = jsonData
    } else {
        data = resp.Data
    }
}
```
- 把 `[]byte` 反序列化为 `interface{}`，再在 `resp.ToJSON()` 时重新序列化——双重开销。
- 大整数会被转成 `float64` 精度丢失（JSON 数字默认走 `float64`）。
- 解码失败时直接塞 `[]byte`（会被 base64 编码），数据形态前后不一致。

**修复建议**：直接透传 `json.RawMessage`：
```go
return &message.Response{
    Cmd:       resp.Cmd,
    RequestID: resp.RequestId,
    Code:      int(resp.Code),
    Msg:       resp.Msg,
    Data:      json.RawMessage(resp.Data), // 原样透传
    Timestamp: resp.Timestamp,
}
```

---

### P1-8 路由全量请求/响应数据用 Info 日志输出

**位置**：[router/router.go#L159-L180](file:///e:/demo/party/packet/backend/gateway/router/router.go)

**问题**：每条 WS 消息打印两条 Info 日志，且包含完整 `data` 字段（可能含 token、余额、手牌等敏感信息）。高并发下日志 IO 成为瓶颈，同时违反最小化日志原则。

**修复建议**：
- 改为 `Debug` 级别；`Info` 仅记录 `cmd`、`user_id`、`request_id`、`code`、`latency`。
- 对敏感字段做脱敏（如 token 展示前后 4 位）。
- 增加采样：同 cmd 每 N 条记录 1 条详细日志。

---

### P1-9 `requestLogger` 把 WS URL 中的 token 写入日志

**位置**：[server/server.go#L317-L337](file:///e:/demo/party/packet/backend/gateway/server/server.go)

**问题**：
```go
query := c.Request.URL.RawQuery
logger.Info("http request", ..., "query", query, ...)
```
`/ws?token=xxxx` 的 token 直接进入访问日志，日志聚合后即可横向获取所有有效 token。

**修复建议**：对 `token` query 参数脱敏后再打印：
```go
sanitized := redactToken(c.Request.URL.Query())
logger.Info("http request", "query", sanitized.Encode(), ...)
```

---

### P1-10 `userConnections` 仅存 `connID` 字符串，每次查找需二次查表

**位置**：[connection/manager.go#L284-L291](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**问题**：
```go
func (m *Manager) GetConnectionByUserID(userID string) (*Connection, bool) {
    connIDI, ok := m.userConnections.Load(userID)
    ...
    return m.GetConnection(connID) // 第二次 sync.Map 查找
}
```
`BroadcastToUser` 每条消息都要两次查 `sync.Map`，高频广播下是热点。

**修复建议**：`userConnections` 直接存 `*Connection`，删除 `GetConnectionByUserID` 中的二次查找；维护 user→conn 映射唯一性即可。

---

### P1-11 `roomUsersCache` 无上限、无后台清理

**位置**：[broadcast/broadcast.go#L31-L33](file:///e:/demo/party/packet/backend/gateway/broadcast/broadcast.go), [broadcast/broadcast.go#L105-L141](file:///e:/demo/party/packet/backend/gateway/broadcast/broadcast.go)

**问题**：
- `sync.Map` 无最大容量，房间数多时内存无界增长。
- 过期项只在被访问时 `Delete`，从未访问的房间永驻内存。
- `cacheTTL = 5s` 写死，不可配置。

**修复建议**：
- 改用 LRU（如 `hashicorp/golang-lru/v2`）限定最大房间数。
- 启动后台 goroutine 定期清理过期项。
- `cacheTTL` 抽到配置项。

---

### P1-12 gRPC 客户端无熔断/超时退避，单后端故障会拖垮 gateway

**位置**：[router/router.go#L119-L173](file:///e:/demo/party/packet/backend/gateway/router/router.go), [discovery/discovery.go#L133-L152](file:///e:/demo/party/packet/backend/gateway/discovery/discovery.go)

**问题**：
- `forwardToService` 用 5s 超时直接调用，无熔断；后端 hang 住时每条消息占用一个 goroutine 5s，1 万 QPS 下瞬间堆积 5 万 goroutine。
- `grpc.Dial` 使用 `insecure`，且未配置 `grpc.WithDefaultServiceConfig` 的健康检查 / 重试策略。
- `nacosResolver` 每 10s 拉一次实例，但 gRPC 客户端不会主动剔除不健康实例。

**修复建议**：
- 接入 `grpc/health` 健康检查 + `grpclb` 或 `resolver` 的健康过滤。
- 引入熔断器（如 `sony/gobreaker`）包装 `Forward`，错误率超阈值时 fail fast。
- 给 `grpc.Dial` 加 `grpc.WithConnectTimeout`、`grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(...))`。

---

## 四、P2 一般问题

### P2-1 `cmd_prefix` 命名误导，实际为精确匹配

**位置**：[router/router.go#L18-L22](file:///e:/demo/party/packet/backend/gateway/router/router.go), [router/router.go#L126-L135](file:///e:/demo/party/packet/backend/gateway/router/router.go)

**问题**：配置字段名为 `cmd_prefix` 暗示前缀匹配，但 `getServiceName` 用 `r.routes[cmd]` 精确匹配。后续如增加 `room_state` 与 `room_state_v2` 会困惑。

**修复建议**：改名为 `cmd`；若确需前缀匹配，则按最长前缀排序匹配。

---

### P2-2 限流器 `IPBurstSize` / `UserBurstSize` 配置项从未使用

**位置**：[middleware/ratelimit.go#L97-L128](file:///e:/demo/party/packet/backend/gateway/middleware/ratelimit.go), [config/config.go#L107-L114](file:///e:/demo/party/packet/backend/gateway/config/config.go)

**问题**：`RateLimiterConfig` 定义了 burst 字段，但 `slidingWindowAllow` 只接收 `limit`，burst 永远无效。Nacos 限流配置里也写了 burst 值，运维误以为生效。

**修复建议**：要么实现 token-bucket 支持突发，要么删除字段并在配置加载时告警。

---

### P2-3 限流 Lua 脚本每次都重新传字符串，未用 `EVALSHA`

**位置**：[middleware/ratelimit.go#L101-L121](file:///e:/demo/party/packet/backend/gateway/middleware/ratelimit.go)

**问题**：每条请求都把完整 Lua 脚本字符串传给 Redis，带宽与解析开销高。

**修复建议**：启动时 `SCRIPT LOAD` 拿到 sha，运行时用 `EVALSHA`，遇到 `NOSCRIPT` 再 fallback。

---

### P2-4 `gateway-ratelimiter.yaml` 与默认值严重不一致

**位置**：[config/gateway-ratelimiter.yaml](file:///e:/demo/party/packet/backend/config/gateway-ratelimiter.yaml), [config/config.go#L181-L200](file:///e:/demo/party/packet/backend/gateway/config/config.go)

**问题**：
- `gateway.yaml` 默认 `ip_requests_per_second: 100`，但 `gateway-ratelimiter.yaml` 写 `10000`，相差 100 倍。
- Nacos 动态加载的值会覆盖本地默认，但 `setRateLimiterDefaults` 在 `gateway.yaml` 加载时仍会兜底到 100，导致行为不可预测。
- `global_requests_per_sec` 与 `ip_requests_per_second` 都是 10000，等于单 IP 即可打满全局。

**修复建议**：统一默认值来源；Nacos 配置缺失时显式报错而非静默使用本地兜底。

---

### P2-5 `handleWebSocket` 未解析 `device_id` / `platform`

**位置**：[server/server.go#L188-L194](file:///e:/demo/party/packet/backend/gateway/server/server.go)

**问题**：README 示例 `ws://...?token=...&device_id=...&platform=...`，但代码只取 `token`，`Connection.Platform` / `DeviceID` 永远为空，注册到 Redis 的 `platform`/`device_id` 也是空，影响多端互踢策略。

**修复建议**：
```go
conn.Platform = c.Query("platform")
conn.DeviceID = c.Query("device_id")
```
并在 `Register` 前完成赋值。

---

### P2-6 `WaitForAllConnectionsClose` 用 `time.Sleep` 轮询

**位置**：[connection/manager.go#L364-L372](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**问题**：100ms 轮询 + busy loop，最多等 30s。空载时也要轮询。

**修复建议**：用 `sync.WaitGroup` 跟踪活跃连接，或用条件变量 / channel 通知。

---

### P2-7 `service/game.go` 与 `service/test.go` 中 `saveUserAndGetInternalID` 几乎完全重复

**位置**：[service/game.go#L122-L135](file:///e:/demo/party/packet/backend/gateway/service/game.go), [service/test.go#L67-L80](file:///e:/demo/party/packet/backend/gateway/service/test.go)

**修复建议**：抽到公共方法或嵌入一个 `userSaverMixin`，减少维护成本。

---

### P2-8 `MemoryGameStore.initSampleData` 硬编码游戏数据

**位置**：[store/memory.go#L69-L86](file:///e:/demo/party/packet/backend/gateway/store/memory.go)

**问题**：游戏列表写死在代码里，新增游戏需重新发版。

**修复建议**：改为从 MySQL/Nacos 加载，`MemoryGameStore` 仅作进程内缓存层。

---

### P2-9 `discovery` 中 gRPC 连接使用 insecure，未启用 TLS

**位置**：[discovery/discovery.go#L138-L143](file:///e:/demo/party/packet/backend/gateway/discovery/discovery.go)

**修复建议**：生产环境启用 mTLS，或至少使用 `credentials.NewTLS` 配置 CA 证书。

---

### P2-10 `nacosResolver` 使用 `r.cc.UpdateState` 旧 API

**位置**：[discovery/discovery.go#L89-L92](file:///e:/demo/party/packet/backend/gateway/discovery/discovery.go)

**问题**：`grpc/resolver` 新版本要求实现 `Resolver` 接口的 `Close` 与 `ResolveNow`，且 `UpdateState` 在 `v1.46+` 返回 error。当前代码忽略返回值。

**修复建议**：升级 gRPC 版本并对齐接口签名。

---

### P2-11 `router.Route` 未校验连接是否已认证

**位置**：[router/router.go#L68-L124](file:///e:/demo/party/packet/backend/gateway/router/router.go)

**问题**：`readPump` 对每条消息调用 `Route`，但 `Route` 不检查 `conn.GetStatus() == StatusAuthed`。当前依赖 `handleConnection` 先认证再启 `readPump` 的调用顺序保证安全，一旦重构易引入漏洞。

**修复建议**：在 `Route` 入口增加状态断言，或在 `readPump` 中显式校验。

---

### P2-12 `handleBroadcastMessage` marshal 失败时返回 err 触发重试

**位置**：[broadcast/broadcast.go#L75-L103](file:///e:/demo/party/packet/backend/gateway/broadcast/broadcast.go)

**问题**：`pushMsg.ToJSON()` 失败时 `return err`，消费者可能无限重试，造成消息堆积。

**修复建议**：marshal 失败应记录 metric + 丢弃，返回 `nil` 让消费者 ack。

---

### P2-13 `getSystemResources` 每次 health 请求都调用 `runtime.ReadMemStats`

**位置**：[health/health.go#L81-L93](file:///e:/demo/party/packet/backend/gateway/health/health.go)

**问题**：`ReadMemStats` 会 STW，频繁健康检查（如 k8s liveness 每 1s）影响在线请求延迟。

**修复建议**：后台 goroutine 周期采样缓存，health 接口返回最近一次采样值。

---

### P2-14 `setDefaults` 缺少必填项校验

**位置**：[config/config.go#L202-L260](file:///e:/demo/party/packet/backend/gateway/config/config.go)

**问题**：仅给空字段填默认值，但不校验 `Redis.Addr`、`Nacos.ServerAddr` 等必填项。配置缺失时直到运行期才报错。

**修复建议**：新增 `Validate()` 方法，启动时调用，缺必填项直接 `Fatal`。

---

## 五、P3 建议性问题

### P3-1 测试覆盖严重不足

**现状**：仅 [keys_test.go](file:///e:/demo/party/packet/backend/gateway/keys_test.go) 一个测试文件，覆盖 key 格式。README 声称“测试覆盖率可达 90%+”，与实际不符。

**建议**：补齐 `connection.Manager`、`middleware.AuthMiddleware`、`router.MessageRouter`、`middleware.RateLimiter` 的单测与集成测试。

---

### P3-2 README 与实际目录结构不符

**位置**：[README.md](file:///e:/demo/party/packet/backend/gateway/README.md)

**问题**：README 提到的 `protocol/`、`metrics/` 目录不存在；性能指标章节“吞吐量提升 67%”无基准数据来源。

**建议**：更新 README，补充真实结构图；性能数据附 benchmark 脚本。

---

### P3-3 `GetConnectionCount` 与 `GetConnectionCountInt` 重复

**位置**：[connection/manager.go#L310-L316](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**建议**：保留一个，调用方按需转型。

---

### P3-4 `EventReconnected` / `EventTimeout` 事件定义但未触发

**位置**：[connection/manager.go#L28-L33](file:///e:/demo/party/packet/backend/gateway/connection/manager.go)

**建议**：补全触发点或删除未用常量，避免误导后续维护者。

---

### P3-5 错误响应中的 `msg` 直接拼接内部错误信息

**位置**：[handler/game.go#L76](file:///e:/demo/party/packet/backend/gateway/handler/game.go), [handler/game.go#L122](file:///e:/demo/party/packet/backend/gateway/handler/game.go)

**问题**：`"Invalid request parameters: " + err.Error()`、`"Failed to start game: " + err.Error()` 把内部错误细节暴露给客户端。

**建议**：对外返回统一错误码与通用消息，内部错误写日志，通过 `request_id` 关联。

---

### P3-6 `Application.errChan` 容量为 1，第二个错误被丢弃

**位置**：[bootstrap/app.go#L100](file:///e:/demo/party/packet/backend/gateway/bootstrap/app.go)

**问题**：`a.errChan = make(chan error, 1)`，多 goroutine 同时出错时第二个 `send` 阻塞导致 goroutine 泄漏。

**建议**：改为 `make(chan error, 4)` 或用 `sync.Once` 保证只发第一个错误。

---

## 六、优化优先级与实施路线

### 第一阶段：安全加固（P0）
1. 轮换所有泄露密钥，配置改环境变量注入。
2. 修复签名重放漏洞（P0-1），增加时间戳校验与防重放。
3. 移除/禁用测试 token 接口（P0-4）。
4. 修复 `fmt.Sprintf` 拼 JSON（P0-2）。
5. 修复 `registerInRedis` panic（P0-3）。
6. 限流器 fail-open 行为可配置（P0-8）。
7. 收紧 `allowed_origins`（P0-7）。

### 第二阶段：稳定性（P1）
1. 统一组件停止职责，消除双停（P1-1）。
2. 修复 IP 锁失效（P1-2、P1-3）。
3. 连接计数防重复 -1（P0-9）。
4. 接入熔断 + gRPC 健康检查（P1-12）。
5. 日志脱敏与降级（P1-8、P1-9）。
6. 修复 TTL 续期缺失（P1-5）。

### 第三阶段：性能与可维护性（P2/P3）
1. 路由透传 `json.RawMessage`（P1-7）。
2. 限流器 `EVALSHA` 优化（P2-3）。
3. `userConnections` 直存 `*Connection`（P1-10）。
4. `roomUsersCache` 加 LRU（P1-11）。
5. 补齐单元测试（P3-1）。
6. 配置校验（P2-14）。

---

## 七、关键代码片段参考

### 安全断言修复（P0-3）
```go
func toInt64(v interface{}) (int64, bool) {
    switch n := v.(type) {
    case int64:
        return n, true
    case int:
        return int64(n), true
    }
    return 0, false
}
```

### 安全 JSON 构造（P0-2）
```go
func buildAuthRequest(connID, token string) []byte {
    data, _ := json.Marshal(map[string]string{"token": token})
    req, _ := json.Marshal(message.Request{
        Cmd: "auth", RequestID: "auth_" + connID,
        Data: data, Timestamp: time.Now().UnixMilli(),
    })
    return req
}
```

### 限流 fail-closed（P0-8）
```go
if err != nil {
    logger.Error("rate limiter redis error", "error", err)
    if rl.failOpen {
        return true, nil
    }
    return false, err
}
```

### 路由透传（P1-7）
```go
return &message.Response{
    Cmd: resp.Cmd, RequestID: resp.RequestId,
    Code: int(resp.Code), Msg: resp.Msg,
    Data: json.RawMessage(resp.Data),
    Timestamp: resp.Timestamp,
}
```

---

## 八、附录：审查文件清单

| 文件 | 行数 | 主要问题 |
|------|------|---------|
| [bootstrap/app.go](file:///e:/demo/party/packet/backend/gateway/bootstrap/app.go) | 297 | errChan 容量 |
| [bootstrap/container.go](file:///e:/demo/party/packet/backend/gateway/bootstrap/container.go) | 161 | 双停 |
| [config/config.go](file:///e:/demo/party/packet/backend/gateway/config/config.go) | 260 | 默认 secret 兜底、缺校验 |
| [connection/connection.go](file:///e:/demo/party/packet/backend/gateway/connection/connection.go) | 146 | 死常量 |
| [connection/manager.go](file:///e:/demo/party/packet/backend/gateway/connection/manager.go) | 406 | 类型断言 panic、计数竞争、TTL 未续期 |
| [server/server.go](file:///e:/demo/party/packet/backend/gateway/server/server.go) | 369 | JSON 注入、token 入日志、device_id 未解析 |
| [middleware/auth.go](file:///e:/demo/party/packet/backend/gateway/middleware/auth.go) | 169 | IP 锁失效、非原子计数 |
| [middleware/signature.go](file:///e:/demo/party/packet/backend/gateway/middleware/signature.go) | 137 | 重放漏洞 |
| [middleware/ratelimit.go](file:///e:/demo/party/packet/backend/gateway/middleware/ratelimit.go) | 235 | fail-open、burst 未用、未用 EVALSHA |
| [router/router.go](file:///e:/demo/party/packet/backend/gateway/router/router.go) | 238 | 二次解码、日志泄露 |
| [handler/game.go](file:///e:/demo/party/packet/backend/gateway/handler/game.go) | 133 | 错误信息泄漏 |
| [service/token.go](file:///e:/demo/party/packet/backend/gateway/service/token.go) | 96 | — |
| [service/game.go](file:///e:/demo/party/packet/backend/gateway/service/game.go) | 135 | 代码重复 |
| [service/test.go](file:///e:/demo/party/packet/backend/gateway/service/test.go) | 80 | 代码重复、生产暴露 |
| [broadcast/broadcast.go](file:///e:/demo/party/packet/backend/gateway/broadcast/broadcast.go) | 184 | cache 无上限、marshal 重试 |
| [discovery/discovery.go](file:///e:/demo/party/packet/backend/gateway/discovery/discovery.go) | 206 | insecure、无熔断 |
| [health/health.go](file:///e:/demo/party/packet/backend/gateway/health/health.go) | 117 | ReadMemStats STW |
| [store/memory.go](file:///e:/demo/party/packet/backend/gateway/store/memory.go) | 86 | 硬编码数据 |
| [keys.go](file:///e:/demo/party/packet/backend/gateway/keys.go) | 61 | — |
| [config/gateway.yaml](file:///e:/demo/party/packet/backend/config/gateway.yaml) | 83 | 硬编码密钥、`*` origin |
| [config/gateway-router.yaml](file:///e:/demo/party/packet/backend/config/gateway-router.yaml) | 57 | 命名误导 |
| [config/gateway-ratelimiter.yaml](file:///e:/demo/party/packet/backend/config/gateway-ratelimiter.yaml) | 7 | 值不一致 |
