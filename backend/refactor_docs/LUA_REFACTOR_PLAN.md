# Lua 脚本重构方案（LUA_REFACTOR_PLAN）

> 本方案基于对 backend 全量 Lua 代码的逐行 review，目标是收敛所有 Lua 脚本到统一框架，消除内联 Lua、孤儿 key、硬编码 TTL、重复脚本、错误码不统一等问题，并在 `CODING_STANDARD.md` §7.2 沉淀可执行的规约。
>
> 创建时间：2026-07-04
> 适用范围：`/Users/aaron.pan/Desktop/party/RedPacket-master/backend`

---

## 一、整体设计思路

### 1.1 现状问题（基于全量 review）

通过对 backend 全量代码 grep `\.lua\.go`、`redis\.Eval`、`cRedis\.NewScript`、`redis\.call`，发现以下问题：

| # | 问题 | 严重程度 | 影响范围 |
|---|---|---|---|
| P0-1 | `common/limiter/limiter.go:36-54` 内联 Lua 字符串 + `redis.Eval`，未走 `redis.Script`，无 EVALSHA 优化 | P0 | 全局限流 |
| P0-2 | `gateway/middleware/ratelimit.go:100-118` 内联 Lua 字符串 + `redis.Eval`，与 limiter.go 脚本**字节级重复** | P0 | 网关限流 |
| P0-3 | `gateway/connection/manager.go:449-481` `LuaRegisterConnection` 内联 Lua 常量 + `redis.Eval`，未走 `redis.Script` | P0 | 连接注册 |
| P0-4 | `scripts/force_end_room.go:15-81` 内联 `LuaEndGame` 常量，与 `game/.../round.lua.go:luaEndGame` **字节级重复** | P0 | 运维脚本 |
| P0-5 | `game/.../robot_lock.lua.go:luaReleaseAssignLock` 与 `common/lock/scripts/release_lock.lua.go:luaReleaseLock` **字节级重复**（都是 `if GET==token then DEL`） | P0 | 机器人锁 |
| P1-1 | `packet.lua.go` / `round.lua.go` 通过 `keyPrefix .. ':packet:info:' .. packetID` 在 Lua 内拼接 key，对应 Go 常量 `KeyPacketInfo` 是 `%s` 模板，存在"Lua 拼接 vs Go 模板"双轨制 | P1 | 红包流程 |
| P1-2 | `packet.lua.go` 内拼接 `keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID`，Go 侧 `rediskeys` 无对应 `KeyRoundGrabbed` 常量（只有 `KeyRoundGrabRecord`）→ **Lua 孤儿 key** | P1 | 红包流程 |
| P1-3 | 13 处 `86400` 硬编码 TTL 散落在 6 个 Lua 脚本中（room_seat/queue/round/penalty/packet） | P1 | 全部业务 Lua |
| P1-4 | `luaGrabPacket` 的 `packetID` 由 Go 侧传入但脚本内不使用 `availablePacketsKey`（KEYS[1]）查找，存在 KEYS/ARGV 混乱 | P1 | 抢红包 |
| P1-5 | `luaAutoDistributePackets` 内 `local userGrabKey = keyPrefix .. ':round:grabbed:' ...` 重复构造，与 `luaGrabPacket` 不一致（后者由 Go 传入 KEYS[2]） | P1 | 红包流程 |
| P2-1 | `luaEndGame`（round.lua.go）和 `scripts/force_end_room.go:LuaEndGame` 两份相同脚本，运维脚本直接复制业务脚本源码 | P2 | 运维脚本 |
| P2-2 | `luaReleaseAssignLock`（robot_lock.lua.go）和 `ReleaseLockScript`（common/lock/scripts）两份相同脚本 | P2 | 机器人锁 |
| P2-3 | `luaRobotGrabPacket` 注释说"避免 math.random"，但 `luaAutoDistributePackets` 仍按 `LRANGE` 顺序分配，未做随机化，与机器人抢红包策略不一致 | P2 | 红包分配 |
| P2-4 | Lua 脚本无单元测试，`scripts/` 目录无 `_test.go` | P2 | 全部业务 Lua |
| P2-5 | `luaSettleRound` 单脚本 280 行，承担"幂等检查 + 回合结算 + 累计金额 + 排名 + 清理"5 项职责，违反 SRP | P2 | 回合结算 |
| P2-6 | `luaHandlePenalty` 的 `penaltyCount` 通过 `GET` 读一次后又 `INCR`，存在读改写不一致（虽然 INCR 原子，但 `local penaltyCount = tonumber(redis.call('GET', penaltyCountKey) or 0)` 读出的值未被使用）→ 死代码 | P2 | 惩罚 |
| P2-7 | Lua 错误码 `{1, 14, 15, 20, 21, 22, 23, 32, 40, 41, 50, 51, 52, 60, 70, 71, 72, 73, 74, 75}` 散落在 8 个脚本中，无统一常量表，Go 侧 `domain.MapLuaError` 是唯一映射点但 Lua 侧无注释 | P2 | 全部业务 Lua |

### 1.2 设计目标

1. **单一真相源**：每段 Lua 脚本只存在一份，禁止跨文件复制。
2. **统一框架**：所有 Lua 脚本必须经 `cRedis.NewScript(name, src)` 注册，禁止内联 `redis.Eval`。
3. **零孤儿 key**：Lua 内拼接的每个 key 都必须在 `common/rediskeys` 有对应常量，并在脚本注释中标注映射关系。
4. **TTL 配置化**：Lua 内不硬编码 TTL，统一由 Go 侧通过 ARGV 传入（从 `cfg.Redis.TTL` 读取）。
5. **错误码统一**：在 `game/domain/lua_codes.go` 集中定义所有 Lua 错误码常量，Lua 脚本和 Go 侧共用。
6. **可测试**：每个 Lua 脚本至少有一个 `TestLuaXxx` 单元测试（miniredis 或 mock）。
7. **规约沉淀**：在 `CODING_STANDARD.md` §7.2 新增 `L-1 ~ L-10` 共 10 条 Lua 规约，与现有 `SC-x`、`DL-x`、`TX-x` 风格一致。

