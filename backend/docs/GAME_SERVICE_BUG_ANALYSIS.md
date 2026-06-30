# Game 服务代码问题分析报告

> 分析范围：`backend/game/` 全模块（application / domain / infrastructure / algorithm / scheduler / server / bootstrap / model）
> 分析方法：逐文件通读 + 调用链交叉验证 + 数学推导
> 已排除：`fix-game-optimization-issues/tasks.md` 中 Task 1-34 已处理的问题
> 置信度要求：所有问题均经代码行确认，附触发条件和证据

---

## 问题总览

| 编号 | 严重级别 | 文件 | 问题摘要 |
|------|----------|------|----------|
| 1 | **High** | `game/application/*.go`（4 个文件 14 处） | 异步 goroutine 缺少 recover()，panic 会崩溃整个 game 进程 |
| 2 | **Medium** | `game/algorithm/packet_generator.go` | `generateNormalPackets` 在特定输入下生成负数/低于最小值的红包金额 |
| 3 | **Medium** | `game/application/robot_scheduler_service.go` | `MaxPlayers` 硬编码为 5，忽略房间实际配置 |
| 4 | **Low** | `game/infrastructure/messaging/game_event_consumer.go` | `SpecialReward` 创建缺少幂等检查 |
| 5 | **Low** | `game/infrastructure/messaging/game_event_consumer.go` | `Packet` 创建缺少幂等检查，重试会永久失败 |
| 6 | **Low** | `game/infrastructure/persistence/redis/repository.go` | 死代码 `SetAllPlayersOnline` / `ResetRoomForNextGame` 存在非原子竞态 |
| 7 | **Low** | `game/bootstrap/app.go` | `Stop()` 未关闭 DB 连接池 |
| 8 | **Low** | `game/bootstrap/app.go` | `platform.NewClient` 失败时 DB 和 KafkaProducer 资源泄漏 |
| 9 | **Low** | `game/application/robot_player.go` | `JoinAndReady` 缺少 `behaviorEngine` nil 检查（与 `ScheduleLeave` 不一致） |

---

## 问题 1：异步 goroutine 缺少 recover()（High）

### 证据

以下 14 处 `go` 语句均无 `defer recover()`，函数体内也无内部 recover：

| 文件 | 行号 | 代码 |
|------|------|------|
| `game_app_service.go` | 266 | `go s.postSendPacketAsync(context.Background(), ...)` |
| `game_app_service.go` | 399 | `go s.settleRound(context.Background(), req.RoomID, roundID)` |
| `game_app_service.go` | 444 | `go s.settleRound(context.Background(), roomID, roundID)` |
| `game_app_service.go` | 498 | `go func() { s.eventPublisher.PublishSessionStart(...) }()` |
| `game_app_service.go` | 813 | `go s.postSendPacketAsync(context.Background(), ...)` |
| `game_app_service.go` | 1034 | `go func() { s.eventPublisher.PublishRoundSettle(...) }()` |
| `game_app_service.go` | 1199 | `go func() { s.eventPublisher.PublishSessionEnd(...) }()` |
| `game_app_service.go` | 1211 | `go s.gameEndCallback(context.Background(), roomID)` |
| `game_app_service.go` | 1320 | `go s.postSendPacketAsync(context.Background(), ...)` |
| `game_app_service.go` | 1479 | `go func() { s.eventPublisher.PublishPacketCreated(...) }()` |
| `room_app_service.go` | 262 | `go s.resumeGameCallback(context.Background(), roomID, currentRound)` |
| `room_app_service.go` | 527 | `go func() { s.tryAutoSubstitute(...) }()` |
| `seat_app_service.go` | 184 | `go func() { s.roomAppService.TryAutoSubstitute(...) }()` |
| `seat_app_service.go` | 308 | `go s.gameService.ResumeGame(context.Background(), ...)` |

### 触发条件

