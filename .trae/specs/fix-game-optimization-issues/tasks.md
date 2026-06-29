# Tasks

## Phase 1: P0 严重问题修复（资金安全 + 系统可用性）

- [x] Task 1: 修复 robot_pool/robot_scheduler/virtual_balance 类型不一致 (C-13)
  - [x] SubTask 1.1: robot_pool.go 统一 SAdd/SRem/SIsMember 用 `converter.FormatID`，GetAvailableRobots 用 `converter.ParseIDStrict`（解析失败记 Warn 日志不静默丢失）
  - [x] SubTask 1.2: robot_scheduler.go 统一 SAdd/SRem/SIsMember 用 `converter.FormatID`，GetRoomRobots 用 `converter.ParseIDStrict`
  - [x] SubTask 1.3: virtual_balance.go 统一 SAdd/SIsMember 用 `converter.FormatID`，SyncToDB 用 `converter.ParseIDStrict`
  - [x] SubTask 1.4: 编译验证通过（`go build ./game/...` 整模块 BUILD OK，gofmt 无格式问题）
  - 备注：经核实 go-redis 对 int64/string 序列化字节相同，原 C-13 "永远返回 false" 的功能性判断是误报；本次修复属代码规范统一（与 keys.go 主流 string 约定一致 + 复用 converter 包 + 避免 snowflake 大 int64 跨语言精度风险），无数据迁移需求

- [x] Task 2: virtual_balance.Deduct 改用 Lua 原子扣减 (C-01) + 删死代码 + 统一 key 规范 (H-47)
  - [x] SubTask 2.1: 新建 settlement/service/lua_scripts.go，定义 luaDeductBalance 脚本（原子执行 IncrBy-检查-回滚-SAdd dirty，返回 1=成功/0=余额不足）
  - [x] SubTask 2.2: settlement/virtual_balance_service.go Deduct 改用 Eval 执行 Lua 脚本，消除三步竞态
  - [x] SubTask 2.3: 删除 game/virtual_balance.go 中从未被调用的 Deduct 死代码（全 game 模块 grep 确认 0 调用）
  - [x] SubTask 2.4: game/keys.go 追加 9 个 robot key 常量（全部 `cashparty:robot:*` 前缀）+ 工厂函数
  - [x] SubTask 2.5: game/robot_pool.go、robot_scheduler.go、virtual_balance.go 删除文件内重复的 key 函数定义，全部改用 keys.go 工厂函数
  - [x] SubTask 2.6: settlement/virtual_balance_service.go 和 robot_checker.go 在本包内定义私有 key 常量（避免循环依赖），key 统一为 `cashparty:robot:*` 前缀
  - [x] SubTask 2.7: game/application/robot_scheduler_service.go 的 scanPattern 改为 `"cashparty:robot:room:*"`
  - [x] SubTask 2.8: scripts/init_robot_accounts.go 的清理 pattern 改为 `"cashparty:robot:*"`
  - [x] SubTask 2.9: 验证 `go build ./game/... ./settlement/...` 编译通过 + `go vet ./scripts/init_robot_accounts.go` 通过 + gofmt 无格式问题
  - 备注：数据迁移由运维侧执行 `redis-cli --scan --pattern 'robot:*' | xargs -I {} redis-cli rename {} cashparty:{}`，或上线后用 init_robot_accounts 脚本重新初始化（需评估机器人余额影响）

- [x] Task 3: 修复 ClearRoomTimeouts 前缀匹配误删 (C-19)
  - [x] SubTask 3.1: timeout_scheduler.go ClearRoomTimeouts 改用 `strings.HasPrefix(member, roomID+":")`
  - [x] SubTask 3.2: 追加 `strings` import
  - [x] SubTask 3.3: 验证 `go build ./game/...` 编译通过 + gofmt 无格式问题