### 1.3 重构原则

- **向后兼容**：不改变 Lua 脚本的对外行为（KEYS/ARGV 顺序、返回值结构）。
- **渐进式**：分 4 个 Phase 推进，每个 Phase 可独立合并、独立验证。
- **不破坏业务**：所有改动通过 `go build` + `go vet` + `gofmt` 验证，关键路径加测试。

---

## 二、目标文件结构

```
backend/
├── common/
│   ├── redis/
│   │   ├── script.go                    # [保留] Script 框架（EVALSHA + fallback）
│   │   └── redis.go                     # [保留] Client.Eval（仅框架内部使用）
│   ├── lock/
│   │   └── scripts/
│   │       └── release_lock.lua.go      # [保留] 通用锁释放脚本（唯一）
│   └── limiter/
│       ├── limiter.go                   # [修改] 删除内联 Lua，改用 scripts.SlidingWindow
│       └── scripts/
│           └── sliding_window.lua.go    # [新增] 滑动窗口限流脚本（统一 limiter + gateway）
├── gateway/
│   ├── connection/
│   │   ├── manager.go                   # [修改] 删除内联 LuaRegisterConnection，改用 scripts.RegisterConnection
│   │   └── scripts/
│   │       └── register_connection.lua.go  # [新增] 连接注册脚本
│   └── middleware/
│       ├── ratelimit.go                 # [修改] 删除内联 Lua，改用 common/limiter/scripts.SlidingWindow
│       └── ...
├── game/
│   ├── domain/
│   │   ├── errors.go                    # [保留] MapLuaError
│   │   └── lua_codes.go                 # [新增] Lua 错误码常量表（单一真相源）
│   └── infrastructure/persistence/redis/
│       └── scripts/
│           ├── registry.go              # [修改] 引用 lua_codes.go 常量，删除 ReleaseAssignLock（改用 common/lock/scripts）
│           ├── room_seat.lua.go         # [修改] TTL 从 ARGV 传入，错误码用常量
│           ├── queue.lua.go             # [修改] 同上
│           ├── packet.lua.go            # [修改] key 拼接改用 Go 传入的 KEYS，删除 keyPrefix 拼接
│           ├── round.lua.go             # [修改] 同上 + 拆分 luaSettleRound（可选）
│           ├── penalty.lua.go           # [修改] 删除死代码，TTL 从 ARGV 传入
│           ├── robot_lock.lua.go        # [删除] 改用 common/lock/scripts.ReleaseLockScript
│           └── *_test.go                # [新增] 每个 Lua 脚本的单元测试
├── settlement/
│   └── infrastructure/persistence/redis/
│       └── scripts/
│           ├── registry.go              # [保留]
│           ├── virtual_balance.lua.go   # [保留] 已符合规约
│           └── virtual_balance_test.go  # [新增] 单元测试
└── scripts/
    └── force_end_room.go                # [修改] 删除内联 LuaEndGame，import game/.../scripts 调用 EndGame.Run
```

### 2.1 文件结构设计要点

1. **脚本就近放置**：业务脚本放在各服务的 `infrastructure/persistence/redis/scripts/`，通用脚本（锁释放、限流）放在 `common/`。
2. **registry.go 是唯一注册点**：每个服务的 `scripts/registry.go` 集中注册该服务所有脚本，调用方只能通过 `scripts.XxxScript.Run` 调用，禁止直接 `cRedis.NewScript`。
3. **错误码集中定义**：`game/domain/lua_codes.go` 定义所有 Lua 错误码（如 `LuaErrRoomNotFound = 1`），Lua 脚本注释中引用这些常量名，Go 侧 `MapLuaError` 也引用。
4. **测试文件同目录**：`xxx.lua.go` 对应 `xxx_test.go`，测试 Lua 脚本在 miniredis 上的行为。

---

## 三、规约设计（CODING_STANDARD.md §7.2 新增 L-1 ~ L-10）

### §7.2 Lua 脚本（MUST）

> 现有 §7.2 已有 4 条规则，本次重构新增 `L-1 ~ L-10` 共 10 条编号规约，与 `SC-x`、`DL-x`、`TX-x` 风格一致。

#### L-1：所有 Lua 脚本必须经 `cRedis.NewScript` 注册（MUST）

- 脚本源码放在 `scripts/<name>.lua.go` 中作为 Go 字符串常量，命名 `lua<Action>`。
- 在 `scripts/registry.go` 集中注册：`var XxxScript = cRedis.NewScript("xxx", luaXxx)`。
- **禁止**在 service / repository / middleware / connection manager 里内联 Lua 字符串再调 `redis.Eval`。
- **违反示例**：`common/limiter/limiter.go:36-54`、`gateway/middleware/ratelimit.go:100-118`、`gateway/connection/manager.go:449-481`、`scripts/force_end_room.go:15-81`（均已在本方案修复）。

#### L-2：禁止脚本字节级重复（MUST）

- 同一逻辑的 Lua 脚本只能存在一份。
- 通用脚本（锁释放、滑动窗口限流）放在 `common/`，业务脚本放在各服务 `scripts/`。
- 运维脚本（`scripts/force_end_room.go`）必须 import 业务脚本，禁止复制源码。
- **违反示例**：`robot_lock.lua.go:luaReleaseAssignLock` 与 `common/lock/scripts/release_lock.lua.go:luaReleaseLock` 重复（本方案删除前者）。

#### L-3：Lua 内禁止拼接 key，所有 key 必须由 Go 侧通过 KEYS 传入（MUST）

- Lua 脚本内**禁止** `keyPrefix .. ':xxx:' .. id` 形式的字符串拼接。
- 所有 Redis key 必须在 Go 侧用 `rediskeys.XxxKey(args)` 构造，通过 `KEYS[1..N]` 传入 Lua。
- 例外：全局自增 ID（如 `KeyGlobalPacketID`）可作为常量直接在 Lua 内引用，但必须在脚本注释中标注对应的 Go 常量名。
- **违反示例**：`packet.lua.go:52` `local packetKey = keyPrefix .. ':packet:info:' .. packetID`（本方案改为 Go 侧构造 `PacketInfoKey(packetID)` 传入 KEYS）。

