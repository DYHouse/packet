# Lua 原子实现重构方案

> 版本：v1.0
> 范围：game-service / settlement-service / robot-scheduler 全部 Redis Lua 脚本
> 目标：在不改变业务语义的前提下，提升 Lua 层的性能、可维护性、可观测性与可测试性，并为未来 Redis Cluster 迁移铺路。

---

## 一、现状评估

### 1.1 脚本清单

全仓库共 **22 个 Lua 脚本**，分布在 4 个文件、3 个 package 中：

| 文件 | 包 | 脚本数 | 用途 |
|---|---|---|---|
| [lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_scripts.go) | `redis` | 12 | 房间/座位/队列/替补管理 |
| [lua_game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_game.go) | `redis` | 8 | 抢红包/发红包/结算/惩罚 |
| [settlement/service/lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go) | `service` | 1 | 虚拟余额原子扣减 |
| [robot_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/robot_scheduler.go#L71) | `redis` | 1 | 分配锁 token 校验释放 |

### 1.2 调用方式

所有业务脚本统一走 [common/redis/redis.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/redis/redis.go#L180) 的 `Client.Eval`：

```go
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
    return c.rdb.Eval(ctx, script, keys, args...)
}
```

- **底层走 `EVAL` 命令**：每次请求都把整段脚本字符串发送到 Redis，Redis 端每次都重新 `lua_load`。
- 唯一例外：[robot_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/robot_scheduler.go#L71) 用 `goredis.NewScript(...)`，go-redis 内部会先 `SCRIPT LOAD` 拿 sha，后续用 `EVALSHA`，遇到 `NOSCRIPT` 自动 fallback。

### 1.3 现状的优点（应保留）

| 优点 | 证据 |
|---|---|
| 错误码集中定义 | [domain/lua_errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/lua_errors.go) 40+ 常量，[errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/errors.go) 统一 `MapLuaError` |
| 关键路径有幂等检查 | `LuaSettleRound` 检查 phase∈{SETTLED,WAIT_SEND,GAME_END}；`LuaSendPacket` 检查 `current_round_id` 与 `LLEN availablePacketsKey` |
| 避免主从复制随机问题 | `LuaRobotGrabPacket` 从 Go 侧传入 `randOffset`，不使用 `math.random` |
| 锁释放有持有者校验 | `releaseAssignLockScript` 用 `GET == token` + `DEL` 的原子 Lua |
| 余额扣减有回滚 | `luaDeductBalance` 用 `INCRBY` → 检查 `<0` → 回滚 |
| TTL 统一 24h | 几乎所有 SET/EXPIRE 用 `86400` |

### 1.4 关键问题清单

| # | 问题 | 严重度 | 类型 |
|---|---|---|---|
| P1 | 全部脚本用 `EVAL`，无 `EVALSHA` 缓存 | High | 性能 |
| P2 | Lua 脚本无单测、无集成测 | High | 质量 |
| P3 | 脚本散落在 service / infrastructure 多层 | Medium | 架构 |
| P4 | 业务错误码在 Lua 中是魔法数字 | Medium | 可维护性 |
| P5 | Lua 内拼接 key（`keyPrefix .. ':packet:info:' .. id`） | Medium | Cluster 兼容性 |
| P6 | 长脚本（`LuaSettleRound` 190 行）混合多种关注点 | Medium | 可维护性 |
| P7 | TTL `86400` 硬编码 15+ 处 | Low | 可配置性 |
| P8 | 返回值结构不统一，Go 侧用 `result[0..N]` 位置索引 | Low | 可维护性 |
| P9 | 无 Lua 执行指标/日志 | Medium | 可观测性 |
| P10 | `LuaHandlePenalty` 存在死代码 | Low | 代码质量 |
| P11 | 无脚本版本管理，热更新困难 | Low | 运维 |
| P12 | 未做 Redis Cluster hash tag 规划 | Medium | 演进 |

---

## 二、重构目标

1. **性能**：所有脚本走 `EVALSHA`，单次调用网络流量下降 60%+（`LuaSettleRound` 6KB → ~40 字节 sha）。
2. **可维护**：脚本集中管理，错误码与 TTL 单一来源，长脚本拆分。
3. **可观测**：每次 Lua 执行有耗时、错误码、重试指标。
4. **可测试**：每个脚本有 `miniredis` 单测覆盖正常/边界/并发场景。
5. **可演进**：key 设计预留 hash tag，为 Redis Cluster 留出迁移路径。
6. **零业务回归**：分阶段灰度，保留旧路径回退能力。

---

## 三、详细重构方案

### 3.1 P1 — 引入 EVALSHA 调用层（High）

#### 现状

[common/redis/redis.go#L180](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/redis/redis.go#L180) 直接调用 `rdb.Eval`，每次发送整段脚本。

#### 方案

在 `common/redis` 新增 `ScriptRegistry`，复用 go-redis 内置的 `goredis.NewScript`：

```go
// common/redis/script.go (新增)
package redis

import (
    "context"
    "github.com/redis/go-redis/v9"
)

// Script 包装 go-redis 的 NewScript，自动处理 SCRIPT LOAD + EVALSHA + NOSCRIPT fallback
type Script struct {
    script *redis.Script
    name   string
}

func NewScript(name, src string) *Script {
    return &Script{script: redis.NewScript(src), name: name}
}

// Run 执行脚本，keys 与 args 透传
func (s *Script) Run(ctx context.Context, c *Client, keys []string, args ...interface{}) *redis.Cmd {
    return s.script.Run(ctx, c.Raw(), keys, args...)
}

// Name 返回脚本名（用于指标/日志）
func (s *Script) Name() string { return s.name }
```

调用方改造（以 [repository.go#L186](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go#L186) 为例）：

```go
// 改造前
result, err := r.client.Eval(ctx, LuaSelectSeat, keys, args...).Slice()

// 改造后
result, err := scripts.SelectSeat.Run(ctx, r.client, keys, args...).Slice()
```

`scripts` 包集中注册所有脚本：

```go
// game/infrastructure/persistence/redis/scripts/registry.go (新增)
package scripts

import cRedis "github.com/cashparty/backend/common/redis"

var (
    SelectSeat       = cRedis.NewScript("select_seat", LuaSelectSeat)
    CancelSeat       = cRedis.NewScript("cancel_seat", LuaCancelSeat)
    JoinAsSpectator  = cRedis.NewScript("join_spectator", LuaJoinAsSpectator)
    LeaveRoom        = cRedis.NewScript("leave_room", LuaLeaveRoom)
    KickPlayer       = cRedis.NewScript("kick_player", LuaKickPlayerAndInterrupt)
    AutoSeatAndReady = cRedis.NewScript("auto_seat_ready", LuaAutoSeatAndReady)
    Enqueue          = cRedis.NewScript("enqueue", LuaEnqueue)
    Dequeue          = cRedis.NewScript("dequeue", LuaDequeue)
    AutoSubstitute   = cRedis.NewScript("auto_substitute", LuaAutoSubstitute)
    PlayerReady      = cRedis.NewScript("player_ready", LuaPlayerReady)
    HandleSeatTimeout = cRedis.NewScript("handle_seat_timeout", LuaHandleSeatTimeout)
    TryStartGame     = cRedis.NewScript("try_start_game", LuaTryStartGame)

    GrabPacket           = cRedis.NewScript("grab_packet", LuaGrabPacket)
    RobotGrabPacket      = cRedis.NewScript("robot_grab_packet", LuaRobotGrabPacket)
    AutoDistributePackets = cRedis.NewScript("auto_distribute", LuaAutoDistributePackets)
    SendPacket           = cRedis.NewScript("send_packet", LuaSendPacket)
    EndGame              = cRedis.NewScript("end_game", LuaEndGame)
    SettleRound          = cRedis.NewScript("settle_round", LuaSettleRound)
    HandlePenalty        = cRedis.NewScript("handle_penalty", LuaHandlePenalty)
    DistributePenalty    = cRedis.NewScript("distribute_penalty", LuaDistributePenalty)
)
```

#### 收益

- 每次 Lua 调用网络流量从 ~1-6KB 降到 ~64 字节（sha1 + 参数）
- Redis 端省去 `lua_load` 开销（高频脚本如 `LuaGrabPacket` 在抢红包峰值 QPS 千级时收益显著）
- `goredis.NewScript` 内部已处理 `NOSCRIPT` fallback，无需手写

#### 风险与回退

- `SCRIPT LOAD` 在 Redis 重启/`FLUSH` 后 sha 失效 → go-redis 自动 fallback 到 `EVAL`，无业务影响
- 集群/Sentinel failover 后新 master 没有 sha → 同上自动 fallback

---

### 3.2 P3 — 脚本目录结构重组（Medium）

#### 现状

| 文件 | 行数 | 脚本数 | 问题 |
|---|---|---|---|
| [lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_scripts.go) | 781 | 12 | 房间/座位/队列/替补四类不相关逻辑混在一起 |
| [lua_game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_game.go) | 739 | 8 | 抢红包/发红包/结算/惩罚混在一起 |
| [settlement/service/lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go) | 24 | 1 | DDD 违规：脚本源码放在 service 层 |
| [robot_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/robot_scheduler.go#L71) | - | 1 | 内联在业务文件，未与其它脚本统一管理 |

整体目录：

```
game/infrastructure/persistence/redis/
├── lua_scripts.go        # 房间/座位脚本（781 行）
├── lua_game.go           # 游戏流程脚本（739 行）
├── repository.go         # 调用方
└── robot_scheduler.go    # 含内联 releaseAssignLockScript
settlement/service/
└── lua_scripts.go        # 余额扣减脚本（在 service 层，DDD 违规）
```

#### 拆分原则

- **按业务域聚合**，不按脚本数机械拆分：一个业务域内的脚本放同一文件，跨域的才分开。
- **避免一个脚本一个文件**：22 个文件反而增加导航成本，5-7 个文件最合适。
- **内联脚本必须抽离**：业务文件不应持有 Lua 源码。
- **service 层不持有脚本**：脚本属基础设施，应下沉到 infrastructure。

#### 方案

按业务域拆分为 7 个文件，集中到 `scripts/` 子包：

```
game/infrastructure/persistence/redis/
├── scripts/
│   ├── registry.go              # 集中注册（§3.1 的 Script 实例）
│   ├── room_seat.lua.go         # 房间/座位管理（9 个脚本）
│   │                            #   JoinAsSpectator / SelectSeat / CancelSeat
│   │                            #   LeaveRoom / KickPlayerAndInterrupt
│   │                            #   PlayerReady / HandleSeatTimeout
│   │                            #   AutoSeatAndReady / TryStartGame
│   ├── queue.lua.go             # 队列与替补（3 个脚本）
│   │                            #   Enqueue / Dequeue / AutoSubstitute
│   ├── packet.lua.go            # 红包流程（4 个脚本）
│   │                            #   GrabPacket / RobotGrabPacket
│   │                            #   AutoDistributePackets / SendPacket
│   ├── round.lua.go             # 回合结算（2 个脚本）
│   │                            #   EndGame / SettleRound
│   ├── penalty.lua.go           # 惩罚（2 个脚本）
│   │                            #   HandlePenalty / DistributePenalty
│   └── robot_lock.lua.go        # 机器人锁（1 个脚本）
│                                #   releaseAssignLockScript（从 robot_scheduler.go 抽出）
├── repository.go                # 调用方（不变，改用 scripts.XXX）
└── ...
settlement/infrastructure/persistence/redis/
└── virtual_balance.lua.go       # luaDeductBalance（从 service 层迁来，DDD 修正）
```

#### 关键迁移

1. **`settlement/service/lua_scripts.go` → `settlement/infrastructure/persistence/redis/virtual_balance.lua.go`**（**必做**，DDD 违规修正）
   - `luaDeductBalance` 脚本源码随之迁移
   - `VirtualBalanceService`（service 层）改为依赖 `VirtualBalanceRedis`（infrastructure 层）接口
   - service 层不再直接持有 Redis 脚本字符串

2. **`robot_scheduler.go` 中的 `releaseAssignLockScript` 抽离到 `scripts/robot_lock.lua.go`**（**必做**，内联清理）
   - 与其它脚本统一注册到 `registry.go`
   - `robot_scheduler.go` 只保留业务调用逻辑

3. **`lua_scripts.go`（781 行）按业务域拆为 2 个文件**（建议）
   - `room_seat.lua.go`：9 个房间/座位相关脚本
   - `queue.lua.go`：3 个队列/替补脚本
   - 拆分后单文件 300-500 行，可读性显著提升

4. **`lua_game.go`（739 行）按业务域拆为 3 个文件**（建议）
   - `packet.lua.go`：4 个红包流程脚本
   - `round.lua.go`：2 个回合结算脚本（含 `LuaSettleRound` 190 行大脚本）
   - `penalty.lua.go`：2 个惩罚脚本
   - 变更隔离：红包算法调整不会触发结算脚本的 review，git blame 更清晰

#### 拆分收益

| 维度 | 改造前 | 改造后 |
|---|---|---|
| 单文件最大行数 | 781 行 | ~400 行（room_seat） |
| 业务域隔离 | 无（房间/队列混在一个文件） | 有（一文件一业务域） |
| DDD 合规 | service 层持有脚本 | 脚本全在 infrastructure |
| 内联脚本 | releaseAssignLock 散在业务文件 | 统一到 scripts/ |
| 变更影响范围 | 改一个脚本可能 diff 大文件 | 改动局限在所属业务域文件 |

#### 优先级

| 动作 | 必要性 | 备注 |
|---|---|---|
| settlement 脚本迁到 infrastructure | **必做** | DDD 违规，硬需求 |
| releaseAssignLockScript 抽离 | **必做** | 内联在业务文件，硬需求 |
| 按业务域拆分 game 的两个大文件 | 建议 | 可读性提升，配合 §3.1 EVALSHA 改造一起做 |
| 集中 registry.go | 建议 | 配合 §3.1 EVALSHA 改造一起做 |

---

### 3.3 P4 — 错误码单一来源（Medium）

#### 现状

Go 侧已集中定义（[domain/lua_errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/lua_errors.go)），但 Lua 脚本里仍是魔法数字：

```lua
return {60, 0, 0, 0, 'only player can grab packet', 0}   -- 60 是什么？
return {40, 0, 0, 0, 'not in grabbing phase', 0}         -- 40 是什么？
```

#### 方案

由于 Lua 不支持 `import` Go 常量，采用**双轨制**：

1. **生成式**：写一个 `go generate` 脚本，从 `domain/lua_errors.go` 生成 `lua_constants.lua`：
   ```lua
   -- AUTO-GENERATED. DO NOT EDIT.
   local ERR_SUCCESS = 0
   local ERR_ROOM_NOT_FOUND = 1
   local ERR_NOT_PLAYER = 60
   ...
   ```
   每个脚本顶部 `local ERR = require('constants')`（Redis Lua 不支持 require，需用字符串拼接注入）。

2. **注入式（推荐，无需 codegen）**：在 `NewScript` 时自动把常量表拼接到脚本头部：
   ```go
   func NewScript(name, src string) *Script {
       fullSrc := luaConstantsPreamble + src
       return &Script{script: redis.NewScript(fullSrc), name: name}
   }
   ```
   `luaConstantsPreamble` 由 `domain.LuaErrorCodes()` 反射生成。

3. **文档化（短期，最小改动）**：在每个脚本头部注释里补全错误码映射表（部分脚本已有，需补齐）。

推荐先做方案 3（文档），下个迭代做方案 2（自动注入）。

---

### 3.4 P5 — Lua 内 key 拼接清理（Medium）

#### 现状

```lua
-- LuaGrabPacket
local packetKey = keyPrefix .. ':packet:info:' .. packetID
local availableKey = keyPrefix .. ':packet:available:' .. packetID

-- LuaSettleRound
local packetKey = keyPrefix .. ':packet:info:' .. packetIDStr
```

`keyPrefix` 作为 ARGV 传入，Lua 内动态拼 key。

#### 问题

1. **Redis Cluster 不兼容**：`cashparty:packet:info:{packetID}` 与 `cashparty:room:hash:{roomID}` 不在同一 slot，跨 slot 操作报 `CROSSSLOT` 错误。
2. **重复拼接**：同一个 packetKey 在 grab / robot_grab / auto_distribute / settle 里都拼一遍，命名漂移风险。

#### 方案

**理想**：所有 key 在 Go 侧构造好通过 `KEYS[]` 传入。但 `LuaGrabPacket` 在调用时不知道要抢哪个 packet（packetID 是参数），`LuaRobotGrabPacket` 更是循环查找——这种情况无法预构造。

**务实方案**：

1. **能用 KEYS 的全用 KEYS**：`LuaSettleRound` 里 `packetIDs` 来自 `LRANGE availablePacketsKey`，可以在 Go 侧先 `LRANGE` 拿到 ID 列表，把所有 `packet:info:{id}` key 预构造后传入。但这破坏原子性（LRANGE 和 settle 之间有间隙）——**不可行**。

2. **引入 hash tag**：把所有可能被同一脚本访问的 key 用 `{roomID}` 作为 hash tag：
   ```
   cashparty:{roomID}:packet:info:{packetID}
   cashparty:{roomID}:round:state:{roundID}
   ```
   这样 Cluster 模式下它们落同一 slot。**这是 Cluster 迁移的必要前置工作**，建议本次重构先做 key 命名规范文档，下个迭代落地。

3. **抽取 key builder 到共享 Lua 片段**：通过 §3.3 的 preamble 机制注入：
   ```lua
   local function packetInfoKey(prefix, id) return prefix .. ':packet:info:' .. id end
   ```
   消除拼写漂移，但不解决 Cluster 问题。

---

### 3.5 P6 — 长脚本拆分（Medium）

#### 现状

[LuaSettleRound](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_game.go#L465) ~190 行，承担 6 项职责：
1. 幂等性检查
2. 构建用户红包映射
3. 构建结果列表 + 计算 minAmount
4. 更新 session 累计金额
5. 更新回合状态 + 设置 next_sender
6. 游戏结束时构建 finalResults（含排序）

#### 方案

**保留单脚本**（原子性优先），但内部分块并加注释：

```lua
-- ========== 1. 幂等性检查 ==========
local phase = redis.call('HGET', roundStateKey, 'phase')
if phase == 'SETTLED' or phase == 'WAIT_SEND' or phase == 'GAME_END' then
    return {2, ...}
end
...

-- ========== 2. 构建用户红包映射 ==========
...

-- ========== 3. 构建结果 + 计算 minAmount ==========
...

-- ========== 4. 更新 session 累计 ==========
...

-- ========== 5. 更新回合状态 ==========
...

-- ========== 6. 游戏结束构建 finalResults ==========
if isGameEnd == 1 then
    -- 排序逻辑可考虑移到 Go 侧（减少 Lua 复杂度）
end
```

**可选优化**：把 `finalResults` 的排序移到 Go 侧。Lua 已返回 `totalsList` 原始数据，Go 用 `sort.Slice` 排序——降低 Lua 复杂度，且 Go 排序性能更好（Lua 的 `table.sort` 在数据量大时较慢）。需评估是否破坏原子性：排序只是返回值加工，不涉及 Redis 写，**安全**。

---

### 3.6 P7 — TTL 参数化（Low）

#### 现状

`86400` 在 15+ 处硬编码。

#### 方案

把 TTL 作为 ARGV 传入，Go 侧统一定义：

```go
// common/redis/ttl.go
const DefaultRedisTTL = 86400 // 24h，与历史行为一致
```

脚本改造（示例）：
```lua
-- 改造前
redis.call('SET', userGrabKey, '1', 'EX', 86400)

-- 改造后
redis.call('SET', userGrabKey, '1', 'EX', defaultTTL)
```

`defaultTTL` 通过 §3.3 的 preamble 注入。

**优先级低**：当前 TTL 一致，无业务诉求。仅在需要差异化 TTL（如临时延长 grab key TTL）时再做。

---

### 3.7 P8 — 返回值结构统一（Low）

#### 现状

返回值 arity 从 3 到 10 不等，Go 侧位置索引：

```go
code := parseLuaCode(result[0])
playerCount := parseInt(result[2])
maxPlayers := parseInt(result[3])
```

#### 方案

**结构化返回**：所有脚本统一返回 `{code, data, msg}` 三元组，`data` 用 cjson 编码：

```lua
-- 改造前
return {0, tonumber(packetID), amount, position, '', isLast}

-- 改造后
return cjson.encode({
    code = 0,
    data = {
        packet_id = tonumber(packetID),
        amount = amount,
        position = position,
        is_last = isLast
    },
    msg = ''
})
```

Go 侧：

```go
type LuaResult[T any] struct {
    Code int    `json:"code"`
    Data T      `json:"data"`
    Msg  string `json:"msg"`
}

func RunScript[T any](ctx context.Context, s *redis.Script, c *redis.Client, keys []string, args ...any) (*LuaResult[T], error) {
    raw, err := s.Run(ctx, c.Raw(), keys, args...).Text()
    if err != nil { return nil, err }
    var r LuaResult[T]
    if err := json.Unmarshal([]byte(raw), &r); err != nil { return nil, err }
    return &r, nil
}
```

**优先级低**：改造面大，收益主要是可读性。建议新脚本采用，旧脚本仅在改动时迁移。

---

### 3.8 P9 — 可观测性（Medium）

#### 方案

在 `Script.Run` 外层包装 metrics + log：

```go
func (s *Script) Run(ctx context.Context, c *Client, keys []string, args ...interface{}) *redis.Cmd {
    start := time.Now()
    cmd := s.script.Run(ctx, c.Raw(), keys, args...)
    
    dur := time.Since(start)
    status := "ok"
    if cmd.Err() != nil {
        status = "error"
    }
    
    metrics.LuaExecutionDuration.WithLabelValues(s.name, status).Observe(dur.Seconds())
    if dur > 100*time.Millisecond {
        logger.Warn("lua script slow",
            "script", s.name,
            "duration", dur.String(),
            "key_count", len(keys),
        )
    }
    return cmd
}
```

指标维度：`script_name`, `status`（ok/business_error/redis_error）
告警阈值：p99 > 50ms 或 5min 内 business_error 率 > 1%

---

### 3.9 P10 — 修复死代码（Low）

#### 现状

[LuaHandlePenalty](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_game.go#L658)：

```lua
local penaltyCount = tonumber(redis.call('GET', penaltyCountKey) or 0)  -- 计算后从未使用
local newCount = redis.call('INCR', penaltyCountKey)
```

`penaltyCount` 变量未被引用，属死代码。

#### 方案

直接删除该行：

```lua
local newCount = redis.call('INCR', penaltyCountKey)
```

---

### 3.10 P2 — 测试体系（High）

#### 方案

引入 [`miniredis`](https://github.com/alicebob/miniredis)（纯 Go 实现的 Redis 模拟器，支持 Lua）：

```go
// game/infrastructure/persistence/redis/scripts/grab_packet_test.go
package scripts_test

import (
    "context"
    "testing"
    "github.com/alicebob/miniredis/v2"
    cRedis "github.com/cashparty/backend/common/redis"
    "github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
    "github.com/stretchr/testify/require"
)

func setupMiniRedis(t *testing.T) (*cRedis.Client, *miniredis.Miniredis) {
    mr := miniredis.RunT(t)
    rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
    return &cRedis.Client{ /* wrap */ }, mr
}

func TestLuaGrabPacket_Success(t *testing.T) {
    c, mr := setupMiniRedis(t)
    // seed: playersKey, roundStateKey, availablePacketsKey, packet:info, packet:available
    mr.HSet("cashparty:room:players:R1", "U1", `{"user_id":"U1",...}`)
    mr.HSet("cashparty:round:state:RD1", "phase", "GRABBING", "grab_end_time", "9999999999")
    mr.LPush("cashparty:round:available_packets:RD1", "P1")
    mr.Set("cashparty:packet:available:P1", "1")
    mr.Set("cashparty:packet:info:P1", `{"packet_id":"P1","amount":100,"position":1}`)

    keys := []string{...}
    args := []interface{}{"U1", "1000", "30", "R1", "cashparty", "P1"}
    res, err := scripts.GrabPacket.Run(context.Background(), c, keys, args...).Slice()
    require.NoError(t, err)
    require.Equal(t, int64(0), res[0].(int64)) // code=0
    require.Equal(t, int64(100), res[2].(int64)) // amount
}
```

**覆盖矩阵**（每个脚本至少）：

| 场景 | 验证点 |
|---|---|
| 正常路径 | code=0，数据正确 |
| 幂等性 | 重复调用返回相同结果，不重复写 |
| 边界（空集合/超时/已抢） | 返回正确业务错误码 |
| 并发 | 多 goroutine 同时调用，结果一致 |

---

### 3.11 P11 — 脚本版本管理（Low）

#### 方案

在脚本注册时附加版本号：

```go
GrabPacket = cRedis.NewScript("grab_packet", "v1.2", LuaGrabPacket)
```

启动时打印所有脚本 sha + 版本，便于排查「线上 Lua 与代码不一致」问题：

```
INFO  lua script registered  name=grab_packet version=v1.2 sha=9a3b...
```

---

### 3.12 P12 — Redis Cluster hash tag 规划（Medium）

#### 现状

所有 key 用 `cashparty:` 前缀，无 hash tag。

#### 方案

制定 key 命名规范，所有同一房间内的 key 共享 `{roomID}` hash tag：

```
# 现状
cashparty:room:hash:R1
cashparty:room:players:R1
cashparty:round:state:RD1           # 跨 slot！
cashparty:packet:info:P1            # 跨 slot！

# 目标
cashparty:{R1}:room:hash
cashparty:{R1}:room:players
cashparty:{R1}:round:state:RD1
cashparty:{R1}:packet:info:P1
```

**本次只做规范文档**，不实际改造（改造影响面大，需独立项目推进）。

---

## 四、实施计划

### 4.1 分阶段路线图

| 阶段 | 任务 | 影响面 | 风险 | 优先级 |
|---|---|---|---|---|
| **P0** | §3.1 EVALSHA 调用层 + §3.2 目录重组 | common/redis + game/settlement redis 层 | 低（go-redis 内部已处理 fallback） | High |
| **P0** | §3.9 可观测性（metrics + slow log） | common/redis | 低 | High |
| **P0** | §3.10 测试体系（miniredis） | 新增 test 文件 | 无 | High |
| **P1** | §3.3 错误码注入 preamble | scripts 包 | 低 | Medium |
| **P1** | §3.5 长脚本分块注释 + finalResults 排序移到 Go | lua_game.go | 中（需回归测试） | Medium |
| **P1** | §3.4 key builder 抽取到 preamble | scripts 包 | 低 | Medium |
| **P2** | §3.10 修复死代码 | lua_game.go | 无 | Low |
| **P2** | §3.6 TTL 参数化 | 全脚本 | 低 | Low |
| **P2** | §3.7 返回值结构化（新脚本采用） | 渐进 | 低 | Low |
| **P3** | §3.11 版本号 + §3.12 hash tag 规范文档 | 文档 | 无 | Low |

### 4.2 验证策略

1. **单元测试**：每个脚本 miniredis 覆盖（§3.10）
2. **基准对比**：改造前后用 `go test -bench` 对比 `LuaGrabPacket` / `LuaSettleRound` 的 QPS 与延迟
3. **灰度**：先在 1 个实例启用 EVALSHA 路径，观察 1 天指标无异常后全量
4. **回退**：保留 `Client.Eval` 旧方法，配置开关 `lua.use_evalsha=true/false`，遇问题秒级回退

### 4.3 验收标准

- [ ] 所有 22 个脚本通过 miniredis 单测
- [ ] `go test ./...` 全绿
- [ ] `go vet ./...` 无警告
- [ ] 灰度环境 Lua p99 延迟下降 ≥ 30%
- [ ] 灰度环境 5min 内无 `NOSCRIPT` 错误告警
- [ ] scripts 包集中注册，settlement/service 层不再直接持有 Lua 源码
- [ ] 每个脚本有头部文档（KEYS/ARGV/返回值/错误码）

---

## 五、附录

### 5.1 脚本调用方清单（改造影响面）

| 脚本 | 调用方 | 调用次数/秒（估） |
|---|---|---|
| LuaGrabPacket | [repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go) → grab_service | 高（峰值 1k+） |
| LuaRobotGrabPacket | robot_player | 中 |
| LuaSendPacket | game_app_service | 中 |
| LuaSettleRound | settlement_service | 中 |
| LuaSelectSeat / LuaAutoSeatAndReady | seat_app_service | 高 |
| LuaJoinAsSpectator | room_app_service | 高 |
| luaDeductBalance | virtual_balance_service | 中 |
| releaseAssignLockScript | robot_scheduler_service | 低 |

### 5.2 参考文档

- [GATEWAY_OPTIMIZATION_REVIEW.md#L486](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/GATEWAY_OPTIMIZATION_REVIEW.md#L486)：已建议 EVALSHA 改造
- [GAME_SERVICE_BUG_ANALYSIS.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/docs/GAME_SERVICE_BUG_ANALYSIS.md)：相关 bug 记录
- [Redis 官方：EVAL vs EVALSHA](https://redis.io/docs/manual/programmability/eval/)
- [go-redis Script 文档](https://pkg.go.dev/github.com/redis/go-redis/v9#Script)

### 5.3 改造前后对比示例

**改造前**（[repository.go#L186](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go#L186)）：

```go
result, err := r.client.Eval(ctx, LuaSelectSeat, keys, args...).Slice()
if err != nil {
    return err
}
code := parseLuaCode(result[0])
if code != domain.LuaSuccess {
    return domain.MapLuaError(code)
}
return nil
```

每次发送 ~1.5KB 脚本字符串。

**改造后**：

```go
result, err := scripts.SelectSeat.Run(ctx, r.client, keys, args...).Slice()
if err != nil {
    return err
}
code := parseLuaCode(result[0])
if code != domain.LuaSuccess {
    return domain.MapLuaError(code)
}
return nil
```

首次 `SCRIPT LOAD` 后，后续每次发送 40 字节 sha1 + 参数；自动 `NOSCRIPT` fallback。
