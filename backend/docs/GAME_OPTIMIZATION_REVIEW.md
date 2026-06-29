# Backend Game 代码审查与优化文档

> 审查范围：`packet/backend/game/` 下全部代码（algorithm / application / domain / infrastructure / scheduler / server / bootstrap / model），以及 `cmd/game/main.go` 与 `config/game.yaml`、`config/algorithm.yaml`
>
> 审查时间：2026-06-29
>
> 审查文件数：60+ Go 文件 + 2 配置文件
>
> 问题总计：严重 22 / 高 60+ / 中 80+ / 低 50+

---

## 一、执行摘要（Executive Summary）

本次审查覆盖 backend game 服务从入口、引导、配置、领域模型、应用服务、基础设施（Redis/MySQL/Kafka）、调度器到 gRPC 服务层的完整链路。整体架构清晰（DDD 分层 + 仓储模式 + 事件驱动），但在以下五个维度存在系统性风险，建议按优先级治理：

1. **资金安全（最高优先级）**：虚拟余额扣减非原子、Lua 脚本内使用 `math.random` 破坏主从一致性、惩罚分配整除丢失资金、扣款与状态更新跨多个存储无事务边界。
2. **数据一致性**：广播错误被吞、Kafka 消费幂等存在 TOCTOU、结算失败不回滚事务、TraceID 秒级冲突导致事件被误判去重丢弃。
3. **并发安全**：`PacketGenerator` 配置热更新与生成并发读写未加锁、`db_repository.go` 懒初始化非线程安全、`robot_pool / robot_scheduler / virtual_balance` 中 `SAdd` 传 int64 与 `SIsMember` 传 string 类型不一致导致查询永远返回 false。
4. **Redis Cluster 兼容性（远期备注，当前不处理）**：Lua 脚本动态拼接 key 跨 hash slot。当前部署使用 Sentinel 模式（见 `config/redis-sentinel-example.yaml`），不触发该问题；仅在远期迁移到 Redis Cluster 时需要处理，暂不纳入修复路线图。
5. **可维护性**：硬编码魔法值散落（"0" / "cashparty" / 5 / 30s / 0.8）、常量跨包重复定义、错误被 `_` 忽略遍布、goroutine 滥用 `context.Background()` 丢失取消信号。

**P0 立即修复**：`virtual_balance.go` 扣减非原子、`robot_pool.go` 类型不一致、`game.yaml` 明文密码、`generic_service.go` 限流器传 nil、`ClearRoomTimeouts` 前缀匹配误删。

**P1 一周内修复**：Lua `math.random`、消费幂等 TOCTOU、`PacketGenerator` 竞态、广播错误吞没、结算事务边界。

**P2 迭代治理**：函数拆分、常量抽取、测试补全、可观测性增强、配置环境化。

---

## 二、严重问题清单（P0 / Critical）

### 2.1 资金安全