- [x] Task 4: 恢复 generic_service 抢红包限流 (C-16)
  - [x] SubTask 4.1: container.go 追加 UserLimiter 字段 + `limiter.NewUserLimiter(redis)` 初始化；app.go 从 container 取 UserLimiter 传入 NewGRPCServer（替代 nil）
  - [x] SubTask 4.2: generic_service.go handleGrabPacket 处理 AllowGrab error：err!=nil 时 fail-open 放行并 Warn 日志
  - [x] SubTask 4.3: 验证 `go build ./game/... ./common/limiter/...` 编译通过；limiter 内部 redis.Eval 失败已 fail-open 返回 (true, nil)，generic_service 层再加 err 检查防御性兜底

- [ ] Task 5: game.yaml 凭据移出环境变量 (C-15)
  - [ ] SubTask 5.1: game.yaml dsn 改为 `${MYSQL_DSN}` 或拆分字段用 `${MYSQL_PASSWORD}`
  - [ ] SubTask 5.2: merchant_secret 改为 `${MERCHANT_SECRET}`
  - [ ] SubTask 5.3: nacos password 改为 `${NACOS_PASSWORD}`，redis password 改为 `${REDIS_PASSWORD}`
  - [ ] SubTask 5.4: 确认 config 加载逻辑支持环境变量替换（若不支持需补充解析逻辑）
  - [ ] SubTask 5.5: 文档记录所需环境变量清单

- [x] Task 6: 修复 model gorm/json 标签错误 (C-18)
  - [x] SubTask 6.1: model/round.go RoundGrabRecord 3 个字段标签修复（Amount/IsMin/IsAutoAssigned 把 gorm 约束从 json tag 剥离到独立 gorm tag）
  - [x] SubTask 6.2: model/session.go GameSession 3 个字段标签修复（RoomFee/MaxRounds/PlayerCount）
  - [x] SubTask 6.3: 全局 grep `json:"[^"]*;[^"]*"` 确认仅此 6 处，无其他类似错误
  - [x] SubTask 6.4: 验证 `go build ./game/...` 编译通过；DB 侧约束由用户后续统一清库重建，本次仅修代码层 tag

- [x] Task 7: 修复 packet_generator PacketCount=1 金额丢失 (C-20)
  - [x] SubTask 7.1: packet_generator.go generateNormalPackets 在 `req.PacketCount == 1` 分支 `amounts[0] = req.TotalAmount`（原代码 `amounts[0] = minAmount` 导致金额丢失，minAmount 远小于 totalAmount）
  - [x] SubTask 7.2: 补充 packet_generator_test.go 覆盖 PacketCount=1 场景（100/1/9999 分三种金额），通过 ValidatePackets 校验金额一致性

- [x] Task 8: Generator 入口校验 PacketCount/TotalAmount (C-21)
  - [x] SubTask 8.1: leopard.go Generate 入口校验 PacketCount<=0、TotalAmount<=0 返回 ErrCodeInvalidPacketCount error，避免后续除零 panic
  - [x] SubTask 8.2: straight.go Generate 入口校验 PacketCount<=0、TotalAmount<=0 返回 ErrCodeInvalidPacketCount error，避免 `(totalYuan - minIntSum) / n` 除零 panic
  - [x] SubTask 8.3: 补充边界测试用例（PacketCount=0/-1、TotalAmount=0/-1）覆盖 leopard 和 straight 两个生成器
  - 备注：生产链路 straight/leopard 仅由 packet_generator.go 内部调用，前面有 validateRequest 兜底，本次修复属防御性编程，保护 public 类型的直接调用场景（如测试、未来外部调用）

## Phase 2: P1 高优先级修复 - 资金与一致性

- [x] Task 9: Lua 移除 math.random，Go 侧预生成随机数 (C-02)
  - [x] SubTask 9.1: 识别 lua_game.go 中所有 math.random/math.randomseed 调用点（共 2 处：LuaRobotGrabPacket 第124-132行洗牌 + LuaAutoDistributePackets 第219行死代码 seed；LuaDistributePenalty 仅用 math.floor 不在范围）
  - [x] SubTask 9.2: LuaRobotGrabPacket 移除 math.randomseed/math.random 洗牌，改为接收 ARGV[6]=randOffset，用 `(randOffset + i) % count + 1` 轮询起点随机化；LuaAutoDistributePackets 移除无用的 math.randomseed 死代码
  - [x] SubTask 9.3: grab_service.go RobotGrabPacket 用 `rand.Intn(1000)` 预生成 randOffset 加入 ARGV（Lua 侧 % packetCount 取模，安全）
  - [x] SubTask 9.4: 验证 `go build ./game/...` 编译通过；修复后 Lua 脚本完全确定性（输入相同→输出相同），主从复制一致；随机性由 Go 侧 math/rand 提供（机器人选包场景，非密码学场景，足够）
  - 备注：LuaDistributePenalty 的 remainder 处理经 Task 10 核实为误报，资金分配由 settlement 侧正确处理