#### L-4：Lua 孤儿 key 禁止（MUST）

- Lua 脚本中引用的每个 key 都必须在 `common/rediskeys/keys.go` 有对应常量。
- 脚本文件头部 MUST 注释 `Lua key → Go 常量` 映射表。
- **违反示例**：`packet.lua.go` 内拼接 `keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID`，Go 侧无对应 `KeyRoundGrabbed` 常量（本方案新增 `KeyRoundGrabbed = KeyPrefix + ":round:grabbed:%s:%s"`）。

#### L-5：Lua 内禁止硬编码 TTL（MUST）

- TTL 必须由 Go 侧通过 `ARGV` 传入，从 `cfg.Redis.TTL.XxxTTL` 读取。
- **禁止** `redis.call('EXPIRE', key, 86400)` 硬编码。
- **违反示例**：`room_seat.lua.go:55` `redis.call('SET', userRoomKey, roomIDStr, 'EX', 86400)`（本方案改为 `redis.call('SET', userRoomKey, roomIDStr, 'EX', ARGV[5])`）。

#### L-6：Lua 错误码必须引用常量名（MUST）

- 所有 Lua 错误码（如 `1`、`14`、`20`）必须在 `game/domain/lua_codes.go` 定义为常量（如 `LuaErrRoomNotFound = 1`）。
- Lua 脚本注释中 MUST 引用常量名：`return {LuaErrRoomNotFound, ...}`（Lua 无常量机制，用注释标注）。
- Go 侧 `domain.MapLuaError` 必须引用相同常量。
- **违反示例**：8 个 Lua 脚本内散落 20+ 个魔法数字错误码（本方案统一收敛到 `lua_codes.go`）。

#### L-7：Lua 返回值第一项必须是整数 code（MUST）

- 由 `parseLuaCode` 解析，经 `domain.MapLuaError` 映射为 `*message.GameError`。
- code = 0 表示成功，非 0 表示错误。
- **禁止**返回字符串作为第一项。

#### L-8：Lua 内禁止使用 `math.random` / `math.randomseed`（MUST）

- Redis Lua 禁用随机函数，会导致主从复制不一致。
- 随机性必须由 Go 侧预生成，通过 `ARGV` 传入（参考 `luaRobotGrabPacket` 的 `randOffset`）。

#### L-9：Lua 内禁止使用 `KEYS` 命令（MUST）

- `redis.call('KEYS', pattern)` 会阻塞 Redis，禁止在生产 Lua 脚本中使用。
- 遍历 key 必须用 `SCAN`（Go 侧）或通过已知 key 集合（如 `SMEMBERS`、`HKEYS`、`LRANGE`）。

#### L-10：每个 Lua 脚本必须有单元测试（SHOULD）

- 测试文件 `xxx_test.go` 放在 `scripts/` 目录下。
- 使用 `miniredis` 或 `redismock` 在本地执行 Lua 脚本，验证返回值结构。
- 至少覆盖：成功路径、错误码路径、幂等性路径。

---

## 四、重构任务分解（4 Phase，20 Task）

### Phase 1：消除内联 Lua（P0）

> 目标：消除 4 处内联 Lua，收敛到 `redis.Script` 框架。

#### Task L-1.1：新建 `common/limiter/scripts/sliding_window.lua.go`

- 新建 `common/limiter/scripts/sliding_window.lua.go`，定义 `luaSlidingWindow` 和 `SlidingWindowScript`。
- 脚本内容合并 `limiter.go:36-54` 和 `ratelimit.go:100-118`（两者字节级重复）。
- 新建 `common/limiter/scripts/registry.go` 注册 `SlidingWindowScript`。
- 修改 `common/limiter/limiter.go:Allow`：删除内联 `script`，改用 `scripts.SlidingWindowScript.Run`。
- 修改 `gateway/middleware/ratelimit.go:slidingWindowAllow`：删除内联 `script`，改用 `limiterScripts.SlidingWindowScript.Run`（import `common/limiter/scripts`）。
- 验证：`go build ./common/limiter/... ./gateway/middleware/...`。

#### Task L-1.2：新建 `gateway/connection/scripts/register_connection.lua.go`

- 新建 `gateway/connection/scripts/register_connection.lua.go`，定义 `luaRegisterConnection` 和 `RegisterConnectionScript`。
- 新建 `gateway/connection/scripts/registry.go` 注册。
- 修改 `gateway/connection/manager.go`：
  - 删除 `const LuaRegisterConnection = ...`（line 449-481）。
  - `registerInRedis` 改用 `connScripts.RegisterConnectionScript.Run`。
- 验证：`go build ./gateway/connection/...`。

#### Task L-1.3：`scripts/force_end_room.go` 改用业务脚本

- 修改 `scripts/force_end_room.go`：
  - 删除 `const LuaEndGame = ...`（line 15-81）。
  - import `github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts`。
  - 改用 `scripts.EndGame.Run(ctx, client, keys, args...)`。
  - 注意：`scripts.EndGame.Run` 接收 `*cRedis.Client`，但 force_end_room.go 用原生 `*redis.Client`。需要新建一个 `*cRedis.Client` 包装，或在 `scripts` 包新增 `RunWithRawClient` 辅助函数。
- 验证：`go build ./scripts/...`（注意：scripts 目录有预存在的 `main redeclared` 错误，需单独构建 `go build force_end_room.go`）。

### Phase 2：消除脚本重复 + 孤儿 key（P0+P1）

#### Task L-2.1：删除 `robot_lock.lua.go`，改用 `common/lock/scripts`

- 修改 `game/infrastructure/persistence/redis/robot_scheduler.go`：
  - `ReleaseAssignLock` 方法改用 `lockScripts.ReleaseLockScript.Run`（已 import `lockScripts "github.com/cashparty/backend/common/lock/scripts"`）。
  - 删除 `import "github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"` 中对 `scripts.ReleaseAssignLock` 的引用（如有）。
