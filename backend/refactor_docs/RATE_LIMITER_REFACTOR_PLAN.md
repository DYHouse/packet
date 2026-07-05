# 限流（Rate Limiting）完整重构方案

> 文档版本：v2.0
> 创建日期：2026-07-05
> 适用范围：`common/limiter`、`gateway/middleware/auth`、`game/server`、`game/bootstrap`
> 规约依据：`CODING_STANDARD.md` §4.5（错误处理）、§6.2（Redis key）、§6.5（并发安全）、§16（字符串拼接 SC-1~SC-10）

---

## 目录

1. [重构策略：删除 Gin 限流，聚焦业务限流](#1-重构策略删除-gin-限流聚焦业务限流)
2. [现状全景](#2-现状全景)
3. [问题清单](#3-问题清单)
4. [目标架构](#4-目标架构)
5. [详细重构方案](#5-详细重构方案)
6. [配置设计](#6-配置设计)
7. [迁移步骤](#7-迁移步骤)
8. [测试计划](#8-测试计划)
9. [验收清单](#9-验收清单)

---

## 1. 重构策略：删除 Gin 限流，聚焦业务限流

### 1.1 为什么删除 Gin HTTP 限流

本项目的业务流量路径为：

```
客户端 → WebSocket 连接 → Gateway (MessageRouter) → gRPC Forward → Game 服务
         ↓
   Gin 仅处理 /ws 升级握手 + /health 健康检查
```

经核实 [gateway/router/router.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/router/router.go) 和 [gateway/server/server.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go)：

- **业务流量全部走 WebSocket 命令**：`grab_packet`、`send_packet`、`join_room`、`select_seat` 等通过 WebSocket 消息进入，经 `MessageRouter.Route` → gRPC `Forward` 转发到 Game 服务，**完全不经过 Gin 业务中间件**
- **Gin 路由只处理**：
  - `/ws`：WebSocket 升级握手（一次性，握手后走 `readPump`）
  - `/health`、`/ready`、`/live`：K8s 健康探针（**禁止限流**，否则触发实例重启）
  - `/game/list`、`/game/start`：HTTP 游戏入口（非高频）
  - `/test/token`：测试接口（生产环境关闭）
- **Gin 限流中间件实际从未生效**：`UserRateLimitMiddleware`、`GrabRateLimitMiddleware`、`SendPacketRateLimitMiddleware`、`JoinRoomRateLimitMiddleware`、`SelectSeatRateLimitMiddleware` 全部定义但**从未在任何路由注册**（死代码）

因此 Gin HTTP 限流中间件对本项目**毫无价值**，反而带来维护负担和配置噪音。

### 1.2 真正需要限流的位置

| 限流场景 | 位置 | 优先级 | 现状 |
|----------|------|--------|------|
| WebSocket 命令级限流（grab/send_packet/join_room 等） | `game/server/generic_service.go` handler | 高 | 仅 grab 有限流（有 bug），其他无限流 |
| Auth 失败锁定（防暴力破解） | `gateway/middleware/auth.go` | 高 | 有机制但 bug 严重（写入 Redis 锁从不读取） |
| 单用户连接数限制 | `gateway/connection/manager.go` | 高 | 已实现（`MaxConnections` + `GatewayConnKey` 单点登录） |
| 全局 QPS 兜底 | 不需要 | 低 | — |

**结论**：删除 Gateway Gin 限流整套机制，限流统一收敛到 **Game 服务层**（业务限流）+ **Gateway Auth 层**（认证锁定）。

---

## 2. 现状全景

### 2.1 限流代码分布（重构前）

| # | 文件 | 类型 | 作用域 | 处置 |
|---|------|------|--------|------|
| 1 | [common/limiter/limiter.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go) | `RateLimiter` / `UserLimiter` / `IPLimiter` | 通用限流 | 保留并重构 |
| 2 | [common/limiter/scripts/sliding_window.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/scripts/sliding_window.lua.go) | Lua 脚本 | 滑动窗口 | 修复 member 碰撞 |
| 3 | [gateway/middleware/ratelimit.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go) | Gin 限流中间件 | HTTP | **删除整个文件** |
| 4 | [gateway/config/rate_limiter.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/rate_limiter.go) | Gin 限流配置 | 配置 | **删除整个文件** |
| 5 | [config/gateway-ratelimiter.yaml](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/config/gateway-ratelimiter.yaml) | nacos 配置 | 配置 | **删除整个文件** |
| 6 | [gateway/bootstrap/config_listener.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/config_listener.go) | nacos 监听 | 配置热更新 | **删除文件或清理监听** |
| 7 | [game/server/generic_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go) | `UserLimiter` 调用 | 抢红包 | 修复 + 扩展 |
| 8 | [gateway/middleware/auth.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/auth.go) | 失败次数锁定 | 认证 | 修复 Redis 检查 |

### 2.2 调用链路图（重构前）

```
┌─────────────────────────────────────────────────────────────────────┐
│ Gateway (HTTP/Gin)                                                  │
│                                                                     │
│  server.go:setupRoutes()                                           │
│    └── wsGroup.Use(RateLimitMiddleware(s.rateLimiter))  ← 仅 IP 限流 │
│         └── middleware.RateLimiter.AllowIP()                       │
│              └── slidingWindowAllow()                              │
│                   └── SlidingWindowScript.Run()                     │
│                                                                     │
│  ⚠ UserRateLimitMiddleware          ← 定义但未注册                    │
│  ⚠ GrabRateLimitMiddleware          ← 定义但未注册                    │
│  ⚠ SendPacketRateLimitMiddleware    ← 定义但未注册                    │
│  ⚠ JoinRoomRateLimitMiddleware      ← 定义但未注册                    │
│  ⚠ SelectSeatRateLimitMiddleware    ← 定义但未注册                    │
│                                                                     │
│  auth.go (AuthMiddleware)                                          │
│    └── failedAttempts (sync.Map)  ← 内存级，非持久化                  │
│    └── isLocked() ← ⚠ 不检查 Redis 的 GatewayLockedIPKey            │
│    └── recordFailedAttempt() → Redis SET (GatewayLockedIPKey)       │
│                                ↑ 写入但从不读取！                     │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ Game (gRPC)                                                          │
│                                                                     │
│  Forward() → 按 cmd 分发到 handler                                   │
│    └── handleGrabPacket() → userLimiter.AllowGrab()                 │
│         └── RateLimiter.Allow()                                     │
│              └── SlidingWindowScript.Run()                          │
│                                                                     │
│  ⚠ handleJoinRoom    ← 无限流                                        │
│  ⚠ handleSendPacket  ← 无限流                                        │
│  ⚠ handleSelectSeat  ← 无限流                                        │
│  ⚠ handleAutoMatch   ← 无限流                                        │
│  ⚠ handlePlayerReady ← 无限流                                        │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ common/limiter/limiter.go  ← 通用限流包                               │
│                                                                     │
│  RateLimiter ──┬── Allow()          ← 滑动窗口                       │
│                ├── AllowN()         ← Allow 的包装                   │
│                └── FixedWindow()    ← 固定窗口（INCR+EXPIRE 非原子）  │
│                                                                     │
│  UserLimiter ──┬── AllowGrab()   ← ⚠ 数据竞争！修改共享 cfg.Key     │
│                ├── AllowJoin()    ← 死代码                           │
│                └── AllowCreate() ← 死代码                           │
│                                                                     │
│  IPLimiter ────┬── Allow()             ← 死代码                      │
│                ├── AllowConnection()   ← 死代码                      │
│                └── AllowRequest()      ← 死代码                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.3 调用链路图（重构后目标）

```
┌─────────────────────────────────────────────────────────────────────┐
│ Gateway (HTTP/Gin)                                                  │
│                                                                     │
│  server.go:setupRoutes()                                           │
│    └── /ws (WebSocket 升级，无限流)                                  │
│    └── /health, /ready, /live (健康检查，无限流)                      │
│    └── /game/list, /game/start (HTTP 入口，无限流)                   │
│                                                                     │
│  ❌ 删除 gateway/middleware/ratelimit.go                            │
│  ❌ 删除 gateway/config/rate_limiter.go                             │
│  ❌ 删除 config/gateway-ratelimiter.yaml                            │
│  ❌ 删除 registerRateLimiterConfigListener                         │
│  ❌ 删除 loadRateLimiterConfigFromNacos                             │
│  ❌ 删除 GatewayNacosConfig.RateLimiterDataID/RateLimiterGroup     │
│  ❌ 删除 Config.RateLimiter 字段                                    │
│  ❌ 删除 Container.RateLimiter 字段                                 │
│                                                                     │
│  auth.go (AuthMiddleware) ← 保留并修复                              │
│    └── isLocked() 检查 Redis GatewayLockedIPKey                    │
│    └── recordFailedAttempt() 用 Redis INCR 持久化                    │
│    └── clearFailedAttempts() 清理 Redis 计数                         │
│    └── 删除 sync.Map failedAttempts 和 cleanupRoutine              │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ Game (gRPC) — 业务限流核心层                                          │
│                                                                     │
│  Forward() → 按 cmd 分发到 handler                                   │
│    └── handleGrabPacket()    → userLimiter.Allow(ctx, userID, "grab")│
│    └── handleSendPacket()    → userLimiter.Allow(ctx, userID, "send_packet")│
│    └── handleJoinRoom()     → userLimiter.Allow(ctx, userID, "join_room")│
│    └── handleSelectSeat()   → userLimiter.Allow(ctx, userID, "select_seat")│
│    └── handleAutoMatch()    → userLimiter.Allow(ctx, userID, "auto_match")│
│    └── handlePlayerReady()  → userLimiter.Allow(ctx, userID, "player_ready")│
│                                                                     │
│  限流配置从 game-ratelimiter.yaml + nacos 热更新读取                  │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ common/limiter/ (统一限流框架)                                       │
│                                                                     │
│  RateLimiter (统一入口)                                             │
│    └── Allow(ctx, key, limit, window) → (bool, error)              │
│         └── 返回真实 error，支持可配 fail 策略                       │
│         └── Metrics 指标收集                                         │
│                                                                     │
│  UserLimiter (业务限流器，配置驱动)                                  │
│    └── Allow(ctx, userID, cmd) → (bool, error)                     │
│    └── configs 从配置文件读取，支持 nacos 热更新                      │
│    └── 值类型 configs，无共享指针                                    │
│                                                                     │
│  SlidingWindowScript (Lua) ← 修复 member 碰撞                        │
│  ❌ 删除 IPLimiter                                                  │
│  ❌ 删除 AllowJoin, AllowCreate (死代码)                             │
│  ❌ 删除 FixedWindow (非原子，无调用方)                              │
│  ❌ 删除 AllowN (Allow 的包装，无调用方)                             │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 3. 问题清单

### P0 — 严重缺陷（必须修复）

#### P0-1：`UserLimiter` 并发修改共享 `*LimitConfig` 指针（数据竞争）

**文件**：[common/limiter/limiter.go:98-113](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go#L98-L113)

**代码**：
```go
func (ul *UserLimiter) AllowGrab(ctx context.Context, userID string) (bool, error) {
    cfg := ul.configs["grab"]           // 取出共享 *LimitConfig 指针
    cfg.Key = fmt.Sprintf("grab:%s", userID)  // ⚠ 修改共享指针的 Key 字段
    return ul.limiter.Allow(ctx, cfg)
}
```

**问题**：`ul.configs["grab"]` 返回的是 `*LimitConfig` 指针，多个 goroutine 同时调用 `AllowGrab` 时，`cfg.Key` 会被并发覆写。导致：
- 用户 A 的请求可能使用了用户 B 的 key
- 限流完全失效或限流到错误的用户

**复现场景**：2 个 goroutine 同时调用 `AllowGrab("userA")` 和 `AllowGrab("userB")`，最终 `cfg.Key` 可能是 `grab:userA` 或 `grab:userB`，另一个用户使用错误的 key。

**规约违反**：CODING_STANDARD.md §6.5（并发安全）

---

#### P0-2：`RateLimiter.Allow` 吞没错误，返回 `(true, nil)` 导致 fail-open 不可控

**文件**：[common/limiter/limiter.go:33-44](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go#L33-L44)

**代码**：
```go
func (l *RateLimiter) Allow(ctx context.Context, cfg *LimitConfig) (bool, error) {
    ...
    result, err := limiterScripts.SlidingWindowScript.Run(...).Int64()
    if err != nil {
        logger.Error("rate limiter error", "key", key, "error", err)
        return true, nil  // ⚠ 吞没错误，返回 (true, nil)
    }
    return result == 1, nil
}
```

**问题**：
1. Redis 故障时返回 `(true, nil)` 而非 `(true, err)`，调用方无法区分"允许通过"和"Redis 故障放行"
2. [game/server/generic_service.go:349-361](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go#L349-L361) 的 `if err != nil` 分支变成死代码——永远不可能进入

**影响**：涉及资金的操作（grab）在 Redis 故障时静默 fail-open，无告警可观测，调用方的错误处理逻辑形同虚设。

**规约违反**：CODING_STANDARD.md §4.5（错误处理）

---

#### P0-3：滑动窗口 Lua 脚本 member 碰撞（ZADD 覆写）

**文件**：[common/limiter/scripts/sliding_window.lua.go:31](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/scripts/sliding_window.lua.go#L31)

**代码**：
```lua
redis.call('ZADD', key, now, now)  -- score=now, member=now
```

**问题**：`member` 使用 `now`（纳秒时间戳）。ZSET 中 member 必须唯一，若两个请求在同一纳秒到达：
- 第二个 `ZADD` 会更新已有 member 的 score，而非新增
- `ZCARD` 计数少 1，实际放行数超过 limit

**复现**：多核 CPU 上两个 goroutine 几乎同时调用 `time.Now().UnixNano()` 可获得相同值；高并发下必现。

**影响**：限流上限被绕过，高并发下实际放行数 > 配置 limit。

---

#### P0-4：Auth 中间件 `isLocked` 不检查 Redis 锁

**文件**：[gateway/middleware/auth.go:82-86, 128-134](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/auth.go#L82-L134)

**代码**：
```go
// isLocked 只检查本地 sync.Map
func (m *AuthMiddleware) isLocked(ip string) bool {
    attempts, ok := m.failedAttempts.Load(ip)
    if !ok {
        return false
    }
    return attempts.(int) >= m.maxAttempts
}

// recordFailedAttempt 写入 Redis 锁
func (m *AuthMiddleware) recordFailedAttempt(ctx context.Context, ip string) {
    ...
    if newAttempts >= m.maxAttempts {
        key := gateway.GatewayLockedIPKey(ip)
        m.redis.Set(ctx, key, "1", m.lockDuration)  // ← 写入 Redis
        // 但 isLocked 从不读取这个 key！
    }
}
```

**问题**：
1. `isLocked` 只检查内存 `sync.Map`，不检查 Redis `GatewayLockedIPKey`
2. `recordFailedAttempt` 写入 Redis 锁但从不读取 → 死写入
3. 多实例部署时，实例 A 锁定 IP，实例 B 的 `sync.Map` 无记录，攻击者切换实例即可绕过
4. 进程重启后 `sync.Map` 清空，所有锁失效

**影响**：暴力破解防护在多实例部署下完全失效。

---

### P1 — 高优先级问题

#### P1-1：所有限流阈值硬编码，无法通过配置调整

**文件**：
- [common/limiter/limiter.go:79-94](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go#L79-L94)：`grab=10/s`, `join=5/min`, `create=3/min`
- [gateway/middleware/auth.go:43-44](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/auth.go#L43-L44)：`maxAttempts=5`, `lockDuration=15min`

**问题**：无法通过配置文件或 nacos 热更新调整限流阈值，必须改代码重新部署。

**规约违反**：project_memory.md "Scheduler intervals/timeouts/InitialDelay MUST be set via configuration files, 禁止硬编码"

---

#### P1-2：Game 服务 `handleJoinRoom` / `handleSendPacket` / `handleSelectSeat` 等无限流

**文件**：[game/server/generic_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go)

**问题**：仅 `handleGrabPacket` 调用了 `userLimiter.AllowGrab`。以下命令无限流：
- `handleJoinRoom`（入房）
- `handleAutoMatch`（自动匹配）
- `handleSendPacket`（发红包）
- `handleSelectSeat`（选座）
- `handlePlayerReady`（准备）

**影响**：恶意用户可高频调用上述接口，消耗服务器资源或触发竞态。

---

#### P1-3：`IPLimiter`、`AllowJoin`、`AllowCreate`、`FixedWindow`、`AllowN` 全部为死代码

**文件**：[common/limiter/limiter.go:46-68, 104-135](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go#L46-L135)

**问题**：以下代码在项目中无任何调用方：
- `IPLimiter` 整个类型
- `UserLimiter.AllowJoin`、`UserLimiter.AllowCreate`
- `RateLimiter.FixedWindow`（且非原子，见已删除的 P0-4）
- `RateLimiter.AllowN`（Allow 的包装）

**规约违反**：project_memory.md "Dead code (unused topics, event types, and methods) must be removed during refactoring"

---

#### P1-4：Auth 中间件 `cleanupRoutine` 每小时清空所有失败计数

**文件**：[gateway/middleware/auth.go:152-175](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/auth.go#L152-L175)

**问题**：
```go
ticker := time.NewTicker(1 * time.Hour)
case <-ticker.C:
    m.failedAttempts.Range(func(key, value interface{}) bool {
        m.failedAttempts.Delete(key)  // ⚠ 清空所有，包括已锁定的
        return true
    })
```
- 每小时无条件清空所有失败计数，包括已达 `maxAttempts` 的锁定 IP
- `lockDuration` 为 15 分钟，但 cleanup 间隔 1 小时——锁定 IP 在清空后可立即重试
- 应改为只清理过期记录（基于时间戳），或依赖 Redis TTL 自动过期

---

#### P1-5：无任何限流指标收集

**问题**：
- 无 allow / reject / error 计数
- 无 Prometheus metrics 暴露
- 无法观测限流效果和 Redis 故障率

---

### P2 — 中等优先级问题

#### P2-1：`common/limiter/limiter.go` 的 key 构造不使用 `rediskeys` 工厂函数

**文件**：[common/limiter/limiter.go:23, 34, 55](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go#L23)

**问题**：
```go
prefix: rediskeys.KeyPrefix + ":" + prefix,  // 手工拼接
key := fmt.Sprintf("%s:%s", l.prefix, cfg.Key)  // 手工拼接
```

**规约违反**：CODING_STANDARD.md §16 SC-5（Redis key 必须使用 `common/rediskeys` 工厂函数）

---

## 4. 目标架构

### 4.1 设计原则

1. **业务限流为核心**：限流统一收敛到 Game 服务层，按 cmd 维度限流
2. **删除 Gin 限流**：Gateway 不再做 HTTP 限流，仅保留 Auth 失败锁定
3. **单一真相源**：所有限流阈值通过 `game-ratelimiter.yaml` + nacos 配置，禁止硬编码
4. **错误透明**：`Allow` 返回 `(bool, error)`，错误由调用方决定 fail-open/fail-closed
5. **Fail 策略可配**：配置项控制 Redis 故障时 fail-open 或 fail-closed
6. **Key 规范**：全部使用 `common/rediskeys` 工厂函数
7. **指标可观测**：暴露 allow/reject/error 指标
8. **无死代码**：删除未使用的 `IPLimiter`、`AllowJoin`、`AllowCreate`、`FixedWindow`、`AllowN`

### 4.2 重构后的限流层次

```
┌─────────────────────────────────────────────────────────────────────┐
│ 网络入口层（Gateway）                                                 │
│  ├── /ws WebSocket 升级      → 无限流（握手一次性）                    │
│  ├── /health 健康检查         → 无限流（K8s 探针）                      │
│  ├── /game/list HTTP 入口    → 无限流（非高频）                       │
│  └── Auth 失败锁定            → Redis 持久化（防暴力破解）              │
└─────────────────────────────────────────────────────────────────────┘
                                  ↓ WebSocket 消息
┌─────────────────────────────────────────────────────────────────────┐
│ 业务限流层（Game 服务 generic_service.go）                            │
│  ├── handleGrabPacket    → Allow(ctx, userID, "grab")              │
│  ├── handleSendPacket    → Allow(ctx, userID, "send_packet")       │
│  ├── handleJoinRoom      → Allow(ctx, userID, "join_room")         │
│  ├── handleSelectSeat    → Allow(ctx, userID, "select_seat")       │
│  ├── handleAutoMatch     → Allow(ctx, userID, "auto_match")        │
│  └── handlePlayerReady   → Allow(ctx, userID, "player_ready")      │
└─────────────────────────────────────────────────────────────────────┘
                                  ↓
┌─────────────────────────────────────────────────────────────────────┐
│ 限流框架层（common/limiter）                                          │
│  ├── UserLimiter.Allow(ctx, userID, cmd) → (bool, error)           │
│  │    └── 配置驱动：从 game-ratelimiter.yaml 读取各 cmd 的 limit     │
│  │    └── 值类型 configs，无共享指针                                  │
│  │                                                                   │
│  ├── RateLimiter.Allow(ctx, key, limit, window) → (bool, error)   │
│  │    └── 返回真实 error，支持 failOpen 配置                         │
│  │    └── Metrics 指标收集                                          │
│  │                                                                   │
│  └── SlidingWindowScript (Lua)                                      │
│       └── member 唯一性保证                                          │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 5. 详细重构方案

### 5.1 Phase 1：删除 Gin 限流整套机制

#### 5.1.1 删除文件

| 文件 | 操作 |
|------|------|
| [gateway/middleware/ratelimit.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go) | 删除整个文件 |
| [gateway/config/rate_limiter.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/config/gateway-ratelimiter.yaml](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/config/gateway-ratelimiter.yaml) | 删除整个文件 |

#### 5.1.2 修改文件

**[gateway/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/config.go)**：删除 `RateLimiter` 字段
```go
// 删除前
type Config struct {
    // ...
    RateLimiter RateLimiterConfig `mapstructure:"rate_limiter" yaml:"rate_limiter"`
}

// 删除后
type Config struct {
    // ...
    // RateLimiter 字段已删除
}
```

**[gateway/config/defaults.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/defaults.go)**：删除 `setRateLimiterDefaults` 调用
```go
// 删除 setRateLimiterDefaults(&cfg.RateLimiter) 这一行
```

**[gateway/config/nacos.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/nacos.go)**：删除 `RateLimiterDataID`、`RateLimiterGroup`
```go
type GatewayNacosConfig struct {
    commonconfig.NacosConfig `mapstructure:",squash" yaml:",inline"`
    RouterDataID             string `mapstructure:"router_data_id" yaml:"router_data_id"`
    RouterGroup              string `mapstructure:"router_group" yaml:"router_group"`
    // RateLimiterDataID 和 RateLimiterGroup 已删除
}
```

**[gateway/bootstrap/container.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/container.go)**：
- 删除 `RateLimiter *middleware.RateLimiter` 字段
- 删除 `c.RateLimiter = middleware.NewRateLimiter(...)` 初始化
- 删除 `c.RateLimiter` 传入 `server.NewServer` 的参数
- 删除 `Container.Stop()` 中的 `c.RateLimiter.Stop()`

**[gateway/server/server.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go)**：
- 删除 `Server.rateLimiter` 字段
- 删除 `NewServer` 的 `rateLimiter` 参数
- 删除 `setupRoutes` 中的 `wsGroup.Use(middleware.RateLimitMiddleware(s.rateLimiter))`

**[gateway/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go)**：
- 删除 `loadRateLimiterConfigFromNacos` 函数
- 删除 `if nacosClient != nil && cfg.Nacos.RateLimiterDataID != ""` 段
- 删除 `cfg.RateLimiter = *rlCfg` 等

**[gateway/bootstrap/config_listener.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/config_listener.go)**：
- 删除 `registerRateLimiterConfigListener` 函数
- 删除 `registerConfigListeners` 中的调用
- 若 `registerConfigListeners` 变空，整个文件可删除

**[config/gateway.yaml](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/config/gateway.yaml)**：
- 删除 `rate_limiter:` 段
- 删除 `rate_limiter_data_id`、`rate_limiter_group`

---

### 5.2 Phase 2：修复 `common/limiter` 限流框架

#### 5.2.1 修复 `UserLimiter` 数据竞争（P0-1）

**文件**：`common/limiter/limiter.go`

**方案**：`configs` 改为 `map[string]LimitConfig`（值类型），`Allow` 创建新 `LimitConfig`。

```go
type UserLimiter struct {
    limiter *RateLimiter
    configs map[string]LimitConfig  // 值类型，非指针
    mu      sync.RWMutex            // 保护 configs 的热更新
}

func NewUserLimiter(redis *cRedis.Client, configs map[string]LimitConfig) *UserLimiter {
    return &UserLimiter{
        limiter: NewRateLimiter(redis),
        configs: configs,
    }
}

// UpdateConfigs 支持配置热更新
func (ul *UserLimiter) UpdateConfigs(configs map[string]LimitConfig) {
    ul.mu.Lock()
    defer ul.mu.Unlock()
    ul.configs = configs
}

// Allow 统一限流入口，按 cmd 维度限流
func (ul *UserLimiter) Allow(ctx context.Context, userID, cmd string) (bool, error) {
    ul.mu.RLock()
    cfg, ok := ul.configs[cmd]
    ul.mu.RUnlock()
    if !ok {
        // 未配置限流的 cmd，默认放行
        return true, nil
    }

    // 创建新 LimitConfig，不修改共享配置
    return ul.limiter.Allow(ctx, &LimitConfig{
        Key:    fmt.Sprintf("%s:%s", cmd, userID),
        Limit:  cfg.Limit,
        Window: cfg.Window,
    })
}
```

**删除**：`AllowGrab`、`AllowJoin`、`AllowCreate` 三个方法，统一用 `Allow(ctx, userID, cmd)`。

#### 5.2.2 修复 `RateLimiter.Allow` 错误吞没（P0-2）

**文件**：`common/limiter/limiter.go`

```go
type RateLimiter struct {
    redis    *cRedis.Client
    failOpen bool              // Redis 故障时是否放行
    metrics  *Metrics
}

func NewRateLimiter(redis *cRedis.Client, opts ...Option) *RateLimiter {
    rl := &RateLimiter{
        redis:    redis,
        failOpen: true,  // 默认 fail-open
        metrics:  &Metrics{},
    }
    for _, opt := range opts {
        opt(rl)
    }
    return rl
}

type Option func(*RateLimiter)

func WithFailOpen(failOpen bool) Option {
    return func(rl *RateLimiter) {
        rl.failOpen = failOpen
    }
}

func (l *RateLimiter) Allow(ctx context.Context, cfg *LimitConfig) (bool, error) {
    key := rediskeys.RateLimitCmdKey(cfg.Key, "")  // 使用 rediskeys 工厂函数
    // 或新增专用 key 工厂
    now := time.Now().UnixNano()
    member := fmt.Sprintf("%d-%d", now, rand.Int63())  // 唯一 member

    result, err := limiterScripts.SlidingWindowScript.Run(
        ctx, l.redis, []string{key}, cfg.Limit, int64(cfg.Window), now, member,
    ).Int64()
    if err != nil {
        l.metrics.IncError()
        logger.Error("rate limiter redis error",
            "key", key, "error", err, "fail_open", l.failOpen)
        return l.failOpen, err  // 返回真实 error
    }

    if result == 1 {
        l.metrics.IncAllow()
    } else {
        l.metrics.IncReject()
    }
    return result == 1, nil
}
```

**删除**：
- `AllowN`（Allow 的包装，无调用方）
- `FixedWindow`（非原子，无调用方）
- `IPLimiter` 整个类型（死代码）
- `NewIPLimiter`、`IPLimiter.Allow`、`AllowConnection`、`AllowRequest`

#### 5.2.3 修复 Lua 脚本 member 碰撞（P0-3）

**文件**：`common/limiter/scripts/sliding_window.lua.go`

```lua
-- ARGV[4] = unique member（Go 侧生成，保证唯一）
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local member = ARGV[4]

local windowStart = now - window

redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStart)

local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window / 1000000)
    return 1
end

return 0
```

**Go 侧**：见 5.2.2 的 `Allow` 实现。

**更新测试**：`common/limiter/scripts/sliding_window_test.go` 所有调用需传入 `member` 参数。

#### 5.2.4 新增 `common/limiter/metrics.go`

```go
package limiter

import "sync/atomic"

// Metrics 限流指标收集
type Metrics struct {
    AllowCount   int64
    RejectCount  int64
    ErrorCount   int64
}

func (m *Metrics) IncAllow()  { atomic.AddInt64(&m.AllowCount, 1) }
func (m *Metrics) IncReject() { atomic.AddInt64(&m.RejectCount, 1) }
func (m *Metrics) IncError()  { atomic.AddInt64(&m.ErrorCount, 1) }

func (m *Metrics) Snapshot() (allow, reject, errCount int64) {
    return atomic.LoadInt64(&m.AllowCount),
        atomic.LoadInt64(&m.RejectCount),
        atomic.LoadInt64(&m.ErrorCount)
}
```

#### 5.2.5 新增 rediskeys

**文件**：`common/rediskeys/keys.go`

```go
// KeyRateLimitCmd 命令维度限流 key（已有）
KeyRateLimitCmd = KeyPrefix + ":ratelimit:cmd:%s:%s"

// 新增：业务限流专用 key 工厂
// RateLimitCmdKey 生成命令级限流 key
// cmd: 命令名（如 "grab"）
// userID: 用户 ID
func RateLimitCmdKey(cmd, userID string) string {
    return fmt.Sprintf(KeyRateLimitCmd, cmd, userID)
}
```

---

### 5.3 Phase 3：修复 Auth 锁定机制（P0-4、P1-4）

**文件**：`gateway/middleware/auth.go`

**方案**：`isLocked` 检查 Redis；失败计数持久化到 Redis；删除 `sync.Map` 和 `cleanupRoutine`。

```go
type AuthMiddleware struct {
    tokenService  *service.TokenService
    redis         *cRedis.Client
    maxAttempts   int           // 从配置读取
    lockDuration  time.Duration // 从配置读取
    counterWindow time.Duration // 失败计数窗口
    ctx           context.Context
    cancel        context.CancelFunc
}

func NewAuthMiddleware(tokenService *service.TokenService, redis *cRedis.Client, cfg AuthLockConfig) *AuthMiddleware {
    ctx, cancel := context.WithCancel(context.Background())
    return &AuthMiddleware{
        tokenService:  tokenService,
        redis:         redis,
        maxAttempts:   cfg.MaxAttempts,
        lockDuration:  cfg.LockDuration,
        counterWindow: cfg.CounterWindow,
        ctx:           ctx,
        cancel:        cancel,
    }
}

// isLocked 检查 Redis 锁
func (m *AuthMiddleware) isLocked(ctx context.Context, ip string) bool {
    key := gateway.GatewayLockedIPKey(ip)
    exists, err := m.redis.Exists(ctx, key).Result()
    if err != nil {
        logger.Error("failed to check ip lock in redis",
            "ip", ip, "error", err)
        return false  // Redis 故障时 fail-open，避免锁死所有用户
    }
    return exists > 0
}

// recordFailedAttempt 用 Redis INCR 持久化失败计数
func (m *AuthMiddleware) recordFailedAttempt(ctx context.Context, ip string) {
    counterKey := rediskeys.GatewayAuthFailKey(ip)
    count, err := m.redis.Incr(ctx, counterKey).Result()
    if err != nil {
        logger.Error("failed to record failed attempt",
            "ip", ip, "error", err)
        return
    }
    if count == 1 {
        if err := m.redis.Expire(ctx, counterKey, m.counterWindow).Err(); err != nil {
            logger.Warn("failed to set expire on auth fail counter",
                "ip", ip, "error", err)
        }
    }

    if count >= int64(m.maxAttempts) {
        lockKey := gateway.GatewayLockedIPKey(ip)
        if err := m.redis.Set(ctx, lockKey, "1", m.lockDuration).Err(); err != nil {
            logger.Error("failed to set ip lock",
                "ip", ip, "error", err)
        }
        logger.Warn("IP locked due to too many failed attempts",
            "ip", ip, "attempts", count, "lock_duration", m.lockDuration)
    }
}

// clearFailedAttempts 清理 Redis 计数
func (m *AuthMiddleware) clearFailedAttempts(ctx context.Context, ip string) {
    counterKey := rediskeys.GatewayAuthFailKey(ip)
    lockKey := gateway.GatewayLockedIPKey(ip)
    if err := m.redis.Del(ctx, counterKey, lockKey).Err(); err != nil {
        logger.Warn("failed to clear failed attempts",
            "ip", ip, "error", err)
    }
}

func (m *AuthMiddleware) Stop() {
    m.cancel()
}
```

**删除**：
- `failedAttempts sync.Map` 字段
- `cleanupRoutine` 方法（改由 Redis TTL 自动过期）
- `cleanupRoutine` 的 `wg.Add(1)` 和 goroutine 启动

**新增 rediskeys**：
```go
// common/rediskeys/keys.go
const KeyGatewayAuthFail = KeyPrefix + ":gateway:auth_fail:%s"

func GatewayAuthFailKey(ip string) string {
    return fmt.Sprintf(KeyGatewayAuthFail, ip)
}
```

---

### 5.4 Phase 4：Game 服务统一业务限流（P1-2）

**文件**：`game/server/generic_service.go`

#### 5.4.1 `handleGrabPacket` 修复 error 处理（P0-2）

```go
func (s *GenericServiceServer) handleGrabPacket(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    var data struct {
        RoomID   string `json:"room_id"`
        PacketID string `json:"packet_id"`
    }
    if resp, failed := s.parseRequestData(req, &data); failed {
        return resp, nil
    }

    // 业务限流
    allowed, err := s.userLimiter.Allow(ctx, req.UserId, "grab")
    if err != nil {
        // Redis 故障，按配置决定 fail 策略
        if s.grabFailOpen {
            logger.Warn("grab rate limiter error, fail-open",
                "user_id", req.UserId, "request_id", req.RequestId, "error", err)
            // 继续执行 grab 逻辑
        } else {
            logger.Error("grab rate limiter error, fail-closed",
                "user_id", req.UserId, "request_id", req.RequestId, "error", err)
            return s.errorResponse(req, message.CodeSystemError,
                "rate limit service unavailable"), nil
        }
    } else if !allowed {
        return s.errorResponse(req, message.CodeRateLimitExceeded,
            message.GetErrorMsg(message.CodeRateLimitExceeded)), nil
    }

    result, err := s.gameAppSvc.GrabPacket(ctx, &application.GrabPacketRequest{
        RoomID:   data.RoomID,
        UserID:   req.UserId,
        PacketID: data.PacketID,
    })
    if err != nil {
        return s.handleError(req, err), nil
    }

    return s.successResponse(req, map[string]interface{}{
        "packet_id": result.PacketID,
        "amount":    currency.NewMoneyFromFen(result.Amount),
        "position":  result.Position,
        "is_last":   result.IsLast,
    }), nil
}
```

#### 5.4.2 新增其他 handler 的限流

为 `handleJoinRoom`、`handleSendPacket`、`handleSelectSeat`、`handleAutoMatch`、`handlePlayerReady` 增加限流。

**抽取通用限流检查方法**：

```go
// checkRateLimit 统一限流检查
// cmd: 命令名，对应配置中的 key
// failOpen: 该命令在 Redis 故障时是否 fail-open
func (s *GenericServiceServer) checkRateLimit(ctx context.Context, req *commonPb.ForwardRequest, cmd string, failOpen bool) (*commonPb.ForwardResponse, bool) {
    allowed, err := s.userLimiter.Allow(ctx, req.UserId, cmd)
    if err != nil {
        if failOpen {
            logger.Warn("rate limiter error, fail-open",
                "cmd", cmd, "user_id", req.UserId,
                "request_id", req.RequestId, "error", err)
            return nil, true  // 继续执行
        }
        logger.Error("rate limiter error, fail-closed",
            "cmd", cmd, "user_id", req.UserId,
            "request_id", req.RequestId, "error", err)
        return s.errorResponse(req, message.CodeSystemError,
            "rate limit service unavailable"), false
    }
    if !allowed {
        logger.Warn("rate limit exceeded",
            "cmd", cmd, "user_id", req.UserId, "request_id", req.RequestId)
        return s.errorResponse(req, message.CodeRateLimitExceeded,
            message.GetErrorMsg(message.CodeRateLimitExceeded)), false
    }
    return nil, true
}
```

**应用示例**：

```go
func (s *GenericServiceServer) handleSendPacket(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    var data struct {
        RoomID string `json:"room_id"`
    }
    if resp, failed := s.parseRequestData(req, &data); failed {
        return resp, nil
    }

    // 业务限流（资金操作，fail-closed）
    if resp, ok := s.checkRateLimit(ctx, req, "send_packet", s.financialFailOpen); !ok {
        return resp, nil
    }

    result, err := s.gameAppSvc.SendPacket(ctx, &application.SendPacketRequest{
        RoomID: data.RoomID,
        UserID: req.UserId,
    })
    if err != nil {
        return s.handleError(req, err), nil
    }

    return s.successResponse(req, map[string]interface{}{
        "packet_count": result.PacketCount,
    }), nil
}

func (s *GenericServiceServer) handleJoinRoom(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    var data struct {
        RoomID string `json:"room_id"`
    }
    if resp, failed := s.parseRequestData(req, &data); failed {
        return resp, nil
    }

    // 业务限流（非资金操作，fail-open）
    if resp, ok := s.checkRateLimit(ctx, req, "join_room", true); !ok {
        return resp, nil
    }

    result, err := s.roomAppSvc.JoinAndAutoSeat(ctx, &application.JoinRoomRequest{
        UserID: req.UserId,
        RoomID: data.RoomID,
    })
    // ...
}
```

**为以下 handler 增加限流**：
- `handleJoinRoom` → cmd="join_room"，fail-open
- `handleAutoMatch` → cmd="auto_match"，fail-open
- `handleSelectSeat` → cmd="select_seat"，fail-open
- `handlePlayerReady` → cmd="player_ready"，fail-open
- `handleSendPacket` → cmd="send_packet"，fail-closed（资金操作）
- `handleGrabPacket` → cmd="grab"，fail-closed（资金操作）

#### 5.4.3 修改 `GenericServiceServer` 构造与配置

```go
type GenericServiceServer struct {
    // ... 已有字段
    userLimiter        *limiter.UserLimiter
    grabFailOpen        bool  // grab 命令 fail-open 策略
    financialFailOpen   bool  // 资金命令（send_packet 等）fail-open 策略
}
```

**配置注入**：从 `game-ratelimiter.yaml` 读取。

---

### 5.5 Phase 5：业务限流配置与 nacos 热更新

#### 5.5.1 新建配置文件 `config/game-ratelimiter.yaml`

```yaml
# 业务限流配置
# limit: 窗口内允许的最大请求数
# window: 时间窗口（Go duration 格式）
# fail_open: Redis 故障时是否放行（资金相关操作建议 false）

defaults:
  fail_open: true              # 通用命令默认 fail-open
  financial_fail_open: false   # 资金命令（grab, send_packet）默认 fail-closed

commands:
  grab:
    limit: 10
    window: "1s"
    fail_open: false           # 覆盖默认值，资金操作 fail-closed
  send_packet:
    limit: 5
    window: "1s"
    fail_open: false
  join_room:
    limit: 5
    window: "1m"
  auto_match:
    limit: 3
    window: "10s"
  select_seat:
    limit: 10
    window: "1s"
  player_ready:
    limit: 5
    window: "1s"

# Auth 锁定配置
auth_lock:
  max_attempts: 5
  lock_duration: "15m"
  counter_window: "15m"
```

#### 5.5.2 Game 配置结构定义

**新建文件**：`game/config/rate_limiter.go`

```go
package config

import (
    "fmt"
    "time"

    commonconfig "github.com/cashparty/backend/common/config"
)

// RateLimiterConfig 业务限流配置
type RateLimiterConfig struct {
    Defaults     DefaultsConfig            `mapstructure:"defaults" yaml:"defaults"`
    Commands     map[string]CommandLimit  `mapstructure:"commands" yaml:"commands"`
    AuthLock     AuthLockConfig           `mapstructure:"auth_lock" yaml:"auth_lock"`
}

type DefaultsConfig struct {
    FailOpen          bool `mapstructure:"fail_open" yaml:"fail_open"`
    FinancialFailOpen bool `mapstructure:"financial_fail_open" yaml:"financial_fail_open"`
}

type CommandLimit struct {
    Limit    int           `mapstructure:"limit" yaml:"limit"`
    Window   time.Duration `mapstructure:"window" yaml:"window"`
    FailOpen *bool         `mapstructure:"fail_open" yaml:"fail_open"`  // 可选，覆盖默认值
}

type AuthLockConfig struct {
    MaxAttempts   int           `mapstructure:"max_attempts" yaml:"max_attempts"`
    LockDuration  time.Duration `mapstructure:"lock_duration" yaml:"lock_duration"`
    CounterWindow time.Duration `mapstructure:"counter_window" yaml:"counter_window"`
}

// LoadRateLimiterFromContent 从 YAML 内容解析限流配置
func LoadRateLimiterFromContent(content string) (*RateLimiterConfig, error) {
    var raw RateLimiterConfig
    if err := commonconfig.LoadYAMLFromContent(content, &raw); err != nil {
        return nil, fmt.Errorf("failed to parse rate limiter config: %w", err)
    }
    setDefaults(&raw)
    return &raw, nil
}

func setDefaults(cfg *RateLimiterConfig) {
    // 默认值设置
    if cfg.Commands == nil {
        cfg.Commands = make(map[string]CommandLimit)
    }
    // ... 补充各命令的默认值（如未配置）
}
```

#### 5.5.3 nacos 热更新

**新建文件**：`game/bootstrap/rate_limiter_listener.go`

```go
package bootstrap

import (
    "context"
    "runtime/debug"
    "sync"

    "github.com/cashparty/backend/common/logger"
    "github.com/cashparty/backend/common/nacos"
    gameConfig "github.com/cashparty/backend/game/config"
    "github.com/cashparty/backend/common/limiter"
)

// registerRateLimiterListener 注册业务限流配置热更新
func registerRateLimiterListener(
    ctx context.Context,
    wg *sync.WaitGroup,
    nacosClient *nacos.Client,
    dataID, group string,
    userLimiter *limiter.UserLimiter,
) {
    if dataID == "" {
        return
    }
    wg.Add(1)
    go func() {
        defer wg.Done()
        defer func() {
            if r := recover(); r != nil {
                logger.Error("rate limiter config listener panic",
                    "data_id", dataID, "panic", r, "stack", string(debug.Stack()))
            }
        }()
        select {
        case <-ctx.Done():
            return
        default:
        }
        if err := nacosClient.ListenConfig(dataID, group, func(content string) {
            rlCfg, err := gameConfig.LoadRateLimiterFromContent(content)
            if err != nil {
                logger.Warn("parse rate limiter config failed, keep old config",
                    "data_id", dataID, "error", err)
                return
            }
            // 转换为 limiter.LimitConfig map 并热更新
            configs := make(map[string]limiter.LimitConfig)
            for cmd, c := range rlCfg.Commands {
                configs[cmd] = limiter.LimitConfig{
                    Limit:  int64(c.Limit),
                    Window: c.Window,
                }
            }
            userLimiter.UpdateConfigs(configs)
            logger.Info("rate limiter config reloaded", "data_id", dataID)
        }); err != nil {
            logger.Warn("listen rate limiter config failed",
                "data_id", dataID, "error", err)
        }
    }()
}
```

#### 5.5.4 修改 `game/bootstrap/container.go` 与 `app.go`

- `Container.UserLimiter` 初始化时传入配置驱动的 `configs`
- `NewGRPCServer` 传入 `grabFailOpen`、`financialFailOpen` 配置
- `app.go` 中注册 nacos 限流配置监听

---

## 6. 配置设计

### 6.1 配置文件总览

| 文件 | 用途 | nacos DataID |
|------|------|--------------|
| `config/game-ratelimiter.yaml` | 业务限流配置 | `game-ratelimiter.yaml` |

### 6.2 删除的配置

| 删除项 | 原位置 |
|--------|--------|
| `rate_limiter:` 段 | `config/gateway.yaml` |
| `rate_limiter_data_id`、`rate_limiter_group` | `config/gateway.yaml` nacos 段 |
| `config/gateway-ratelimiter.yaml` | 整个文件删除 |
| `RateLimiterConfig` 结构 | `gateway/config/rate_limiter.go`（整个文件删除） |

### 6.3 完整 `config/game-ratelimiter.yaml`

见 5.5.1。

### 6.4 `config/game.yaml` 中新增 nacos 配置

```yaml
nacos:
  enabled: true
  # ... 已有配置
  rate_limiter_data_id: "game-ratelimiter.yaml"
  rate_limiter_group: "DEFAULT_GROUP"
```

---

## 7. 迁移步骤

### Phase 1：删除 Gin 限流（清理死代码）

| 步骤 | 文件 | 动作 | 关联问题 |
|------|------|------|----------|
| 1.1 | 删除 `gateway/middleware/ratelimit.go` | 删除整个文件 | 死代码清理 |
| 1.2 | 删除 `gateway/config/rate_limiter.go` | 删除整个文件 | 死代码清理 |
| 1.3 | 删除 `config/gateway-ratelimiter.yaml` | 删除整个文件 | 死代码清理 |
| 1.4 | `gateway/config/config.go` | 删除 `RateLimiter` 字段 | 死代码清理 |
| 1.5 | `gateway/config/defaults.go` | 删除 `setRateLimiterDefaults` 调用 | 死代码清理 |
| 1.6 | `gateway/config/nacos.go` | 删除 `RateLimiterDataID`、`RateLimiterGroup` | 死代码清理 |
| 1.7 | `gateway/bootstrap/container.go` | 删除 `RateLimiter` 字段和初始化 | 死代码清理 |
| 1.8 | `gateway/bootstrap/app.go` | 删除 `loadRateLimiterConfigFromNacos` 及调用 | 死代码清理 |
| 1.9 | `gateway/bootstrap/config_listener.go` | 删除 `registerRateLimiterConfigListener` | 死代码清理 |
| 1.10 | `gateway/server/server.go` | 删除 `rateLimiter` 字段、参数、`RateLimitMiddleware` 注册 | 死代码清理 |
| 1.11 | `config/gateway.yaml` | 删除 `rate_limiter:` 段和 nacos 中的 `rate_limiter_data_id` | 死代码清理 |
| 1.12 | 验证 | `go build ./...` 通过，无编译错误 | 验证 |

### Phase 2：修复 `common/limiter` 限流框架

| 步骤 | 文件 | 动作 | 关联问题 |
|------|------|------|----------|
| 2.1 | `common/limiter/limiter.go` | `UserLimiter.configs` 改为值类型 map，新增 `Allow(ctx, userID, cmd)` | P0-1 |
| 2.2 | `common/limiter/limiter.go` | 删除 `AllowGrab`、`AllowJoin`、`AllowCreate` | P1-3 |
| 2.3 | `common/limiter/limiter.go` | `RateLimiter.Allow` 返回真实 error，新增 `failOpen` 字段 | P0-2 |
| 2.4 | `common/limiter/limiter.go` | 删除 `AllowN`、`FixedWindow`、`IPLimiter` | P1-3 |
| 2.5 | `common/limiter/limiter.go` | key 构造改用 `rediskeys` 工厂函数 | P2-1 |
| 2.6 | `common/limiter/scripts/sliding_window.lua.go` | 新增 ARGV[4] member 参数 | P0-3 |
| 2.7 | `common/limiter/scripts/sliding_window_test.go` | 更新测试用例传入 member | P0-3 |
| 2.8 | 新建 `common/limiter/metrics.go` | 指标收集 | P1-5 |
| 2.9 | `common/rediskeys/keys.go` | 新增 `KeyGatewayAuthFail` | — |

### Phase 3：修复 Auth 锁定机制

| 步骤 | 文件 | 动作 | 关联问题 |
|------|------|------|----------|
| 3.1 | `gateway/middleware/auth.go` | `isLocked` 检查 Redis `GatewayLockedIPKey` | P0-4 |
| 3.2 | `gateway/middleware/auth.go` | `recordFailedAttempt` 用 Redis INCR 持久化 | P0-4 |
| 3.3 | `gateway/middleware/auth.go` | `clearFailedAttempts` 清理 Redis 计数 | P0-4 |
| 3.4 | `gateway/middleware/auth.go` | 删除 `failedAttempts sync.Map` 字段 | P0-4 |
| 3.5 | `gateway/middleware/auth.go` | 删除 `cleanupRoutine` 方法及 goroutine | P1-4 |
| 3.6 | `gateway/middleware/auth.go` | `NewAuthMiddleware` 接受 `AuthLockConfig` 参数 | P1-1 |
| 3.7 | `gateway/bootstrap/container.go` | `NewAuthMiddleware` 调用传入配置 | P1-1 |

### Phase 4：Game 服务统一业务限流

| 步骤 | 文件 | 动作 | 关联问题 |
|------|------|------|----------|
| 4.1 | `game/server/generic_service.go` | 新增 `checkRateLimit` 通用方法 | P1-2 |
| 4.2 | `game/server/generic_service.go` | `handleGrabPacket` 用 `checkRateLimit` 替换原逻辑 | P0-2 |
| 4.3 | `game/server/generic_service.go` | `handleSendPacket` 增加限流（fail-closed） | P1-2 |
| 4.4 | `game/server/generic_service.go` | `handleJoinRoom` 增加限流 | P1-2 |
| 4.5 | `game/server/generic_service.go` | `handleSelectSeat` 增加限流 | P1-2 |
| 4.6 | `game/server/generic_service.go` | `handleAutoMatch` 增加限流 | P1-2 |
| 4.7 | `game/server/generic_service.go` | `handlePlayerReady` 增加限流 | P1-2 |
| 4.8 | `game/server/generic_service.go` | `NewGRPCServer` / `NewGenericServiceServer` 接受 fail-open 配置 | P1-1 |
| 4.9 | `game/bootstrap/container.go` | `UserLimiter` 初始化改为配置驱动 | P1-1 |

### Phase 5：业务限流配置与 nacos 热更新

| 步骤 | 文件 | 动作 | 关联问题 |
|------|------|------|----------|
| 5.1 | 新建 `game/config/rate_limiter.go` | 定义 `RateLimiterConfig` 结构 | P1-1 |
| 5.2 | 新建 `config/game-ratelimiter.yaml` | 业务限流配置 | P1-1 |
| 5.3 | `config/game.yaml` | nacos 段新增 `rate_limiter_data_id` | P1-1 |
| 5.4 | 新建 `game/bootstrap/rate_limiter_listener.go` | nacos 热更新监听 | P1-1 |
| 5.5 | `game/bootstrap/app.go` | 注册 nacos 限流配置监听 | P1-1 |

### Phase 6：测试

| 步骤 | 文件 | 动作 |
|------|------|------|
| 6.1 | 新建 `common/limiter/limiter_test.go` | 单元测试 |
| 6.2 | `common/limiter/scripts/sliding_window_test.go` | 更新测试 |
| 6.3 | 新建 `gateway/middleware/auth_test.go` | Auth 锁定测试 |
| 6.4 | 新建 `game/server/generic_service_test.go` | 业务限流测试 |

---

## 8. 测试计划

### 8.1 单元测试

#### `common/limiter/limiter_test.go`（新建）

| 测试 | 验证点 |
|------|--------|
| `TestUserLimiter_Allow_Concurrent` | 100 goroutine 并发调用 Allow，验证 key 不串、限流正确 |
| `TestUserLimiter_Allow_UnconfiguredCmd` | 未配置的 cmd 默认放行 |
| `TestUserLimiter_UpdateConfigs` | 配置热更新生效 |
| `TestRateLimiter_Allow_RedisError_ReturnsError` | mock Redis 故障，验证返回 `(failOpen, err)` |
| `TestRateLimiter_Allow_Allowed` | 正常放行 |
| `TestRateLimiter_Allow_Rejected` | 超限拒绝 |
| `TestRateLimiter_FailOpen_True` | failOpen=true 时 Redis 故障放行 |
| `TestRateLimiter_FailOpen_False` | failOpen=false 时 Redis 故障拒绝 |
| `TestRateLimiter_Metrics` | 验证 allow/reject/error 计数 |

#### `common/limiter/scripts/sliding_window_test.go`（修改）

| 测试 | 验证点 |
|------|--------|
| `TestSlidingWindowScript_SameNanosecond` | 两个相同 now + 不同 member，验证都被计数 |
| `TestSlidingWindowScript_OverLimitRejected` | 更新参数签名（新增 member） |
| 已有测试 | 更新参数签名 |

#### `gateway/middleware/auth_test.go`（新建）

| 测试 | 验证点 |
|------|--------|
| `TestAuthMiddleware_IsLocked_ChecksRedis` | 验证 isLocked 读取 Redis |
| `TestAuthMiddleware_RecordFailedAttempt_PersistsToRedis` | 验证失败计数持久化 |
| `TestAuthMiddleware_LockAfterMaxAttempts` | 达到 maxAttempts 后 Redis 锁写入 |
| `TestAuthMiddleware_ClearFailedAttempts` | 成功登录后清理 Redis 计数 |
| `TestAuthMiddleware_RedisError_FailOpen` | Redis 故障时 isLocked 返回 false |

#### `game/server/generic_service_test.go`（新建）

| 测试 | 验证点 |
|------|--------|
| `TestHandleGrabPacket_RateLimitExceeded` | grab 超限返回 CodeRateLimitExceeded |
| `TestHandleGrabPacket_RedisError_FailClosed` | grab Redis 故障 fail-closed |
| `TestHandleSendPacket_RateLimitExceeded` | send_packet 超限返回限流错误 |
| `TestHandleJoinRoom_RateLimitExceeded` | join_room 超限返回限流错误 |
| `TestCheckRateLimit_UnconfiguredCmd` | 未配置 cmd 默认放行 |

### 8.2 集成测试

| 场景 | 验证点 |
|------|--------|
| 多实例 auth 锁定 | 实例 A 锁定 IP，实例 B 拒绝该 IP |
| Nacos 配置热更新 | 更新限流阈值后立即生效 |
| Redis 故障 fail-open | 通用接口放行，资金接口拒绝（financial_fail_open=false） |
| 高并发 grab 限流 | 100 并发 grab，仅 10 个通过 |

---

## 9. 验收清单

### Phase 1：Gin 限流删除验收

- [ ] `gateway/middleware/ratelimit.go` 已删除
- [ ] `gateway/config/rate_limiter.go` 已删除
- [ ] `config/gateway-ratelimiter.yaml` 已删除
- [ ] `gateway/config/config.go` 无 `RateLimiter` 字段
- [ ] `gateway/config/defaults.go` 无 `setRateLimiterDefaults` 调用
- [ ] `gateway/config/nacos.go` 无 `RateLimiterDataID`/`RateLimiterGroup`
- [ ] `gateway/bootstrap/container.go` 无 `RateLimiter` 字段
- [ ] `gateway/bootstrap/app.go` 无 `loadRateLimiterConfigFromNacos`
- [ ] `gateway/bootstrap/config_listener.go` 无 `registerRateLimiterConfigListener`
- [ ] `gateway/server/server.go` 无 `rateLimiter` 字段和 `RateLimitMiddleware` 注册
- [ ] `config/gateway.yaml` 无 `rate_limiter:` 段
- [ ] `go build ./...` 通过
- [ ] grep 确认 `RateLimitMiddleware`、`GrabRateLimitMiddleware` 等已清零

### Phase 2：`common/limiter` 修复验收

- [ ] `UserLimiter.configs` 为 `map[string]LimitConfig`（值类型）
- [ ] `UserLimiter.Allow(ctx, userID, cmd)` 统一入口
- [ ] `AllowGrab`/`AllowJoin`/`AllowCreate` 已删除
- [ ] `RateLimiter.Allow` 返回真实 error
- [ ] `RateLimiter` 支持 `failOpen` 配置
- [ ] `AllowN`/`FixedWindow`/`IPLimiter` 已删除
- [ ] key 构造使用 `rediskeys` 工厂函数
- [ ] Lua 脚本 `ZADD` member 唯一
- [ ] `Metrics` 已实现并埋点
- [ ] `KeyGatewayAuthFail` 已新增到 `common/rediskeys/keys.go`

### Phase 3：Auth 锁定修复验收

- [ ] `isLocked` 检查 Redis `GatewayLockedIPKey`
- [ ] `recordFailedAttempt` 使用 Redis INCR 持久化
- [ ] `clearFailedAttempts` 清理 Redis 计数
- [ ] `failedAttempts sync.Map` 字段已删除
- [ ] `cleanupRoutine` 已删除
- [ ] `NewAuthMiddleware` 接受 `AuthLockConfig` 参数

### Phase 4：Game 业务限流验收

- [ ] `handleGrabPacket` 的 `if err != nil` 分支可达且有正确逻辑
- [ ] `handleSendPacket` 有限流（fail-closed）
- [ ] `handleJoinRoom` 有限流
- [ ] `handleSelectSeat` 有限流
- [ ] `handleAutoMatch` 有限流
- [ ] `handlePlayerReady` 有限流
- [ ] `checkRateLimit` 通用方法已抽取
- [ ] `NewGRPCServer` 接受 fail-open 配置

### Phase 5：配置与热更新验收

- [ ] `game/config/rate_limiter.go` 已新建
- [ ] `config/game-ratelimiter.yaml` 已新建
- [ ] `config/game.yaml` nacos 段含 `rate_limiter_data_id`
- [ ] `game/bootstrap/rate_limiter_listener.go` 已新建
- [ ] nacos 配置变更后 `UserLimiter.UpdateConfigs` 被调用
- [ ] nacos 监听 goroutine 使用 ctx + WaitGroup

### 通用验收

- [ ] 所有新增/修改代码注释为中文（§17.1）
- [ ] 所有 Redis key 通过 `common/rediskeys` 定义（§16 SC-5）
- [ ] 所有 error 返回值被检查或显式忽略（§4.5）
- [ ] 所有并发访问的共享字段有同步机制（§6.5）
- [ ] `go build ./...` 通过
- [ ] `go test ./common/limiter/... ./gateway/middleware/... ./game/server/...` 通过
- [ ] `go vet ./...` 无警告
- [ ] grep 确认硬编码限流值已清零：
  - `grep -rn "limit.*10.*time.Second" common/limiter/ game/server/` 应无结果
  - `grep -rn "maxAttempts.*5" gateway/middleware/auth.go` 应无结果

---

## 附录 A：影响面分析

| 文件 | 变更类型 | 影响 |
|------|----------|------|
| `gateway/middleware/ratelimit.go` | 删除 | Gin HTTP 限流移除 |
| `gateway/config/rate_limiter.go` | 删除 | Gin 限流配置移除 |
| `config/gateway-ratelimiter.yaml` | 删除 | nacos 配置移除 |
| `gateway/config/config.go` | 修改 | 删除 `RateLimiter` 字段 |
| `gateway/config/defaults.go` | 修改 | 删除 `setRateLimiterDefaults` 调用 |
| `gateway/config/nacos.go` | 修改 | 删除 `RateLimiterDataID`/`RateLimiterGroup` |
| `gateway/bootstrap/container.go` | 修改 | 删除 `RateLimiter` 字段，`AuthMiddleware` 改配置注入 |
| `gateway/bootstrap/app.go` | 修改 | 删除 `loadRateLimiterConfigFromNacos` |
| `gateway/bootstrap/config_listener.go` | 修改/删除 | 删除 `registerRateLimiterConfigListener` |
| `gateway/server/server.go` | 修改 | 删除 `rateLimiter` 字段和中间件注册 |
| `gateway/middleware/auth.go` | 重构 | Redis 持久化锁定 |
| `config/gateway.yaml` | 修改 | 删除 `rate_limiter:` 段 |
| `common/limiter/limiter.go` | 重构 | 修复 P0 bug，删除死代码 |
| `common/limiter/scripts/sliding_window.lua.go` | 修改 | member 唯一性 |
| `common/limiter/scripts/sliding_window_test.go` | 修改 | 更新测试签名 |
| `common/limiter/metrics.go` | 新建 | 指标收集 |
| `common/rediskeys/keys.go` | 新增 key | `KeyGatewayAuthFail` |
| `game/config/rate_limiter.go` | 新建 | 业务限流配置结构 |
| `config/game-ratelimiter.yaml` | 新建 | 业务限流配置 |
| `config/game.yaml` | 修改 | nacos 段新增 `rate_limiter_data_id` |
| `game/bootstrap/rate_limiter_listener.go` | 新建 | nacos 热更新 |
| `game/bootstrap/container.go` | 修改 | `UserLimiter` 配置驱动初始化 |
| `game/bootstrap/app.go` | 修改 | 注册 nacos 限流监听 |
| `game/server/generic_service.go` | 修改 | 统一业务限流 |

---

## 附录 B：规约对照

| 问题 | 规约条款 |
|------|----------|
| P0-1 | §6.5 并发安全 |
| P0-2 | §4.5 错误处理 |
| P0-3 | — （逻辑正确性） |
| P0-4 | — （功能正确性） |
| P1-1 | project_memory "禁止硬编码" |
| P1-3 | project_memory "Dead code must be removed" |
| P1-4 | — （功能正确性） |
| P2-1 | §16 SC-5 Redis key 使用 rediskeys 工厂 |

---

## 附录 C：删除 Gin 限流的理论依据

### C.1 项目流量路径分析

通过 [gateway/router/router.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/router/router.go) 的 `Route` 方法确认：

```go
func (r *MessageRouter) Route(ctx context.Context, conn *connection.Connection, rawMessage []byte) {
    var req message.Request
    json.Unmarshal(rawMessage, &req)  // 解析 WebSocket 消息
    // ...
    resp := r.forwardToService(ctx, serviceName, conn, &req)  // gRPC 转发
}
```

WebSocket 消息 → `MessageRouter.Route` → `forwardToService` → gRPC `Forward` → Game 服务 `GenericServiceServer.Forward` → 按 cmd 分发到 handler。

**业务流量完全不经过 Gin 中间件链**。

### C.2 Gin 路由的限流价值评估

| Gin 路由 | 是否需要限流 | 理由 |
|----------|--------------|------|
| `/ws` | 否 | WebSocket 升级一次性握手，连接后走 `readPump`；握手本身不消耗资源 |
| `/health`、`/ready`、`/live` | 否 | K8s 健康探针，限流会触发实例重启 |
| `/game/list`、`/game/start` | 否 | HTTP 游戏入口，非高频，且已有 `SignatureMiddleware` 验签 |
| `/test/token` | 否 | 生产环境关闭 |

### C.3 Gin 限流中间件死代码证据

[gateway/server/server.go:setupRoutes()](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go#L127-L148) 中：

```go
wsGroup := s.engine.Group("")
wsGroup.Use(middleware.RateLimitMiddleware(s.rateLimiter))  // 仅 IP 限流
wsGroup.GET("/ws", s.handleWebSocket)

game := s.engine.Group("/game")
game.Use(s.signatureMiddleware.VerifySignature())  // 仅验签，无限流
```

以下中间件**定义但从未注册**：
- `UserRateLimitMiddleware`
- `GrabRateLimitMiddleware`
- `SendPacketRateLimitMiddleware`
- `JoinRoomRateLimitMiddleware`
- `SelectSeatRateLimitMiddleware`

### C.4 结论

Gin 限流对本项目是**纯负担**：
1. 业务流量不走 Gin 中间件 → 限流不生效
2. 已注册的 `RateLimitMiddleware`（IP 维度）仅作用于 `/ws` 升级 → 无意义
3. 5 个命令级中间件从未注册 → 死代码
4. 配置链路（nacos 监听、config、defaults）维护成本高 → 无收益
5. 与 `common/limiter` 的 `RateLimiter` 类型同名 → 混淆

因此删除 Gin 限流是正确的架构决策，符合 "Avoid over-engineering. Only make changes that are directly requested or clearly necessary" 原则。