- [x] Task 10: 惩罚分配 remainder 处理 (C-03) — **经核实为误报，无需修复**
  - 核实结论：spec 描述"Lua 未处理 remainder 导致资金分配问题"不成立
  - 资金流追踪：game_app_service.go#L705 调用 DistributePenalty → Lua 仅返回 shareAmount（floor 值）用于广播显示 → game_app_service.go#L727 构造 distReq（Amount=meta.RoomFee，Recipients=dist.Recipients）→ settlement_service.go#L320 DistributePenaltyFromPlatform **独立计算并正确处理 remainder**（前 remainder 人分 shareAmount+1，总额 = req.Amount）
  - 关键点：Lua 的 shareAmount 不参与资金记账，仅用于 game_app_service.go#L744 广播 PenaltyShare；实际资金分配完全由 settlement 侧处理，已正确处理 remainder
  - 唯一小问题：广播显示的 shareAmount 是 floor 值，与实际分配（前 remainder 人 +1）差 1 分，属前端展示问题，修复需改 PushGameInterrupted 协议，超出本次范围
  - 边缘情况验证：recipients 为空 / penaltyAmount=0 / HKEYS 顺序随机 均无资金安全问题

- [x] Task 11: game_event_consumer 结算失败回滚事务 (C-06)
  - [x] SubTask 11.1: handleRoundSettle 中 SettleRound 失败返回 error 触发事务回滚（原代码只记日志吞 error）
  - [x] SubTask 11.2: handleSessionEnd 中 SettleGame 失败返回 error 回滚
  - [x] SubTask 11.3: handleSessionEnd update session player total_profit 失败返回 error 回滚
  - [x] SubTask 11.4: 防重试重复：grab_record 改用 FirstOrCreate（按 round_id+user_id 查重），重试时依赖 SettleRound 内部幂等（RoundStatusCredited 返回 nil）+ session 幂等检查 + SettleGame 内部幂等（allSettled 返回 nil）
  - 风险点已核实：SettleRound/SettleGame 用 settlement 侧独立 db 句柄开自己的事务，与 consumer 外层事务不共享连接；但 SettleRound/SettleGame 内部事务自己也会回滚，整体失败时两边都能回滚，重试安全
  - 幂等性核实：grab_count/total_grab/send_count/total_send 用 `gorm.Expr("... + 1")` 在 consumer 事务内，事务回滚后这些 +1 也被撤销，重试从原值开始，不存在 +2 问题
  - 发现新 bug（不在 Task 11 范围）：settlement_service.go L104-108 SettleReward 失败只记日志不 return err，creditRound 成功但 reward 未结算时 SettleRound 仍返回 nil，需单独修复

