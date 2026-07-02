# Tasks

## P0 阶段 — 性能/架构/可观测/测试基线

- [x] Task 1: 创建 `common/redis/script.go` EVALSHA 调用层
  - [x] SubTask 1.1: 新增 `Script` struct，封装 `goredis.NewScript`，提供 `Name()` / `Src()` / `Run(ctx, c, keys, args...)` 方法
  - [x] SubTask 1.2: 新增 `lua.use_evalsha` 配置开关（默认 true），false 时回退到 `Client.Eval`；`common/config` 新增 `LuaConfig.UseEvalSHA *bool`，运行时通过 `redis.SetUseEvalSHA` 切换
  - [x] SubTask 1.3: 在 `Client` 上保留 `Eval` 方法不删除，仅作为回退路径
- [x] Task 2: 创建 `game/infrastructure/persistence/redis/scripts/registry.go` 集中注册 game 的 20 个脚本
  - [x] SubTask 2.1: registry.go 已注册 20 个脚本（房间/座位 9 + 队列 3 + 红包 4 + 结算 2 + 惩罚 2）为包级 `var`
  - [x] SubTask 2.2: 每个脚本通过 `cRedis.NewScript(name, src)` 注册
  - **注**：Lua 字符串源码暂保留在父包 `lua_scripts.go` / `lua_game.go`，registry.go 通过 import 父包引用常量。当前父包未 import scripts，无成环。字符串源码迁移到 scripts 包延后到 Task 7（届时 application 层 5+ 文件的 `redis.LuaXxx` 引用一并迁移到 `scripts.X.Run`，避免提前破坏应用层编译）
- [x] Task 3: 拆分 `lua_scripts.go`（781 行）为 `room_seat.lua.go`（9 脚本）+ `queue.lua.go`（3 脚本）
  - [x] SubTask 3.1: 常量字符串迁移到 scripts 包内（`luaXxx` 小写包内私有），原父包 `lua_scripts.go` 已删除
  - [x] SubTask 3.2: registry.go 改为引用同包常量，删除父包 import
- [x] Task 4: 拆分 `lua_game.go`（739 行）为 `packet.lua.go`（4）+ `round.lua.go`（2）+ `penalty.lua.go`（2）
  - 已与 Task 3 一并完成，原父包 `lua_game.go` 已删除
- [x] Task 5: 抽离 `robot_scheduler.go` 内联 `releaseAssignLockScript` 到 `scripts/robot_lock.lua.go` 并注册
  - 已创建 `scripts/robot_lock.lua.go`（`luaReleaseAssignLock` 常量）
  - registry.go 已注册 `ReleaseAssignLock = cRedis.NewScript("release_assign_lock", luaReleaseAssignLock)`
  - `robot_scheduler.go` 内联 var 已删除，`ReleaseAssignLock` 方法改用 `scripts.ReleaseAssignLock.Run`，`goredis` import 已删除
- [x] Task 6: settlement 脚本 DDD 修正
  - [x] SubTask 6.1: 新建 `settlement/infrastructure/persistence/redis/virtual_balance.lua.go`，迁移 `luaDeductBalance` 源码
  - [x] SubTask 6.4: 通过 `cRedis.NewScript("deduct_balance", luaDeductBalance)` 注册 `DeductBalance` 实例，与 game 统一调用方式
  - [x] SubTask 6.3: `VirtualBalanceService.Deduct` 改用 `redis.DeductBalance.Run(ctx, s.redis, ...)`，service 层 `lua_scripts.go` 已删除
  - **注**：SubTask 6.2（新建 `VirtualBalanceRedis` struct + `Deduct` 方法）未单独实施——脚本实例 `DeductBalance` 直接作为包级 var 暴露，service 层直接调用，无需额外包装 struct，更简洁
- [x] Task 7: 改造所有调用方使用 `scripts.X.Run` 替代 `client.Eval`
  - [x] SubTask 7.1: `repository.go` 9 个调用点已改造（SelectSeat / CancelSeat / JoinAsSpectator / LeaveRoom / KickPlayer→scripts.KickPlayer / AutoSeatAndReady / Enqueue / Dequeue / AutoSubstitute）
  - [x] SubTask 7.2: `robot_scheduler.go` 1 个调用点已改造（见 Task 5）
  - [x] SubTask 7.3: settlement `virtual_balance_service.go` 1 个调用点已改造（见 Task 6）
  - [x] SubTask 7.4（额外）：5 个 application 文件 11 个调用点已改造
    - `grab_service.go`：GrabPacket / RobotGrabPacket / AutoDistributePackets / SendPacket
    - `penalty_service.go`：HandlePenalty / DistributePenalty
    - `seat_app_service.go`：PlayerReady / HandleSeatTimeout
    - `game_app_service.go`：TryStartGame / SettleRound / EndGame
- [ ] Task 8: 在 `Script.Run` 外层包装可观测性
  - [ ] SubTask 8.1: 记录耗时、status（ok/business_error/redis_error）、script_name 指标
  - [ ] SubTask 8.2: >100ms 输出 WARN 日志（script name / duration / key_count）
  - [ ] SubTask 8.3: 暴露 Prometheus 指标 `lua_execution_duration_seconds{script, status}`
- [ ] Task 9: 引入 miniredis 测试基础设施
  - [ ] SubTask 9.1: 添加 `github.com/alicebob/miniredis/v2` 依赖
  - [ ] SubTask 9.2: 在 `scripts/` 包提供 `setupMiniRedis(t)` 测试 helper
