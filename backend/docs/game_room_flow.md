# 游戏房间状态管理与玩家转换流程

## 一、房间状态定义

```go
type RoomStatus int

const (
    RoomStatusIdle        RoomStatus = 0  // 空闲
    RoomStatusWaiting     RoomStatus = 1  // 等待
    RoomStatusPlaying     RoomStatus = 2  // 游戏中
    RoomStatusInterrupted RoomStatus = 4  // 中断
)
```

## 二、玩家状态定义

```go
type PlayerStatus int

const (
    PlayerStatusOnline  PlayerStatus = 0  // 在线
    PlayerStatusOffline PlayerStatus = 1  // 离线
    PlayerStatusReady   PlayerStatus = 2  // 准备
    PlayerStatusPlaying PlayerStatus = 3  // 游戏中
)
```

## 三、状态机流转图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              房间状态机                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   ┌──────┐                                                                  │
│   │ 空闲 │                                                                  │
│   │  0   │                                                                  │
│   └──┬───┘                                                                  │
│      │ 观众加入                                                              │
│      ↓                                                                      │
│   ┌──────┐     所有玩家准备 + 人满     ┌──────────┐                          │
│   │ 等待 │ ─────────────────────────→ │  倒计时   │                          │
│   │  1   │                            │ (3秒)    │                          │
│   └──┬───┘                            └────┬─────┘                          │
│      │                                     │                                │
│      │                                     ↓                                │
│      │                                ┌──────┐                              │
│      │                                │游戏中│                              │
│      │                                │  2   │                              │
│      │                                └──┬───┘                              │
│      │                                   │                                  │
│      │              ┌────────────────────┼────────────────────┐             │
│      │              │                    │                    │             │
│      │              ↓                    ↓                    ↓             │
│      │         ┌────────┐          ┌────────┐          ┌────────┐          │
│      │         │游戏结束 │          │玩家被踢 │          │正常结束 │          │
│      │         │回到等待 │          │中断状态 │          │回到等待 │          │
│      │         └───┬────┘          └────┬───┘          └───┬────┘          │
│      │             │                    │                    │             │
│      └─────────────┼────────────────────┼────────────────────┘             │
│                    │                    │                                  │
│                    │              ┌─────┴─────┐                            │
│                    │              │   中断    │                            │
│                    │              │    4      │                            │
│                    │              └─────┬─────┘                            │
│                    │                    │                                  │
│                    │              观众补位+准备                             │
│                    │                    │                                  │
│                    │              ┌─────↓─────┐                            │
│                    │              │ 系统发红包 │                            │
│                    │              │ 继续游戏   │                            │
│                    │              └─────┬─────┘                            │
│                    │                    │                                  │
│                    └────────────────────┼──────────────────────────────────┘
│                                         │                                  
│                                         ↓                                  
│                                    ┌──────┐                                
│                                    │游戏中│                                
│                                    │  2   │                                
│                                    └──────┘                                
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 四、正常游戏开始流程

### 4.1 流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│                         正常游戏开始流程                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  观众进入房间 ──→ 选择座位 ──→ 点击准备 ──→ 变成玩家                   │
│                                     │                              │
│                                     ↓                              │
│                        所有玩家准备好 + 人满                         │
│                                     │                              │
│                                     ↓                              │
│                              3秒倒计时                              │
│                                     │                              │
│                                     ↓                              │
│                              游戏开始                               │
│                                     │                              │
│                                     ↓                              │
│                            系统发送第一局红包                         │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.2 详细步骤

| 步骤 | 操作 | 状态变化 | 代码位置 |
|------|------|---------|---------|
| 1 | 观众加入房间 | 房间状态: `Idle` → `Waiting` | `room_app_service.go:JoinRoom` |
| 2 | 选择座位 | 观众获得座位号 | `seat_app_service.go:SelectSeat` |
| 3 | 点击准备 | 观众 → 玩家, 状态: `Ready` | `seat_app_service.go:SetReady` |
| 4 | 检查开始条件 | 人满 + 所有玩家准备 | `lua_scripts.go:LuaTryStartCountdown` |
| 5 | 倒计时 | 广播倒计时 3,2,1 | `game_app_service.go:StartGameCountdownWithEndTime` |
| 6 | 游戏开始 | 房间状态: `Waiting` → `Playing` | `lua_scripts.go:LuaTryStartGame` |
| 7 | 系统发红包 | 系统发送第一局红包 | `game_app_service.go:startFirstRound` |