这些 goroutine 内部调用链涉及 Redis 操作（`GetRoomMeta`、`GetRoomStateData`）、Lua 脚本执行（`settleRound` → `LuaSettleRound`）、类型断言（解析 Lua 返回值）、广播（`Broadcast`）、事件发布等。任何一处 panic（如 nil 指针解引用、类型断言失败、切片越界）会直接崩溃整个 game 进程，导致**该实例上所有房间的游戏状态全部丢失**。

### 与 Task 32 的关系

Task 32 仅为 `endGameWithOptions` 的 2 处 goroutine 添加了 recover。上述 14 处是同一模式但未被覆盖。

### 与 project_memory 约束的关系

project_memory 硬约束要求：
> "All asynchronous goroutines in application layer must use app-level context derived from AsyncTaskRunner, not context.Background()"

上述 14 处全部使用 `context.Background()`，且无 recover，同时违反两条约束。

### 解决方案

为所有 application 层异步 goroutine 统一添加 `defer recover()` + 业务上下文日志。推荐封装辅助函数：

```go
// safeGo 在 application/ 目录新建 async_helper.go
func safeGo(name string, ctx context.Context, fn func(ctx context.Context)) {
    go func() {
        defer func() {
            if r := recover(); r != nil {
                logger.Error("async task panic",
                    "task", name,
                    "panic", r,
                    "stack", string(debug.Stack()),
                )
            }
        }()
        fn(ctx)
    }()
}
```

调用方改写示例：

```go
// 修改前
go s.settleRound(context.Background(), req.RoomID, roundID)

// 修改后
safeGo("settleRound", context.Background(), func(ctx context.Context) {
    s.settleRound(ctx, req.RoomID, roundID)
})
```

---

## 问题 2：PacketGenerator 生成负数/低于最小值的红包金额（Medium）

### 证据