- [x] Task 11.5（新增）: SettleReward 失败吞 err 修复（Task 11 发现的衍生 bug）
  - [x] SubTask 11.5.1: settlement_service.go SettleRound 中 SettleReward 失败上抛 err（替换原 logger.Error 吞 err），触发 caller (game_event_consumer) 事务回滚
  - [x] SubTask 11.5.2: creditRound 不再在内部调用 UpdateRoundSettlementCredited/CreateBillsAndUpdateSettlement 标记 Credited；改为返回 (totalSettleAmount, settleUserCount, err) 供 SettleRound 在 credit + reward 全部成功后统一标记 Credited
  - [x] SubTask 11.5.3: bill_manager.go 删除 CreateBillsAndUpdateSettlement（创建 bill + 标 Credited 的耦合方法），新增纯写 bill 的 CreateBillsOnly
  - [x] SubTask 11.5.4: reward_settler.go SettleReward 入口加幂等检查（GetBillByRoundTypeAndUser 查平台支出 bill，已成功直接返回 nil），防止 Kafka 重试时 reward bills 重复创建
  - [x] SubTask 11.5.5: go build ./settlement/... + go vet ./settlement/... + go build ./game/... + go vet ./game/... 全部通过
  - 修复前：creditRound 标 Credited + caller 提交事务 → 重试早返回 nil，reward 永远丢失
  - 修复后：creditRound 写 bills（保留），reward err 上抛 → caller 事务回滚 → 重试时 settlement 非 Credited，重新进入 credit+reward；SettleReward 入口幂等检查防止重复创建 bills
  - 残留风险：reward 最终失败会留下"已写 grab bills 但未 Credited 且无 reward bills"的中间态，需后续接入 SettlementCheckService 巡检

- [x] Task 12: broadcaster 包装层记 Warn 日志（C-07，采用方案 B：spec 原文"返回 error 或至少记录日志告警"后半选项）
  - [x] SubTask 12.1（方案调整）: domain/repository.go Broadcaster 接口签名保持不变（不改 error 返回值）
  - [x] SubTask 12.2（方案调整）: infrastructure/broadcast/broadcaster.go 包装层替换 `_ =` 为 `if err := ...; err != nil { logger.Warn(...) }`，Warn 日志带 room_id/event/exclude_user_id/user_id 业务上下文
  - [x] SubTask 12.3（方案调整）: 35 处调用方零改动（domain 接口未变），由包装层统一处理 error
  - [x] SubTask 12.4: go build ./game/... + go vet ./game/... 通过；底层 KafkaBroadcaster/RedisPubSubBroadcaster 已在 Error 级别记发送失败，本层 Warn 补业务上下文，不阻塞主流程
  - 选择 Warn 而非 Error：广播失败是预期内可恢复故障（玩家可重连拉状态恢复），避免 Error 级别告警风暴

- [x] Task 13: room_event_consumer 解析失败不丢消息 (C-08) — **经核实为误报，无需修复**
  - 核实结论：consumer 唯一职责是 syncRoomCounts（Redis→DB 快照同步），幂等且最终一致
  - ParseRoomEvent 失败 return nil 正确：坏 JSON 重试无用，下次事件会刷新 DB
  - default 分支 return nil 正确：未处理的事件类型不影响 count，本就该跳过
  - DLQ 方案反而有害：旧消息重放会用旧 count 覆盖新 count，造成数据错误
  - 可选改进：加 metric 监控失败率（非 bug 修复，属可观测性增强，不在本 spec 范围）
  - 顺带清理死代码：删除从未被调用的 NewPlayerLeaveEvent / NewPlayerDisconnectEvent 函数、PlayerLeavePayload / PlayerDisconnectPayload struct、consumer 中对应的 case 分支与 handlePlayerLeave 方法
  - 保留 RoomEventPlayerLeave / RoomEventPlayerDisconnect 常量（iota 链中段，删除会改变后续常量值，破坏 Kafka 消息兼容性）
  - go build ./game/... + go vet ./game/... 通过

- [x] Task 14: 消费幂等用 SET NX 原子抢占 (C-09)
  - [x] SubTask 14.1: game_event_consumer 删除 isProcessed/markProcessed，新增 tryAcquire/releaseAcquire，HandleEvent 改用 SetNX 原子抢占 + 失败时 release（Del key）
  - [x] SubTask 14.2: room_event_consumer 同样改造，handleMessage 用 tryAcquire/releaseAcquire
  - [x] SubTask 14.3（方案调整）: SetNX 失败（已处理）跳过；SetNX error 时 fail-open 返回 true（不返回 error，因 Kafka consumer 不重试，返回 error 无意义，依赖业务侧幂等兜底——Task 11 已保证）
  - [x] SubTask 14.4: go build ./game/... + go vet ./game/... 通过；SetNX 原子性消除 Exists+Set 两步竞态窗口
  - 设计要点：成功后不再调 markProcessed（SetNX 已完成"检查+设置"）；失败时调 releaseAcquire 释放抢占，让 Kafka 重试能重新进入；TTL 保留原值（room_event 24h、game_event 7d）；Redis 故障 fail-open 与原 isProcessed 忽略 err 行为一致
  - 与 Task 11 协同：Task 14 是前置防御层（Kafka 消费幂等），Task 11 是后置兜底（业务侧幂等），双层防护

