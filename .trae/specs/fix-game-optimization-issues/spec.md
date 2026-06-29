# Fix Game Optimization Issues Spec

## Why

[GAME_OPTIMIZATION_REVIEW.md](file:///e:/demo/party/packet/backend/docs/GAME_OPTIMIZATION_REVIEW.md) 审查发现 backend/game 服务存在 22 个严重问题和 70+ 个高优先级问题，涵盖资金安全、数据一致性、并发安全、错误处理、调度器稳定性五大维度。这些问题会导致：虚拟余额可变负、机器人池判断完全失效、抢红包无限流、明文凭据泄露、消费幂等竞态重复结算、goroutine 泄漏等生产事故。本 spec 落地 P0（立即修复）和 P1（一周内修复）路线图，确保系统资金安全与运行稳定。

## What Changes

### P0 严重问题修复（8 项）
- 修复 robot_pool/robot_scheduler/virtual_balance 的 SAdd(int64) 与 SIsMember(string) 类型不一致，统一为 string（用 converter.FormatID/ParseIDStrict，与 keys.go 主流约定一致，避免 snowflake 大 int64 跨语言精度风险）。注：经核实 go-redis 对 int64/string 序列化字节相同，当前功能正常，此修复属代码规范统一，非功能性 bug
- virtual_balance.Deduct 改用 Lua 脚本原子扣减，避免余额变负且无回滚
- timeout_scheduler.ClearRoomTimeouts 前缀匹配改用 `strings.HasPrefix(member, roomID+":")`，避免误删其他房间超时
- generic_service 传入 UserLimiter 实例，处理 AllowGrab error（Redis 故障时 fail-open），恢复抢红包限流
- game.yaml 移除明文 root 密码与商户密钥，改为环境变量注入 `${ENV_VAR}` 占位
- 修复 model/round.go、model/session.go 的 gorm/json 标签错误（`;not null` 错写入 json tag）
- packet_generator generateNormalPackets 在 PacketCount=1 时 amounts[0]=TotalAmount
- Leopard/Straight Generator 入口校验 PacketCount<=0 与 TotalAmount<=0 返回 error

### P1 高优先级修复（23 项）
- Lua 脚本移除 math.random/math.randomseed，Go 侧预生成随机数通过 ARGV 传入
- 惩罚分配 remainder 分配给前 remainder 个玩家，避免整除丢失资金
- game_event_consumer 结算失败作为事务回滚条件
- broadcaster.Broadcast/BroadcastToUser 返回 error 或至少记录日志告警
- room_event_consumer 解析失败返回 error 让 Kafka 重试，不直接 ACK 丢消息
- 消费幂等用 `SET NX traceID EX ttl` 原子抢占，替代 Exists+Set 的 TOCTOU
- game_event_publisher TraceID 改用 UUID 或纳秒时间戳，避免秒级冲突丢事件
- PacketGenerator 用 atomic.Pointer[Config] 不可变快照，UpdateConfig 整体替换指针
- db_repository 用 sync.Once 初始化各子 repo，保证并发安全
- robot_scheduler_service.Start 加锁检查已启动，避免 goroutine 泄漏
- convertAlgorithmConfig 用指针区分"未设置"与"显式 0"
- reward_controller.checkGuarantee 用 SetNX 原子抢占
- packet_generator crand.Int 错误检查，避免 nil 解引用 panic
- game_app_service endGameWithOptions error 记录日志并上报监控
- robot_behavior.parseRetryFromEnd grab 场景固定从 parts[4] 读 retryCount
- lua_scripts.go EXPIRE 改为每次操作刷新或移除 TTL，避免长局数据丢失
- redis/repository.go 类型断言加 ok 检查，避免 panic
- virtual_balance.SyncToDB 用 SPOP 逐个弹出，避免 Del 丢失新成员
- robot_scheduler.ReleaseAssignLock 用 Lua 校验持有者，避免误删他人锁
- user_repository.CreateOrUpdateUser 改用 DoUpdates 真正实现 upsert
- room_event_publisher 用 event.RoomID 作为 Kafka key，保证同房间事件顺序
- 调度器（timeout_scheduler/virtual_balance_sync）handler 加 WaitGroup/recover/超时 ctx
- generic_service.GracefulStop 加超时，先 GracefulStop 等待再 Stop 兜底
- bootstrap app.go/container.go 关闭顺序治理（先停 gRPC 再关依赖）+ 超时保护

### 明确不做
- H-37（Lua 跨 hash slot）：当前 Sentinel 模式不触发，远期迁移 Cluster 时再处理
- P2 中低优先级问题（函数拆分、常量治理、测试补全、可观测性等）：留待后续迭代

## Impact

- Affected specs: 无（首次针对 game 服务的修复 spec）
- Affected code:
  - `game/algorithm/`: packet_generator.go, reward_controller.go, leopard.go, straight.go, config.go
  - `game/application/`: game_app_service.go, robot_behavior.go, robot_scheduler_service.go
  - `game/domain/`: events.go（TraceID 生成）
  - `game/infrastructure/broadcast/`: broadcaster.go
  - `game/infrastructure/messaging/`: game_event_consumer.go, game_event_publisher.go, room_event_consumer.go, room_event_publisher.go
  - `game/infrastructure/persistence/mysql/`: db_repository.go, user_repository.go
  - `game/infrastructure/persistence/redis/`: repository.go, robot_pool.go, robot_scheduler.go, virtual_balance.go, lua_game.go, lua_scripts.go, keys.go
  - `game/scheduler/`: timeout_scheduler.go, virtual_balance_sync.go
  - `game/server/`: generic_service.go
  - `game/bootstrap/`: app.go, container.go
  - `game/model/`: round.go, session.go
  - `config/`: game.yaml

## ADDED Requirements

### Requirement: 资金扣减原子性
系统 SHALL 使用 Lua 脚本原子完成虚拟余额扣减（读取余额→校验充足→扣减），扣减失败时余额不变。

#### Scenario: 余额充足
- **WHEN** 并发调用 Deduct 且各请求金额之和不超过余额
- **THEN** 所有请求按到达顺序依次成功扣减，余额正确递减

#### Scenario: 余额不足
- **WHEN** 调用 Deduct 时余额小于扣减金额
- **THEN** 返回错误，余额保持不变（不变负）

### Requirement: 消费幂等原子性
系统 SHALL 使用 `SET NX traceID EX ttl` 原子操作标记消息已处理，避免 Exists+Set 的 TOCTOU 竞态。

#### Scenario: 并发消费同一 traceID
- **WHEN** Kafka 重平衡导致同一 traceID 被两个消费者并发拉取
- **THEN** 仅一个消费者 SET NX 成功并执行业务，另一个失败跳过

### Requirement: 抢红包限流
系统 SHALL 对抢红包接口应用 UserLimiter，Redis 故障时 fail-open（放行并告警）而非 fail-closed（全拒）。

#### Scenario: 正常限流
- **WHEN** 用户抢红包频率超过阈值
- **THEN** 超限请求被拒绝并返回限流错误码

#### Scenario: Redis 故障
- **WHEN** UserLimiter 调用 Redis 失败
- **THEN** 请求放行，记录 Warn 日志告警

### Requirement: 配置凭据安全
配置文件 SHALL NOT 包含明文密码或密钥，敏感字段 MUST 通过环境变量注入。

#### Scenario: 配置加载
- **WHEN** 应用启动读取 game.yaml
- **THEN** 密码/密钥字段从 `${ENV_VAR}` 占位符解析为环境变量值，缺失时启动失败并明确报错

## MODIFIED Requirements

### Requirement: Generator 入参校验
LeopardGenerator 和 StraightGenerator 的 Generate 方法 SHALL 在入口校验 `req.PacketCount <= 0` 与 `req.TotalAmount <= 0`，返回 error 而非 panic。

#### Scenario: PacketCount 为 0
- **WHEN** 直接调用 LeopardGenerator.Generate 传入 PacketCount=0
- **THEN** 返回 ErrCodeInvalidPacketCount 错误，不触发除零 panic

### Requirement: PacketGenerator 配置热更新
PacketGenerator SHALL 使用 atomic.Pointer[Config] 保护配置与子生成器引用，UpdateConfig 整体替换指针，Generate 等读取侧无锁访问快照。

#### Scenario: 并发更新与生成
- **WHEN** UpdateConfig 与 Generate 并发调用
- **THEN** Generate 读取到的是完整的旧配置或新配置，不会读到半更新状态

### Requirement: 优雅关闭
gRPC 服务 GracefulStop SHALL 设置超时（如 30s），超时后调用 Stop 强制关闭；bootstrap.Stop SHALL 先停止接受新请求（gRPC GracefulStop）再关闭依赖资源（调度器/Redis/Kafka），并设置整体超时。

#### Scenario: handler 阻塞
- **WHEN** 关闭时某 handler 阻塞
- **THEN** 30s 后强制 Stop 退出进程，不永久阻塞

### Requirement: 消息消费错误处理
room_event_consumer 解析消息失败 SHALL 返回 error 让 Kafka 重试，不直接 ACK 丢弃；game_event_consumer 结算失败 SHALL 作为事务回滚条件。

#### Scenario: 格式错误的消息
- **WHEN** 收到格式错误的事件消息
- **THEN** 返回 error，Kafka 重试，不永久丢失

#### Scenario: 结算失败
- **WHEN** settlementService.SettleRound 返回 error
- **THEN** 事务回滚，round 状态不变，Kafka 重试

## REMOVED Requirements

### Requirement: Lua math.random 随机性
**Reason**: Redis Lua 脚本必须确定性以支持主从复制和 AOF 持久化，math.random 破坏一致性。
**Migration**: 随机数改由 Go 侧预生成通过 ARGV 传入 Lua 脚本。
