# Verification Checklist

## Phase 1: P0 严重问题修复

### 资金安全
- [ ] robot_pool.go 的 SAdd 与 SIsMember 使用相同类型（string），IsAvailable 返回正确结果
- [ ] robot_scheduler.go 的 SAdd 与 SIsMember 使用相同类型，IsActiveRobot 返回正确结果
- [ ] virtual_balance.go 的 SAdd(robot set) 与 SIsMember 使用相同类型，IsRobot 返回正确结果
- [ ] virtual_balance.go Deduct 使用 Lua 脚本原子扣减，并发场景余额不变负
- [ ] Deduct 余额不足时返回 error，余额保持不变
- [ ] dirty 标记 SAdd 失败时记录日志

### 调度器正确性
- [ ] timeout_scheduler.go ClearRoomTimeouts 使用 `strings.HasPrefix(member, roomID+":")`
- [ ] roomID="12" 时不会误删 "123:..." 的 member

### 限流
- [ ] app.go 传入 UserLimiter 实例（非 nil）给 NewGRPCServer
- [ ] generic_service.go handleGrabPacket 在 userLimiter != nil 时调用 AllowGrab
- [ ] AllowGrab 返回 error 时 fail-open 放行并记录 Warn 日志
- [ ] AllowGrab 返回 allowed=false 时返回限流错误码

### 配置安全
- [ ] game.yaml 不含明文 root 密码（改为 ${MYSQL_PASSWORD} 或 ${MYSQL_DSN}）
- [ ] game.yaml 不含明文 merchant_secret（改为 ${MERCHANT_SECRET}）
- [ ] game.yaml 不含明文 nacos password（改为 ${NACOS_PASSWORD}）
- [ ] game.yaml 不含明文 redis password（改为 ${REDIS_PASSWORD}）
- [ ] config 加载逻辑支持环境变量替换
- [ ] 环境变量缺失时启动失败并明确报错

### Model 标签
- [ ] model/round.go RoundGrabRecord.Amount 标签为 `json:"amount" gorm:"not null"`
- [ ] model/round.go RoundGrabRecord.IsMin 标签为 `json:"is_min" gorm:"default:0"`
- [ ] model/round.go RoundGrabRecord.IsAutoAssigned 标签为 `json:"is_auto_assigned" gorm:"default:0"`
- [ ] model/session.go GameSession.RoomFee 标签为 `json:"room_fee" gorm:"not null"`
- [ ] model/session.go GameSession.MaxRounds 标签为 `json:"max_rounds" gorm:"not null"`
- [ ] model/session.go GameSession.PlayerCount 标签为 `json:"player_count" gorm:"not null"`
- [ ] JSON 序列化字段名不含分号或空格
- [ ] gorm 建表时 not null 与 default 约束生效

### PacketGenerator
- [ ] packet_generator.go generateNormalPackets 在 PacketCount=1 时 amounts[0]=req.TotalAmount
- [ ] PacketCount=1 时 ValidatePackets 返回 true
- [ ] leopard.go Generate 入口校验 PacketCount<=0 返回 error
- [ ] leopard.go Generate 入口校验 TotalAmount<=0 返回 error
- [ ] straight.go Generate 入口校验 PacketCount<=0 返回 error
- [ ] straight.go Generate 入口校验 TotalAmount<=0 返回 error
- [ ] 直接调用 Generator 传入 PacketCount=0 不触发 panic

## Phase 2: P1 资金与一致性

- [ ] lua_game.go 不再调用 math.random / math.randomseed
- [ ] Lua 脚本通过 ARGV 接收随机数
- [ ] Go 侧 Eval 调用时预生成随机数传入 ARGV
- [ ] lua_game.go LuaDistributePenalty 的 shareAmount * #remainingPlayers + remainder == penaltyAmount
- [ ] remainder 分配给前 remainder 个玩家（每人 shareAmount+1）
- [ ] game_event_consumer.go handleRoundSettle 中 SettleRound 失败返回 error 触发回滚
- [ ] game_event_consumer.go SettleGame 失败返回 error 触发回滚
- [ ] game_event_consumer.go update session player total_profit 失败返回 error 触发回滚
- [ ] domain/repository.go Broadcaster 接口 Broadcast 返回 error
- [ ] domain/repository.go Broadcaster 接口 BroadcastToUser 返回 error
- [ ] infrastructure/broadcast/broadcaster.go 实现返回 error
- [ ] 广播失败时调用方记录 Warn 日志
- [ ] room_event_consumer.go ParseRoomEvent 失败返回 error（非 nil）
- [ ] room_event_consumer.go default 分支返回 error（非 nil）
- [ ] game_event_consumer.go 幂等检查使用 SetNX（非 Exists+Set）
- [ ] room_event_consumer.go 幂等检查使用 SetNX
- [ ] SetNX 失败（已处理）跳过业务执行
- [ ] SetNX error 时返回 error 让 Kafka 重试
- [ ] game_event_publisher.go TraceID 包含 UUID 或纳秒时间戳，保证同房间同秒唯一