- [x] Task 15: TraceID 改用 UUID 避免秒级冲突 (C-10) — **经核实为误报，无需修复**
  - 核实结论：所有 4 处 game_event TraceID 生成（SessionStart/RoundSettle/SessionEnd/PacketCreated）均已用 `idgen.GenerateString()` snowflake ID，非秒级时间戳
  - snowflake ID 分析：毫秒级时间戳 + 10 bits 节点 ID + 12 bits 序列号，单节点每毫秒 4096 ID，多节点通过 NODE_ID 环境变量区分，无冲突可能
  - publisher 中 `fmt.Sprintf("evt_%s_%d", event.RoomID, event.Timestamp)` 是 fallback 兜底，实际所有 caller 都已设置 TraceID，永不执行
  - room_event 的 EventID 已用 `uuid.New().String()`，无冲突
  - Task 14 已用 SetNX 原子抢占，即使假设冲突也不会重复处理
  - 顺带清理死代码：删除 publisher 中永不执行的 fallback，改为 return error 强制 caller 设置 TraceID，避免未来新增 publisher 方法时误用
  - go build ./game/... + go vet ./game/... 通过

## Phase 3: P1 高优先级修复 - 并发安全

- [x] Task 16: PacketGenerator 用 atomic.Pointer[Config] (C-11)
  - [x] SubTask 16.1: packet_generator.go config/straightGenerator/leopardGenerator/rewardController 字段改为 atomic.Pointer[X] 包装
  - [x] SubTask 16.2: UpdateConfig 构建新快照后 atomic Store（4 个 Pointer 各自 Store，读侧 Load 拿到完整一致的一组指针）
  - [x] SubTask 16.3: Generate 入口 Load 一次快照传给 validateRequest/generateNormalPackets/calculateDynamicMinAmount；ValidatePackets 内 Load；保证一次 Generate 内 config 一致，避免 nacos 推送在执行过程中替换 config 导致校验/计算不一致
  - [x] SubTask 16.4: 移除 packet_generator.go 与 reward_controller.go 的 rngMu 锁（crypto/rand.Reader 本身并发安全）；randomInt/randomRange/shuffle/randomFloat 删除 Lock/Unlock
  - [x] SubTask 16.5: go build ./game/... + go vet ./game/... 通过；go test -race ./game/algorithm/... 通过
  - 设计要点：RewardController 内部 config 字段保持裸 *RewardControlConfig（构造后 immutable，整体 RewardController 对象被 atomic.Pointer 替换，无需内部再加锁）；多实例场景安全（atomic.Pointer 是单进程原语，nacos 推送延迟是分布式固有特性，Redis SetNX 保证同一 round 只在一个实例生成）

- [x] Task 17: db_repository 并发安全初始化 (C-12) — **方案偏差：用 eager init 替代 sync.Once**
  - [x] SubTask 17.1: DBRepositoryImpl 所有子 repo 字段在 NewDBRepository 构造时一次性初始化
  - [x] SubTask 17.2: RoomDBRepo/SessionDBRepo/UserDBRepo/RoomConfigDBRepo/RoundDBRepo/HistoryDBRepo 方法直接返回字段，删除 `if r.xxx == nil` lazy init
  - [x] SubTask 17.3: GormTransactionImpl 同样改造，新增 NewGormTransaction 构造函数，WithTransaction 用其初始化事务内子 repo
  - [x] 验证：go build ./game/... + go vet ./game/... 通过
  - 方案选择理由：所有 NewGormXxxRepository 均为纯内存构造（仅 set db 字段，无 IO 无副作用），eager init 零成本；构造后字段只读，天然并发安全，无锁无 race；代码比 sync.Once 更简洁（避免字段数 × 2 膨胀）；spec 中 sync.Once 只是建议，eager init 同样满足"保证并发安全"核心诉求