### 4.3 相关代码

**选择座位** - [seat_app_service.go:67-130](../game/application/seat_app_service.go#L67-L130)
```go
func (s *SeatAppService) SelectSeat(ctx context.Context, req *SelectSeatRequest) (*SelectSeatResponse, error) {
    // 检查房间状态
    // 调用 LuaSelectSeat 脚本
    // 设置座位超时定时器
    // 广播房间状态
}
```

**准备开始** - [seat_app_service.go:226-340](../game/application/seat_app_service.go#L226-L340)
```go
func (s *SeatAppService) SetReady(ctx context.Context, req *SetReadyRequest) (*SetReadyResponse, error) {
    // 获取房间状态
    // 调用 SetReady 转换为玩家
    // 根据房间状态判断:
    //   - Waiting: 触发倒计时
    //   - Interrupted: 恢复游戏
}
```

## 五、惩罚踢出流程

### 5.1 流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│                         惩罚踢出流程                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  玩家超时未发红包                                                     │
│         │                                                           │
│         ↓                                                           │
│  第一次惩罚 (罚款) ──→ 第二次惩罚 (罚款) ──→ 踢出房间                  │
│                                                       │             │
│                                                       ↓             │
│                                              房间状态 → 中断          │
│                                                       │             │
│                                                       ↓             │
│                                              等待观众补位 (30秒)      │
│                                                       │             │
│                                         ┌─────────────┴──────────┐  │
│                                         │                        │  │
│                                         ↓                        ↓  │
│                                    补位成功                  补位超时 │
│                                    继续游戏                  游戏结束 │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 5.2 详细步骤

| 步骤 | 操作 | 状态变化 | 代码位置 |
|------|------|---------|---------|
| 1 | 发送超时检测 | - | `scheduler/timeout_scheduler.go` |
| 2 | 应用惩罚 | 惩罚次数 +1 | `penalty_service.go:ApplyPenalty` |
| 3 | 判断是否踢出 | 惩罚次数 >= 2 则踢出 | `lua_game.go:LuaHandlePenalty` |
| 4 | 踢出玩家 | 玩家从房间移除 | `game_app_service.go:handleKickAndReplace` |
| 5 | 房间中断 | 房间状态: `Playing` → `Interrupted` | `game_app_service.go:handleKickAndReplace` |
| 6 | 等待补位 | 设置30秒补位超时 | `scheduler.SetTimeout(TimeoutTypeReplace)` |

### 5.3 相关代码

**惩罚处理** - [penalty_service.go:30-76](../game/application/penalty_service.go#L30-L76)
```go
func (s *PenaltyService) ApplyPenalty(ctx context.Context, roomID, userID string, penaltyType domain.PenaltyType, roomFee int64) (*domain.PenaltyResult, error) {
    // 调用 LuaHandlePenalty 脚本
    // 返回惩罚次数和是否需要踢出
}
```

**踢出并等待补位** - [game_app_service.go:1267-1319](../game/application/game_app_service.go#L1267-L1319)
```go
func (s *GameAppService) handleKickAndReplace(ctx context.Context, roomID, userID string, penaltyAmount int64) {
    // 踢出玩家
    // 更新房间状态为中断
    // 设置补位超时
    // 广播等待补位消息
}
```

## 六、中断恢复流程

### 6.1 流程图

```
┌─────────────────────────────────────────────────────────────────────┐
│                         中断恢复流程                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  玩家被踢出 ──→ 房间状态变成中断 ──→ 系统罚款                          │
│                                         │                           │
│                                         ↓                           │
│                                    观众补位                          │
│                                         │                           │
│                                         ↓                           │
│                                    选择座位                          │
│                                         │                           │
│                                         ↓                           │
│                                    点击准备                          │
│                                         │                           │
│                                         ↓                           │
│                           所有玩家准备好 + 人满                       │
│                                         │                           │
│                                         ↓                           │
│                              清除 next_sender_id                     │
│                                         │                           │
│                                         ↓                           │
│                         系统发送当前回合红包 (无倒计时)                │
│                                         │                           │
│                                         ↓                           │
│                                    继续游戏                          │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 6.2 详细步骤

| 步骤 | 操作 | 状态变化 | 代码位置 |
|------|------|---------|---------|
| 1 | 观众进入中断房间 | 房间状态: `Interrupted` | `room_app_service.go:JoinRoom` |
| 2 | 选择空座位 | 观众获得座位号 | `seat_app_service.go:SelectSeat` |
| 3 | 点击准备 | 观众 → 玩家, 状态: `Ready` | `seat_app_service.go:SetReady` |
| 4 | 检查恢复条件 | 人满 + 所有玩家准备 | `lua_scripts.go:LuaTryResumeFromInterrupt` |
| 5 | 恢复游戏 | 房间状态: `Interrupted` → `Playing` | `lua_scripts.go:LuaTryResumeFromInterrupt` |
| 6 | 系统发红包 | 系统发送当前回合红包 | `game_app_service.go:systemSendRound` |

### 6.3 相关代码

**检查恢复条件** - [lua_scripts.go:623-679](../game/infrastructure/persistence/redis/lua_scripts.go#L623-L679)
```lua
-- LuaTryResumeFromInterrupt
-- 检查房间状态是否为中断
-- 检查玩家是否都已准备好
-- 原子性地将状态从 Interrupted 改为 Playing
-- 返回当前回合数
```

**恢复游戏** - [game_app_service.go:1410-1457](../game/application/game_app_service.go#L1410-L1457)
```go
func (s *GameAppService) ResumeGame(ctx context.Context, req *ResumeGameRequest) error {
    // 清除替换超时定时器
    // 广播游戏恢复消息
    // 调用 systemSendRound 由系统发送红包
}
```

**系统发送红包** - [game_app_service.go:1459-1555](../game/application/game_app_service.go#L1459-L1555)
```go
func (s *GameAppService) systemSendRound(ctx context.Context, roomID string, meta *domain.RoomMeta, roundNo int) {
    // 生成红包金额
    // 调用 LuaSystemSendResumeRound 脚本
    // 设置抢红包超时
    // 广播回合开始消息
}
```

## 七、Lua 脚本说明

### 7.1 选择座位

**LuaSelectSeat** - [lua_scripts.go:145-205](../game/infrastructure/persistence/redis/lua_scripts.go#L145-L205)
- 检查房间状态 (允许 `Waiting` 和 `Interrupted`)
- 检查座位是否已被占用
- 更新观众座位信息
- 设置座位归属

### 7.2 准备开始

**LuaSetReady** - [lua_scripts.go:258-314](../game/infrastructure/persistence/redis/lua_scripts.go#L258-L314)
- 检查房间状态 (允许 `Waiting` 和 `Interrupted`)
- 检查观众是否已选座
- 将观众转换为玩家
- 更新房间玩家数量

### 7.3 开始倒计时

**LuaTryStartCountdown** - [lua_scripts.go:536-583](../game/infrastructure/persistence/redis/lua_scripts.go#L536-L583)
- 检查房间状态是否为 `Waiting`
- 检查玩家是否已满
- 检查所有玩家是否已准备
- 设置倒计时结束时间

### 7.4 开始游戏

**LuaTryStartGame** - [lua_scripts.go:585-622](../game/infrastructure/persistence/redis/lua_scripts.go#L585-L622)
- 检查房间状态是否为 `Waiting`
- 检查倒计时是否结束
- 原子性地更新房间状态为 `Playing`

### 7.5 中断恢复

**LuaTryResumeFromInterrupt** - [lua_scripts.go:623-679](../game/infrastructure/persistence/redis/lua_scripts.go#L623-L679)
- 检查房间状态是否为 `Interrupted`
- 检查玩家是否已满
- 检查所有玩家是否已准备
- 原子性地更新房间状态为 `Playing`
- 返回当前回合数

### 7.6 系统发送恢复红包

**LuaSystemSendResumeRound** - [lua_game.go:700-783](../game/infrastructure/persistence/redis/lua_game.go#L700-L783)
- 检查房间状态是否为 `Playing`
- 清除 `next_sender_id`
- 生成红包
- 设置回合状态
- 更新当前回合数

## 八、消息类型

### 8.1 推送消息类型

| 类型 | 说明 | 数据结构 |
|------|------|---------|
| `game_start` | 游戏开始 | `GameStartPush` |
| `game_resumed` | 游戏恢复 | `GameResumedPush` |
| `countdown` | 倒计时 | `CountdownPush` |
| `round_start` | 回合开始 | `RoundStartPush` |
| `packet_grabbed` | 红包被抢 | `PacketGrabbedPush` |
| `round_end` | 回合结束 | `RoundEndPush` |
| `game_end` | 游戏结束 | `GameEndPush` |
| `penalty` | 惩罚 | `PenaltyPush` |
| `wait_replacement` | 等待补位 | `WaitReplacementPush` |
| `game_interrupted` | 游戏中断 | `GameInterruptedPush` |
| `kicked` | 被踢出 | `KickedPush` |

### 8.2 游戏恢复推送

```go
type GameResumedPush struct {
    RoomID       string `json:"room_id"`
    CurrentRound int32  `json:"current_round"`
    NextSenderID string `json:"next_sender_id"`  // 恢复时为 "0" 表示系统发送
    Message      string `json:"message"`
}
```

## 九、超时处理

### 9.1 超时类型

| 类型 | 说明 | 超时时间 | 处理方式 |
|------|------|---------|---------|
| `TimeoutTypeSeat` | 选座超时 | 30秒 | 踢出房间 |
| `TimeoutTypeReady` | 准备超时 | 30秒 | 踢出房间 |
| `TimeoutTypeSend` | 发红包超时 | 10秒 | 惩罚处理 |
| `TimeoutTypeGrab` | 抢红包超时 | 10秒 | 自动分配 |
| `TimeoutTypeDisconnect` | 断线超时 | 60秒 | 踢出房间 |
| `TimeoutTypeReplace` | 补位超时 | 30秒 | 游戏结束 |

### 9.2 超时调度器

**代码位置** - [scheduler/timeout_scheduler.go](../game/scheduler/timeout_scheduler.go)

```go
type TimeoutScheduler struct {
    redis     *cRedis.Client
    handlers  map[TimeoutType]TimeoutHandler
}

func (s *TimeoutScheduler) SetTimeout(ctx context.Context, timeoutType TimeoutType, roomID, data string)
func (s *TimeoutScheduler) ClearTimeout(ctx context.Context, timeoutType TimeoutType, roomID, data string)
func (s *TimeoutScheduler) ClearRoomTimeouts(ctx context.Context, timeoutType TimeoutType, roomID string)
```

## 十、关键设计决策

### 10.1 为什么中断后由系统发红包？

1. **公平性**: 逃跑玩家已被罚款，补位玩家不应承担发送红包的责任
2. **游戏连续性**: 系统发送红包保证游戏能够继续进行
3. **避免惩罚传递**: 不让补位玩家承担逃跑玩家的惩罚

### 10.2 为什么中断后不需要倒计时？

1. **玩家已准备**: 补位玩家点击准备时已经确认参与
2. **减少等待**: 其他等待的玩家已经等待了足够长时间
3. **快速恢复**: 尽快恢复游戏，提升用户体验

### 10.3 状态机设计原则

1. **原子性**: 所有状态转换通过 Lua 脚本保证原子性
2. **幂等性**: 重复操作不会产生副作用
3. **可追溯**: 每个状态变化都有日志记录
4. **可恢复**: 中断状态可以被恢复到游戏中