- 删除 `game/infrastructure/persistence/redis/scripts/robot_lock.lua.go`。
- 修改 `game/infrastructure/persistence/redis/scripts/registry.go`：删除 `ReleaseAssignLock` 注册行。
- 验证：`go build ./game/...`。

#### Task L-2.2：新增 `KeyRoundGrabbed` 常量，消除 Lua 孤儿 key

- 在 `common/rediskeys/keys.go` 新增：
  ```go
  // KeyRoundGrabbed 玩家本轮已抢标记（Lua 脚本使用）。
  // 对应 Lua 中的 keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID
  KeyRoundGrabbed = KeyPrefix + ":round:grabbed:%s:%s"
  ```
- 在 `packet.lua.go` 和 `round.lua.go` 头部注释更新映射表，标注 `KeyRoundGrabbed`。
- 验证：`go build ./common/... ./game/...`。

#### Task L-2.3：`packet.lua.go` 消除 `keyPrefix` 拼接

- 修改 `luaGrabPacket`：
  - 新增 KEYS[6] = `packetInfoKey`（Go 侧 `rediskeys.PacketInfoKey(packetID)` 构造）。
  - 新增 KEYS[7] = `packetAvailableKey`（Go 侧 `rediskeys.PacketAvailableKey(packetID)` 构造）。
  - 删除 `local keyPrefix = ARGV[5]` 和 `local packetID = ARGV[6]`。
  - 删除 `local packetKey = keyPrefix .. ':packet:info:' .. packetID` 和 `local availableKey = keyPrefix .. ':packet:available:' .. packetID`。
  - 改用 `local packetKey = KEYS[6]` 和 `local availableKey = KEYS[7]`。
- 同样修改 `luaRobotGrabPacket`：
  - 注意：`luaRobotGrabPacket` 在循环内动态构造 `availableKey`，需要改为 Go 侧预构造所有 `availableKey` 列表，通过 KEYS 或 ARGV 传入。或保留 `keyPrefix` 但消除 `keyPrefix .. ':packet:info:'` 拼接。
  - **设计决策**：`luaRobotGrabPacket` 需要在循环内查找可用红包，无法预先知道哪个 packetID 可用。保留 `keyPrefix` 传入，但在脚本头部注释明确所有拼接的 key 与 Go 常量的映射，并在 `rediskeys` 新增对应 `KeyPacketInfoPrefix` 和 `KeyPacketAvailablePrefix`（已存在）。
- 修改 `luaAutoDistributePackets`：同 `luaRobotGrabPacket`，保留 `keyPrefix` 但注释映射。
- 修改 `luaSendPacket`：
  - 新增 KEYS[6] = `globalPacketIDKey`（`rediskeys.KeyGlobalPacketID`）。
  - 循环内构造 `packetKey` 和 `availableKey` 改用 `keyPrefix` 拼接（因为 packetID 在循环内动态生成），但注释映射。
- 修改所有调用方（`game/application/grab_service.go`、`game/application/game_app_service.go`）：传入新增的 KEYS。
- 验证：`go build ./game/...`。

#### Task L-2.4：`round.lua.go` 消除 `keyPrefix` 拼接

- 修改 `luaSettleRound`：
  - 循环内 `local packetKey = keyPrefix .. ':packet:info:' .. packetIDStr` 保留（packetID 动态），但注释映射到 `KeyPacketInfoPrefix`。
  - 新增 `rediskeys.KeyPacketInfoPrefix` 常量（已存在 `KeyPacketAvailablePrefix`，需新增 `KeyPacketInfoPrefix = KeyPrefix + ":packet:info:"`）。
- 验证：`go build ./common/... ./game/...`。

### Phase 3：TTL 配置化 + 错误码统一（P1）

#### Task L-3.1：新增 `RedisTTLConfig` 配置结构体

- 在 `common/config/types.go` 新增：
  ```go
  type RedisTTLConfig struct {
      RoomDataTTL         time.Duration // 房间数据（roomHash/spectators/players/seats/seatOwner）
      UserRoomTTL         time.Duration // user-room 映射
      QueueTTL            time.Duration // 排队队列
      PenaltyRecordTTL    time.Duration // 惩罚记录
      PacketDataTTL       time.Duration // 红包数据（packetInfo/available/userGrab）
      RoundStateTTL       time.Duration // 回合状态
      PenaltyCountTTL     time.Duration // 惩罚计数
  }
  ```
- 在 `game/config/config.go` 新增 `RedisTTL RedisTTLConfig` 字段。
- 在 `game/config/defaults.go` 设置默认值（全部 86400s = 24h，与当前硬编码一致）。
- 在 `config/game.yaml` 新增 `redis_ttl` 配置段。
- 验证：`go build ./...`。

#### Task L-3.2：Lua 脚本 TTL 改为 ARGV 传入

- 修改 `room_seat.lua.go` 所有脚本：
  - `luaJoinAsSpectator`：新增 `ARGV[5] = roomDataTTL`、`ARGV[6] = userRoomTTL`，替换 `86400`。
  - `luaSelectSeat` / `luaCancelSeat` / `luaLeaveRoom` / `luaKickPlayerAndInterrupt` / `luaHandleSeatTimeout` / `luaAutoSeatAndReady`：同上。
- 修改 `queue.lua.go`：`luaEnqueue` / `luaAutoSubstitute` 新增 TTL ARGV。
- 修改 `round.lua.go`：`luaEndGame` / `luaSettleRound` 新增 TTL ARGV。
- 修改 `penalty.lua.go`：`luaHandlePenalty` 新增 TTL ARGV。
- 修改 `packet.lua.go`：所有脚本新增 TTL ARGV。
- 修改所有 Go 侧调用方：从 `cfg.RedisTTL.XxxTTL` 读取，通过 ARGV 传入。
- 验证：`go build ./game/...`。

#### Task L-3.3：新增 `game/domain/lua_codes.go` 错误码常量表