- [ ] Task 18: robot_scheduler_service.Start 加锁防重复 (C-14)
  - [ ] SubTask 18.1: robot_scheduler_service.go 加 mutex 和 started 标志
  - [ ] SubTask 18.2: Start 检查已启动则拒绝（返回 error 或 nil）
  - [ ] SubTask 18.3: Stop 时重置 started 标志

- [ ] Task 19: convertAlgorithmConfig 用指针区分 0 值 (C-17)
  - [ ] SubTask 19.1: config 结构体字段改为指针类型（*int64/*float64）或引入 Optional 包装
  - [ ] SubTask 19.2: app.go convertAlgorithmConfig 判断 nil 而非 > 0
  - [ ] SubTask 19.3: 验证显式设为 0 时配置生效

- [ ] Task 20: reward_controller.checkGuarantee 用 SetNX (C-22)
  - [ ] SubTask 20.1: reward_controller.go checkGuarantee 用 SetNX 替代 Get+Set
  - [ ] SubTask 20.2: SetNX 成功才触发保底，失败（已触发）跳过
  - [ ] SubTask 20.3: 区分 redis.Nil 与其他 error

- [ ] Task 21: packet_generator crand.Int 错误检查 (H-04)
  - [ ] SubTask 21.1: randomInt/randomRange/shuffle 中 crand.Int 错误检查，失败返回 error 或 fallback
  - [ ] SubTask 21.2: 验证系统熵耗尽时不 panic

- [ ] Task 22: robot_behavior.parseRetryFromEnd grab 场景修复 (H-23)
  - [ ] SubTask 22.1: robot_behavior.go grab 场景固定从 parts[4] 读 retryCount，不用通用 parseRetryFromEnd
  - [ ] SubTask 22.2: 验证 retryCount 解析正确

## Phase 4: P1 高优先级修复 - Redis 与 Lua

- [ ] Task 23: lua_scripts.go EXPIRE 改为刷新或移除 (H-40)
  - [ ] SubTask 23.1: 评估长局场景，EXPIRE 改为每次关键操作刷新 TTL
  - [ ] SubTask 23.2: 或移除 TTL 改为业务侧清理（room 销毁时 DEL）
  - [ ] SubTask 23.3: 验证长局（>24h）数据不丢失

- [ ] Task 24: redis/repository.go 类型断言加 ok 检查 (H-42)
  - [ ] SubTask 24.1: JoinAsSpectator/SelectSeat/CancelSeat 等处 `result[i].(type)` 加 ok 检查
  - [ ] SubTask 24.2: 断言失败返回 error 而非 panic
  - [ ] SubTask 24.3: 全局搜索其他无 ok 检查的类型断言并修复

- [ ] Task 25: virtual_balance.SyncToDB 用 SPOP 逐个弹出 (H-46)
  - [ ] SubTask 25.1: virtual_balance.go SyncToDB 用 SPOP 弹出成员而非 SMembers+Del
  - [ ] SubTask 25.2: 失败成员重新 SAdd 回 dirtyKey
  - [ ] SubTask 25.3: 批量 UpdateBalance 优化（可选，用 CASE WHEN 或事务）

- [ ] Task 26: robot_scheduler.ReleaseAssignLock 校验持有者 (H-48)
  - [ ] SubTask 26.1: robot_scheduler.go ReleaseAssignLock 用 Lua 脚本 `if GET key == value then DEL key end`
  - [ ] SubTask 26.2: AcquireAssignLock 保存 token（随机值）用于释放校验

- [ ] Task 27: user_repository.CreateOrUpdateUser 真正 upsert (H-54)
  - [ ] SubTask 27.1: user_repository.go OnConflict 改用 DoUpdates: clause.AssignmentColumns
  - [ ] SubTask 27.2: 验证已存在用户的 nickname/avatar 等字段被更新

- [ ] Task 28: room_event_publisher 用 event.RoomID 作为 key (H-55)
  - [ ] SubTask 28.1: room_event_publisher.go producer.Send 的 key 参数从 nil 改为 []byte(event.RoomID)
  - [ ] SubTask 28.2: 验证同房间事件落入同一分区顺序消费

## Phase 5: P1 高优先级修复 - 调度器与服务稳定性

- [ ] Task 29: 调度器 handler 加 WaitGroup/recover/超时 (H-57/58/59)
  - [ ] SubTask 29.1: timeout_scheduler.go handler goroutine 计入 WaitGroup
  - [ ] SubTask 29.2: handler 调用包装 defer recover + 日志
  - [ ] SubTask 29.3: Stop 等待 handler WaitGroup（带超时）
  - [ ] SubTask 29.4: virtual_balance_sync.go run 循环加 recover，Stop 加 Wait + 超时
  - [ ] SubTask 29.5: virtual_balance_sync.go 校验 interval<=0 设默认值
  - [ ] SubTask 29.6: Redis 调用用 context.WithTimeout 包裹

- [ ] Task 30: generic_service.GracefulStop 加超时 (H-60)
  - [ ] SubTask 30.1: generic_service.go GracefulStop 用 context.WithTimeout(30s)
  - [ ] SubTask 30.2: 超时后调用 server.Stop() 强制关闭
  - [ ] SubTask 30.3: 验证 handler 阻塞时 30s 后退出

- [ ] Task 31: generic_service 错误处理与拦截器 (H-61/63)
  - [ ] SubTask 31.1: 12+ 处 json.Unmarshal(req.Data, &data) 错误检查，失败返回 CodeInvalidParams
  - [ ] SubTask 31.2: Start() 中 Serve 启动失败时返回 error（用 ready channel）
  - [ ] SubTask 31.3: 增加 grpc.UnaryInterceptor 链（recovery/logging）
  - [ ] SubTask 31.4: 内部错误 err.Error() 不返回客户端，用通用提示

- [ ] Task 32: game_app_service endGameWithOptions error 上报 (H-13)
  - [ ] SubTask 32.1: endGameWithOptions 失败记录 Error 日志（含 roomID/roundID）
  - [ ] SubTask 32.2: 失败时触发告警（metrics counter 或通知）
  - [ ] SubTask 32.3: 评估是否加重试机制

- [ ] Task 33: bootstrap 关闭顺序与超时治理 (H-64/65/67/68)
  - [ ] SubTask 33.1: app.go Stop 顺序调整为：grpcServer.GracefulStop(超时) → cancel → Container.Stop(超时) → 关闭 kafka/redis
  - [ ] SubTask 33.2: app.go Start 失败时调用 Stop 清理已启动资源
  - [ ] SubTask 33.3: container.go Stop 加整体超时（context.WithTimeout）
  - [ ] SubTask 33.4: container.go 将 Kafka 消费者纳入生命周期管理（WaitGroup 跟踪）
  - [ ] SubTask 33.5: container.go 校验 SyncInterval<=0 设默认值

## Phase 6: 验证

- [ ] Task 34: 编译与基础验证
  - [ ] SubTask 34.1: go build ./... 编译通过
  - [ ] SubTask 34.2: go vet ./... 无警告
  - [ ] SubTask 34.3: go test -race ./... 现有测试通过
  - [ ] SubTask 34.4: gofmt -l 检查格式

# Task Dependencies

- Task 2 (Lua 原子扣减) 与 Task 9 (Lua 移除 math.random) 都修改 Lua 脚本，建议串行：先 Task 2 再 Task 9
- Task 16 (atomic.Pointer) 依赖 Task 8（Generator 入口校验）已完成，避免冲突
- Task 29/30/33（调度器/服务/引导关闭治理）相互关联，建议同一 phase 内串行
- Task 4 (限流) 依赖 Task 17 (db_repository sync.Once) 无直接依赖，可并行
- Phase 1 (P0) 所有任务优先级最高，应全部完成后再进入 Phase 2-5
- Phase 2-5 内任务大多相互独立，可并行执行