## Phase 3: P1 并发安全

- [ ] packet_generator.go config 字段使用 atomic.Pointer 保护
- [ ] packet_generator.go straightGenerator/leopardGenerator/rewardController 随快照替换
- [ ] UpdateConfig 构建新快照后 atomic Store
- [ ] Generate/validateRequest/calculateDynamicMinAmount/ValidatePackets 使用 atomic Load 读取
- [ ] 移除冗余的 rngMu 锁
- [ ] go test -race ./game/algorithm/... 无竞态报告
- [ ] db_repository.go 各子 repo 使用 sync.Once 初始化
- [ ] 并发调用 RoomDBRepo() 返回同一实例
- [ ] robot_scheduler_service.go Start 加锁检查 started 标志
- [ ] 重复调用 Start 被拒绝（不启动新 goroutine）
- [ ] Stop 重置 started 标志
- [ ] convertAlgorithmConfig 用指针/Optional 区分未设置与显式 0
- [ ] 显式设为 0 的概率/金额配置生效（不被默认值覆盖）
- [ ] reward_controller.go checkGuarantee 使用 SetNX
- [ ] SetNX 成功才触发保底奖励
- [ ] 区分 redis.Nil 与其他 error
- [ ] packet_generator.go crand.Int 错误被检查
- [ ] crand.Int 失败时不 panic（返回 error 或 fallback）
- [ ] robot_behavior.go grab 场景从 parts[4] 读 retryCount（非 parseRetryFromEnd）
- [ ] retryCount 解析正确（不等于 roundID）

## Phase 4: P1 Redis 与 Lua

- [ ] lua_scripts.go 长局（>24h）数据不丢失（TTL 刷新或移除）
- [ ] repository.go JoinAsSpectator 类型断言加 ok 检查
- [ ] repository.go SelectSeat 类型断言加 ok 检查
- [ ] repository.go CancelSeat 类型断言加 ok 检查
- [ ] 类型断言失败返回 error 而非 panic
- [ ] virtual_balance.go SyncToDB 使用 SPOP 弹出成员
- [ ] SyncToDB 失败成员重新 SAdd 回 dirtyKey
- [ ] SyncToDB 不再使用 SMembers+Del 模式
- [ ] robot_scheduler.go ReleaseAssignLock 使用 Lua 校验持有者
- [ ] AcquireAssignLock 保存 token 用于释放校验
- [ ] 非持有者调用 ReleaseAssignLock 不会误删锁
- [ ] user_repository.go CreateOrUpdateUser 使用 DoUpdates
- [ ] 已存在用户的 nickname/avatar 等字段被更新
- [ ] room_event_publisher.go producer.Send 的 key 为 event.RoomID
- [ ] 同房间事件落入同一分区

## Phase 5: P1 调度器与服务稳定性

- [ ] timeout_scheduler.go handler goroutine 计入 WaitGroup
- [ ] handler 调用包装 defer recover
- [ ] handler panic 时记录日志（含 roomID/data）
- [ ] Stop 等待 handler WaitGroup（带超时）
- [ ] virtual_balance_sync.go run 循环有 defer recover
- [ ] virtual_balance_sync.go Stop 调用 Wait（带超时）
- [ ] virtual_balance_sync.go interval<=0 时设默认值（不 panic）
- [ ] timeout_scheduler.go Redis 调用用 context.WithTimeout 包裹
- [ ] generic_service.go GracefulStop 设置超时（30s）
- [ ] GracefulStop 超时后调用 Stop() 强制关闭
- [ ] generic_service.go 12+ 处 json.Unmarshal 错误被检查
- [ ] json.Unmarshal 失败返回 CodeInvalidParams
- [ ] generic_service.go Start() Serve 失败时返回 error
- [ ] 增加 grpc.UnaryInterceptor（recovery 至少）
- [ ] 内部错误 err.Error() 不直接返回客户端
- [ ] game_app_service.go endGameWithOptions 失败记录 Error 日志
- [ ] endGameWithOptions 失败触发告警
- [ ] app.go Stop 顺序：grpcServer.GracefulStop(超时) → cancel → Container.Stop(超时) → 关闭 kafka/redis
- [ ] app.go Start 失败时调用 Stop 清理资源
- [ ] container.go Stop 设置整体超时
- [ ] container.go Kafka 消费者纳入生命周期管理（WaitGroup）
- [ ] container.go 校验 SyncInterval<=0 设默认值

## Phase 6: 整体验证

- [ ] go build ./... 编译通过
- [ ] go vet ./... 无警告
- [ ] go test -race ./... 现有测试通过
- [ ] gofmt -l 检查无格式问题
- [ ] GAME_OPTIMIZATION_REVIEW.md 中 P0 所有问题（C-01 至 C-22 除 H-37）已修复
- [ ] GAME_OPTIMIZATION_REVIEW.md 中 P1 所有问题已修复
- [ ] H-37（Lua 跨 hash slot）明确不修复（Sentinel 模式不触发）
