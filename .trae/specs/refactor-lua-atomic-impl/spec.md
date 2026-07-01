# Lua 原子实现重构 Spec

## Why

[LUA_REFACTOR_PLAN.md](file:///e:/demo/party/packet/backend/docs/LUA_REFACTOR_PLAN.md) 指出 backend 三个服务（game-service / settlement-service / robot-scheduler）共 22 个 Redis Lua 脚本存在性能、可维护性、可观测性、可测试性五类问题：所有脚本走 `EVAL` 每次重传整段源码、脚本散落在 service / infrastructure 多层（DDD 违规）、无单测覆盖、Lua 内魔法数字错误码、长脚本 190 行混合多职责、TTL 硬编码 15+ 处、无执行指标/日志、存在死代码、无版本管理。本 spec 落地 P0/P1/P2/P3 路线图，在不改变业务语义前提下提升 Lua 层工程质量，并保留旧路径回退能力。

## What Changes

### P0 阶段（High，必做）

- **§3.1 EVALSHA 调用层**：在 `common/redis` 新增 `Script` 包装类型（复用 `goredis.NewScript` 自动处理 SCRIPT LOAD + EVALSHA + NOSCRIPT fallback）；新增 `scripts/registry.go` 集中注册所有 22 个脚本
- **§3.2 脚本目录结构重组**：按业务域拆分 game 两个大文件（`lua_scripts.go` 781 行 → `room_seat.lua.go` + `queue.lua.go`；`lua_game.go` 739 行 → `packet.lua.go` + `round.lua.go` + `penalty.lua.go`）；**BREAKING** 内部包路径变更（`settlement/service/lua_scripts.go` → `settlement/infrastructure/persistence/redis/virtual_balance.lua.go`，修正 DDD 违规）；`robot_scheduler.go` 内联的 `releaseAssignLockScript` 抽离到 `scripts/robot_lock.lua.go`
- **§3.9 可观测性**：`Script.Run` 外层包装 metrics（耗时、状态、错误码）+ slow log（>100ms 告警），维度 `script_name` / `status`
- **§3.10 测试体系**：引入 `miniredis` 为每个脚本覆盖正常/边界/幂等/并发场景

### P1 阶段（Medium）

- **§3.3 错误码注入 preamble**：`NewScript` 自动把 `domain.LuaErrorCodes()` 反射生成的常量表拼接到脚本头部，消除 Lua 内魔法数字
- **§3.5 长脚本分块**：`LuaSettleRound` 内部分块加注释（6 段职责）；`finalResults` 排序逻辑移到 Go 侧（`sort.Slice`，不涉及 Redis 写，安全）
- **§3.4 key builder 抽取**：通过 preamble 机制注入共享 `packetInfoKey` / `availableKey` 等 key 构造函数，消除 grab / robot_grab / auto_distribute / settle 四处重复拼接

### P2 阶段（Low）

- **§3.9 死代码修复**：删除 `LuaHandlePenalty` 中未引用的 `penaltyCount` 变量
- **§3.6 TTL 参数化**：`86400` 改为通过 preamble 注入的 `defaultTTL`，Go 侧 `common/redis/ttl.go` 统一定义 `DefaultRedisTTL`
- **§3.7 返回值结构化**：新脚本统一返回 `{code, data, msg}` 三元组（cjson 编码），旧脚本仅在改动时迁移

### P3 阶段（Low）

- **§3.11 版本号管理**：`NewScript` 支持版本号参数，启动时打印所有脚本 name + version + sha 日志

### 不在范围

- **Redis Cluster hash tag 规划**：用户已确认暂不考虑（见 LUA_REFACTOR_PLAN.md 移除记录）
- **旧脚本返回值结构化批量迁移**：改造面大，仅新脚本采用

## Impact

- **Affected specs**：
  - [fix-game-optimization-issues](file:///e:/demo/party/.trae/specs/fix-game-optimization-issues/spec.md)：本 spec 不修改 P0/P1 已落地的 Lua 行为（math.random 移除、ReleaseAssignLock Lua 校验、EXPIRE 调整、virtual_balance 原子扣减、消费幂等 SetNX），仅做工程结构重组与调用层替换
- **Affected code**：
  - `common/redis/`：新增 `script.go` / `ttl.go` / 指标包装
  - `game/infrastructure/persistence/redis/`：拆分 `lua_scripts.go` / `lua_game.go`，新增 `scripts/` 子包，`repository.go` 调用方改用 `scripts.X.Run`
  - `game/infrastructure/persistence/redis/robot_scheduler.go`：内联脚本抽离
  - `settlement/service/lua_scripts.go` → `settlement/infrastructure/persistence/redis/virtual_balance.lua.go`：脚本下沉
  - `settlement/service/virtual_balance_service.go`：改为依赖 infrastructure 层接口
  - `game/domain/lua_errors.go`：提供 `LuaErrorCodes()` 反射接口供 preamble 生成
  - 新增 `*_test.go`：22 个脚本的 miniredis 单测
  - 配置：新增 `lua.use_evalsha` 开关（默认 true，遇问题秒级回退到 `Client.Eval`）

## ADDED Requirements

### Requirement: EVALSHA 调用层

系统 SHALL 通过 `common/redis.Script` 类型包装所有 Lua 脚本调用，自动复用 `goredis.NewScript` 的 SCRIPT LOAD + EVALSHA + NOSCRIPT fallback 机制，禁止业务代码直接调用 `Client.Eval`。

#### Scenario: 首次调用自动 SCRIPT LOAD

- **WHEN** 任一脚本首次在 Redis 实例上执行
- **THEN** go-redis 自动 `SCRIPT LOAD` 拿 sha，后续用 `EVALSHA`
- **AND** 遇 `NOSCRIPT` 错误时自动 fallback 到 `EVAL`，业务无感知

#### Scenario: Sentinel failover 后自动恢复

- **WHEN** Sentinel failover 切换到新 master（新 master 无 sha 缓存）
- **THEN** 首次 `EVALSHA` 返回 `NOSCRIPT`，go-redis 自动 fallback `EVAL` 并重新 LOAD，业务无感知

#### Scenario: 配置开关秒级回退

- **WHEN** 运维将 `lua.use_evalsha` 设置为 `false`
- **THEN** 所有 Lua 调用回退到旧 `Client.Eval` 路径
- **AND** 不需要重启服务

### Requirement: 脚本集中注册

系统 SHALL 在 `game/infrastructure/persistence/redis/scripts/registry.go` 集中注册全部 22 个脚本，每个脚本通过 `cRedis.NewScript(name, src)` 构造为包级 `var`，禁止业务文件内联 Lua 源码。

#### Scenario: 调用方引用脚本

- **WHEN** `repository.go` 需要执行 `LuaSelectSeat`
- **THEN** 通过 `scripts.SelectSeat.Run(ctx, r.client, keys, args...)` 调用
- **AND** 不再 import 任何 `LuaXxx` 字符串常量

### Requirement: DDD 层级合规

settlement-service 的 Lua 脚本源码 SHALL 下沉到 `settlement/infrastructure/persistence/redis/`，service 层通过 infrastructure 接口依赖，禁止 service 层直接持有 Lua 脚本字符串。

#### Scenario: VirtualBalanceService 调用扣减

- **WHEN** `VirtualBalanceService.Deduct` 触发扣减
- **THEN** 调用 `VirtualBalanceRedis.Deduct`（infrastructure 层接口方法）
- **AND** service 层文件不出现 `redis.call` / Lua 源码字符串

### Requirement: 可观测性指标

系统 SHALL 为每次 Lua 脚本执行记录耗时、状态（ok / business_error / redis_error）、脚本名，并提供 slow log（>100ms 告警）。

#### Scenario: 慢脚本告警

- **WHEN** 任一脚本执行耗时 > 100ms
- **THEN** 输出 `WARN` 日志，包含 script name / duration / key_count
- **AND** Prometheus 指标 `lua_execution_duration_seconds{script, status}` 记录本次耗时

### Requirement: 测试覆盖

系统 SHALL 为全部 22 个脚本提供 miniredis 单元测试，覆盖正常路径、幂等性、边界（空集合/超时/已抢）、并发（多 goroutine 同时调用）四类场景。

#### Scenario: 抢红包并发安全

- **WHEN** 100 个 goroutine 同时调用 `LuaGrabPacket` 抢同一红包
- **THEN** 抢到的总额等于红包总额（无超发无少发）
- **AND** 每次调用返回有效 code

## MODIFIED Requirements

### Requirement: LuaSettleRound 长脚本

`LuaSettleRound` SHALL 保留单脚本（原子性优先），但内部分块加注释（6 段职责），且 `finalResults` 排序逻辑移到 Go 侧。

#### Scenario: 游戏结束返回 finalResults

- **WHEN** `isGameEnd == 1`
- **THEN** Lua 返回 `totalsList` 原始数据
- **AND** Go 侧用 `sort.Slice` 按 total 降序排序后再返回上层
- **AND** 排序不涉及 Redis 写，不破坏原子性

## REMOVED Requirements

### Requirement: Redis Cluster hash tag 规划

**Reason**：用户确认当前采用 Sentinel 模型，Redis Cluster 迁移不在当前迭代范围（见 LUA_REFACTOR_PLAN.md 移除记录）。
**Migration**：未来如需迁移到 Redis Cluster，需独立项目推进 key 命名规范（引入 `{roomID}` hash tag）+ 数据迁移，本 spec 不预留相关接口。