- [ ] Task 10: 为 22 个脚本编写 miniredis 单测
  - [ ] SubTask 10.1: 房间/座位类 9 脚本（SelectSeat / CancelSeat / JoinAsSpectator / LeaveRoom / KickPlayer / PlayerReady / HandleSeatTimeout / AutoSeatAndReady / TryStartGame）
  - [ ] SubTask 10.2: 队列/替补类 3 脚本（Enqueue / Dequeue / AutoSubstitute）
  - [ ] SubTask 10.3: 红包流程类 4 脚本（GrabPacket / RobotGrabPacket / AutoDistributePackets / SendPacket）
  - [ ] SubTask 10.4: 回合结算类 2 脚本（EndGame / SettleRound，含幂等性测试）
  - [ ] SubTask 10.5: 惩罚类 2 脚本（HandlePenalty / DistributePenalty）
  - [ ] SubTask 10.6: 机器人锁 1 脚本（releaseAssignLock）
  - [ ] SubTask 10.7: settlement 虚拟余额扣减 1 脚本（含并发不超发测试）

## P1 阶段 — 可维护性

- [ ] Task 11: 错误码 preamble 注入机制
  - [ ] SubTask 11.1: 在 `game/domain/lua_errors.go` 暴露 `LuaErrorCodes()` 返回 `map[string]int`（反射或手工维护）
  - [ ] SubTask 11.2: 在 `common/redis/script.go` 的 `NewScript` 中自动把常量表拼接到脚本头部
- [ ] Task 12: key builder 抽取到 preamble
  - [ ] SubTask 12.1: 在 preamble 注入 `packetInfoKey(prefix, id)` / `packetAvailableKey(prefix, id)` 等共享函数
  - [ ] SubTask 12.2: 改造 `LuaGrabPacket` / `LuaRobotGrabPacket` / `LuaAutoDistributePackets` / `LuaSettleRound` 使用共享函数
- [ ] Task 13: `LuaSettleRound` 长脚本分块 + 排序移到 Go 侧
  - [ ] SubTask 13.1: Lua 内加 6 段职责注释（幂等检查 / 用户红包映射 / 结果+minAmount / session 累计 / 回合状态 / finalResults）
  - [ ] SubTask 13.2: Lua 移除 `table.sort` 排序，返回 `totalsList` 原始数据
  - [ ] SubTask 13.3: Go 侧 `settleRound` 调用方用 `sort.Slice` 按 total 降序排序
  - [ ] SubTask 13.4: 补充 miniredis 测试验证排序行为不变

## P2 阶段 — 清理

- [ ] Task 14: 删除 `LuaHandlePenalty` 中未引用的 `penaltyCount` 变量
- [ ] Task 15: TTL 参数化
  - [ ] SubTask 15.1: 新建 `common/redis/ttl.go` 定义 `DefaultRedisTTL = 86400`
  - [ ] SubTask 15.2: preamble 注入 `defaultTTL` 常量
  - [ ] SubTask 15.3: 改造 15+ 处 `86400` 硬编码为 `defaultTTL`
- [ ] Task 16: 返回值结构化（仅新脚本采用）
  - [ ] SubTask 16.1: 在 `common/redis/script.go` 提供 `RunScript[T any]` 泛型方法解析 `{code, data, msg}` JSON
  - [ ] SubTask 16.2: 文档说明新脚本采用结构化返回，旧脚本仅在改动时迁移

## P3 阶段 — 版本管理

- [ ] Task 17: 脚本版本号管理
  - [ ] SubTask 17.1: `NewScript` 增加版本号参数：`NewScript(name, version, src)`
  - [ ] SubTask 17.2: registry.go 所有脚本注册时填版本号（首版统一 `v1.0`）
  - [ ] SubTask 17.3: 服务启动时打印所有脚本 name + version + sha 日志

## Task Dependencies

- Task 2 依赖 Task 1（registry 使用 Script 类型）
- Task 3、Task 4、Task 5 可与 Task 2 并行（仅是文件拆分）
- Task 6 依赖 Task 1（settlement 也用 Script 类型）
- Task 7 依赖 Task 2 + Task 3 + Task 4 + Task 5 + Task 6（所有脚本注册完成后改造调用方）
- Task 8 依赖 Task 1（在 Script.Run 上包装）
- Task 9 依赖 Task 1（测试需要 Script 类型）
- Task 10 依赖 Task 7 + Task 9（脚本注册完成 + 测试基础设施就绪后才能写测试）
- Task 11、Task 12、Task 13 依赖 Task 7（preamble 改造在调用层稳定后进行）
- Task 14、Task 15、Task 16 互相独立，可与 P1 任务并行
- Task 17 依赖 Task 1（NewScript 签名扩展）

## 验证策略

1. **单元测试**：每个脚本 miniredis 覆盖（Task 10）
2. **基准对比**：改造前后用 `go test -bench` 对比 `LuaGrabPacket` / `LuaSettleRound` 的 QPS 与延迟
3. **灰度**：先在 1 个实例启用 EVALSHA 路径，观察 1 天指标无异常后全量
4. **回退**：保留 `Client.Eval` 旧方法，配置开关 `lua.use_evalsha=true/false`，遇问题秒级回退

## 验收标准

- [ ] 所有 22 个脚本通过 miniredis 单测
- [ ] `go test ./...` 全绿
- [ ] `go vet ./...` 无警告
- [ ] 灰度环境 Lua p99 延迟下降 ≥ 30%
- [ ] 灰度环境 5min 内无 `NOSCRIPT` 错误告警
- [ ] scripts 包集中注册，settlement/service 层不再直接持有 Lua 源码
- [ ] 每个脚本有头部文档（KEYS/ARGV/返回值/错误码）