#### C-01 虚拟余额扣减非原子，余额可变负且无回滚
- 文件：[virtual_balance.go](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/virtual_balance.go#L45-L58)
- 问题：`Deduct` 先执行 `IncrBy(ctx, key, -amount)` 再检查 `newBalance < 0`，余额已变负但未回滚；并发场景下多请求都会变负。`SAdd dirtyKey` 在 `IncrBy` 之后执行，若 `SAdd` 失败则 DB 永不会同步此次变更。
- 修复：改用 Lua 脚本 `local bal = tonumber(GET key); if bal < amount then return -1 end; return INCRBY key -amount` 原子完成；dirty 标记写入失败时回滚扣减或重试。

#### C-02 Lua 脚本使用 math.random 破坏主从一致性
- 文件：[lua_game.go](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_game.go#L124)
- 问题：Redis Lua 脚本必须确定性以支持主从复制和 AOF 持久化。`math.randomseed(now)` + `math.random` 在副本上得到不同结果，导致主从数据不一致；`now` 为秒/毫秒级可预测。
- 修复：在 Go 侧预生成随机数通过 `ARGV` 传入，或使用 `RANDOMKEY`。

#### C-03 惩罚分配整除丢失资金
- 文件：[lua_game.go](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_game.go#L736)
- 问题：`shareAmount = math.floor(penaltyAmount / #remainingPlayers)`，向下取整后 `shareAmount * #remainingPlayers` 小于 `penaltyAmount`，差额资金丢失。
- 修复：记录 remainder 并分配给前 remainder 个玩家，或归入系统账户。

#### C-04 扣款成功后 Round 记录更新失败无回滚
- 文件：[game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L1492-L1695)
- 问题：`initFirstRoundAndDeduct` / `initSystemResumeRoundAndDeduct` / `initPlayerRoundAndDeduct` 中，扣款成功后 `UpdateRoundDeductSuccess`、`UpdateRoundSender`、`UpdateRoundAmount` 错误仅日志记录，扣款已发生但 round 状态可能不一致，无法对账。
- 修复：扣款与 round 记录更新放入同一事务，或引入对账补偿任务。

#### C-05 PenaltyService Lua 成功但扣款失败状态不可回滚
- 文件：[penalty_service.go](file:///e:/demo/party/packet/backend/game/application/penalty_service.go#L35-L106)
- 问题：Lua 先增加 penalty count/amount（Redis 状态变更），随后 `DeductPenaltyToPlatform` 扣款失败时 Redis 状态无法回滚，下次 penalty count 累加错误值。
- 修复：扣款成功后再提交 Redis 状态，或提供回滚 Lua。

### 2.2 数据丢失与一致性

#### C-06 game_event_consumer 结算失败不回滚事务
- 文件：[game_event_consumer.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go#L234-L363)
- 问题：在 `db.Transaction` 内调用 `settlementService.SettleRound` / `SettleGame`，失败时仅日志、**不返回 error**，事务仍提交，round 状态为已结算但资金未变动。`update session player total_profit failed` 同样仅日志不回滚。
- 修复：结算失败作为事务回滚条件；或移出事务后用 Saga 补偿。

#### C-07 broadcaster 完全吞掉错误
- 文件：[broadcaster.go](file:///e:/demo/party/packet/backend/game/infrastructure/broadcast/broadcaster.go#L24-L29)
- 问题：`_ = b.broadcaster.Broadcast(...)`、`_ = b.broadcaster.BroadcastToUser(...)` 完全吞掉 error，业务层以为通知已送达，用户收不到结算/踢人等关键事件。
- 修复：接口返回 error，调用方决定降级；至少记录日志与告警。

#### C-08 room_event_consumer 解析失败直接 ACK 丢消息
- 文件：[room_event_consumer.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/room_event_consumer.go#L36-L38)
- 问题：`ParseRoomEvent` 失败时 `return nil` 直接 ACK，格式错误的事件永久丢失。default 分支同样直接 ACK。
- 修复：返回 error 让 Kafka 重试，或写入死信队列。

#### C-09 消费幂等存在 TOCTOU 竞态
- 文件：[game_event_consumer.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go#L446-L461)
- 问题：`isProcessed` 检查与 `markProcessed` 标记非原子，Kafka 重平衡时同一 traceID 可能被并发消费，导致重复创建 session/round/grab_record。`isProcessed` 中 `Exists` 错误被忽略，Redis 故障时返回 false 重复处理。
- 修复：用 `SET NX` 原子抢占标记，或依赖数据库唯一约束。

#### C-10 game_event_publisher TraceID 秒级冲突
- 文件：[game_event_publisher.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_publisher.go#L49)
- 问题：`event.TraceID = fmt.Sprintf("evt_%s_%d", event.RoomID, event.Timestamp)`，Timestamp 为秒级，同一秒内同房间多事件生成相同 TraceID，后发事件被 consumer 误判为已处理而丢弃。
- 修复：使用 UUID 或纳秒级时间戳。

### 2.3 并发安全

#### C-11 PacketGenerator 配置热更新与生成并发读写未加锁
- 文件：[packet_generator.go](file:///e:/demo/party/packet/backend/game/algorithm/packet_generator.go#L273-L280)
- 问题：`UpdateConfig` 在 `g.rngMu.Lock()` 下替换 `g.config` / `g.straightGenerator` / `g.leopardGenerator` / `g.rewardController`，但 `Generate`、`validateRequest`、`calculateDynamicMinAmount`、`ValidatePackets` 读取这些字段均未持锁。`go test -race` 会报错，可能读到半更新指针导致 panic。
- 修复：使用 `atomic.Pointer[Config]` 或不可变快照，`UpdateConfig` 整体替换指针。

#### C-12 db_repository 懒初始化非线程安全
- 文件：[db_repository.go](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/db_repository.go#L24-L64)
- 问题：`if r.roomRepo == nil { r.roomRepo = NewGormRoomRepository(r.db) }` 在并发调用下创建多个实例，破坏事务隔离。
- 修复：使用 `sync.Once` 或构造函数中一次性初始化。

#### C-13 robot_pool / robot_scheduler / virtual_balance SAdd 与 SIsMember 类型不一致
- 文件：[robot_pool.go](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/robot_pool.go#L28) vs [robot_pool.go#L43](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/robot_pool.go#L43)、[robot_scheduler.go#L49](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/robot_scheduler.go#L49) vs [robot_scheduler.go#L122](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/robot_scheduler.go#L122)、[virtual_balance.go#L125](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/virtual_balance.go#L125) vs [virtual_balance.go#L129](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/virtual_balance.go#L129)
- 问题：`SAdd` 传 int64，`SIsMember` 传 `fmt.Sprintf("%d", userID)` string。go-redis 序列化方式不同，**`IsAvailable` / `IsActiveRobot` / `IsRobot` 永远返回 false**，机器人池/调度器/虚拟余额判断全部失效。
- 修复：统一类型（建议都用 string 或都用 int64）。

#### C-14 robot_scheduler_service.Start 重复调用导致 goroutine 泄漏
- 文件：[robot_scheduler_service.go](file:///e:/demo/party/packet/backend/game/application/robot_scheduler_service.go#L77-L81)
- 问题：`s.ctx, s.cancel = context.WithCancel(...)` 每次 Start 调用都覆盖，之前的 scanLoop goroutine 无法停止。Start 未检查是否已启动。
- 修复：加锁检查 `s.cancel != nil`，已启动则拒绝。

### 2.4 安全与配置

#### C-15 game.yaml 明文硬编码 root 密码与商户密钥
- 文件：[game.yaml](file:///e:/demo/party/packet/backend/config/game.yaml#L27)
- 问题：`dsn: "root:123456@tcp(...)"` root 用户 + 弱口令明文提交；`merchant_secret: "aca5d11a-e481-4163-9505-194564558ae3"` 明文。仓库泄露即被攻破。
- 修复：密码/密钥改为环境变量或 KMS 注入，使用最小权限业务账号。

#### C-16 generic_service 限流器被传 nil，抢红包限流未生效
- 文件：[app.go](file:///e:/demo/party/packet/backend/game/bootstrap/app.go#L207) 与 [generic_service.go#L322](file:///e:/demo/party/packet/backend/game/server/generic_service.go#L322)
- 问题：`server.NewGRPCServer(..., nil, ...)` 硬编码 nil，`handleGrabPacket` 中 `if s.userLimiter != nil` 永远跳过限流。抢红包是高频高耗接口，无限流易被打满。
- 修复：从容器取 `UserLimiter` 并传入；同时 `allowed, _ := s.userLimiter.AllowGrab(...)` 应处理 err（Redis 故障时 fail-closed 全部拒绝，应改 fail-open 或降级）。

#### C-17 convertAlgorithmConfig 用 > 0 判断会覆盖合法的 0 值
- 文件：[app.go](file:///e:/demo/party/packet/backend/game/bootstrap/app.go#L299-L331)
- 问题：`if cfg.MinPacketAmount > 0`、`if cfg.StraightProbability > 0`、`if cfg.LeopardProbability > 0`。运维显式设为 0（如禁止顺子）会被忽略而采用默认值，Nacos 热更新路径同样使用此函数。
- 修复：用指针类型或显式 "是否设置" 标志区分"未设置"与"显式 0"。

#### C-18 model/round.go 与 model/session.go gorm/json 标签错误
- 文件：[round.go](file:///e:/demo/party/packet/backend/game/model/round.go#L45-L47) 与 [session.go](file:///e:/demo/party/packet/backend/game/model/session.go#L19-L23)
- 问题：`` `json:"amount;not null"` ``、`` `json:"is_min;default:0"` ``、`` `json:"room_fee;not null"` `` 等把 gorm 约束错写进 json tag。后果：JSON 字段名变成 `amount;not null` 含分号与空格，前端无法解析；gorm 完全丢失 `not null` 与 `default` 约束。
- 修复：改为 `` `json:"amount" gorm:"not null"` `` 等。

### 2.5 调度器与超时

#### C-19 ClearRoomTimeouts 前缀匹配误删其他房间超时
- 文件：[timeout_scheduler.go](file:///e:/demo/party/packet/backend/game/scheduler/timeout_scheduler.go#L158-L172)
- 问题：member 格式为 `roomID:data`，但 `member[:len(roomID)] == roomID` 未校验冒号分隔符。roomID="12" 时会误删 "123:abc"、"124:xyz" 等其他房间的超时。
- 修复：改为 `strings.HasPrefix(member, roomID+":")`。

#### C-20 generateNormalPackets 在 PacketCount=1 时丢失金额
- 文件：[packet_generator.go](file:///e:/demo/party/packet/backend/game/algorithm/packet_generator.go#L127-L169)
- 问题：当 `req.PacketCount == 1` 时进入 `else` 分支 `amounts[0] = minAmount`（约 `totalAmount/3` 量级），而非 `req.TotalAmount`。单包金额远小于总金额，`req.TotalAmount - minAmount` 凭空消失，`ValidatePackets` 也会返回 false。`validateRequest` 允许 `PacketCount=1`，该路径可达。
- 修复：`else { amounts[0] = req.TotalAmount }`。

#### C-21 LeopardGenerator / StraightGenerator 除零 panic
- 文件：[leopard.go](file:///e:/demo/party/packet/backend/game/algorithm/leopard.go#L16) 与 [straight.go](file:///e:/demo/party/packet/backend/game/algorithm/straight.go#L19-L30)
- 问题：`req.TotalAmount % int64(req.PacketCount)` 与 `(totalYuan - minIntSum) / n` 未校验 PacketCount=0。Generator 是公开导出的，可被外部直接实例化调用，绕过 `PacketGenerator.validateRequest`。
- 修复：方法开头校验 `req.PacketCount <= 0` 与 `req.TotalAmount <= 0` 返回 error。

#### C-22 reward_controller checkGuarantee TOCTOU 竞态重复触发奖励
- 文件：[reward_controller.go](file:///e:/demo/party/packet/backend/game/algorithm/reward_controller.go#L78-L105)
- 问题：`Get(straightKey)` 判断 `== 0` 再 `Set(straightKey, 1, ...)`，两个并发请求可能同时读到 0 都触发顺子奖励。Redis Get 错误被吞，故障时误认为"未触发"再次触发。
- 修复：用 `SetNX` 原子抢占，成功才触发；区分 `redis.Nil` 与其他错误。

---

## 三、高优先级问题清单（P1 / High）

### 3.1 算法层

| ID | 文件 | 问题摘要 |
|---|---|---|
| H-01 | [straight.go#L5](file:///e:/demo/party/packet/backend/game/algorithm/straight.go#L5) | 使用 `math/rand` + `time.Now().UnixNano()` 种子，可预测，与 packet_generator 使用 crypto/rand 不一致 |
| H-02 | [straight.go#L42-L56](file:///e:/demo/party/packet/backend/game/algorithm/straight.go#L42-L56) | 生成结果未 shuffle，按整数位递增排列，外部观察者易识别"递增=顺子局"泄露奖励节奏 |
| H-03 | [straight.go#L21](file:///e:/demo/party/packet/backend/game/algorithm/straight.go#L21) | `(1+n)*n/2` 与 `*100` 在 n/startInt 较大时 int64 溢出，导致金额错误 |
| H-04 | [packet_generator.go#L213](file:///e:/demo/party/packet/backend/game/algorithm/packet_generator.go#L213) | `crand.Int` 错误被忽略（`n, _ := ...`），系统熵耗尽时 `n` 为 nil，`n.Int64()` panic |
| H-05 | [packet_generator.go#L52-L60](file:///e:/demo/party/packet/backend/game/algorithm/packet_generator.go#L52-L60) | guarantee 回退结果被缓存，后续同 RoundID 请求都拿到"无奖励"，且 `straightKey` 已置 1 不再补发 |
| H-06 | [packet_generator.go#L142-L156](file:///e:/demo/party/packet/backend/game/algorithm/packet_generator.go#L142-L156) | `maxAmount` 可能为负，`randomRange(min, negative)` 返回 min，最终 `tempAmounts[last] = tempRemaining` 为负金额红包 |
| H-07 | [packet_generator.go#L207-L240](file:///e:/demo/party/packet/backend/game/algorithm/packet_generator.go#L207-L240) | `crypto/rand.Reader` 本身并发安全，再加 `rngMu` 冗余，热路径每包都加解锁+系统调用，性能差 |
| H-08 | [reward_controller.go#L139-L152](file:///e:/demo/party/packet/backend/game/algorithm/reward_controller.go#L139-L152) | `LeopardProbability + StraightProbability > 1` 时顺子有效概率被压缩，且配置侧无校验 |
| H-09 | [reward_controller.go#L155](file:///e:/demo/party/packet/backend/game/algorithm/reward_controller.go#L155) | `time.Now().Format("2006-01-02")` 使用服务器本地时区，跨日利润统计错位 |
| H-10 | [reward_controller.go#L111-L122](file:///e:/demo/party/packet/backend/game/algorithm/reward_controller.go#L111-L122) | `shouldTriggerGuarantee` 概率 `1/remainingRounds`，remainingRounds=10 时约 35% session 不会触发保底，名不副实 |
| H-11 | [leopard.go#L31](file:///e:/demo/party/packet/backend/game/algorithm/leopard.go#L31) | 奖励 10 倍硬编码且 int64 溢出风险，与顺子 1 倍形成魔法数字差异，不可配置 |
| H-12 | [straight_test.go](file:///e:/demo/party/packet/backend/game/algorithm/straight_test.go) | 测试覆盖严重不足：缺 PacketCount=0/1、TotalAmount<minAmount、溢出、ctx 取消用例；最小金额断言 `< 1` 过弱（应 `< 100`）；Leopard/PacketGenerator/RewardController 无任何测试 |

### 3.2 应用服务层

| ID | 文件 | 问题摘要 |
|---|---|---|
| H-13 | [game_app_service.go#L838](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L838) | `go s.endGameWithOptions(context.Background(), ...)` 异步结束游戏，error 完全丢弃，Lua 失败则游戏永久卡死 |
| H-14 | [game_app_service.go#L846-L1078](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L846-L1078) | `settleRound` 函数 232 行、6 层嵌套，圈复杂度极高，难测试维护 |
| H-15 | [game_app_service.go#L1389-L1417](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L1389-L1417) | `publishPacketCreatedEvent` 循环内 N+1 Redis `Get`，应用 MGet/Pipeline |
| H-16 | [game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go) | 大量 `_` 忽略错误（200/375/420/467/608/850/1144/1221/1383 行），出错时静默用零值 |
| H-17 | [game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go) | 11+ 处 `go s.xxx(context.Background(), ...)` 丢失原 ctx 超时/取消信号 |
| H-18 | [history_service.go#L62-L184](file:///e:/demo/party/packet/backend/game/application/history_service.go#L62-L184) | `GetPlayerSessionDetail` 122 行，串行 6 次 DB 查询无并行，延迟叠加 |
| H-19 | [history_service.go#L203](file:///e:/demo/party/packet/backend/game/application/history_service.go#L203) | `avgProfit = int64(float64(...) / float64(...))` 浮点转 int64 精度丢失，应整数除法 |
| H-20 | [robot_account_service.go#L72-L144](file:///e:/demo/party/packet/backend/game/application/robot_account_service.go#L72-L144) | `BatchCreateRobotsWithBalance` 无事务，中途失败留下脏数据；机器人 ID 从 1 开始，重复调用冲突 |
| H-21 | [robot_account_service.go#L64](file:///e:/demo/party/packet/backend/game/application/robot_account_service.go#L64) | `balanceRequired := int64(roomFee/5 + roomFee*9)` 魔法公式重复 3 次，含义不明 |
| H-22 | [robot_account_service.go#L152](file:///e:/demo/party/packet/backend/game/application/robot_account_service.go#L152) | `GetAvailableRobots(ctx, roomFee, roomFee)` 传相同 min/max，疑似 bug，应传范围 |
| H-23 | [robot_behavior.go#L118-L143](file:///e:/demo/party/packet/backend/game/application/robot_behavior.go#L118-L143) | `parseRetryFromEnd` 把 roundID（数字）误判为 retryCount，retry 逻辑完全错误 |
| H-24 | [robot_behavior.go#L164-L167](file:///e:/demo/party/packet/backend/game/application/robot_behavior.go#L164-L167) | send/leave 失败不重试，机器人永久卡在房间 |
| H-25 | [robot_player.go#L173-L176](file:///e:/demo/party/packet/backend/game/application/robot_player.go#L173-L176) | `_, _ = p.seatAppService.CancelSeat(...)` 完全忽略错误，座位残留 |
| H-26 | [robot_scheduler_service.go#L112](file:///e:/demo/party/packet/backend/game/application/robot_scheduler_service.go#L112) | `scanRooms` 用 `context.Background()` 无超时，单房间阻塞卡死整个 scan |
| H-27 | [robot_scheduler_service.go#L185-L235](file:///e:/demo/party/packet/backend/game/application/robot_scheduler_service.go#L185-L235) | `assignRobotsToRoom` 串行循环，每个机器人多次 Redis 往返，needed=5 时延迟叠加 |
| H-28 | [robot_scheduler_service.go](file:///e:/demo/party/packet/backend/game/application/robot_scheduler_service.go) | 多处错误被忽略（160/166/170/210-211/256-282/299-301/387-408） |
| H-29 | [room_app_service.go#L171-L199](file:///e:/demo/party/packet/backend/game/application/room_app_service.go#L171-L199) | `JoinAndAutoSeat` GetUserById/AutoSeatAndReady 失败时 `return joinResult, nil`，调用方以为成功 |
| H-30 | [room_app_service.go#L283-L316](file:///e:/demo/party/packet/backend/game/application/room_app_service.go#L283-L316) | `tryAutoSubstitute` 余额校验 N+1 且非原子，校验通过后用户可能已被替补 |
| H-31 | [seat_app_service.go#L86-L88](file:///e:/demo/party/packet/backend/game/application/seat_app_service.go#L86-L88) | `SelectSeat` 每次查 DB 判断 isRobot，N+1，未走缓存；GetUserById 错误时机器人被当真人 |
| H-32 | [seat_app_service.go#L209-L329](file:///e:/demo/party/packet/backend/game/application/seat_app_service.go#L209-L329) | `SetReady` 120 行混合 7 类职责 |
| H-33 | [seat_app_service.go#L251](file:///e:/demo/party/packet/backend/game/application/seat_app_service.go#L251) | Lua 成功码不一致：此处 `code==1`，grab_service/penalty_service 为 `code==0` |
| H-34 | [user_service.go#L113-L123](file:///e:/demo/party/packet/backend/game/application/user_service.go#L113-L123) | `SetUserIsRobot` 只 Del `UserByIdKey` 未 Del `UserByUserIDKey`，30 分钟内 is_robot 不一致 |
| H-35 | [user_service.go#L126-L157](file:///e:/demo/party/packet/backend/game/application/user_service.go#L126-L157) | `GetPendingCredit` 多次 Redis `.Val()` 吞错误，故障时返回 0，客户端误以为"无待入账" |
| H-36 | [user_service.go](file:///e:/demo/party/packet/backend/game/application/user_service.go) | `redis.Get().Val()` 无法区分"key 不存在"与"Redis 故障"，故障时多查 DB |

### 3.3 基础设施层

| ID | 文件 | 问题摘要 |
|---|---|---|
| H-37（远期备注，当前不修复） | [lua_game.go#L40-L41](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_game.go#L40-L41) | Lua 动态拼接 key（`keyPrefix .. ':packet:info:' .. packetID`）违反 Redis Cluster hash slot 要求。**当前 Sentinel 模式不触发，仅远期迁移 Cluster 时需处理** |
| H-38 | [lua_game.go#L281-L293](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_game.go#L281-L293) | `scenario==4`（resume_interrupt）仅检查 playerCount，未校验 senderID 合法性，任意玩家可借恢复名义发红包 |
| H-39 | [lua_game.go#L612-L617](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_game.go#L612-L617) | `if roundNo >= maxRounds`，若 maxRounds=0 被显式写入，第一轮就误判游戏结束 |
| H-40 | [lua_scripts.go#L62-L64](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_scripts.go#L62-L64) | `EXPIRE ... 86400`（24h），长局（>24h）数据被 Redis 自动删除 |
| H-41 | [lua_scripts.go#L273](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/lua_scripts.go#L273) | 错误码 32（player_already_sent_packet）未在 `lua_errors.go` / `errors.go` 中映射，落入 default 返回 SystemError |
| H-42 | [repository.go#L251-L253](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/repository.go#L251-L253) | `result[1].(string)` 类型断言无 ok 检查，类型变化时直接 panic |
| H-43 | [repository.go#L116-L132](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/repository.go#L116-L132) | `json.Unmarshal` 失败 `continue` 静默跳过，调用方拿到的列表少人无感知 |
| H-44 | [repository.go#L478-L509](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/repository.go#L478-L509) | `GetQueueList` N+1 查询，每个队列成员单独 `HGet`，应 `HMGet` |
| H-45 | [repository.go#L567-L606](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/repository.go#L567-L606) | `ResetRoomForNextGame` 先 `HGetAll` 读再 pipeline 写，非原子，期间玩家状态变化丢失 |
| H-46 | [virtual_balance.go#L94-L121](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/virtual_balance.go#L94-L121) | `SyncToDB` 非原子：`SMembers` + 同步 + `Del`，同步期间新成员会被 `Del` 删除丢失；循环 N+1；失败成员被 `continue` 后 `Del` 永久丢失 |
| H-47 | [robot_pool.go#L13](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/robot_pool.go#L13) | key 前缀 `"robot:..."` 缺少 `cashparty:` 前缀，与 keys.go 规范不一致，多服务共用 Redis 冲突 |
| H-48 | [robot_scheduler.go#L86-L88](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/redis/robot_scheduler.go#L86-L88) | `ReleaseAssignLock` 直接 `Del`，未校验持有者，TTL 到期后误删他人锁 |
| H-49 | [history_repository.go#L22-L67](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/history_repository.go#L22-L67) | 所有方法 `r.db.Raw(...)` 未 `WithContext(ctx)`，无法传递超时 |
| H-50 | [robot_account_repo.go#L66-L71](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/robot_account_repo.go#L66-L71) | `CreateInBatches(accounts, len(accounts))` batch size 等于总数，等于一次性插入，触发 `max_allowed_packet` |
| H-51 | [robot_account_repo.go#L44-L48](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/robot_account_repo.go#L44-L48) | `UpdateBalance` 直接覆盖更新，与 virtual_balance 并发扣款结合产生覆盖 |
| H-52 | [room_repository.go#L128-L145](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/room_repository.go#L128-L145) | `MatchRoomByBalance` 竞态：多请求匹配同一 room，后续都加入超 max_players；`ORDER BY (max_players - player_count)` 计算列无法走索引 |
| H-53 | [session_repository.go#L114-L138](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/session_repository.go#L114-L138) | `BatchUpdateSessionPlayerStats` N+1 且 `Updates(map{...绝对值})` 非原子 |
| H-54 | [user_repository.go#L20-L25](file:///e:/demo/party/packet/backend/game/infrastructure/persistence/mysql/user_repository.go#L20-L25) | `CreateOrUpdateUser` 用 `OnConflict{DoNothing: true}`，名为 CreateOrUpdate 实际只 Create 不更新 |
| H-55 | [room_event_publisher.go#L29](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/room_event_publisher.go#L29) | `producer.Send(ctx, topic, nil, data)` key 为 nil，同房间事件落不同分区被并发消费，事件乱序 |
| H-56 | [room_event_consumer.go#L87-L102](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/room_event_consumer.go#L87-L102) | `syncRoomCounts` 先 HLen 读 Redis 再 UpdateRoom 写 DB 非原子，期间玩家进出导致计数不一致 |

### 3.4 调度器 / 服务 / 引导

| ID | 文件 | 问题摘要 |
|---|---|---|
| H-57 | [timeout_scheduler.go#L236-L239](file:///e:/demo/party/packet/backend/game/scheduler/timeout_scheduler.go#L236-L239) | handler 以无界 goroutine 启动，无并发上限/WaitGroup/recover，大量超时同时触发产生 goroutine 泄漏 |
| H-58 | [timeout_scheduler.go#L121-L125](file:///e:/demo/party/packet/backend/game/scheduler/timeout_scheduler.go#L121-L125) | `Stop()` 不等待 handler goroutine，Stop 返回后 handler 仍可能访问即将关闭的 Redis，use-after-close |
| H-59 | [virtual_balance_sync.go#L33-L57](file:///e:/demo/party/packet/backend/game/scheduler/virtual_balance_sync.go#L33-L57) | `Stop()` 不 Wait，run 循环无 panic recover，SyncToDB panic 后永久停止同步；`interval=0` 时 `NewTicker(0)` panic |
| H-60 | [generic_service.go#L712-L715](file:///e:/demo/party/packet/backend/game/server/generic_service.go#L712-L715) | `GracefulStop` 无超时，handler 卡住永久阻塞，靠 SIGKILL 强杀 |
| H-61 | [generic_service.go](file:///e:/demo/party/packet/backend/game/server/generic_service.go) | 12+ 处 `json.Unmarshal(req.Data, &data)` 错误被忽略，畸形 JSON 得零值结构静默走错分支 |
| H-62 | [generic_service.go#L695-L710](file:///e:/demo/party/packet/backend/game/server/generic_service.go#L695-L710) | `Start()` 中 `Serve` 异步执行仅记日志，启动失败时 `Start()` 已返回 nil，应用以为服务在跑 |
| H-63 | [generic_service.go](file:///e:/demo/party/packet/backend/game/server/generic_service.go) | 无任何拦截器（recovery/logging/metrics/auth），各 handler 各自处理易遗漏 |
| H-64 | [app.go#L248-L274](file:///e:/demo/party/packet/backend/game/bootstrap/app.go#L248-L274) | `Stop()` 关闭顺序不合理：先 Container.Stop（停调度器）再 grpcServer.Stop，应先停接受新请求再关依赖 |
| H-65 | [app.go#L240-L242](file:///e:/demo/party/packet/backend/game/bootstrap/app.go#L240-L242) | `Start()` 失败时不清理已启动资源（调度器、消费者 goroutine） |
| H-66 | [container.go#L113-L119](file:///e:/demo/party/packet/backend/game/bootstrap/container.go#L113-L119) | Robot 超时时长未通过配置传入，始终用默认 5s；`SyncInterval=0` 时 NewTicker panic |
| H-67 | [container.go#L358-L383](file:///e:/demo/party/packet/backend/game/bootstrap/container.go#L358-L383) | `Stop()` 无超时，串行 8 个 scheduler.Stop，任一卡住整体卡住 |
| H-68 | [container.go#L358-L383](file:///e:/demo/party/packet/backend/game/bootstrap/container.go#L358-L383) | Kafka 消费者未在 Container.Stop 中停止/Wait |
| H-69 | [game.yaml#L4](file:///e:/demo/party/packet/backend/config/game.yaml#L4) | `mode: "debug"` 默认开启；Redis 无密码；`provider: "mock"`；日志 debug 级；Nacos 弱口令 "nacos" |
| H-70 | [game.yaml#L71-L72](file:///e:/demo/party/packet/backend/config/game.yaml#L71-L72) | `service_addr: "127.0.0.1"` 写死本机，容器/多机部署下其他服务无法调用 |
| H-71 | [algorithm.yaml#L9](file:///e:/demo/party/packet/backend/config/algorithm.yaml#L9) | 硬编码具体房间 ID `1776078870944425019`，换环境后失效 |

---

## 四、中优先级问题清单（P2 / Medium，分类汇总）

### 4.1 代码质量

- **函数过长**：`settleRound`(232行)、`SetReady`(120行)、`GetPlayerSessionDetail`(122行)、`JoinAndAutoSeat`(86行)、`BatchCreateRobotsWithBalance`(72行)、`BuildFullRoomState`(92行)、`CancelSeat`(60行)
- **重复代码**：`sessionID fallback` 重复 5 次；`randomDelay` 在 robot_behavior/robot_player/robot_scheduler 重复 3 次；`OnGameEnd` 与 `recycleRoomRobots` 逻辑完全相同；history_repository 4 个方法 COALESCE 表达式重复
- **常量跨包重复**：`RewardType*` / `TriggerType*` 在 `algorithm/model.go`、`algorithm/reward_controller.go`、`model/reward.go` 三处定义，且 model 包缺失 `RewardTypeNone=0`
- **gofmt 未执行**：history_dto.go、room_app_service.go、user_service.go 字段缩进不一致
- **死代码**：`penalty_service.policy` 字段从未使用；`grab_service.GetAvailablePacketID` 已被 RobotGrabPacket 取代；`robot_scheduler_service.randomDelay` 无调用；`ClearAllTimeouts` 与 `ClearAllRoomTimeouts` 完全重复

### 4.2 硬编码魔法值

| 值 | 含义 | 出现位置 |
|---|---|---|
| `"0"` | 系统 UserID | game_app_service.go 10+ 处 |
| `"cashparty"` | AppName / Key 前缀 | game_app_service.go、keys.go、robot_scheduler_service.go |
| `5` | MaxPlayers | robot_player.go、room_state.go、lua_scripts.go |
| `10s` / `30s` | Lock 超时 | game_app_service.go 4 处 |
| `0.8` | scan 时间预算比例 | robot_scheduler_service.go |
| `86400` (24h) | Redis TTL | lua_scripts.go、repository.go 多处 |
| `3` | 倒计时秒数 | lua_scripts.go 3 处、room_app_service.go |
| `30*time.Minute` | 缓存 TTL | user_service.go 4 处 |
| `"system"` | SenderType | game_event_consumer.go |
| `2` | RoomStatusPlaying | user_service.go |

### 4.3 错误处理

- 50+ 处 `_ = ...` 或 `xxx, _ := ...` 吞掉 Redis/DB/JSON 错误
- `fmt.Sscanf(val, "%d", &result)` 忽略返回 error（game_event_consumer.go parseInt64）
- `json.Marshal` 错误被忽略（user_service.go、generic_service.go）
- `redis.Get().Val()` 无法区分 `redis.Nil` 与故障（user_service.go 4 处）
- 内部错误 `err.Error()` 直接返回客户端（generic_service.go L397）

### 4.4 并发与锁

- `RegisterHandler` 非并发安全（timeout_scheduler.go）
- `Container` 字段无并发保护（container.go）
- `InitAppServices` 非幂等，重复调用重建 AppService 但旧 scheduler 持有旧 handler
- `VirtualBalanceSync.Start` 非幂等，多次调用启动多个 run goroutine
- `ReleaseAssignLock` 直接 Del 未校验持有者
- `MatchRoomByBalance` 竞态

### 4.5 Redis / Lua

- `keys.go` `keyPrefix = "cashparty"` 硬编码，多环境共享 Redis 无法隔离
- 缺少 key 版本管理（如 `:v1:`），schema 变更无法平滑迁移
- `lua_game.go` 多处 magic number 错误码（60/40/2 等）无注释
- `lua_scripts.go` `if isRobot == '1' or isRobot == 'true'` 字符串比较 boolean，脆弱
- `lua_scripts.go` `if startedAt ~= 0 and startedAt ~= '0'` 类型混淆
- `lua_scripts.go#L443` `if status == 0 then return {0, 0, 'room not found'}` status 0 是 Idle 合法状态，语义错误
- `robot_pool.GetAvailableRobots` ParseInt 失败 `continue` 静默丢失

### 4.6 MySQL / 仓储

- `HistoryDBRepository` 接口所有方法缺少 `ctx context.Context`
- `RoomRepository` 接口 29 个方法违反 ISP
- `UpdateRoom` 用 `map[string]interface{}` 丧失类型安全
- `DeleteRoom` 硬删除无软删除支持
- `CreateRound` 无 `OnConflict` 幂等保护
- `UpdateRoundFailed` 未检查 round 当前状态
- `EndSession` 无幂等检查
- `SetUserIsRobot` 未检查 RowsAffected
- `UpdateBalance` 无乐观锁

### 4.7 消息与事件

- `game_event_publisher.publish` 无重试机制，Kafka 短暂故障事件丢失
- `room_event_publisher` 无 TraceID/Timestamp 注入
- `room_event_consumer` TTL `24*time.Hour` 硬编码，Kafka 积压超 24h 重复处理
- `game_event_consumer.handlePacketCreated` 未做幂等检查
- `game_event_consumer` 在事务内调用跨服务 `settlementService.SettleRound`，事务嵌套 + 连接耗尽风险
- `game_event_consumer.handleSessionStart` `FirstOrCreate` 不会更新已存在记录的 Nickname/Avatar/SeatNo
- `game_event_consumer.handleRoundSettle` 循环 N+1 创建 grab_record 与更新 session_player

### 4.8 配置与可观测性

- 缺少 metrics/pprof/健康检查端点配置
- 缺少优雅关闭超时配置 `shutdown_timeout`
- 缺少 dev/staging/prod 差异化覆盖机制
- 全链路无 TLS（Redis/MySQL/Kafka/平台/Nacos）
- `algorithm.yaml` 缺少 schema 版本与字段单位注释
- `packet_generator.go` `traceID := req.RoundID` 无独立追踪 ID
- `reward_controller.go` 大量 `logger.Info` 在高频路径刷屏，应降 Debug
- 调度器无 metrics（队列深度、处理延迟、handler 耗时）

### 4.9 测试覆盖

- 整个 game 模块仅 `straight_test.go` 一个测试文件
- LeopardGenerator / PacketGenerator / RewardController 无测试
- 关键资金逻辑（Lua 脚本、扣款、惩罚分配）无回归保护
- `straight_test.go` 最小金额断言 `< 1` 过弱（应 `< 100`）；`if !hasDecimal { t.Logf(...) }` 仅日志不断言失败
- 缺少并发场景测试（race detector 未覆盖）

---

## 五、修复优先级与路线图

### P0 立即修复（1-3 天）

| 顺序 | 问题 ID | 修复内容 | 影响 |
|---|---|---|---|
| 1 | C-13 | 统一 robot_pool/robot_scheduler/virtual_balance 的 SAdd/SIsMember 类型 | 机器人池判断全部失效，影响整个机器人系统 |
| 2 | C-01 | virtual_balance.Deduct 改用 Lua 原子扣减 | 资金安全，余额可变负 |
| 3 | C-19 | ClearRoomTimeouts 前缀匹配改 `strings.HasPrefix` | 误删其他房间超时 |
| 4 | C-16 | generic_service 传入 UserLimiter，处理 AllowGrab error | 抢红包无限流，易被打满 |
| 5 | C-15 | game.yaml 凭据移出，环境变量注入 | 安全风险 |
| 6 | C-18 | model/round.go、model/session.go gorm/json 标签修复 | JSON 字段名异常 + gorm 约束丢失 |
| 7 | C-20 | packet_generator PacketCount=1 时 amounts[0]=TotalAmount | 单包金额丢失 |
| 8 | C-21 | Leopard/Straight Generator 入口校验 PacketCount | 除零 panic |

### P1 一周内修复

| 顺序 | 问题 ID | 修复内容 |
|---|---|---|
| 1 | C-02 | Lua 移除 math.random，Go 侧预生成随机数通过 ARGV 传入 |
| 2 | C-03 | 惩罚分配 remainder 分配给前 remainder 个玩家 |
| 3 | C-06 | game_event_consumer 结算失败回滚事务 |
| 4 | C-07 | broadcaster 返回 error 或至少记录日志 |
| 5 | C-08 | room_event_consumer 解析失败返回 error 让 Kafka 重试 |
| 6 | C-09 | 消费幂等用 SET NX 原子抢占 |
| 7 | C-10 | TraceID 用 UUID 或纳秒时间戳 |
| 8 | C-11 | PacketGenerator 用 atomic.Pointer[Config] 不可变快照 |
| 9 | C-12 | db_repository 用 sync.Once 初始化 |
| 10 | C-14 | robot_scheduler_service.Start 加锁检查已启动 |
| 11 | C-17 | convertAlgorithmConfig 用指针区分未设置与显式 0 |
| 12 | C-22 | reward_controller checkGuarantee 用 SetNX 原子抢占 |
| 13 | H-04 | crand.Int 错误检查 |
| 14 | H-13 | endGameWithOptions error 上报监控 |
| 15 | H-23 | robot_behavior.parseRetryFromEnd grab 场景固定从 parts[4] 读 |
| 16 | H-40 | EXPIRE 改为每次操作刷新或移除 TTL |
| 17 | H-42 | repository 类型断言加 ok 检查 |
| 18 | H-46 | SyncToDB 用 SPOP 逐个弹出 |
| 19 | H-48 | ReleaseAssignLock 用 Lua 校验持有者 |
| 20 | H-54 | CreateOrUpdateUser 改用 DoUpdates |
| 21 | H-55 | room_event_publisher 用 event.RoomID 作为 key |
| 22 | H-57/58/59 | 调度器 handler 加 WaitGroup/recover/超时 ctx |
| 23 | H-60 | GracefulStop 加超时，先 Stop 兜底 |
| 24 | H-64/65/67/68 | bootstrap 关闭顺序与超时治理 |

> 注：H-37（Lua 跨 hash slot）因当前使用 Sentinel 模式不触发，移出 P1 路线图，仅在远期迁移 Redis Cluster 时处理。

### P2 一月内迭代

1. **代码质量**：拆分超长函数（settleRound/SetReady/GetPlayerSessionDetail/JoinAndAutoSeat/BuildFullRoomState）；抽取 `resolveSessionID`、`checkBalanceForReady`、`collectEmptySeats`、`calcBalanceRequired` 辅助函数；运行 gofmt 统一格式
2. **常量治理**：抽取 `game/common/enum` 公共包统一 RewardType/TriggerType；定义 `SystemUserID="0"`、`AppName="cashparty"`、`MaxPlayers=5` 常量；魔法数字移入配置或常量
3. **错误处理**：审计所有 `_` 忽略错误，区分"可忽略"与"必须处理"；统一 Lua 成功码约定；统一 user ID 类型；内部错误不外泄
4. **测试补全**：LeopardGenerator/PacketGenerator/RewardController 单元测试；Lua 脚本集成测试；资金扣减并发测试；race detector 全量跑通
5. **可观测性**：接入 prometheus 指标（队列深度、handler 耗时、Lua 失败率、消费延迟）；增加 pprof 与健康检查端点；日志降级（高频 Info → Debug）
6. **配置治理**：拆分 dev/staging/prod 配置；统一 key 前缀与版本；环境化基础设施地址；TLS 启用
7. **接口治理**：HistoryDBRepository 加 ctx；RoomRepository 按 ISP 拆分；UpdateRoom 改强类型；引入乐观锁（room/round/user balance）
8. **消息层**：game_event_consumer handlePacketCreated 幂等；handleRoundSettle 批量 CreateInBatches + CASE WHEN；结算移出事务用 Saga；引入死信队列与重试退避
9. **限流降级**：UserLimiter 错误 fail-open 策略；接入熔断器保护下游 Redis/MySQL
10. **Redis 治理**：key 前缀统一 `cashparty:`；引入 `:v1:` 版本；长局 TTL 改为业务侧清理。（Cluster hash tag 不做强制要求，维持 Sentinel 模式）

---

## 六、架构性建议

### 6.1 资金一致性方案

当前扣款（平台 RPC）、Redis 状态、DB 记录分散在三个存储无事务边界，建议引入 **Saga 模式 + 对账任务**：

1. 每个资金操作生成 `transaction_id`，持久化到 `fund_transaction` 表
2. 各步骤（扣款 / Redis 状态 / DB 记录）作为 Saga 子事务，记录状态机
3. 失败时按反向顺序补偿
4. 定时对账任务扫描 `pending` 状态事务，与平台/Redis/DB 三方核对

### 6.2 配置不可变快照

引入 `atomic.Pointer[Config]` 模式，`UpdateConfig` 整体替换指针，读取侧无锁。Config 内嵌的子生成器（straight/leopard/rewardController）随快照一起替换，避免半更新。

### 6.3 随机源统一

定义 `RandomProvider` 接口，提供 `Int63n` / `Float64` / `Shuffle`，默认实现用 `crypto/rand` + 预生成池（每 N 次批量生成一次）。生产用安全实现，测试可注入伪随机源做断言。

### 6.4 调度器统一时间轮

当前 6 个独立 `TimeoutScheduler` 各自轮询 Redis，建议引入 **统一时间轮 + Redis Keyspace Notifications**：

- 时间轮在内存管理到期回调，O(1) 检查
- Keyspace Notifications 作为兜底（进程重启后从 Redis 恢复）
- 单 checker goroutine 而非 6 个，降低 Redis 压力

### 6.5 消费者幂等与顺序

- 幂等：用 `SET NX traceID EX ttl` 原子抢占，成功才处理
- 顺序：producer 用 `roomID` 作为 key 保证同房间事件同分区
- 死信：处理失败 N 次后写入死信队列，人工介入
- 重试：指数退避（1s/2s/4s/8s/16s），最大重试 5 次

### 6.6 可观测性三件套

- **Metrics**：prometheus 暴露 Redis Lua 失败率、消费延迟、handler 耗时、队列深度、goroutine 数、连接池占用
- **Tracing**：接入 OpenTelemetry，trace_id 贯穿 HTTP → gRPC → Kafka → Redis/MySQL
- **Logging**：结构化日志（zap），高频路径 Debug 级，关键决策（结算/扣款/奖励触发）Info 级带 trace_id

---

## 七、附录：跨文件共性问题速查

### 7.1 错误吞没热点文件

| 文件 | 忽略错误处数 |
|---|---|
| game_app_service.go | 10+ |
| user_service.go | 8+ |
| seat_app_service.go | 5+ |
| robot_scheduler_service.go | 8+ |
| generic_service.go | 12+ |
| repository.go (redis) | 6+ |
| history_repository.go | 多处 |

### 7.2 goroutine 滥用 context.Background() 热点

| 文件 | 处数 |
|---|---|
| game_app_service.go | 11+ |
| room_app_service.go | 2+ |
| seat_app_service.go | 2+ |
| robot_behavior.go | 4+ |
| timeout_scheduler.go | 1（每超时一个） |
| virtual_balance_sync.go | 1 |

### 7.3 N+1 查询热点

| 位置 | 描述 |
|---|---|
| game_app_service.go#L1389 | publishPacketCreatedEvent 循环 Get |
| room_app_service.go#L283 | tryAutoSubstitute 循环 GetUserById + CheckBalance |
| seat_app_service.go#L86 | SelectSeat 每次查 DB |
| robot_account_service.go#L157 | GetAvailableRobot 循环 GetBalance |
| robot_scheduler_service.go#L185 | assignRobotsToRoom 串行循环 |
| repository.go#L478 | GetQueueList 循环 HGet |
| repository.go#L547 | SetAllPlayersOnline 循环 SavePlayer |
| session_repository.go#L114 | BatchUpdateSessionPlayerStats 循环 Updates |
| virtual_balance.go#L100 | SyncToDB 循环 UpdateBalance |
| game_event_consumer.go#L245 | handleRoundSettle 循环创建/更新 |
| lua_scripts.go#L702 | AutoSubstitute 循环 HGET |

### 7.4 关键缺失测试

| 模块 | 测试状态 |
|---|---|
| algorithm/leopard.go | 无测试 |
| algorithm/packet_generator.go | 无测试 |
| algorithm/reward_controller.go | 无测试 |
| algorithm/straight.go | 仅 3 组正常用例，缺边界/异常 |
| game/application/* | 无测试 |
| game/infrastructure/persistence/redis/* | 无测试 |
| game/infrastructure/messaging/* | 无测试 |
| game/scheduler/* | 无测试 |

---

**文档结束**

> 本文档基于 2026-06-29 代码快照生成。修复时请以最新代码为准，并在 PR 中引用问题 ID（如 `fixes C-13`）便于追溯。