- 新建 `game/domain/lua_codes.go`：
  ```go
  package domain

  // Lua 错误码常量表（单一真相源，Lua 脚本注释和 Go 侧 MapLuaError 共用）
  const (
      LuaErrSuccess              = 0
      LuaErrRoomNotFound         = 1
      LuaErrGameNotInPlaying     = 6
      LuaErrRoomFull             = 15
      LuaErrPacketsAlreadyExist  = 20
      LuaErrAlreadyGrabbed       = 21
      LuaErrPacketNotAvailable   = 22
      LuaErrPacketInfoNotFound   = 23
      LuaErrNotFirstRound        = 50
      LuaErrNotYourTurn          = 51
      LuaErrNoPlayers            = 52
      LuaErrOnlyPlayerCanGrab    = 60
      LuaErrAlreadyQueued        = 70
      LuaErrNotQueued            = 71
      LuaErrRobotNotAllowed      = 72
      LuaErrNoEmptySeat          = 73
      LuaErrSubstituteFail       = 74
      LuaErrQueueEmpty           = 75
      LuaErrPlayerAlreadySent    = 32
      LuaErrSeatOccupied         = 7
      LuaErrInvalidSeatNo        = 8
      LuaErrAlreadyPlayer        = 9
      LuaErrNotInRoom            = 14
      LuaErrNoSeatSelected       = 12
      LuaErrRoomFullTotal        = 3
      LuaErrNotInGrabbingPhase   = 40
      LuaErrGrabTimeout          = 41
  )
  ```
- 修改 `game/domain/errors.go:MapLuaError`：引用上述常量，删除魔法数字。
- 修改所有 Lua 脚本注释：在 `return {1, ...}` 处注释 `-- LuaErrRoomNotFound`。
- 验证：`go build ./game/...`。

#### Task L-3.4：删除 `luaHandlePenalty` 死代码

- 修改 `penalty.lua.go:luaHandlePenalty`：
  - 删除 `local penaltyCount = tonumber(redis.call('GET', penaltyCountKey) or 0)`（line 19，未被使用）。
  - 保留 `local newCount = redis.call('INCR', penaltyCountKey)`。
- 验证：`go build ./game/...`。

### Phase 4：测试 + 规约沉淀（P2）

#### Task L-4.1：新增 `game/.../scripts/room_seat_test.go`

- 测试 `luaJoinAsSpectator`、`luaSelectSeat`、`luaCancelSeat`、`luaLeaveRoom`、`luaPlayerReady`、`luaAutoSeatAndReady`。
- 使用 `github.com/alicebob/miniredis/v2`。
- 覆盖：成功路径、房间不存在（code=1）、座位已占（code=7）、已是玩家（code=9）。
- 验证：`go test ./game/infrastructure/persistence/redis/scripts/...`。

#### Task L-4.2：新增 `game/.../scripts/packet_test.go`

- 测试 `luaGrabPacket`、`luaRobotGrabPacket`、`luaAutoDistributePackets`、`luaSendPacket`。
- 覆盖：成功路径、非玩家（code=60）、已抢（code=21）、红包不存在（code=23）、幂等性。
- 验证：`go test ./game/infrastructure/persistence/redis/scripts/...`。

#### Task L-4.3：新增 `game/.../scripts/round_test.go` + `penalty_test.go` + `queue_test.go`

- 测试 `luaEndGame`、`luaSettleRound`（覆盖幂等性 code=2）、`luaHandlePenalty`、`luaDistributePenalty`、`luaEnqueue`、`luaDequeue`、`luaAutoSubstitute`。
- 验证：`go test ./game/infrastructure/persistence/redis/scripts/...`。

#### Task L-4.4：新增 `settlement/.../scripts/virtual_balance_test.go`

- 测试 `luaDeductBalance`：余额充足（返回 1）、余额不足（返回 0）、并发安全。
- 验证：`go test ./settlement/infrastructure/persistence/redis/scripts/...`。

#### Task L-4.5：新增 `common/limiter/scripts/sliding_window_test.go`

- 测试 `SlidingWindowScript`：窗口内允许、超限拒绝、窗口滑动后允许。
- 验证：`go test ./common/limiter/scripts/...`。

#### Task L-4.6：新增 `gateway/connection/scripts/register_connection_test.go`

- 测试 `RegisterConnectionScript`：首次注册（返回 {0,'',''}）、重复注册同 connID（返回 {0,'',''}）、重复注册不同 connID（返回 {1,oldConnID,oldNodeID}）。
- 验证：`go test ./gateway/connection/scripts/...`。

#### Task L-4.7：CODING_STANDARD.md §7.2 新增 L-1 ~ L-10 规约