[packet_generator.go#L133-L186](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go#L133-L186) `generateNormalPackets` + [packet_generator.go#L188-L212](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go#L188-L212) `calculateDynamicMinAmount`

**根因**：`calculateDynamicMinAmount` 在 `maxPossibleMin < configMin` 时返回 `configMin`（L194-196），但 `maxPossibleMin` 是保证剩余金额足够分配的数学上界。返回比它更大的值会导致 `generateNormalPackets` 中 `remaining` 不足。

```go
// L193-196
maxPossibleMin := (totalAmount - int64(packetCount-1)) / int64(packetCount)
if maxPossibleMin < configMin {
    return configMin  // ← 返回值 > maxPossibleMin，导致后续 remaining 不足
}
```

```go
// L140-162 关键路径
remaining := req.TotalAmount - minAmount
otherCount := req.PacketCount - 1
otherMinAmount := minAmount + 1
// ...
for i := 0; i < otherCount-1; i++ {
    maxAmount := tempRemaining - int64(remainingCount-1)*otherMinAmount  // ← 可能为负
    // ...
    amount := g.randomRange(otherMinAmount, upperBound)  // ← min >= max 时返回 min
    tempRemaining -= amount                               // ← tempRemaining 持续减少
}
tempAmounts[otherCount-1] = tempRemaining  // ← 最终赋值为负数或低于 otherMinAmount
```

### 数学推导

Bug 触发条件：`validateRequest` 通过（`totalAmount >= packetCount * configMin`）但 `totalAmount < packetCount * configMin + (packetCount - 1)`，即 `maxPossibleMin < configMin` 但 `totalAmount >= minTotal`。

此时 `remaining = totalAmount - configMin < (packetCount - 1) * (configMin + 1) = otherCount * otherMinAmount`，循环中每次扣减 `otherMinAmount` 后 `tempRemaining` 最终为负。

**具体 trace（`totalAmount=5, packetCount=5, configMin=1`）**：
1. `validateRequest`：`minTotal = 5`，`5 >= 5` 通过
2. `calculateDynamicMinAmount`：`maxPossibleMin = (5-4)/5 = 0 < 1` → 返回 `configMin = 1`
3. `minAmount=1, remaining=4, otherCount=4, otherMinAmount=2`
4. 循环 3 次：每次 `maxAmount` 为负 → `randomRange(2, 负数) = 2` → `tempRemaining` 依次为 `2 → 0 → -2`
5. `tempAmounts[3] = -2`
6. 最终 amounts = `[1, 2, 2, 2, -2]`，总和=5（正确），但有一个红包为 **-2 分**

### 生产触发评估

| 条件 | 当前生产值 | 是否触发 |
|------|-----------|----------|
| `MinPacketAmount` | 1（algorithm.yaml） | 否（configMin=1 时需 actualAmount < 9） |
| 最低 RoomFee | 100 分（init_rooms.go） | - |
| 佣金率 | 5%（DefaultCommissionConfig） | - |
| `actualAmount = 100 - 5 = 95` | 95 >> 9 | 否 |

**当前生产配置不触发**。但如果运维通过 Nacos 调高 `MinPacketAmount`（如设为 19），则 `actualAmount=95, packetCount=5, configMin=19` 会触发（`5*19=95 <= 95 < 5*19+4=99`），生成低于最小值的红包。

### 严重性

Medium —— 算法正确性缺陷，当前生产配置下不触发，但配置变更后可能触发，且生成的非法金额会直接写入 Redis 和广播给玩家。

### 解决方案

在 `calculateDynamicMinAmount` 中，当 `maxPossibleMin < configMin` 时返回 `maxPossibleMin` 而非 `configMin`（放宽 min 包约束以保证算法可行性）：

```go
maxPossibleMin := (totalAmount - int64(packetCount-1)) / int64(packetCount)
if maxPossibleMin < configMin {
    // 配置的最小值在当前 totalAmount 下不可达，返回数学上界保证算法正确
    if maxPossibleMin < 0 {
        return 0
    }
    return maxPossibleMin
}
```

同时在 `generateNormalPackets` 入口加防御性校验：

```go
if remaining < int64(otherCount)*otherMinAmount {
    // 余额不足以让每个"非最小"包都 > minAmount，回退到均分
    base := remaining / int64(otherCount)
    extra := remaining % int64(otherCount)
    // ... 分配 base+1 给 extra 个包，base 给其余
}
```

---

## 问题 3：MaxPlayers 硬编码为 5（Medium）

### 证据

[robot_scheduler_service.go#L21-L27](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_scheduler_service.go#L21-L27) `roomCandidate` 结构体无 `MaxPlayers` 字段：

```go
type roomCandidate struct {
    RoomID           string
    ReadyPlayerCount int
    WaitingSince     time.Time
    SeatedCount      int
    RoomFee          int
}
```

[robot_scheduler_service.go#L174](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_scheduler_service.go#L174)：

```go
// 3. Calculate needed robots = MaxPlayers - seated count
maxPlayers := MaxPlayers  // ← 硬编码常量 = 5
needed := maxPlayers - room.SeatedCount
```

[robot_scheduler_service.go#L341](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_scheduler_service.go#L341)：

```go
maxPlayers := MaxPlayers  // ← 硬编码常量 = 5
// ...
if room.SeatedCount >= maxPlayers {
    continue
}
```

`MaxPlayers` 常量定义在 [robot_config.go#L11](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_config.go#L11)：

```go
const MaxPlayers = 5
```

而 [model/config.go#L9](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/config.go#L9) 允许 `MaxPlayers` 配置为其他值：

```go
MaxPlayers int `json:"max_players" gorm:"default:5"`
```

### 触发条件

任何 `room_configs` 表中 `MaxPlayers != 5` 的房间。`getWaitingRooms`（L299）已从 `state` 读取了 `MaxPlayers`，但未传入 `roomCandidate`。

- `MaxPlayers=3` 的房间，3 人已满座时：`needed = 5 - 3 = 2`，调度器尝试向满座房间塞 2 个机器人，`JoinAndReady`/`SelectSeat` 会返回 `ErrNoEmptySeat`，但每次扫描仍消耗机器人池资源、获取/释放锁、产生 Warn 日志
- `MaxPlayers=8` 的房间，6 人入座时：`6 >= 5` 为 true，被 `filterRoomsNeedingRobots` 过滤掉，永远不会分配机器人

### 生产触发评估

当前 `init_rooms.go` 所有房间均为 `MaxPlayers=5`，**当前不触发**。但模型允许其他值，未来新增房间配置时会触发。

### 解决方案

1. 给 `roomCandidate` 添加 `MaxPlayers int` 字段
2. `getWaitingRooms` 中从 `state.MaxPlayers` 填充（L318-329 附近），`<= 0` 时 fallback 到 `MaxPlayers` 常量
3. L174 和 L341 改用 `room.MaxPlayers`

```go
type roomCandidate struct {
    // ...
    MaxPlayers       int
}

// getWaitingRooms 中
candidates = append(candidates, roomCandidate{
    // ...
    MaxPlayers: func() int {
        if state.MaxPlayers > 0 {
            return state.MaxPlayers
        }
        return MaxPlayers
    }(),
})

// L174
maxPlayers := room.MaxPlayers

// L341
maxPlayers := room.MaxPlayers
```

---

## 问题 4：SpecialReward 创建缺少幂等检查（Low）

### 证据

[game_event_consumer.go#L306-L326](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go#L306-L326)：

```go
if data.RewardType > 0 && data.RewardAmount > 0 {
    // ...
    reward := &model.SpecialReward{
        RoomID:      parseInt64(event.RoomID),
        SessionID:   sessionIDInt64,
        RoundID:     parseInt64(event.RoundID),
        // ...
    }
    if err := tx.Create(reward).Error; err != nil {
        return fmt.Errorf("create special reward record failed: %w", err)
    }
}
```

对比同文件 L261 的 grab record 使用了 `FirstOrCreate`：

```go
result := tx.Where(grabRecord).Assign(...).FirstOrCreate(grabRecord)
```

[model/reward.go#L15-L29](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/reward.go#L15-L29) `SpecialReward` 无 `(session_id, round_id)` 唯一索引：

```go
type SpecialReward struct {
    ID          int64 `json:"id" gorm:"primaryKey;autoIncrement"`
    SessionID   int64 `json:"session_id" gorm:"index;not null"`  // 普通索引
    RoundID     int64 `json:"round_id" gorm:"index;not null"`    // 普通索引
    // ...
}
```

### 触发条件

Kafka 重试（`tryAcquire` 的 SetNX key TTL = 7 天过期后）。条件：原事务已提交 + Kafka offset 未 commit + 7 天后 SetNX key 过期 + 重试消息到达。触发后会创建重复的 `SpecialReward` 记录。

### 严重性

Low —— 触发条件极端（需 7 天后重试），且 `SpecialReward` 仅用于统计/记录，不参与资金结算。但违反了 Task 11/14 建立的双层幂等设计原则。

### 解决方案

改用 `FirstOrCreate`：

```go
result := tx.Where(&model.SpecialReward{
    SessionID: sessionIDInt64,
    RoundID:   parseInt64(event.RoundID),
}).FirstOrCreate(reward)
if result.Error != nil {
    return fmt.Errorf("create or get special reward failed: %w", result.Error)
}
```

或添加唯一索引：`ALTER TABLE special_rewards ADD UNIQUE INDEX uk_session_round (session_id, round_id);`

---

## 问题 5：Packet 创建缺少幂等检查，重试会永久失败（Low）

### 证据

[game_event_consumer.go#L181-L192](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go#L181-L192)：

```go
for _, p := range data.Packets {
    packet := &model.Packet{
        PacketID: parseInt64(p.PacketID),  // ← primaryKey
        // ...
    }
    if err := tx.Create(packet).Error; err != nil {
        return fmt.Errorf("create packet failed: %w", err)
    }
}
```

[model/packet.go#L6](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/packet.go#L6) `PacketID` 是主键：

```go
PacketID int64 `json:"packet_id" gorm:"primaryKey"`
```

### 触发条件

同问题 4（Kafka 重试 + SetNX 7 天过期）。重试时 `tx.Create` 会因主键冲突报 `Duplicate entry` 错误，导致整个事务回滚，事件**永久无法处理成功**。

对比同文件的 round 状态更新（L171-178）用了 `Updates` + WHERE，是幂等的；grab record 用了 `FirstOrCreate`，也是幂等的。唯独 packet 创建不幂等。

### 严重性

Low —— 触发条件极端，但一旦触发会导致该 round 的所有后续事件（round_settle 等）也无法处理，因为 `handlePacketCreated` 一直失败。不过 `handleRoundSettle` 内部有独立的 `RoundStatusCredited` 幂等检查，资金安全不受影响。

### 解决方案

```go
if err := tx.Where(&model.Packet{PacketID: parseInt64(p.PacketID)}).
    FirstOrCreate(packet).Error; err != nil {
    return fmt.Errorf("create or get packet failed: %w", err)
}
```

---

## 问题 6：死代码存在非原子竞态（Low）

### 证据

[repository.go#L547-L565](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go#L547-L565) `SetAllPlayersOnline`：

```go
func (r *RoomRepository) SetAllPlayersOnline(ctx context.Context, roomID string, isOnline bool) error {
    players, err := r.GetPlayers(ctx, roomID)  // ← HGetAll 读取
    // ...
    for _, player := range players {
        player.DisconnectedAt = ...
        if err := r.SavePlayer(ctx, roomID, player); err != nil {  // ← HSet 写入
            return err
        }
    }
    return nil
}
```

`GetPlayers`（HGetAll）和 `SavePlayer`（HSet）之间非原子。如果循环期间其他 goroutine 调用 `SavePlayer` 修改了某个 player，该修改会被循环中读到的旧数据覆盖。

[repository.go#L567-L606](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go#L567-L606) `ResetRoomForNextGame` 同样的 `HGetAll` + pipeline `HSet` 非原子模式。

### 死代码确认

全 backend grep 确认：两个方法仅在 `domain/repository.go` 接口声明和 `redis/repository.go` 实现中出现，**零调用方**。

### 严重性

Low —— 死代码，不影响生产。但接口声明意味着未来可能被调用，调用时会引入竞态。

### 解决方案

删除死代码（接口声明和实现一起删），或如果未来需要，改用 Lua 脚本原子更新。

---

## 问题 7：Stop() 未关闭 DB 连接池（Low）

### 证据

[app.go#L248-L274](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go#L248-L274) `Stop()` 关闭了 Redis、KafkaProducer、nacos，但未关闭 DB：

```go
func (a *Application) Stop() {
    // ... cancel ctx, stop schedulers, stop gRPC ...
    if a.nacos != nil {
        a.nacos.Close()
    }
    if a.Container.KafkaProducer != nil {
        a.Container.KafkaProducer.Close()
    }
    if a.Container.Redis != nil {
        a.Container.Redis.Close()
    }
    // ← 缺少 a.Container.DB 关闭
    logger.Info("game service stopped")
}
```

### 触发条件

每次优雅关闭（SIGINT/SIGTERM）。

### 严重性

Low —— 进程退出时 OS 回收文件描述符，DB 连接最终会被关闭。但 DB 服务器侧需要等 TCP keepalive 超时才能回收连接资源，短暂占用连接池配额。

### 解决方案

```go
if a.Container.DB != nil {
    if sqlDB, err := a.Container.DB.DB(); err == nil {
        sqlDB.Close()
    }
}
```

---

## 问题 8：platform.NewClient 失败时资源泄漏（Low）

### 证据

[app.go#L113-L125](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go#L113-L125)：

```go
db, err := mysql.NewDB(&cfg.MySQL)          // L113 已创建 DB
// ...
kafkaProducer := kafka.NewProducerWithBrokers(cfg.Kafka.Brokers)  // L119 已创建 Producer

platformClient, err := platform.NewClient(&cfg.Platform)  // L121
if err != nil {
    redisClient.Close()  // ← 只关了 redis
    // ← db 和 kafkaProducer 未关闭
    return nil, fmt.Errorf("failed to create platform client: %w", err)
}
```

### 触发条件

`platform.NewClient` 失败（如无效 URL、TLS 握手失败、网络问题）。

### 严重性

Low —— 调用方 `Run()` 中 `panic(err)` 退出进程，OS 回收资源。但如果被测试代码或重试逻辑调用，泄漏会累积。

### 解决方案

```go
platformClient, err := platform.NewClient(&cfg.Platform)
if err != nil {
    redisClient.Close()
    if sqlDB, e := db.DB(); e == nil {
        sqlDB.Close()
    }
    kafkaProducer.Close()
    return nil, fmt.Errorf("failed to create platform client: %w", err)
}
```

---

## 问题 9：robot_player JoinAndReady 缺少 nil 检查（Low）

### 证据

[robot_player.go#L88-L89](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_player.go#L88-L89)：

```go
delay := p.randomDelay(p.config.Behavior.SeatDelayMin, p.config.Behavior.SeatDelayMax)
p.behaviorEngine.ScheduleAction(roomID, robotUserID, "seat", delay)  // ← 无 nil 检查
```

对比同文件 [robot_player.go#L191-L199](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_player.go#L191-L199) `ScheduleLeave` 有 nil 检查：

```go
func (p *RobotPlayer) ScheduleLeave(...) {
    if p.behaviorEngine == nil {
        logger.Warn("behavior engine not set, cannot schedule leave", ...)
        return
    }
    p.behaviorEngine.ScheduleAction(roomID, robotUserID, "leave", delay)
}
```

### 触发条件

`SetBehaviorEngine` 未被调用时 `JoinAndReady` 被调用（初始化顺序错误、单元测试构造场景）。生产中 bootstrap 正常注入，不触发。

### 严重性

Low —— 生产初始化顺序保护，但与 `ScheduleLeave` 不一致，属防御性编程缺失。

### 解决方案

```go
func (p *RobotPlayer) JoinAndReady(...) error {
    // ...
    if p.behaviorEngine == nil {
        logger.Warn("behavior engine not set, cannot schedule seat",
            "room_id", roomID, "user_id", robotUserID)
        return nil
    }
    p.behaviorEngine.ScheduleAction(roomID, robotUserID, "seat", delay)
    return nil
}
```

---

## 修复优先级建议

| 优先级 | 问题 | 理由 |
|--------|------|------|
| **P0** | 问题 1（goroutine recover） | 14 处 panic 风险，任何一处触发即崩溃全进程，影响所有房间 |
| **P1** | 问题 2（packet_generator） | 算法正确性缺陷，配置变更后可能产生非法金额 |
| **P1** | 问题 3（MaxPlayers 硬编码） | 新增非 5 人房间时机器人调度失效 |
| **P2** | 问题 4、5（幂等） | 触发条件极端，但违反幂等设计原则 |
| **P3** | 问题 6-9 | 死代码/资源清理/防御性编程，当前无实际影响 |

---

## 附录：已排除的误报项

以下项经核实不构成 bug，未列入本报告：

- `reward_controller.checkGuarantee` 竞态：Task 20 已评估，强行修复会引入更严重 bug
- `crand.Int` 错误检查：Task 21 已评估，属过度防御
- `lua_scripts.go` EXPIRE 问题：Task 23 已评估，业务时长远小于 TTL
- `redis/repository.go` 类型断言：Task 24 已评估，go-redis 类型映射确定
- `user_repository.CreateOrUpdateUser` upsert：Task 27 已评估，低优先级
- `room_event_publisher` Kafka key：Task 28 已评估，consumer 无顺序依赖
- `bootstrap` 关闭顺序：Task 33 已评估，无功能性 bug
