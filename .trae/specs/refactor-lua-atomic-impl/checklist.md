# Checklist

## P0 阶段验收

- [ ] `common/redis/script.go` 存在 `Script` struct，封装 `goredis.NewScript`，提供 `Name()` / `Run()` 方法
- [ ] `lua.use_evalsha` 配置开关生效：true 走 EVALSHA，false 回退 `Client.Eval`，无需重启
- [ ] `game/infrastructure/persistence/redis/scripts/registry.go` 集中注册 game 的 20 个脚本（包级 `var`）
- [ ] `lua_scripts.go`（原 781 行）已拆分为 `room_seat.lua.go` + `queue.lua.go`
- [ ] `lua_game.go`（原 739 行）已拆分为 `packet.lua.go` + `round.lua.go` + `penalty.lua.go`
- [ ] `robot_scheduler.go` 内联的 `releaseAssignLockScript` 已抽离到 `scripts/robot_lock.lua.go` 并注册
- [ ] `settlement/service/lua_scripts.go` 已删除，源码下沉到 `settlement/infrastructure/persistence/redis/virtual_balance.lua.go`
- [ ] `VirtualBalanceService` 依赖 `VirtualBalanceRedis` infrastructure 接口，service 层文件无 `redis.call` / Lua 源码字符串
- [ ] `repository.go` / `robot_scheduler.go` / settlement 调用方全部使用 `scripts.X.Run`，不再 `import` Lua 字符串常量
- [ ] `Script.Run` 包装 metrics（耗时 / status / script_name）+ slow log（>100ms WARN 日志）
- [ ] Prometheus 指标 `lua_execution_duration_seconds{script, status}` 已暴露
- [ ] `github.com/alicebob/miniredis/v2` 依赖已添加
- [ ] `scripts/` 包提供 `setupMiniRedis(t)` 测试 helper
- [ ] 22 个脚本均有 miniredis 单测，覆盖正常/边界/幂等/并发四类场景
- [ ] `LuaGrabPacket` 并发测试：100 goroutine 抢同一红包，总额守恒无超发
- [ ] `LuaSettleRound` 幂等性测试：重复调用返回相同结果，不重复写

## P1 阶段验收

- [ ] `domain/lua_errors.go` 暴露 `LuaErrorCodes()` 返回 `map[string]int`
- [ ] `NewScript` 自动把错误码常量表拼接到脚本头部
- [ ] Lua 脚本内不再出现裸数字错误码（如 `60`、`40`），改用 `ERR_NOT_PLAYER` 等命名常量
- [ ] preamble 注入 `packetInfoKey` / `packetAvailableKey` 等共享 key 构造函数
- [ ] `LuaGrabPacket` / `LuaRobotGrabPacket` / `LuaAutoDistributePackets` / `LuaSettleRound` 使用共享 key 函数，无重复拼接
- [ ] `LuaSettleRound` 内部有 6 段职责注释（幂等检查 / 用户红包映射 / 结果+minAmount / session 累计 / 回合状态 / finalResults）
- [ ] `LuaSettleRound` 不再调用 `table.sort`，返回 `totalsList` 原始数据
- [ ] Go 侧 `settleRound` 调用方用 `sort.Slice` 按 total 降序排序
- [ ] miniredis 测试验证排序行为与改造前一致

## P2 阶段验收

- [ ] `LuaHandlePenalty` 中 `penaltyCount` 变量已删除
- [ ] `common/redis/ttl.go` 定义 `DefaultRedisTTL = 86400`
- [ ] preamble 注入 `defaultTTL` 常量
- [ ] 15+ 处 `86400` 硬编码全部改为 `defaultTTL`
- [ ] `common/redis/script.go` 提供 `RunScript[T any]` 泛型方法解析 `{code, data, msg}` JSON
- [ ] 文档说明新脚本采用结构化返回，旧脚本不强制迁移

## P3 阶段验收

- [ ] `NewScript` 签名为 `NewScript(name, version, src)`
- [ ] registry.go 所有脚本注册时填版本号
- [ ] 服务启动日志包含所有脚本的 name + version + sha

## 全局验收

- [ ] `go test ./...` 全绿
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 无输出
- [ ] 灰度环境 Lua p99 延迟下降 ≥ 30%
- [ ] 灰度环境 5min 内无 `NOSCRIPT` 错误告警
- [ ] 每个脚本有头部文档（KEYS / ARGV / 返回值 / 错误码）
- [ ] `Client.Eval` 旧方法保留作为回退路径，未被删除
- [ ] 未引入 Redis Cluster hash tag 相关代码（用户已确认暂不考虑）