- 在 `CODING_STANDARD.md` §7.2 现有 4 条规则后，新增 `L-1 ~ L-10` 共 10 条编号规约（内容见本方案 §三）。
- 在 §7.2 末尾添加引用：「详见 [§19 Lua 脚本规约](#19-lua-脚本规约)」。
- 新增 §19 章节，标题「Lua 脚本规约」，内容为 L-1 ~ L-10 详细说明。
- 更新 CODING_STANDARD.md 目录，新增 §19 条目。
- 更新附录 A 参考实现索引，新增 Lua 脚本框架、Lua 错误码常量表。
- 更新附录 B 变更记录。

#### Task L-4.8：全局验证

- `go build ./common/... ./game/... ./settlement/... ./gateway/...` 通过。
- `go vet ./common/... ./game/... ./settlement/... ./gateway/...` 通过。
- `gofmt -l` 无新增文件输出。
- `go test ./.../scripts/...` 全部通过。
- grep `redis\.Eval` 在 service/repository/middleware 层为 0（仅 `common/redis/script.go` 和 `common/redis/redis.go` 保留）。
- grep `const lua\w+ = ` 仅在 `*.lua.go` 文件中出现。
- grep `86400` 在 `*.lua.go` 文件中为 0。
- grep `keyPrefix \.\.` 在 `*.lua.go` 文件中仅在注释中出现（实际拼接改为 KEYS 传入或注释标注映射）。
- grep `math\.random` 在 `*.lua.go` 文件中为 0。
- grep `redis\.call\('KEYS'` 在 `*.lua.go` 文件中为 0。

---

## 五、Lua 脚本清单（重构后）

| # | 脚本名 | 文件位置 | 用途 | KEYS 数 | ARGV 数 | 错误码 |
|---|---|---|---|---|---|---|
| 1 | `release_lock` | `common/lock/scripts/release_lock.lua.go` | 通用锁释放（token 校验） | 1 | 1 | 0/1 |
| 2 | `sliding_window` | `common/limiter/scripts/sliding_window.lua.go` | 滑动窗口限流 | 1 | 3 | 0/1 |
| 3 | `register_connection` | `gateway/connection/scripts/register_connection.lua.go` | 连接注册（踢旧） | 1 | 5 | 0/1 |
| 4 | `deduct_balance` | `settlement/.../scripts/virtual_balance.lua.go` | 虚拟余额扣减 | 2 | 2 | 0/1 |
| 5 | `join_spectator` | `game/.../scripts/room_seat.lua.go` | 加入观众 | 4 | 6 | 0/1/3/4/15 |
| 6 | `select_seat` | `game/.../scripts/room_seat.lua.go` | 选座 | 5 | 4 | 0/1/6/7/8/9/14 |
| 7 | `cancel_seat` | `game/.../scripts/room_seat.lua.go` | 取消选座 | 5 | 1 | 0/1/6/14 |
| 8 | `leave_room` | `game/.../scripts/room_seat.lua.go` | 离开房间 | 6 | 2 | 0/1/14/16 |
| 9 | `kick_player` | `game/.../scripts/room_seat.lua.go` | 踢人 | 5 | 4 | 0/1/14/32 |
| 10 | `player_ready` | `game/.../scripts/room_seat.lua.go` | 玩家准备 | 3 | 2 | 1/12/14 |
| 11 | `handle_seat_timeout` | `game/.../scripts/room_seat.lua.go` | 座位超时 | 6 | 1 | 0/1/2 |
| 12 | `auto_seat_ready` | `game/.../scripts/room_seat.lua.go` | 自动选座+准备 | 5 | 3 | 0/1/6/9/14/73 |
| 13 | `try_start_game` | `game/.../scripts/room_seat.lua.go` | 尝试开始游戏 | 1 | 1 | 0/1 |
| 14 | `enqueue` | `game/.../scripts/queue.lua.go` | 加入排队 | 4 | 2 | 0/1/9/14/70/72 |
| 15 | `dequeue` | `game/.../scripts/queue.lua.go` | 退出排队 | 2 | 1 | 0/1/71 |
| 16 | `auto_substitute` | `game/.../scripts/queue.lua.go` | 自动替补 | 6 | 2 | 0/1/6/73/74/75 |
| 17 | `grab_packet` | `game/.../scripts/packet.lua.go` | 抢红包 | 5 | 6 | 0/21/22/23/40/41/60 |
| 18 | `robot_grab_packet` | `game/.../scripts/packet.lua.go` | 机器人抢红包 | 5 | 6 | 0/21/22/23/40/41/60 |
| 19 | `auto_distribute` | `game/.../scripts/packet.lua.go` | 自动分配红包 | 5 | 3 | 0 |
| 20 | `send_packet` | `game/.../scripts/packet.lua.go` | 发红包 | 5 | 15 | 0/6/20/50/51/52 |
| 21 | `end_game` | `game/.../scripts/round.lua.go` | 游戏结束 | 5 | 2 | 0/1 |
| 22 | `settle_round` | `game/.../scripts/round.lua.go` | 回合结算 | 6 | 3 | 0/1/2/3 |
| 23 | `handle_penalty` | `game/.../scripts/penalty.lua.go` | 处理惩罚 | 5 | 4 | 0 |
| 24 | `distribute_penalty` | `game/.../scripts/penalty.lua.go` | 分配惩罚 | 2 | 2 | 0 |

**合计**：24 个 Lua 脚本（重构前 26 个，删除 2 个重复：`robot_lock.lua.go` 和 `force_end_room.go:LuaEndGame`）。

---

## 六、关键改动点详解

### 6.1 `packet.lua.go` key 传递方式调整

**现状**（`luaGrabPacket`）：
```lua
local keyPrefix = ARGV[5]
local packetID = ARGV[6]
local packetKey = keyPrefix .. ':packet:info:' .. packetID
local availableKey = keyPrefix .. ':packet:available:' .. packetID
```

**问题**：
- `keyPrefix` 拼接违反 L-3。
- `KeyPacketInfo` 是 `%s` 模板，Lua 用 `..` 拼接，双轨制。

**重构后**（`luaGrabPacket`）：
```lua
-- KEYS[6] = packetInfoKey (Go: rediskeys.KeyPacketInfo(packetID))
-- KEYS[7] = packetAvailableKey (Go: rediskeys.KeyPacketAvailable(packetID))
local packetKey = KEYS[6]
local availableKey = KEYS[7]
```

**例外**（`luaRobotGrabPacket`、`luaAutoDistributePackets`、`luaSendPacket`、`luaSettleRound`）：
- 这些脚本在循环内动态生成 packetID，无法预先传入 KEYS。
- **保留 `keyPrefix` 传入**，但在脚本头部注释明确映射：
  ```lua
  -- keyPrefix .. ':packet:info:' .. packetID  → rediskeys.KeyPacketInfoPrefix + packetID
  --                                            （工厂函数 rediskeys.PacketInfoKey(packetID)）
  ```
- 在 `rediskeys` 新增 `KeyPacketInfoPrefix = KeyPrefix + ":packet:info:"`（已存在 `KeyPacketAvailablePrefix`）。

### 6.2 `robot_lock.lua.go` 删除

**现状**：
- `game/.../scripts/robot_lock.lua.go:luaReleaseAssignLock` 与 `common/lock/scripts/release_lock.lua.go:luaReleaseLock` 字节级重复。

**重构后**：
- 删除 `robot_lock.lua.go`。
- `game/infrastructure/persistence/redis/robot_scheduler.go:ReleaseAssignLock` 改用 `lockScripts.ReleaseLockScript.Run`（已 import）。
- `game/.../scripts/registry.go` 删除 `ReleaseAssignLock` 注册行。

### 6.3 `limiter.go` + `ratelimit.go` 合并

**现状**：
- `common/limiter/limiter.go:36-54` 和 `gateway/middleware/ratelimit.go:100-118` 内联相同 Lua 脚本。

**重构后**：
- 新建 `common/limiter/scripts/sliding_window.lua.go`，定义 `SlidingWindowScript`。
- `limiter.go` 和 `ratelimit.go` 都 import `common/limiter/scripts`，调用 `slidingWindowScripts.SlidingWindowScript.Run`。

### 6.4 `force_end_room.go` 改用业务脚本

**现状**：
- `scripts/force_end_room.go:15-81` 复制了 `game/.../round.lua.go:luaEndGame` 的源码。

**重构后**：
- 删除 `const LuaEndGame = ...`。
- import `github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts`。
- 改用 `scripts.EndGame.Run(ctx, cRedisClient, keys, args...)`。
- 由于 `scripts.EndGame.Run` 接收 `*cRedis.Client`，而 `force_end_room.go` 用原生 `*redis.Client`，需要在 `common/redis` 新增 `NewClientFromRaw(rdb *redis.Client) *Client` 辅助函数，或在 `scripts` 包新增 `RunWithRawClient(ctx, rdb, keys, args)` 方法。

### 6.5 Lua 错误码统一

**现状**：
- 8 个 Lua 脚本内散落 20+ 个魔法数字错误码。
- Go 侧 `domain.MapLuaError` 用 `switch code` 映射，但 Lua 侧无注释。

**重构后**：
- 新建 `game/domain/lua_codes.go`，定义所有错误码常量。
- `domain/errors.go:MapLuaError` 改用常量引用。
- 所有 Lua 脚本在 `return {1, ...}` 处注释 `-- LuaErrRoomNotFound`。
- 脚本头部注释列出该脚本用到的所有错误码常量名。

---

## 七、风险与缓解

| 风险 | 缓解措施 |
|---|---|
| `packet.lua.go` key 传递方式变更，调用方需同步修改 | Task L-2.3 明确要求修改所有调用方，`go build` 验证 |
| TTL 配置化后，配置未设置会导致 TTL=0（key 立即过期） | `SetRedisTTLDefaults` 在 `defaults.go` 中设置默认值 86400s |
| `force_end_room.go` 跨包 import 业务脚本，可能引入循环依赖 | `scripts/` 是独立 main 包，import `game/.../scripts` 不构成循环 |
| Lua 脚本测试需要 miniredis 依赖 | 在 `go.mod` 新增 `github.com/alicebob/miniredis/v2` 依赖 |
| 错误码常量化后，Lua 注释与 Go 常量可能不同步 | L-6 规约要求注释引用常量名，code review 检查 |

---

## 八、验证清单

### 8.1 构建验证

```bash
cd /Users/aaron.pan/Desktop/party/RedPacket-master/backend

# 全量构建（忽略 scripts/ 目录预存在的 main redeclared 错误）
go build ./common/... ./game/... ./settlement/... ./gateway/...

# vet
go vet ./common/... ./game/... ./settlement/... ./gateway/...

# gofmt
gofmt -l common/ game/ settlement/ gateway/

# 测试
go test ./.../scripts/...
```

### 8.2 grep 验证

```bash
# 1. 无内联 Lua（service/repository/middleware 层）
grep -rn "redis\.Eval\|\.Eval(ctx" --include="*.go" common/ game/ settlement/ gateway/ \
  | grep -v "common/redis/script.go" \
  | grep -v "common/redis/redis.go"
# 期望：无输出

# 2. 无脚本字节级重复
grep -rn "if redis.call.*GET.*KEYS\[1\].*ARGV\[1\].*then.*return redis.call.*DEL" --include="*.lua.go" .
# 期望：仅 common/lock/scripts/release_lock.lua.go 一处

# 3. 无 86400 硬编码
grep -rn "86400" --include="*.lua.go" .
# 期望：无输出

# 4. 无 math.random
grep -rn "math\.random" --include="*.lua.go" .
# 期望：无输出

# 5. 无 redis.call('KEYS')
grep -rn "redis\.call.*'KEYS'" --include="*.lua.go" .
# 期望：无输出

# 6. Lua 孤儿 key 检查
grep -rn "keyPrefix \.\." --include="*.lua.go" . | grep -v "^.*//"
# 期望：所有匹配行都有对应的 rediskeys 常量注释

# 7. 错误码常量化
grep -rn "return {1," --include="*.lua.go" . | head -20
# 期望：每行附近注释有 LuaErrXxx 常量名

# 8. robot_lock.lua.go 已删除
ls game/infrastructure/persistence/redis/scripts/robot_lock.lua.go 2>&1
# 期望：No such file or directory

# 9. force_end_room.go 无内联 Lua
grep -n "const LuaEndGame" scripts/force_end_room.go
# 期望：无输出

# 10. CODING_STANDARD.md 包含 §19
grep -n "^## 19\. " CODING_STANDARD.md
# 期望：## 19. Lua 脚本规约
```

### 8.3 功能验证

- Lua 脚本行为不变（KEYS/ARGV 顺序、返回值结构）。
- 限流功能正常（limiter + ratelimit 共用脚本后行为一致）。
- 连接注册功能正常（踢旧逻辑不变）。
- force_end_room 运维脚本可正常执行。

---

## 九、任务依赖关系

```
Phase 1（P0，可并行）:
  L-1.1 (sliding_window) ──┐
  L-1.2 (register_conn) ──┤
  L-1.3 (force_end_room) ──┤
                            ↓
Phase 2（P0+P1，部分依赖）:
  L-2.1 (删 robot_lock) ─────── 独立
  L-2.2 (KeyRoundGrabbed) ──── 独立
  L-2.3 (packet.lua 改造) ─── 依赖 L-2.2
  L-2.4 (round.lua 改造) ──── 依赖 L-2.2
                            ↓
Phase 3（P1，部分依赖）:
  L-3.1 (RedisTTLConfig) ─── 独立
  L-3.2 (Lua TTL ARGV) ──── 依赖 L-3.1
  L-3.3 (lua_codes.go) ──── 独立
  L-3.4 (删死代码) ────────── 独立
                            ↓
Phase 4（P2，部分依赖）:
  L-4.1 ~ L-4.6 (测试) ──── 依赖 Phase 1-3 完成
  L-4.7 (CODING_STANDARD §19) ── 独立
  L-4.8 (全局验证) ──────── 依赖所有任务完成
```

---

## 十、附录

### 附录 A：现有 Lua 脚本调用方清单

| 脚本 | 调用方文件 | 调用方方法 |
|---|---|---|
| `JoinAsSpectator` | `game/.../repository.go` | `JoinAsSpectator` |
| `SelectSeat` | `game/.../repository.go` | `SelectSeat` |
| `CancelSeat` | `game/.../repository.go` | `CancelSeat` |
| `LeaveRoom` | `game/.../repository.go` | `LeaveRoom` |
| `KickPlayer` | `game/.../repository.go` | `KickPlayer` |
| `PlayerReady` | `game/.../repository.go` | `PlayerReady` |
| `HandleSeatTimeout` | `game/.../repository.go` | `HandleSeatTimeout` |
| `AutoSeatAndReady` | `game/.../repository.go` | `AutoSeatAndReady` |
| `TryStartGame` | `game/.../repository.go` | `TryStartGame` |
| `Enqueue` | `game/.../repository.go` | `Enqueue` |
| `Dequeue` | `game/.../repository.go` | `Dequeue` |
| `AutoSubstitute` | `game/.../repository.go` | `AutoSubstitute` |
| `GrabPacket` | `game/application/grab_service.go` | `GrabPacket` |
| `RobotGrabPacket` | `game/application/grab_service.go` | `RobotGrabPacket` |
| `AutoDistributePackets` | `game/application/grab_service.go` | `AutoDistributePackets` |
| `SendPacket` | `game/application/game_app_service.go` | `SendPacket` |
| `EndGame` | `game/application/game_app_service.go` | `EndGame` + `scripts/force_end_room.go` |
| `SettleRound` | `game/application/game_app_service.go` | `SettleRound` |
| `HandlePenalty` | `game/application/penalty_service.go` | `HandlePenalty` |
| `DistributePenalty` | `game/application/penalty_service.go` | `DistributePenalty` |
| `ReleaseAssignLock` | `game/.../robot_scheduler.go` | `ReleaseAssignLock`（重构后改用 `ReleaseLockScript`） |
| `DeductBalance` | `settlement/service/virtual_balance_service.go` | `DeductBalance` |
| `LuaRegisterConnection`（内联） | `gateway/connection/manager.go` | `registerInRedis` |
| `sliding_window`（内联×2） | `common/limiter/limiter.go` + `gateway/middleware/ratelimit.go` | `Allow` / `slidingWindowAllow` |

### 附录 B：Lua 错误码映射表（`domain.MapLuaError`）

| 错误码 | 常量名（重构后） | 含义 |
|---|---|---|
| 0 | `LuaErrSuccess` | 成功 |
| 1 | `LuaErrRoomNotFound` | 房间不存在 |
| 3 | `LuaErrRoomFullTotal` | 房间总人数已满 |
| 6 | `LuaErrGameNotInPlaying` | 游戏不在进行中 |
| 7 | `LuaErrSeatOccupied` | 座位已被占用 |
| 8 | `LuaErrInvalidSeatNo` | 座位号无效 |
| 9 | `LuaErrAlreadyPlayer` | 已是玩家 |
| 12 | `LuaErrNoSeatSelected` | 未选座 |
| 14 | `LuaErrNotInRoom` | 不在房间内 |
| 15 | `LuaErrRoomFull` | 观众席已满 |
| 16 | `LuaErrPlayerCannotLeave` | 玩家不能离开 |
| 20 | `LuaErrPacketsAlreadyExist` | 红包已存在 |
| 21 | `LuaErrAlreadyGrabbed` | 已抢过 |
| 22 | `LuaErrPacketNotAvailable` | 红包不可用 |
| 23 | `LuaErrPacketInfoNotFound` | 红包信息不存在 |
| 32 | `LuaErrPlayerAlreadySentPacket` | 玩家已发红包 |
| 40 | `LuaErrNotInGrabbingPhase` | 不在抢红包阶段 |
| 41 | `LuaErrGrabTimeout` | 抢红包超时 |
| 50 | `LuaErrNotFirstRound` | 不是首轮 |
| 51 | `LuaErrNotYourTurn` | 未轮到你 |
| 52 | `LuaErrNoPlayers` | 无玩家 |
| 60 | `LuaErrOnlyPlayerCanGrab` | 只有玩家能抢 |
| 70 | `LuaErrAlreadyQueued` | 已在队列 |
| 71 | `LuaErrNotQueued` | 不在队列 |
| 72 | `LuaErrRobotNotAllowed` | 机器人不允许 |
| 73 | `LuaErrNoEmptySeat` | 无空座 |
| 74 | `LuaErrSubstituteFail` | 替补失败 |
| 75 | `LuaErrQueueEmpty` | 队列为空 |

### 附录 C：rediskeys 与 Lua key 映射表（重构后）

| Lua 内拼接（如有） | Go 常量 | 工厂函数 |
|---|---|---|
| `keyPrefix .. ':packet:info:' .. packetID` | `KeyPacketInfoPrefix` + `KeyPacketInfo` | `PacketInfoKey(packetID)` |
| `keyPrefix .. ':packet:available:' .. packetID` | `KeyPacketAvailablePrefix` + `KeyPacketAvailable` | `PacketAvailableKey(packetID)` |
| `keyPrefix .. ':global:packet_id'` | `KeyGlobalPacketID` | - |
| `keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID` | `KeyRoundGrabbed`（新增） | `RoundGrabbedKey(roundID, userID)`（新增） |

---

## 十一、变更记录

| 日期 | 变更 |
|---|---|
| 2026-07-04 | 创建本方案，基于全量 Lua 代码 review |
