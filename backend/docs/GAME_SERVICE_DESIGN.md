# Game 服务设计文档

> 基于代码实现整理，覆盖房间设计、游戏设计、机器人系统、调度器、结算、消息、广播等核心模块
> 代码路径：`backend/game/`

---

## 目录

1. [系统架构总览](#1-系统架构总览)
2. [房间设计](#2-房间设计)
3. [游戏设计](#3-游戏设计)
4. [红包与算法设计](#4-红包与算法设计)
5. [机器人系统设计](#5-机器人系统设计)
6. [调度器设计](#6-调度器设计)
7. [结算系统设计](#7-结算系统设计)
8. [消息与事件设计](#8-消息与事件设计)
9. [广播设计](#9-广播设计)
10. [数据持久化设计](#10-数据持久化设计)
11. [Redis Key 设计](#11-redis-key-设计)
12. [Lua 脚本清单](#12-lua-脚本清单)
13. [gRPC 接口设计](#13-grpc-接口设计)
14. [启动与生命周期](#14-启动与生命周期)
15. [抢红包服务（GrabService）详细设计](#15-抢红包服务grabservice详细设计)
16. [历史查询服务（HistoryService）详细设计](#16-历史查询服务historyservice详细设计)
17. [用户服务（UserService）详细设计](#17-用户服务userservice详细设计)
18. [房间应用服务（RoomAppService）完整流程](#18-房间应用服务roomappservice完整流程)
19. [机器人调度服务（RobotSchedulerService）详细算法](#19-机器人调度服务robotschedulerservice详细算法)
20. [机器人玩家与账号服务实现](#20-机器人玩家与账号服务实现)
21. [游戏状态领域模型与阶段枚举](#21-游戏状态领域模型与阶段枚举)
22. [关键流程时序图](#22-关键流程时序图)
23. [错误码与错误处理](#23-错误码与错误处理)
24. [并发控制与锁策略汇总](#24-并发控制与锁策略汇总)

---

## 1. 系统架构总览

### 1.1 分层架构（DDD 风格）

```
┌─────────────────────────────────────────────────────────────┐
│                    Gateway（独立进程）                        │
│              WebSocket/TCP ←→ gRPC 转发                       │
└───────────────────────────┬─────────────────────────────────┘
                            │ gRPC (Forward / SaveUser)
┌───────────────────────────▼─────────────────────────────────┐
│                      Game 服务                                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  server/  gRPC 服务入口，命令分发                       │   │
│  ├──────────────────────────────────────────────────────┤   │
│  │  application/  应用服务层（业务编排）                   │   │
│  │    - RoomAppService   房间管理                          │   │
│  │    - SeatAppService   座位管理                          │   │
│  │    - GameAppService   游戏主流程                        │   │
│  │    - GrabService      抢红包                            │   │
│  │    - PenaltyService   惩罚                              │   │
│  │    - RobotPlayer/RobotBehaviorEngine/RobotScheduler  │   │
│  ├──────────────────────────────────────────────────────┤   │
│  │  domain/  领域模型与接口                                │   │
│  │    - Room/Player/Spectator/GameState/Round/Penalty    │   │
│  │    - Repository 接口、Event 接口、Broadcaster 接口      │   │
│  ├──────────────────────────────────────────────────────┤   │
│  │  infrastructure/  基础设施实现                          │   │
│  │    - persistence/redis/  Redis 仓储 + Lua 脚本         │   │
│  │    - persistence/mysql/  MySQL 仓储                    │   │
│  │    - messaging/  Kafka 事件发布/消费                    │   │
│  │    - broadcast/  广播包装层                             │   │
│  ├──────────────────────────────────────────────────────┤   │
│  │  algorithm/  红包生成 + 奖励控制                        │   │
│  │  scheduler/  超时调度 + 虚拟余额同步                    │   │
│  │  bootstrap/  应用启动 + DI 容器                         │   │
│  └──────────────────────────────────────────────────────┘   │
└───────┬──────────────────┬───────────────────┬──────────────┘
        │ Redis            │ MySQL              │ Kafka
        │ (热状态/ Lua)     │ (持久化/审计)       │ (事件总线)
```

### 1.2 核心设计决策

| 决策 | 说明 |
|------|------|
| **CQRS-lite** | Redis 为权威热状态（房间/座位/round/红包均由 Lua 原子操作），MySQL 为持久化投影，由 Kafka consumer 异步写入 |
| **事件驱动持久化** | 游戏操作原子修改 Redis 后发布 Kafka 事件；`GameEventConsumer` 事务性写入 MySQL，解耦热路径与持久化 |
| **Lua 脚本原子性** | 所有多键变更（座位/ready/发红包/抢/结算/结束）均为单 Lua 脚本，Redis 单线程执行保证原子 |
| **双层幂等** | Redis SetNX（traceID/eventID）前置去重 + DB FirstOrCreate/状态预检查后置兜底 |
| **DI 容器** | 构造注入 + setter 注入打破循环依赖（Room ↔ Game ↔ Seat, RobotPlayer ↔ BehaviorEngine） |
| **广播 fire-and-forget** | 广播失败仅 Warn 日志不阻塞主流程，玩家可重连拉状态恢复 |

---

## 2. 房间设计

### 2.1 房间状态机

```
                          JoinAsSpectator
   Idle(0) ─────────────────────────────► Waiting(1)
                                                      │
                                 playerCount≥maxPlayers│
                                 (LuaPlayerReady/      │
                                  LuaAutoSeatAndReady) │
                                                      ▼
                                               Playing(2)
                                              │     │
              KickPlayerAndInterrupt           │     │ LuaEndGame
              (玩家中途离开/被踢)               │     │ (正常结束/扣费失败)
                   ┌──────────────────────────┘     │
                   ▼                                 │
            Interrupted(4) ───AutoSubstitute────────┘
                   │  (替补满员 → ResumeGame)
                   │
                   │ OnReplaceTimeout
                   │ (替补超时 → LuaEndGame)
                   └──────────────► Waiting(1)
```

| 状态 | 值 | 含义 |
|------|----|----|
| `RoomStatusIdle` | 0 | 房间已创建，无人加入 |
| `RoomStatusWaiting` | 1 | 有旁观者/玩家，未开始 |
| `RoomStatusPlaying` | 2 | 游戏进行中（含倒计时阶段） |
| `RoomStatusInterrupted` | 4 | 玩家中途被踢，等待替补 |

> 注：值 `3` 被跳过；`Playing(2)` 在 Lua 中兼用作"倒计时进行中"状态（`started_at` 未设置表示倒计时中，已设置表示游戏已开始）。

### 2.2 房间数据结构

**`domain.RoomMeta`**（`domain/room.go:12-28`）— Redis hash 权威元数据：

| 字段 | 类型 | 说明 |
|------|------|------|
| RoomID | string | 房间 ID |
| RoomNo | string | 展示编号 |
| ConfigID/ConfigName | int64/string | 关联 room_configs |
| RoomFee | int64 | 房费（分） |
| MaxPlayers | int | 最大玩家数（默认 5） |
| MaxRounds | int | 最大轮数（默认 10） |
| MaxSpectators | int | 最大旁观者数 |
| Status | RoomStatus | 房间状态 |
| CurrentRound | int | 当前轮次 |
| CurrentRoundID | string | 当前轮 ID |
| CurrentSessionID | string | 当前会话 ID |
| PlayerCount | int | 玩家数 |
| SpectatorCount | int | 旁观者数 |
| StartedAt | *int64 | 游戏开始时间戳 |
| CountdownEndTime | int64 | 倒计时结束时间 |
| NextSenderID | string | 下一轮发包人 ID |
| InterruptedAt | int64 | 中断时间 |

**`model.Room`**（`model/room.go:14-32`）— MySQL `rooms` 表，结构与 RoomMeta 对齐，增加 `CreatorID`、`CurrentSessionID int64`、时间戳。

**`domain.Player`**（`domain/room.go:30-37`）：
```go
type Player struct {
    UserID, Nickname, Avatar string
    SeatNo                    int
    DisconnectedAt            *int64  // nil=在线
    IsRobot                   bool
}
func (p *Player) IsOnline() bool   // DisconnectedAt == nil
func (p *Player) CanGrab() bool    // == IsOnline
```

**`domain.Spectator`**（`domain/room.go:39-45`）：`UserID, Nickname, Avatar, SeatNo, IsRobot`。

**`domain.QueueInfo`**（`domain/room.go:47-53`）：`UserID, Nickname, Avatar, QueuePosition, QueuedAt`。

### 2.3 房间生命周期

#### 房间创建
- 房间在 MySQL `rooms` 表预创建（`scripts/init_rooms.go`），含 `ConfigID/ConfigName, RoomFee, MaxPlayers, MaxRounds, MaxSpectators`
- Redis 状态**懒初始化**：玩家首次加入时 `RoomAppService.JoinRoom` → `repo.InitRoom`（`redis/repository.go:608`），仅当 hash 不存在时写入，TTL 24h

#### 玩家加入流程
```
JoinRoom (LuaJoinAsSpectator)
  ├── 校验：room 存在、未在别的房间（userRoomKey）、容量检查
  ├── 写入 spectators hash、设置 userRoomKey（24h）
  ├── status 0→1
  └── 广播 PushRoomState、发布 SpectatorJoinEvent

JoinAndAutoSeat (LuaAutoSeatAndReady)  [可选，自动入座]
  ├── 余额检查（balanceService.CheckBalanceForReady）
  ├── 找到第一个空座位（GETBIT 1..maxPlayers）
  ├── 旁观者→玩家转换（HDEL spectators, HSET players）
  ├── ready_at = now
  ├── 若 playerCount≥maxPlayers 且 status==Waiting → shouldStartCountdown=1, status=2, countdown_end_time=now+3
  ├── 若 status==Interrupted 且替补满员 → shouldStartCountdown=2（触发 ResumeGame）
  └── 广播 PushRoomState、发布 PlayerReadyEvent
```

#### 座位机制（4 种）
| 机制 | Lua 脚本 | 触发场景 |
|------|---------|---------|
| 手动选座 | `LuaSelectSeat` | 旁观者指定具体座位号 |
| 自动入座 | `LuaAutoSeatAndReady` | 加入时自动选座 + ready |
| 排队 | `LuaEnqueue`/`LuaDequeue` | 无空座时排队等待 |
| 自动替补 | `LuaAutoSubstitute` | 座位释放时从队列弹出首位补上 |

排队数据结构：Sorted Set `cashparty:room:queue:{roomID}`，score=加入时间戳。

#### 离开/踢出
- **离开**（`LuaLeaveRoom`）：仅旁观者可直接离开；玩家返回 `LuaErrPlayerCannotLeave(16)`，必须被踢
- **踢出**（`LuaKickPlayerAndInterrupt`）：游戏内踢人，`Playing(2)→Interrupted(4)`，释放座位；若被踢者是当前轮发包人返回 `32`
- **座位超时踢出**（`LuaHandleSeatTimeout`）：旁观者选座后未在超时内 ready

#### 房间重置
- **正常结束**（`LuaEndGame`）：`status→Waiting(1)`，重置 `current_round=0`，删除 `next_sender_id/countdown_end_time/started_at`，玩家转回旁观者（保留座位）
- **手动重置**（`ResetRoomForNextGame`）：同上 + 清空每个 player 的 `disconnected_at`（当前为死代码，无调用方）

### 2.4 房间状态视图构建

`application/room_state.go` 的 `BuildFullRoomState` 将 Redis 状态转为前端 DTO：

```go
type RoomState struct {
    RoomID, RoomNo      string
    RoomFee             currency.Money
    Status              int
    CurrentRound, MaxRounds int
    PlayerCount, SpectatorCount int
    MaxPlayers, MaxSpectators  int
    Players    []*PlayerInfo
    Spectators []*SpectatorInfo
    Seats      []*SeatInfo   // 1..MaxPlayers，含 Occupied/UserID/Ready/IsRobot
    QueueList  []*QueueInfo
}
```

座位视图构建逻辑：对每个座位 1..MaxPlayers，若 player 占据则 `Occupied=true, Ready=true`；若旁观者占据（`SeatOwners`）则 `Occupied=true, Ready=false`。

---

## 3. 游戏设计

### 3.1 游戏会话（Session）

**`model.GameSession`**（`model/session.go:13-30`）— MySQL `game_sessions` 表：

| 字段 | 说明 |
|------|------|
| SessionID | 雪花 ID |
| RoomID/RoomNo | 关联房间 |
| ConfigID/ConfigName/RoomFee | 房间配置快照 |
| MaxRounds | 最大轮数 |
| ActualRounds | 实际轮数 |
| CurrentRound | 当前轮次 |
| PlayerCount | 玩家数 |
| Status | `Playing=0, Completed=1, Abnormal=2` |
| StartedAt/EndedAt | 时间戳 |
| EndReason | 结束原因 |

**`model.SessionPlayer`**（`model/session.go:34-52`）— MySQL `session_players` 表，按 (SessionID, UserID) 唯一索引：
- `SendCount/GrabCount`：发包/抢包次数
- `TotalSend/TotalGrab/TotalProfit`：累计金额（分）
- `SeatNo/IP/DeviceID/JoinedAt/LeftAt`

#### 会话状态机
```
Playing(0) ──正常结束/扣费失败/替补超时──► Completed(1) 或 Abnormal(2)
```

结束原因（`EndReason`）：
- `ReasonNormalEnd`：达到 MaxRounds 正常结束
- `ReasonFirstRoundDeductFailed` / `ReasonLaterRoundDeductFailed` / `ReasonPenaltyDeductFailed`：扣费失败
- `ReasonReplacementTimeout`：替补超时

### 3.2 轮次（Round）

**`model.Round`**（`model/round.go:15-35`）— MySQL `rounds` 表：

| 字段 | 说明 |
|------|------|
| RoundID | 雪花 ID |
| SessionID/RoomID | 关联 |
| RoundNo | 轮次序号 |
| Status | `Pending=0, Sending=1, Grabbing=2, Ended=3, Failed=4` |
| SenderID/SenderType | 发包人 ID/类型 |
| TotalAmount | 红包总额 |
| Commission | 佣金 |
| DeductScene | 扣费场景：`FirstRoundShare=1, LaterRoundMin=2, SystemPacket=3` |
| DeductAmount/DeductStatus | 扣费金额/状态 |
| SettleTraceID | 结算追踪 ID |

#### 轮次状态机（Redis `round:state:{roundID}.phase`）
```
(不存在) ──LuaSendPacket──► GRABBING
GRABBING ──最后一人抢(LuaGrabPacket)──► SETTLING
GRABBING ──OnGrabTimeout/LuaAutoDistributePackets──► SETTLING
GRABBING/SETTLING ──LuaSettleRound──► SETTLED (幂等：code=2 跳过)
SETTLED ──roundNo≥maxRounds──► GAME_END
```

### 3.3 游戏主流程

```
1. 倒计时
   playerCount≥maxPlayers (Waiting) → status=2, countdown_end_time=now+3
   调度 TimeoutTypeReady

2. 开始游戏 (HandleReadyTimeout → StartGame)
   ├── LuaTryStartGame (幂等：仅 started_at 未设置时成功)
   ├── startGameCore: 生成 sessionID, UpdateRoomSessionID, 清除所有房间超时
   ├── 广播 PushGameStart
   ├── 发布 SessionStart 事件 (含玩家列表)
   └── startFirstRound

3. 首轮 (系统发包)
   ├── initRoundAndDeduct: 创建 Round 记录, 按人头均摊扣除 roomFee (DeductForFirstRound)
   ├── sendPacketPipeline (SendScenarioFirstRound, sender="0", SenderTypeSystem)
   │   ├── packetGenerator.Generate → 生成红包金额
   │   ├── grabService.InitRoundPackets (LuaSendPacket) → 写入 packet:info, available_packets, roundState
   │   └── postSendPacketAsync: 发布 PacketCreated 事件, 设 TimeoutTypeGrab, 广播 PushRoundStart
   └── 更新 Round status=Sending

4. 后续轮 (玩家发包)
   ├── SendPacket: 获取 SendPacketLockKey, 校验 next_sender_id==userID
   ├── initLaterRoundAndDeduct: 最小抢者支付 roomFee (DeductForLaterRound)
   ├── sendPacketPipeline (SendScenarioPlayerManual, SenderTypePlayer)
   └── 同上

5. 抢红包
   ├── GrabPacket (LuaGrabPacket): 校验 phase==GRABBING, 未重复抢, 包可用
   │   ├── 标记 packet is_grabbed, DEL available, SET userGrabbed, SADD grabbers
   │   ├── 若 SCARD grabbers ≥ packet_count → phase=SETTLING, isLast=1
   │   └── 广播 PushPacketGrabbed
   ├── 若 isLast → go settleRound(roomID, roundID)
   └── 机器人抢 (LuaRobotGrabPacket): 原子随机选包+抢，Go 侧预生成 randOffset

6. 结算 (settleRound)
   ├── 获取 SettleLockKey (30s)
   ├── LuaSettleRound (幂等):
   │   ├── 构建每玩家结果, 找最小抢者(下轮发包人)
   │   ├── 更新 session_player_totals 累计抢额
   │   ├── phase=SETTLED, DEL available_packets, HDEL roomHash.current_round_id
   │   ├── 若 rewardType==Leopard 或 allSameAmount → minAmountPlayer="0" (系统发包)
   │   ├── HSET roomHash next_sender_id = minAmountPlayer
   │   └── 若 roundNo≥maxRounds → phase=GAME_END, 构建最终排名, DEL session_totals
   ├── 广播 PushRoundEnd
   ├── 发布 RoundSettle 事件
   ├── 若 isGameEnd → endGameWithOptions(ReasonNormalEnd)
   └── 否则调度 TimeoutTypeSend 给下轮发包人

7. 结束游戏 (endGameWithOptions, 统一出口)
   ├── LuaEndGame (幂等, AllowedStatus 网关):
   │   ├── status→Waiting(1), current_round=0
   │   ├── DEL next_sender_id/countdown_end_time/started_at
   │   ├── DEL seatsKey/seatOwnerKey
   │   └── 玩家转回旁观者(保留座位), 重新占据座位 bit
   ├── 为每个 ex-player 设 TimeoutTypeSeat (需重新 ready 否则被踢)
   ├── 广播房间状态
   ├── 发布 SessionEnd 事件 (含 ActualRounds/EndReason/FinalResults)
   └── gameEndCallback → RobotSchedulerService.OnGameEnd (回收机器人)
```

### 3.4 发包场景（SendScenario）

| 场景 | 值 | SenderType | SenderID | 说明 |
|------|----|-----------|----------|------|
| FirstRound | 1 | system | "0" | 首轮系统发包 |
| PlayerManual | 2 | player | userID | 玩家手动发包 |
| TimeoutForced | 3 | system_forced | "0" | 发包超时强制系统发包 |
| ResumeInterrupt | 4 | system_resume | "0" | 中断恢复后系统发包 |
| LeopardReward | 5 | system | "0" | 豹子奖励系统发包 |

### 3.5 惩罚机制

**惩罚类型**（`domain/penalty.go`）：
- `PenaltyTypeSendTimeout=1`：发包超时
- `PenaltyTypeLeaveDuringGame=2`：游戏中离开
- `PenaltyTypeDisconnectTimeout=3`：断线超时

**惩罚策略**（`PenaltyPolicy`）：
- `MaxPenaltyCount=2`：最大惩罚次数
- `ShouldKick(count) = count >= MaxPenaltyCount`
- 首次惩罚：扣 roomFee，强制系统发包（`forceSendPacketForPlayer`）
- 二次惩罚：扣 roomFee，踢人+中断+尝试替补或等待替补

**惩罚流程**（`OnSendTimeout`）：
```
ApplyPenalty(SendTimeout)
  ├── LuaHandlePenalty: 累加 penaltyCount, 记录事件, kickRequired=(count>=2)
  ├── settlementService.DeductPenaltyToPlatform(roomFee)
  ├── 若 DeductFailed → handleDeductFailure (中断游戏)
  ├── 若 KickRequired → handleKickAndReplace
  │   ├── KickPlayerAndInterrupt (Playing→Interrupted)
  │   ├── 设 TimeoutTypeReplace
  │   ├── tryAutoSubstitute (尝试替补)
  │   └── 无替补 → 广播 PushWaitReplacement
  └── 否则 → forceSendPacketForPlayer (用惩罚金额系统强制发包)
```

**替补超时**（`OnReplaceTimeout`，仅 `Interrupted` 状态）：
- `DistributePenalty`：将离开者的 roomFee 均分给剩余玩家
- `endGameWithOptions(ReasonReplacementTimeout)`

### 3.6 中断与恢复

```
中断 (Interrupted):
  玩家被踢/断线 → KickPlayerAndInterrupt → status=Interrupted
  尝试替补 (tryAutoSubstitute)
    ├── 替补成功 → shouldStartCountdown=2 → ResumeGame
    └── 无替补 → 等待 TimeoutTypeReplace → OnReplaceTimeout → 结束游戏

恢复 (ResumeGame):
  清除 TimeoutTypeReplace
  广播 PushGameResumed
  systemSendRound (SendScenarioResumeInterrupt, SenderTypeSystemResume)
```

---

## 4. 红包与算法设计

### 4.1 红包生成器（PacketGenerator）

**配置**（`algorithm/config.go`）：
```go
type Config struct {
    MinPacketAmount     int64   // 最小红包金额，默认 1 分
    StraightProbability float64 // 顺子概率，默认 0.08
    LeopardProbability  float64 // 豹子概率，默认 0.002
    RewardControl       *RewardControlConfig
}
```

**生成流程**（`Generate`）：
1. Load config 快照（`atomic.Pointer`，保证一次 Generate 内一致）
2. `validateRequest`：`TotalAmount>0`, `1≤PacketCount≤100`, `TotalAmount ≥ PacketCount*MinPacketAmount`
3. 读取房间元信息（currentRound, maxRounds, sessionID）
4. **缓存**：`redis.Get(RoundPacketsKey(roundID))`，命中则返回（幂等）
5. `rewardController.DetermineRewardType` → (rewardType, triggerType)
6. 按 rewardType 分发：
   - `Straight` → `straightGen.Generate`，失败回退 `generateNormalPackets`
   - `Leopard` → `leopardGen.Generate`，失败回退
   - `None` → `generateNormalPackets`
7. `SetNX` 缓存结果（1h TTL），竞争失败则重读缓存

### 4.2 普通红包（generateNormalPackets）

算法（`packet_generator.go:133-186`）：
1. `calculateDynamicMinAmount`：动态计算最小包金额，范围 `[configMin, min(avg/3, (total-(count-1))/count)]`
2. 随机选 `minIndex` 放最小金额
3. 剩余金额在 `count-1` 个包间随机分配，每包 ≥ `minAmount+1`
4. Fisher-Yates shuffle（`crypto/rand`）

### 4.3 顺子红包（StraightGenerator，`algorithm/straight.go`）

"顺子" = 红包金额的整数部分（元）构成连续序列，如 1,2,3,4,5 元。

生成算法：
1. `minIntSum = (1+n)*n/2`（1..n 元的最小和）
2. `startInt = (totalYuan - minIntSum)/n`（≥1，起始元）
3. 整数部分：`(startInt+i)*100` for i in 0..n-1
4. 余数（分）随机分配到各包
5. **奖励金额 = TotalAmount × 1**

校验（`Check`）：提取每包 `amount/100` 整数部分，排序后检查 `intParts[i] == intParts[i-1]+1`。

### 4.4 豹子红包（LeopardGenerator，`algorithm/leopard.go`）

"豹子" = 所有红包金额相等。

生成算法：
1. 要求 `total % count == 0`（均分）
2. `baseAmount = total/count`，要求 `≥ MinPacketAmount`
3. 所有包 = `baseAmount`
4. **奖励金额 = TotalAmount × 10**（高额奖励）

### 4.5 奖励控制（RewardController，`algorithm/reward_controller.go`）

**`DetermineRewardType(roomID, sessionID, currentRoundNo, maxRounds) → (rewardType, triggerType)`**：

```
1. 查询 room_config (RoomConfigs[roomID])
2. 若 GuaranteeEnabled → checkGuarantee
   ├── 顺子保底: Redis 检查 RewardCycleStraightKey
   │   ├── 本 session 未中过顺子 + shouldTriggerGuarantee(remainingRounds) → 触发
   │   └── Set key=1 (24h TTL) 标记已触发
   └── 豹子保底: 同上
3. 若 ProbabilityEnabled && isProbabilityAllowed → checkProbability
   ├── isProbabilityAllowed: GlobalSwitchEnabled && (ProfitRatioThreshold==0 || 当前利润率≥阈值)
   └── checkProbability: 随机数 < LeopardProbability → Leopard; 否则 < StraightProbability → Straight
4. 否则 → (None, 0)
```

**保底触发概率**（`shouldTriggerGuarantee`）：
- `remainingRounds ≤ 1`：**必定触发**（兜底）
- 否则：`1/remainingRounds` 概率触发（均匀分散到剩余轮次）

**利润率计算**（`GetCurrentProfitRatio`）：
- 读 Redis hash `profit:daily:{today}` 的 `total_bet/total_win/total_reward`
- `profit = total_bet - total_win - total_reward`
- `ratio = profit / total_bet`

**触发类型**：
- `TriggerTypeGuarantee=1`：保底触发
- `TriggerTypeProbability=2`：概率触发

### 4.6 奖励记录

`model.SpecialReward`（`model/reward.go`）记录每次奖励触发：
- `RewardType`：1=顺子, 2=豹子
- `TriggerType`：1=保底, 2=概率
- `Amount`：每玩家奖励金额
- `TotalReward`：平台总奖励支出 = `Amount × PlayerCount`

---

## 5. 机器人系统设计

### 5.1 机器人配置

**`common/config/config.go` RobotConfig**：
```go
type RobotConfig struct {
    Enabled   bool
    Scheduler SchedulerConfig  // 扫描间隔/锁TTL/池配置
    Behavior  BehaviorConfig   // 行为延迟/概率
    Account   AccountConfig    // 余额/同步配置
}
```

**SchedulerConfig 关键字段**：
| 字段 | 默认 | 说明 |
|------|------|------|
| ScanInterval | 5s | 扫描房间间隔 |
| MinRealPlayers | 2 | 最少真实玩家才配机器人 |
| MaxRobotsPerRoom | 3 | 每房最多机器人数 |
| RobotAssignLockTTL | 10s | 单机器人分配锁 |
| RoomAssignLockTTL | 30s | 房间级限流锁 |
| ReserveCount | 5 | 池储备阈值 |

**BehaviorConfig 关键字段**：
| 字段 | 默认 | 说明 |
|------|------|------|
| SeatDelayMin/Max | 2s/5s | 入座延迟 |
| GrabDelayMin/Max | 1s/8s | 抢包延迟 |
| SendDelayMin/Max | 2s/5s | 发包延迟 |
| GrabSkipProb | 0 | 跳过抢包概率 |
| ActionRetryMax | 2 | 动作重试次数 |

### 5.2 机器人账号模型

**`model.RobotAccount`**（`model/robot_account.go`）— MySQL `robot_accounts` 表：

| 字段 | 说明 |
|------|------|
| UserID | 关联 users.id |
| Status | `Inactive=0, Idle=1, InGame=2, Disabled=3` |
| VirtualBalance | 虚拟余额（分） |
| TotalVirtualDebit/Credit | 累计虚拟扣/加 |
| MinRoomFee/MaxRoomFee | 可加入的房间费范围 |
| TotalGames/TotalProfit | 统计 |

### 5.3 机器人池与调度

**Redis 数据结构**：
| Key | 类型 | 说明 |
|-----|------|------|
| `robot:pool:available` | Set | 可用机器人 ID 集合 |
| `robot:room:{roomID}` | Set | 房间内机器人集合 |
| `robot:assign:{userID}` | String | 单机器人分配锁（UUID token） |
| `robot:room_assign:{roomID}` | String | 房间级限流锁 |
| `robot:virtual_balance:{userID}` | String | 虚拟余额缓存 |
| `robot:virtual_balance:dirty` | Set | 待同步 DB 的脏余额集合 |
| `robot:scheduler:active` | Set | 活跃机器人集合 |
| `robot:user_ids` | Set | 全局机器人 ID 集合 |

**分配算法**（`assignRobotsToRoom`）：
```
1. 获取 RoomAssignLock (房间级限流, 30s TTL)
2. 回收僵尸机器人 (在房间集合但未入座)
3. needed = MaxPlayers(5) - SeatedCount, clamp to MaxRobotsPerRoom - existingRobots
4. 循环 needed 次:
   a. GetAvailableRobot(roomFee) — DB 查询 + 余额检查 (balanceRequired = roomFee/5 + roomFee*9)
   b. AcquireAssignLock(robotUserID, token) — 单机器人锁, 10s TTL
   c. MarkRobotInGame (Status→InGame, 移出 available pool)
   d. AddRobotToRoom + AddToActiveSet
   e. robotPlayer.JoinAndReady — 失败则回滚 (MarkRobotIdle, RemoveRobotFromRoom)
```

**分布式锁安全**（Task 26 修复）：
- `AcquireAssignLock` 生成 UUID token 作为 SetNX value
- `ReleaseAssignLock` 用 Lua 脚本 `if GET key == token then DEL key end`，防止 TTL 过期后误删他人锁

### 5.4 机器人行为引擎

**`RobotBehaviorEngine`**（`application/robot_behavior.go`）实现 `RobotActionScheduler` 接口，通过 `TimeoutScheduler` 调度延迟动作。

**动作数据编码**：
- 标准：`"robotUserID:action:uuid:retryCount"`（seat/ready/send/leave）
- Grab（5 段）：`"robotUserID:grab:roundID:uuid:retryCount"`（保留 roundID 以跨重试）

**动作分发**（`HandleRobotTimeout`）：
| Action | 调用 | 失败重试 |
|--------|------|---------|
| seat | `robotPlayer.SelectSeat` | 是（≤ActionRetryMax） |
| ready | `robotPlayer.Ready` | 是 |
| grab | `robotPlayer.GrabPacket` | 是（保留 roundID） |
| send | `robotPlayer.SendPacket` | 否 |
| leave | `handleLeaveAction` | 否 |

**事件回调**：
- `OnPacketCreated(roomID, roundID)`：为每个房间机器人按 `(1-GrabSkipProb)` 概率调度 grab
- `OnRoundSettle(roomID, minPlayerID, isGameEnd)`：若未结束且 minPlayerID 是机器人，调度 send
- `OnGameEnd(roomID)`：立即 `LeaveRoomNow` 所有房间机器人

### 5.5 机器人生命周期

```
创建 (BatchCreateRobotsWithBalance)
  ├── DB: RobotAccount{Status:Idle, VirtualBalance:initial}
  ├── Redis: AddToRobotSet + SetBalance + AddToAvailablePool
  └── Status = Idle

分配 (scanRooms → assignRobotsToRoom)
  ├── GetAvailableRobot + AcquireAssignLock
  ├── MarkRobotInGame (Idle→InGame, 移出 available pool)
  ├── AddRobotToRoom + AddToActiveSet
  └── JoinAndReady → 调度 seat → SelectSeat → 调度 ready → Ready

游戏内行为
  ├── OnPacketCreated → 调度 grab → GrabPacket
  ├── OnRoundSettle → 调度 send → SendPacket
  └── 所有动作经 TimeoutScheduler 延迟执行

回收 (OnGameEnd / cleanupEndedRooms)
  ├── LeaveRoomNow (同步)
  ├── handleLeaveAction (幂等):
  │   ├── 检查是否仍在房间机器人集合
  │   ├── LeaveRoom (容忍 CodeNotInRoom)
  │   └── RemoveRobotFromRoom + RemoveFromActiveSet + MarkRobotIdle
  └── Status → Idle, 重新加入 available pool

僵尸回收 (recycleZombieRobots)
  └── 在房间集合但未入座的机器人 → MarkRobotIdle + LeaveRoom
```

### 5.6 虚拟余额管理

**`VirtualBalanceService`**（`redis/virtual_balance.go`）：
- `Credit(userID, amount)`：`IncrBy` 余额 + `SAdd` dirty 集合
- `GetBalance(userID)`：Redis 优先，miss 时从 DB 加载回填
- `SyncToDB()`：**SPOP 逐个弹出** dirty 成员（原子，无竞态），更新 DB，失败回 SAdd

**`VirtualBalanceSyncScheduler`**（`scheduler/virtual_balance_sync.go`）：
- 定时驱动 `SyncToDB`，默认 30s 间隔
- 每次 `SyncToDB` 包 10s 超时（`context.WithTimeout`）
- `defer recover` 防崩溃，`Stop` 等 WaitGroup（10s 超时）

---

## 6. 调度器设计

### 6.1 超时调度器（TimeoutScheduler）

**`scheduler/timeout_scheduler.go`** — Redis ZSET 延迟任务队列，每种超时类型一个 ZSET，score=Unix 过期时间戳。

**超时类型**：
| 类型 | 默认时长 | 检查间隔 | 说明 |
|------|---------|---------|------|
| `TimeoutTypeSeat` | 30s | 1s | 旁观者选座后未 ready 踢出 |
| `TimeoutTypeReady` | 3s | 500ms | 倒计时结束 → StartGame |
| `TimeoutTypeGrab` | 20s | 500ms | 抢包超时 → AutoDistribute |
| `TimeoutTypeSend` | 30s | 1s | 发包超时 → OnSendTimeout |
| `TimeoutTypeReplace` | 30s | 1s | 替补超时 → OnReplaceTimeout |
| `TimeoutTypeRobot` | 5s | 500ms | 机器人动作调度 |

**核心方法**：
- `SetTimeout(type, roomID, data, customDuration...)`：`ZAdd` member=`"roomID:data"`
- `ClearTimeout(type, roomID, data)`：`ZRem`
- `ClearRoomTimeouts(type, roomID)`：`ZRangeByScore` + 前缀匹配 `roomID+":"` + `ZRem`

**Checker 机制**：
- 每种类型一个 `runChecker` goroutine，按 `CheckInterval` tick
- `ZRangeByScore(-inf, now)` 取到期任务，`ZRem` 认领（`removed==0` 则被其他实例取走，跳过）
- Handler 在独立 goroutine 执行，`handlerWg` 跟踪，`defer recover` 防崩溃
- `Stop` 等 `handlerWg`（10s 超时兜底）

### 6.2 结算调度器（Settlement Schedulers）

位于 `settlement/scheduler/`，均 Redis ZSET 驱动：

| 调度器 | 职责 |
|--------|------|
| `CreditRetryScheduler` | 平台加币失败重试 |
| `RefundProcessScheduler` | 退款处理 |
| `SettlementCheckScheduler` | 结算巡检（异常恢复） |
| `GameSettleRetryScheduler` | 游戏级结算重试 |
| `GameSettleTimeoutScheduler` | 游戏级结算超时处理 |

### 6.3 虚拟余额同步调度器

见 [5.6 虚拟余额管理](#56-虚拟余额管理)。

---

## 7. 结算系统设计

### 7.1 结算服务组成

`settlement/service/` 提供：

| 服务 | 职责 |
|------|------|
| `DeductService` | 平台扣币（首轮流摊/后续轮最小者/系统包/惩罚） |
| `RefundService` | 退款处理 |
| `RewardSettler` | 奖励结算（顺子/豹子平台支出） |
| `GameSettleService` | 游戏级结算（SessionEnd 时统一结算） |
| `SettlementService` | 聚合服务，对外暴露 `SettleRound`/`SettleGame`/`DistributePenaltyFromPlatform` 等 |
| `BalanceService` | 余额查询 |
| `BillManager` | 账单记录管理 |
| `ExceptionManager` | 异常记录管理 |
| `PlatformCallManager` | 平台调用管理 |
| `CreditRetryService` | 加币重试 |
| `RobotChecker` | 机器人校验（结算时判断是否机器人，机器人走虚拟余额） |

### 7.2 扣费场景

| 场景 | 触发 | 扣费方 | 金额 |
|------|------|-------|------|
| FirstRoundShare | 首轮开始 | 所有玩家均摊 | roomFee 每人 |
| LaterRoundMin | 后续轮开始 | 最小抢者 | roomFee |
| SystemPacket | 系统发包 | 平台承担 | roomFee |
| Penalty | 发包超时/离开 | 违规者 | roomFee |

### 7.3 结算流程

**轮次结算**（`SettleRound`，由 `GameEventConsumer.handleRoundSettle` 调用）：
1. 幂等检查：round 已 Credited 则返回 nil
2. `creditRound`：为每个抢包玩家创建 bill（bill_type=3 grab 收入）
3. `SettleReward`：若触发奖励，为每个玩家创建奖励 bill（bill_type=10/11）
4. 标记 round 为 Credited（credit + reward 全部成功后）

**游戏结算**（`SettleGame`，由 `handleSessionEnd` 调用）：
1. 幂等检查：所有 round 已结算则返回 nil
2. 执行游戏级平台结算

**幂等保障**：
- `SettleRound` 内部 `RoundStatusCredited` 检查
- `SettleReward` 入口 `GetBillByRoundTypeAndUser` 查重
- `SettleGame` 内部 `allSettled` 检查
- Kafka consumer 层 `FirstOrCreate` grab_record + session 预检查

### 7.4 账单类型（bill_type）

| 类型 | 说明 | 金额方向 |
|------|------|---------|
| 2 | 首轮费 | 负 |
| 3 | 抢包收入 | 正 |
| 4 | 发包支出 | 负 |
| 8 | 惩罚 | 负 |
| 10, 11 | 奖励收入 | 正 |
| 12 | 排除项（历史聚合不计算） | — |

---

## 8. 消息与事件设计

### 8.1 Kafka Topic

| Topic | 用途 |
|-------|------|
| `cashparty.game.events` | 游戏事件（SessionStart/PacketCreated/RoundSettle/SessionEnd） |
| `cashparty.room.events` | 房间事件（join/leave/seat/ready 等） |
| `cashparty.gateway.broadcast` | 广播消息（Gateway 消费推 WebSocket） |

### 8.2 游戏事件

**`GameEvent`**（`domain/events.go:98-106`）：
```go
type GameEvent struct {
    EventType GameEventType
    RoomID, SessionID, RoundID string
    Timestamp int64
    Data      interface{}
    TraceID   string  // 雪花 ID，幂等去重用
}
```

**事件类型与数据**：
| 事件 | Data 结构 | 消费侧动作 |
|------|-----------|-----------|
| `session_start` | SessionStartData{RoomNo, ConfigID, RoomFee, MaxRounds, Players[]} | 创建 GameSession + SessionPlayers |
| `packet_created` | PacketCreatedData{RoundID, RoundNo, SenderID, SenderType, TotalAmount, Commission, Packets[]} | 更新 Round(status=Sending) + 创建 Packets |
| `round_settle` | RoundSettleData{RoundNo, SenderID, TotalAmount, Results[], MinPlayerID, IsGameEnd, RewardType, RewardAmount, FinalResults[]} | 更新 Round(Ended) + 创建 GrabRecords + 更新 SessionPlayer 统计 + SettleRound |
| `session_end` | SessionEndData{ActualRounds, EndReason, FinalResults[]} | 更新 Session(Completed) + 更新 total_profit + SettleGame |

**发布**（`GameEventPublisher`）：
- Kafka key = `{roomID}_{sessionID}`（同房同分区保证顺序）
- TraceID 必填，否则返回 error

**消费**（`GameEventConsumer`）：
- 幂等：`tryAcquire(traceID)` Redis SetNX，TTL 7 天，失败释放 key 允许重试
- 事务：`db.Transaction` 包裹所有 DB 写 + `SettleRound`/`SettleGame` 调用，失败回滚
- 后置回调：`robotBehaviorEngine.OnPacketCreated` / `OnRoundSettle`

### 8.3 房间事件

**`RoomEvent`**（`domain/events.go:29-36`）：
```go
type RoomEvent struct {
    EventID   string  // UUID
    EventType RoomEventType
    RoomID, UserID string
    Payload   interface{}
    OccurredAt time.Time
}
```

**事件类型**（14 种）：SpectatorJoin/Leave, PlayerJoin/Leave, StatusChange, SeatSelect/Cancel, PlayerReady, SpectatorKick, PlayerDisconnect/Reconnect, QueueJoin/Leave, Substitute。

**消费**（`RoomEventConsumer`）：
- 幂等：`tryAcquire(eventID)` Redis SetNX，TTL 24h
- **唯一职责**：`syncRoomCounts` — 从 Redis `HLen` 读 player/spectator 数量覆盖到 DB `rooms` 表
- 幂等快照同步：与顺序无关，最终一致
- 仅处理 5 种事件（SpectatorJoin/Leave, PlayerReady, SeatCancel, SpectatorKick），其余 default 跳过

### 8.4 事件流（完整链路）

```
游戏操作 (Redis Lua 原子变更)
    │
    ├── 同步: 广播 Push 消息 → Kafka gateway.broadcast → Gateway → WebSocket → 客户端
    │
    └── 异步: 发布 GameEvent → Kafka game.events
                │
                └── GameEventConsumer
                      ├── 幂等检查 (SetNX traceID)
                      ├── DB 事务 (Round/GrabRecord/SessionPlayer/Session 更新)
                      ├── SettleRound/SettleGame (平台结算)
                      └── 机器人行为回调 (OnPacketCreated/OnRoundSettle)
```

---

## 9. 广播设计

### 9.1 广播架构

```
Application Service
    │
    ▼
GameBroadcaster (game/infrastructure/broadcast/)  ── 包装层, Warn 日志, fire-and-forget
    │
    ▼
Broadcaster 接口 (common/broadcast/)
    │
    ├── KafkaBroadcaster ──► Kafka topic: cashparty.gateway.broadcast
    │
    └── RedisPubSubBroadcaster ──► Redis channel: cashparty:gateway:broadcast
```

后端选择由 `cfg.Broadcast.Mode` 决定（`ModeKafka` 或 `ModeRedisPubSub`）。

### 9.2 广播消息结构

**`BroadcastMessage`**（`common/message/broadcast.go`）：
```go
type BroadcastMessage struct {
    TargetType string          // "room" 或 "user"
    TargetID   string          // roomID (room 模式)
    UserIDs    []string        // userIDs (user 模式)
    ExcludeID  string          // 排除的用户
    Event      string          // 事件名
    Data       json.RawMessage // 载荷
}
```

### 9.3 Push 消息类型

| 事件常量 | 说明 |
|---------|------|
| `PushRoomState` | 房间状态变更 |
| `PushSpectatorJoined/Left` | 旁观者加入/离开 |
| `PushPlayerJoined/Left` | 玩家加入/离开 |
| `PushSeatSelected/Cancelled` | 选座/取消座位 |
| `PushPlayerReady` | 玩家 ready |
| `PushPlayerDisconnected/Reconnected` | 断线/重连 |
| `PushGameStart/Resumed` | 游戏开始/恢复 |
| `PushCountdownStart` | 倒计时开始 |
| `PushRoundStart` | 轮次开始（含红包列表） |
| `PushPacketGrabbed` | 抢包通知 |
| `PushRoundEnd` | 轮次结束（含结果/排名） |
| `PushAutoDistribute` | 自动分配剩余包 |
| `PushPenalty` | 惩罚通知 |
| `PushWaitReplacement` | 等待替补 |
| `PushGameInterrupted` | 游戏中断 |
| `PushSubstitute` | 替补入场 |
| `PushKicked` | 被踢通知 |
| `PushDequeued` | 被移出队列 |

---

## 10. 数据持久化设计

### 10.1 DB 表清单

| 表 | 模型 | 说明 |
|----|------|------|
| `rooms` | model.Room | 房间（MySQL 投影） |
| `room_configs` | model.RoomConfig | 房间配置 |
| `game_sessions` | model.GameSession | 游戏会话 |
| `session_players` | model.SessionPlayer | 会话玩家统计（唯一索引 sessionID+userID） |
| `rounds` | model.Round | 轮次 |
| `round_grab_records` | model.RoundGrabRecord | 抢包记录 |
| `packets` | model.Packet | 红包 |
| `special_rewards` | model.SpecialReward | 奖励记录 |
| `users` | model.User | 用户 |
| `robot_accounts` | model.RobotAccount | 机器人账号 |
| `bill_record` | settlement model | 账单记录（结算层） |
| `round_settlements` | settlement model | 轮次结算 |
| `refund_audits` | settlement model | 退款审计 |
| `exception_records` | settlement model | 异常记录 |
| `platform_call_logs` | settlement model | 平台调用日志 |

### 10.2 仓储层设计

**`DBRepositoryImpl`**（`mysql/db_repository.go`）聚合 6 个子仓储：
- `RoomDBRepo`, `SessionDBRepo`, `UserDBRepo`, `RoomConfigDBRepo`, `RoundDBRepo`, `HistoryDBRepo`
- 构造时 eager 初始化所有子仓储（并发安全）
- `WithTransaction(fn)` 提供 `Transaction` 接口（含 RoomDBRepo/SessionDBRepo/UserDBRepo/RoundDBRepo）

**`RoomRepository`**（Redis 实现，`redis/repository.go`）— 与 DB 仓储区分：
- 房间热状态操作（Meta/Players/Spectators/Seats/Queue）
- 所有操作通过 Lua 脚本原子执行

### 10.3 事务管理

**模式 1 — 应用层 `DBRepository.WithTransaction`**：
```go
dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
    tx.RoomDBRepo().UpdateRoom(...)
    tx.RoundDBRepo().UpdateRoundStatus(...)
    return nil
})
```

**模式 2 — Consumer 直接 `db.Transaction`**：
```go
c.db.Transaction(func(tx *gorm.DB) error {
    // 更新 Round, GrabRecord, SessionPlayer
    // 调用 settlementService.SettleRound (独立 DB 连接, 不共享事务)
    return err  // 失败回滚 Round/GrabRecord/SessionPlayer
})
```

> 注：`SettleRound`/`SettleGame` 用独立 DB 句柄开自己的事务，与 consumer 外层事务不共享连接。整体失败时两边都回滚，重试安全（靠各自幂等）。

### 10.4 历史查询

**双路查询**：
- **session_players 路径**（legacy）：简单聚合，`profit = total_grab - total_send`
- **bill_record 路径**（对账级，权威）：`SUM(CASE WHEN bill_type IN (2,4,8) AND amount<0 THEN ABS(amount) END)` 为总投注，`SUM(CASE WHEN bill_type IN (3,10,11) AND amount>0 THEN amount END)` 为总收入，`profit = income - bet`

---

## 11. Redis Key 设计

所有 key 前缀 `cashparty:`。

### 11.1 房间相关

| Key 模式 | 类型 | TTL | 说明 |
|---------|------|-----|------|
| `room:hash:{roomID}` | Hash | 24h | 房间元数据 |
| `room:players:{roomID}` | Hash | - | 玩家集合（userID→JSON） |
| `room:spectators:{roomID}` | Hash | - | 旁观者集合 |
| `room:seats:{roomID}` | Bitmap | - | 座位占用（bit=seatNo） |
| `room:seat:owner:{roomID}` | Hash | - | 座位→userID |
| `room:queue:{roomID}` | SortedSet | - | 排队队列（score=时间戳） |
| `player:room:{userID}` | String | 24h | 用户当前所在房间（单房间约束） |
| `room:no_to_id:{roomNo}` | String | - | 房间号→房间 ID 映射 |

### 11.2 游戏相关

| Key 模式 | 类型 | TTL | 说明 |
|---------|------|-----|------|
| `round:state:{roundID}` | Hash | - | 轮次状态（phase/sender/amounts 等） |
| `round:available_packets:{roundID}` | List | - | 可抢包 ID 列表 |
| `round:grabbers:{roundID}` | Set | - | 已抢者集合 |
| `round:grabbed:{roundID}:{userID}` | String | 24h | 防重复抢标记 |
| `packet:info:{packetID}` | String | 24h | 红包 JSON |
| `session:{sessionID}:player:totals` | Hash | - | 会话累计抢额 |

### 11.3 锁

| Key 模式 | TTL | 说明 |
|---------|-----|------|
| `lock:game_start:{roomID}` | - | 开始游戏锁 |
| `lock:settle:{roomID}:{roundID}` | 30s | 结算锁 |
| `lock:game_end:{roomID}` | - | 结束游戏锁 |
| `lock:send_packet:{roomID}:{userID}` | 10s | 发包锁 |
| `lock:replace_timeout:{roomID}:{userID}` | 30s | 替补超时锁 |

### 11.4 机器人相关

| Key 模式 | 类型 | 说明 |
|---------|------|------|
| `robot:pool:available` | Set | 可用机器人池 |
| `robot:room:{roomID}` | Set | 房间机器人集合 |
| `robot:assign:{userID}` | String | 分配锁（UUID token） |
| `robot:room_assign:{roomID}` | String | 房间限流锁 |
| `robot:virtual_balance:{userID}` | String | 虚拟余额缓存 |
| `robot:virtual_balance:dirty` | Set | 待同步 DB 脏集合 |
| `robot:user_ids` | Set | 全局机器人 ID |

### 11.5 其他

| Key 模式 | 类型 | TTL | 说明 |
|---------|------|-----|------|
| `penalty:count:{roomID}:{userID}` | String | 24h | 惩罚计数 |
| `penalty:record:{roomID}:{userID}` | List | 24h | 惩罚记录 |
| `reward:cycle:{roomID}:{sessionID}:straight/leopard` | String | 24h | 奖励保底标记 |
| `profit:daily:{date}` | Hash | 7d | 每日利润统计 |
| `game:event:processed:{traceID}` | String | 7d | 事件幂等 |
| `room:event:processed:{eventID}` | String | 24h | 事件幂等 |
| `timeout:{type}` | SortedSet | - | 超时任务队列 |

---

## 12. Lua 脚本清单

### 12.1 房间/座位/队列（`lua_scripts.go`）

| 脚本 | 作用 |
|------|------|
| `LuaJoinAsSpectator` | 加入旁观者，容量检查，设 userRoomKey，status 0→1 |
| `LuaSelectSeat` | 旁观者手动选座，SETBIT + HSET seatOwner |
| `LuaCancelSeat` | 释放座位，仅 Waiting/Interrupted 允许 |
| `LuaLeaveRoom` | 旁观者离开（玩家拒绝），清座位 + userRoom |
| `LuaKickPlayerAndInterrupt` | 踢玩家，Playing→Interrupted，释放座位 |
| `LuaTryStartGame` | 幂等开始游戏，仅 status==2 且倒计时结束 |
| `LuaPlayerReady` | 旁观者→玩家 + ready，触发倒计时或恢复 |
| `LuaHandleSeatTimeout` | 座位超时踢旁观者 |
| `LuaAutoSeatAndReady` | 原子自动选座 + ready |
| `LuaEnqueue` | 入队（拒绝机器人/玩家/重复） |
| `LuaDequeue` | 出队 |
| `LuaAutoSubstitute` | 从队列弹出首位替补空座位 |

### 12.2 游戏/红包/惩罚（`lua_game.go`）

| 脚本 | 作用 |
|------|------|
| `LuaSendPacket` | 统一发包入口（所有场景），创建 packet keys，设 roundState，phase=GRABBING |
| `LuaGrabPacket` | 玩家抢指定包，校验 phase/时间/重复，标记包，最后一人触发 SETTLING |
| `LuaRobotGrabPacket` | 机器人原子随机选包+抢（Go 侧预生成 randOffset，无 math.random） |
| `LuaAutoDistributePackets` | 抢包超时自动分配剩余包给未抢者 |
| `LuaSettleRound` | 幂等结算，构建结果，找最小抢者，更新累计，phase=SETTLED/GAME_END |
| `LuaEndGame` | 幂等结束，status→Waiting，重置轮次，玩家→旁观者（保留座位） |
| `LuaHandlePenalty` | 累加惩罚计数，记录事件，判断 kickRequired |
| `LuaDistributePenalty` | 均分惩罚金额给剩余玩家 |

---

## 13. gRPC 接口设计

### 13.1 服务定义

`GenericServiceServer`（`server/generic_service.go`）实现 `commonPb.GenericServiceServer`。

**主要方法**：
- `Forward(ctx, *ForwardRequest) (*ForwardResponse, error)`：统一命令分发
- `SaveUser(ctx, *SaveUserRequest) (*SaveUserResponse, error)`：保存用户

### 13.2 命令清单

| Cmd | Handler | App Service 方法 |
|-----|---------|-----------------|
| `join_room` | handleJoinRoom | roomAppSvc.JoinAndAutoSeat |
| `auto_match` | handleAutoMatch | roomAppSvc.AutoMatchAndJoin |
| `leave_room` | handleLeaveRoom | roomAppSvc.LeaveRoom |
| `room_state` | handleRoomState | roomAppSvc.GetRoomState |
| `select_seat` | handleSelectSeat | seatAppSvc.SelectSeat |
| `cancel_seat` | handleCancelSeat | seatAppSvc.CancelSeat |
| `player_ready` | handlePlayerReady | seatAppSvc.PlayerReady |
| `send_packet` | handleSendPacket | gameAppSvc.SendPacket |
| `grab_packet` | handleGrabPacket | gameAppSvc.GrabPacket（含限流） |
| `get_room_list` | handleGetRoomList | roomAppSvc.GetRoomList |
| `get_room_type_list` | handleGetRoomTypeList | roomAppSvc.GetRoomTypeList |
| `reconnect` | handleReconnect | roomAppSvc.HandleReconnect |
| `get_user_balance` | handleGetUserBalance | balanceSvc + userSvc |
| `enqueue` | handleEnqueue | roomAppSvc.Enqueue |
| `dequeue` | handleDequeue | roomAppSvc.Dequeue |
| `get_player_history` | handleGetPlayerHistory | historySvc.GetPlayerHistory |
| `get_player_session_detail` | handleGetPlayerSessionDetail | historySvc.GetPlayerSessionDetail |
| `get_player_stats` | handleGetPlayerStats | historySvc.GetPlayerStats |

### 13.3 错误处理约定

- 业务错误不返回 gRPC error，编码在 `ForwardResponse.Code` 中
- `handleError` 提取 `GameError` code/msg，否则 `CodeSystemError`
- 防止内部错误泄漏给客户端
- `recoveryUnaryInterceptor` 兜底 panic → `codes.Internal`
- `loggingUnaryInterceptor` 记录请求耗时

### 13.4 gRPC 服务器配置

- Keepalive：`MaxConnectionIdle=15min`, `Time=5min`, `Timeout=1min`
- 最大消息：10MB
- `GracefulStop` 30s 超时后强制 `Stop()`
- `Start` 用 100ms ready channel 检测立即失败

---

## 14. 启动与生命周期

### 14.1 启动顺序

`NewApplicationWithConfig`（`bootstrap/app.go`）：
1. Nacos 配置加载（主配置 + 算法配置）
2. Logger 初始化
3. ID 生成器初始化（雪花）
4. Redis 客户端
5. 分布式锁初始化（`lock.InitLocker(redis)`）
6. MySQL DB
7. Kafka Producer
8. Platform Client
9. 结算服务组件（BillManager/TraceIDGenerator/DeductService/RefundService/RewardSettler/GameSettleService/SettlementService）
10. 算法配置转换 + PacketGenerator
11. DI Container 创建
12. `InitAppServices`（构造应用服务 + 打破循环依赖 + 注册超时 handler + 初始化结算调度器 + 初始化机器人服务）

`Start`：
1. 创建 RoomEventConsumer（Kafka group `game-room-events-{nodeID}`）
2. 创建 GameEventConsumer（Kafka group `game-events-{nodeID}`）
3. `StartSchedulers`（Timeout + 结算调度器 + 机器人调度器 + 虚拟余额同步）
4. 启动 Kafka consumer goroutines
5. 创建 gRPC server
6. Nacos 服务注册 + 算法配置热更新监听
7. gRPC server Start

### 14.2 循环依赖打破

| 关系 | 打破方式 |
|------|---------|
| RoomAppService ↔ GameAppService | `GameAppService.SetRoomAppService` setter |
| SeatAppService ↔ RoomAppService | `SeatAppService.SetRoomAppService` setter |
| RoomAppService → GameAppService.ResumeGame | `RoomAppService.SetResumeGameCallback` 注入回调 |
| GameAppService → RobotSchedulerService.OnGameEnd | `GameAppService.SetGameEndCallback` 注入回调 |
| RobotPlayer ↔ RobotBehaviorEngine | `RobotPlayer.SetBehaviorEngine` setter |
| RobotSchedulerService ↔ RobotBehaviorEngine | `RobotSchedulerService.SetBehaviorEngine` setter |

### 14.3 优雅关闭

`Stop` 顺序：
1. `cancel context`（通知所有 goroutine）
2. `Container.Stop()`：
   - VirtualBalanceSyncScheduler.Stop（10s 超时）
   - RobotSchedulerService.Stop
   - TimeoutScheduler.Stop（等 handlerWg，10s 超时）
   - 结算调度器 Stop
3. `grpcServer.Stop`（GracefulStop 30s 超时）
4. nacos.Close()
5. KafkaProducer.Close()
6. Redis.Close()

### 14.4 配置热更新

- Nacos 监听算法配置变更 → `PacketGenerator.UpdateConfig`（`atomic.Pointer.Store` 原子替换 config + 重建 generators/controller）
- 读侧 `Generate` 每次调用 `atomic.Load` 拿一致快照

---

## 附录：关键设计约束

1. **所有异步 goroutine 须用 app context + recover**（部分已实现，见 `GAME_SERVICE_BUG_ANALYSIS.md` 问题 1）
2. **Kafka 消费幂等用 SetNX 原子抢占**，Redis 故障 fail-open + 业务侧幂等兜底
3. **SettleReward 失败须上抛 error** 触发事务回滚，creditRound 标 Credited 须在 credit + reward 全部成功后
4. **机器人分配锁用 UUID token + Lua 释放**，防止 TTL 过期后误删他人锁
5. **PacketGenerator config/generator 字段用 atomic.Pointer**，防止 nacos 配置更新时的数据竞争
6. **Repository 聚合对象 eager 初始化**，构造后字段只读，无锁并发安全
7. **Lua 脚本禁用 math.random**，随机性由 Go 侧 `crypto/rand` 提供，保证主从复制一致
8. **广播失败仅 Warn 日志**，不阻塞主流程，玩家可重连拉状态恢复

---

## 15. 抢红包服务（GrabService）详细设计

> 代码：`game/application/grab_service.go`
> GrabService 是抢红包的"门面层"，所有抢包/发包的核心逻辑封装在 Redis Lua 脚本中，Go 侧仅做参数装配与返回值解析。

### 15.1 服务结构

```go
type GrabService struct {
    redis       *cRedis.Client
    grabTimeout int64  // 抢红包超时（秒），传给 Lua 校验
    sendTimeout int64  // 发包超时（秒）
}
```

构造函数 `NewGrabService(redis, grabTimeout, sendTimeout time.Duration)` 将 Duration 转秒存储。

### 15.2 GrabPacket（玩家主动抢包）

```go
func (s *GrabService) GrabPacket(ctx, roomID, roundID, userID, packetID string) (*domain.GrabResult, error)
```

**5 个 Redis Key**：
- `RoundAvailablePacketsKey(roundID)` — 可用红包列表
- `UserGrabbedKey(roundID, userID)` — 玩家是否已抢标记
- `RoundGrabbersKey(roundID)` — 本轮抢包人集合
- `RoundStateKey(roundID)` — 回合状态
- `RoomPlayersKey(roomID)` — 房间玩家集合

**6 个 ARG**：userID、当前时间戳、grabTimeout、roomID、固定字符串 `"cashparty"`、packetID（玩家指定要抢哪个红包）。

**返回值映射**（`LuaGrabPacket`）：
```
res[0] = code
res[1] = packetID
res[2] = amount
res[3] = position
res[4] = (跳过, 预留/剩余红包数)
res[5] = isLast (1=最后一个抢包,触发结算)
```

Go 侧通过 `domain.MapLuaError(code)` 将 Lua 错误码映射为业务 error。位置分配（Position）和 isLast 判断都在 Lua 侧完成，Go 仅透传。

### 15.3 RobotGrabPacket（机器人抢包）

```go
func (s *GrabService) RobotGrabPacket(ctx, roomID, roundID, userID string) (*domain.GrabResult, error)
```

**与 GrabPacket 的关键差异**：
- **不传 packetID**：机器人不指定具体红包，由 Lua 随机选
- **预生成随机数**：第 6 个 ARG 是 `rand.Intn(1000)`
- 调用 `LuaRobotGrabPacket`，原子完成"随机选包+抢包"

**为什么预生成随机数**：Redis Lua 禁用 `math.random`（会导致主从复制不一致），所以在 Go 侧生成随机数传入，Lua 侧用 `% packetCount` 取模。1000 是 100（packetCount 上限）的 10 倍冗余，模偏差 < 1%，对机器人场景可接受。

### 15.4 GetAvailablePacketID（已废弃的两步式）

```go
func (s *GrabService) GetAvailablePacketID(ctx, roomID, roundID string) (string, error)
```

通过 `LRange` 取出全部可用 packetID，再用 `rand.Intn` 选一个。**注释明确说明存在竞态**，已被 `RobotGrabPacket` 替代。返回 `CodeNoPacket` 表示无可用红包。

### 15.5 AutoDistribute（超时自动分发）

```go
func (s *GrabService) AutoDistribute(ctx, roomID, roundID string) (int, []domain.DistributeResult, error)
```

由 `OnGrabTimeout` 调度触发，将剩余红包分发给未抢玩家。

**5 个 Key**：在 GrabPacket 基础上增加 `RoomHashKey(roomID)`，去掉 `UserGrabbedKey`（批量分发，非单人）。

**3 个 ARG**：时间戳、`"cashparty"`、roundID。

**返回值解析**：
- `res[1]` = distributedCount（分发数量）
- `res[2]` = 二维数组，每项 `[UserID, Amount, Position]` 三元组

返回 `[]domain.DistributeResult{UserID, Amount, Position}` 用于后续结算/通知。

### 15.6 InitRoundPackets（初始化一轮红包）

```go
func (s *GrabService) InitRoundPackets(ctx, roomID, roundID, senderID, senderType string,
    totalAmount, commission, actualAmount int64,
    packetAmounts []int64, roundNo int,
    scenario domain.SendScenario, rewardType int, rewardAmount int64) (string, []string, error)
```

**这是"发红包/开局建包"的核心入口**，参数：

| 参数 | 含义 |
|---|---|
| senderID/senderType | 发包者 ID 与类型（玩家/系统/机器人） |
| totalAmount | 红包总金额（分） |
| commission | 平台抽成 |
| actualAmount | 实际发出去的金额（= total - commission） |
| packetAmounts | 拆分后的每个红包金额数组 |
| roundNo | 第几局 |
| scenario | 发包场景（domain.SendScenario 枚举） |
| rewardType/rewardAmount | 奖励类型与金额 |

**5 个 Key**：`RoomHashKey`、`RoomPlayersKey`、`RoundStateKey`、`RoundAvailablePacketsKey`、`RoundGrabbersKey`。

**13 个 ARG**：上述业务参数 + 时间戳、grabTimeout、`"cashparty"`、`amountsJSON`（packetAmounts 的 JSON 序列化）、roundID、roomID、`int(scenario)`。

**返回值**：
- `res[1]` = roundIDResult（Lua 侧可能生成新 roundID）
- `res[2]` = packetIDsJSON（生成的红包 ID 列表）

Go 侧 `json.Unmarshal` 为 `[]int64` 后通过 `converter.FormatIDs` 转为字符串切片。

**关键观察**：InitRoundPackets 本身没有 Go 实现的"位置分配"或"isLast 判断"逻辑，所有这些都在 `LuaSendPacket` 脚本中。Go 只是把拆好的金额数组传进去并接收生成的 packetID 列表。

### 15.7 并发控制要点

- 全部抢包/发包操作通过 Redis Lua 原子性保证（Redis 单线程执行 Lua）
- 机器人抢包特别说明：不能用 Lua 内 `math.random`，所以预生成随机数传入
- 超时分发（AutoDistribute）由调度层在 grabTimeout 到期后触发，避免红包永远卡住

---

## 16. 历史查询服务（HistoryService）详细设计

> 代码：`game/application/history_service.go` + `history_dto.go`
> 提供玩家历史列表、单局详情、累计统计三个查询能力，采用"双路查询"设计平衡性能与完整性。

### 16.1 服务结构

```go
type HistoryService struct {
    dbRepo  domain.DBRepository              // 数据库仓储
    billMgr *settlementService.BillManager   // 账单管理（注入但本文件未直接使用）
}
```

### 16.2 DTO 数据结构

| 结构 | 用途 | 关键字段 |
|---|---|---|
| `PlayerHistoryReq` | 历史列表请求 | Page, PageSize, StartDate, EndDate, ConfigName |
| `PlayerHistoryResp` | 历史列表响应 | List, Total, Page, PageSize |
| `PlayerHistoryItem` | 列表项 + 详情中 MyStats 复用 | 见下 |
| `SessionInfo` | 会话基本信息 | SessionID, RoomNo, ConfigName, RoomFee, MaxRounds, ActualRounds, Status, StartedAt, EndedAt, EndReason |
| `GrabDetail` | 抢包明细 | PacketID, Amount, IsMin, IsAutoAssigned, GrabbedAt |
| `SendDetail` | 发包明细 | TotalAmount, StartedAt |
| `RoundDetail` | 回合明细 | RoundID, RoundNo, SenderID, SenderType, TotalAmount, StartedAt, EndedAt, Status, MyGrab(*), MySend(*) |
| `PlayerSessionDetailResp` | 单局详情响应 | Session, MyStats, Rounds |
| `PlayerStatsResp` | 玩家累计统计 | TotalGames, WinCount, LoseCount, WinRate, TotalProfit, TotalSend, FirstRoundFee, Penalty, TotalBet, TotalGrab, TotalSendCount, TotalGrabCount, AvgProfit |

**PlayerHistoryItem 字段**（最核心，聚合会话信息 + 玩家在该会话的个人数据）：
- 会话层：SessionID, RoomNo, ConfigName, RoomFee, MaxRounds, ActualRounds, Status, StartedAt, EndedAt, EndReason
- 玩家层：SeatNo, SendCount, GrabCount, TotalSend, FirstRoundFee, Penalty, TotalBet, TotalGrab, Profit, JoinedAt, LeftAt

所有金额字段统一使用 `currency.Money`（分转元封装），时间统一使用毫秒时间戳（int64）。

### 16.3 GetPlayerHistory（玩家历史列表）

```go
func (s *HistoryService) GetPlayerHistory(ctx, userID int64, req PlayerHistoryReq) (*PlayerHistoryResp, error)
```

**分页处理**：page 默认 1，pageSize 默认 20，offset = (page-1)*pageSize。

**双路查询 #1（列表页）**：
- 调用 `dbRepo.HistoryDBRepo().ListPlayerSessionsWithBill(userID, StartDate, EndDate, ConfigName, pageSize, offset)`
- 返回 `rows` + `total`
- 该方法名"WithBill"暗示是 game_sessions 与 bill_record 的 JOIN/聚合查询
- **一次性拿到会话信息和玩家在该会话的统计**
- 通过 `playerSessionBillRowToItem` 转换为 `PlayerHistoryItem`
- 注释明确说明：**不含 session_players 的 SeatNo/JoinedAt/LeftAt/Nickname/Avatar 字段，这些字段填零值**

设计意图：列表页性能优化，不做 session_players 的 JOIN，详情页才补齐。

### 16.4 GetPlayerSessionDetail（单局详情）

最复杂的方法，10 步流程：

```go
func (s *HistoryService) GetPlayerSessionDetail(ctx, userID, sessionID int64) (*PlayerSessionDetailResp, error)
```

1. **校验玩家归属**：`GetPlayerSession(userID, sessionID)`，nil 则返回 `CodePlayerNotInSession`。同时拿到 player 的 SeatNo/JoinedAt/LeftAt
2. **查询会话基本信息**：`GetSession(sessionID)`，nil 则 `CodeSessionNotFound`
3. **查询会话所有回合**：`GetSessionRounds(sessionID)`
4. **查询玩家抢包记录**：`ListPlayerGrabRecords(sessionID, userID)`
5. **查询玩家结果卡片**：`GetPlayerSessionBillSummary(userID, sessionID)` — 基于 bill_record 聚合出 SendCount/GrabCount/TotalSend/FirstRoundFee/Penalty/TotalBet/TotalGrab/Profit
6. **查询玩家发包回合**：`GetPlayerSendRounds(sessionID, userID)`
7. **构建 round_id → grabRecord 索引**（map[int64]model.RoundGrabRecord）
8. **构建 round_id → sendRound 索引**（map[int64]model.Round）
9. **组装回合明细**：遍历 rounds，每条 RoundDetail 填基础字段；通过 grabByRound map 补 MyGrab（含 IsMin、IsAutoAssigned 标志）；通过 sendByRound map 补 MySend
10. **组装 MyStats**：把 billSummary 的聚合数据 + player 的 SeatNo/JoinedAt/LeftAt 合并到 PlayerHistoryItem
11. **返回** `PlayerSessionDetailResp{Session, MyStats, Rounds}`

**双路查询 #2（详情页）**：
- 详情页是"会话+回合+抢包+发包+账单"多路查询
- 通过两个 map 索引在内存中 join，避免 N+1
- `MyGrab` 和 `MySend` 都是指针，`omitempty` 序列化时未抢/未发的回合不输出对应字段
- `IsMin` 标志手气最差（红包最小），`IsAutoAssigned` 标志是否系统超时自动分发
- 时间使用 `timeToMs` 统一处理 `*time.Time` 的 nil 情况

### 16.5 GetPlayerStats（玩家累计统计）

```go
func (s *HistoryService) GetPlayerStats(ctx, userID int64) (*PlayerStatsResp, error)
```

调用 `AggregatePlayerStatsFromBill(userID)` 从 bill_record 聚合：
- agg == nil 时返回空 resp（新玩家）
- 计算 loseCount = TotalGames - WinCount
- WinRate = WinCount / TotalGames（除零保护）
- AvgProfit = TotalProfit / TotalGames

返回 `PlayerStatsResp`，包含 14 个统计字段。

### 16.6 双路查询设计权衡

| 场景 | 数据源 | 是否含 session_players | 说明 |
|---|---|---|---|
| 列表页（GetPlayerHistory） | game_sessions + bill_record 聚合 | 否（零值） | 减少 JOIN，列表页只展示核心字段 |
| 详情页（GetPlayerSessionDetail） | game_sessions + rounds + grab_records + send_rounds + bill_record + session_players | 是（完整） | 多路查询 + 内存 map join，保证完整性 |

这种设计平衡了列表页性能（少 JOIN）与详情页完整性。

---

## 17. 用户服务（UserService）详细设计

> 代码：`game/application/user_service.go`
> 用户身份与基础信息的管理服务，职责单一：用户创建/查询（带 Redis 缓存）、标记机器人、查询待入账金额。

### 17.1 服务结构

```go
type UserService struct {
    dbRepo    domain.DBRepository
    redis     *cRedis.Client
    avatarCfg *config.AvatarConfig   // 头像配置
}
```

### 17.2 SaveUser（保存/获取用户）

```go
func (s *UserService) SaveUser(ctx, userID, nickname, avatar, ip, deviceID string) (string, string, error)
```

**三级查找流程**：
1. **Redis 缓存命中**：`UserByUserIDKey(userID)`，反序列化成功直接返回（id, avatar）。日志 "user found in cache, skip save"
2. **DB 已存在**：`GetUser(userID)` 命中则回填缓存（TTL 30min）并返回
3. **创建新用户**：
   - avatar 为空且配置了 avatarCfg 时，调 `utils.GetRandomAvatar(BaseURL, DefaultCount)` 随机分配
   - `model.NewUser(...)` 构造实体
   - `CreateOrUpdateUser` 写 DB
   - 写缓存（TTL 30min）
   - 返回 (格式化ID字符串, avatar)

返回值是 `(格式化ID字符串, avatar, error)`，调用方通常用格式化 ID 做后续业务关联。

### 17.3 GetUser / GetUserById

两个查询方法，区别仅在于查询键：
- `GetUser(userID)`：按业务 userID（字符串）查，cache key `UserByUserIDKey`
- `GetUserById(id)`：按主键 id 查，cache key `UserByIdKey`

两者都是"先查缓存 → 未命中查 DB → 回填缓存（30min）"模式。

### 17.4 SetUserIsRobot（标记机器人）

```go
func (s *UserService) SetUserIsRobot(ctx, id int64) error
```

1. `UserDBRepo().SetUserIsRobot(ctx, id)` 更新 DB
2. 失效 `UserByIdKey(id)` 缓存
3. **best-effort**：由于此处只有 int64 id 没有 user_id 字符串，无法失效 `UserByUserIDKey`，只能等 30min TTL 自然过期

注释说明这是可接受的——因为 `SetUserIsRobot` 调用时机在 `SaveUser` 流程之后，那时 user_id 缓存已经过期或不再使用。

### 17.5 GetPendingCredit（待入账金额）

```go
func (s *UserService) GetPendingCredit(ctx, userID string) int64
```

这是 UserService 中**唯一涉及游戏状态读取**的方法，5 步流程：

1. `PlayerRoomKey(userID)` 拿当前房间 ID，空或 "0" 返回 0
2. `RoomHashKey(roomID)` HGetAll 拿房间 meta，空返回 0
3. **校验房间状态**：`status != 2`（非 Playing）返回 0
4. 取 `current_session_id`，空返回 0
5. `SessionPlayerTotalsKey(sessionID)` 这个 hash 中 HGet userID 字段，拿到累计金额（抢红包+奖励）

返回 int64（单位：分）。

**用途**：游戏进行中查询玩家"暂未结算到账户但已抢到"的金额，用于 UI 实时展示。

---

## 18. 房间应用服务（RoomAppService）完整流程

> 代码：`game/application/room_app_service.go`
> 房间生命周期管理：加入、自动匹配、重连、离开、替补、排队、查询。

### 18.1 服务结构

```go
type RoomAppService struct {
    repo              domain.RoomRepository
    dbRepo            domain.DBRepository
    userService       *UserService
    broadcaster       domain.Broadcaster
    publisher         domain.EventPublisher
    scheduler         *scheduler.TimeoutScheduler
    settlementService *settlementService.SettlementService
    balanceService    *settlementService.BalanceService
    resumeGameCallback ResumeGameCallback  // 由 GameAppService 注入，打破循环依赖
}

type ResumeGameCallback func(ctx context.Context, roomID string, currentRound int)
```

`resumeGameCallback` 用于解决与 `GameAppService` 的循环依赖——当自动替补补满房间后，需要触发中断游戏恢复，但房间服务不应直接依赖游戏服务，因此用回调注入。

### 18.2 JoinRoom 完整流程

```
JoinRoom(req{RoomID, UserID}):
  1. userService.GetUserById → CodeUserNotFound
  2. repo.GetRoomMeta(roomID):
     ├── 失败 (Redis 无元数据) → 回源 DB:
     │   ├── dbRepo.RoomDBRepo().GetRoom → 组装 RoomMeta
     │   └── repo.InitRoom 写入 Redis (失败 → CodeSystemError)
     └── 成功
  3. 构造 domain.Spectator{UserID, Nickname, Avatar, IsRobot}
  4. repo.JoinAsSpectator (LuaJoinAsSpectator)
  5. publisher.Publish(NewSpectatorJoinEvent)
  6. broadcaster.Broadcast(PushRoomState, BuildFullRoomState, exclude=userID)
  7. 返回 JoinRoomResult{IsSpectator: true}
```

注意：`JoinRoom` 仅以观战者身份入房，不上座、不准备。

### 18.3 JoinAndAutoSeat 流程

真实玩家"入房即上座即准备"，两阶段：

```
JoinAndAutoSeat(req):
  1. joinResult = JoinRoom(req)  // 先入房
  2. meta = repo.GetRoomMeta
  3. 状态门控: meta.Status == Playing → 直接返回 (不自动上座)
  4. 余额校验:
     ├── userService.GetUserById
     └── balanceService.CheckBalanceForReady
         └── 失败/不足 → 保持观战身份返回
  5. autoResult = repo.AutoSeatAndReady (LuaAutoSeatAndReady)
     └── 失败 → 保持观战身份返回
  6. scheduler.ClearTimeout(TimeoutTypeSeat, roomID, userID)
  7. publisher.Publish(NewPlayerReadyEvent)
  8. broadcaster.Broadcast(PushRoomState)
  9. handleCountdownAfterSeat(ShouldStartCountdown, CountdownEndTime, CurrentRound)
  10. 返回 JoinRoomResult{IsSpectator: false, SeatNo, AutoSeated: true}
```

### 18.4 handleCountdownAfterSeat（倒计时与恢复分流）

通过 `ShouldStartCountdown` 标志位分派：

| 值 | 含义 | 动作 |
|----|------|------|
| `1` | 新倒计时 | 计算 countdownDuration，广播 `PushCountdownStart`，调度 `TimeoutTypeReady` |
| `2` | 中断恢复 | 异步 `go resumeGameCallback(ctx, roomID, currentRound)` |
| 其他 | 无操作 | — |

设计意图：仓储层 `AutoSeatAndReady`/`AutoSubstitute` 通过该标志位让 application 层决定是开新一轮准备倒计时，还是直接恢复中断态游戏。

### 18.5 AutoMatchAndJoin（自动匹配）

```
AutoMatchAndJoin(req{UserID}):
  1. userService.GetUserById → CodeUserNotFound
  2. settlementService.CheckBalance(userID, 0) 拿余额
  3. dbRepo.RoomDBRepo().MatchRoomByBalance(balance)  // DB 层匹配
  4. JoinAndAutoSeat(roomID, userID)  // 复用入房+上座流程
```

匹配职责下沉到 DB 仓储层，应用层只做"取余额 → 匹配 → 入房"的编排。

### 18.6 HandleReconnect（重连机制）

```
HandleReconnect(req{RoomID, UserID}):
  1. player = repo.GetPlayer(roomID, userID)
  2. spectator = repo.GetSpectator(roomID, userID)
  3. 两者都 nil → CodeNotInRoom
  4. 玩家身份: 取 Nickname/SeatNo, 发 NewPlayerReconnectEvent
     观战者身份: seatNo = 0, 不发领域事件
  5. broadcaster.Broadcast(PushPlayerReconnected, exclude=userID)
  6. stateData = repo.GetRoomStateData
  7. 返回 ReconnectResult{RoomID, RoomState: BuildFullRoomState(stateData)}
```

**关键设计**：重连流程没有重新触发游戏逻辑、没有重新调度超时；它只做"通知他人 + 下发当前房间完整状态"。真正的状态恢复依赖 Redis 中的房间状态数据与既有的调度任务。

### 18.7 LeaveRoom 流程

```
LeaveRoom(req{RoomID, UserID, Reason}):
  1. player = repo.GetPlayer → 存 freedSeatNo (用于后续替补)
  2. repo.RemoveFromQueue (best-effort)
  3. spectator = repo.GetSpectator (用于事件区分)
  4. repo.LeaveRoom (LuaLeaveRoom)
  5. scheduler.ClearAllUserTimeouts(roomID, userID)
  6. 若 spectator != nil: publisher.Publish(NewSpectatorLeaveEvent)
  7. broadcaster.Broadcast(PushRoomState)
  8. 若 freedSeatNo > 0: go tryAutoSubstitute(ctx, roomID, freedSeatNo)  // 异步替补
  9. 返回 LeaveRoomResult{}
```

**注意**：仅对观战者发布事件，未对玩家身份发布"player leave"事件（事件来源由其他流程或替补/结算产生）。

### 18.8 tryAutoSubstitute（替补机制）

```
tryAutoSubstitute(ctx, roomID, seatNo):
  1. meta = repo.GetRoomMeta
  2. 队列余额过滤 (仅 balanceService != nil):
     ├── queueList = repo.GetQueueList
     ├── 对每个排队者 q:
     │   ├── userService.GetUserById(q.UserID)
     │   ├── balanceService.CheckBalanceForReady
     │   ├── 失败/不足 → repo.Dequeue + 推 PushDequeued("余额不足")
     │   └── 充足 → break
  3. subResult = repo.AutoSubstitute(roomID, seatNo) (LuaAutoSubstitute)
     ├── 失败 → 返回 nil
     └── 队列空 → 返回 nil
  4. publisher.Publish(NewSubstituteEvent)
  5. broadcaster.Broadcast(PushSubstitute)
     broadcaster.Broadcast(PushRoomState)
  6. handleCountdownAfterSeat(subResult.ShouldStartCountdown, ...)
  7. 返回 subResult
```

**关键设计意图**：替补前会"过滤"队列中余额不足者，保证只有合格的候选者被 `AutoSubstitute` 取出。替补成功后既能开启新一轮准备，也能在中断态下触发 `ResumeGameCallback`。

### 18.9 Enqueue / Dequeue（排队）

**Enqueue**：
```
Enqueue(req{RoomID, UserID}):
  1. position = repo.Enqueue (LuaEnqueue)
  2. spectator = repo.GetSpectator (取昵称/头像)
  3. publisher.Publish(NewQueueJoinEvent)
  4. broadcaster.Broadcast(PushRoomState)
  5. 返回 EnqueueResult{QueuePosition: position, RoomState}
```

**Dequeue**：
```
Dequeue(req{RoomID, UserID}):
  1. repo.Dequeue (LuaDequeue)
  2. publisher.Publish(NewQueueLeaveEvent(reason="user_cancel"))
  3. broadcaster.Broadcast(PushRoomState)
  4. 返回 DequeueResult{RoomState}
```

**注意**：替补机制中余额不足强制出队用的是 `repo.Dequeue`（仓储层）+ `PushDequeued` 推送，**绕过**了本 `Dequeue` 方法（不走 `user_cancel` 事件），有意区分"用户主动取消"与"系统强制出队"两种语义。

### 18.10 GetRoomList（批量查询）

```
GetRoomList(roomType, status, page, pageSize):
  1. rooms, _ = dbRepo.RoomDBRepo().GetRoomList(roomType, status, page, pageSize)
  2. total, _ = dbRepo.RoomDBRepo().GetRoomCount(roomType, status)
  3. 收集 roomIDs
  4. stateDataMap = repo.GetRoomSeatsBatch(roomIDs)  // 批量取 Redis 状态
  5. 组装 RoomListItem:
     ├── Redis 状态存在: stateData.MaxPlayers = r.MaxPlayers (DB 兜底)
     │   seats = BuildFullRoomState(stateData).Seats
     │   currentRound, status 从 Redis 取
     └── Redis 不存在: status = int(r.Status), seats 为空
  6. 返回 (items, total)
```

`stateData.MaxPlayers` 被显式赋值 `r.MaxPlayers`，说明 Redis 状态数据中可能没有可靠的 MaxPlayers，需要从 DB 兜底。

---

## 19. 机器人调度服务（RobotSchedulerService）详细算法

> 代码：`game/application/robot_scheduler_service.go`
> 常驻后台服务，周期性扫描 Waiting 房间并分配机器人，游戏结束后回收机器人。

### 19.1 服务结构

```go
type roomCandidate struct {
    RoomID           string
    ReadyPlayerCount int       // 已就绪的真实玩家数（不算机器人）
    WaitingSince     time.Time // 等待起始时间，用于排序
    SeatedCount      int       // 已入座总人数（含机器人）
    RoomFee          int       // 房费，用于匹配机器人余额要求
}

type RobotSchedulerService struct {
    accountSvc          *RobotAccountService
    robotPlayer         *RobotPlayer
    behaviorEngine      *RobotBehaviorEngine  // 后置注入，打破循环依赖
    repo                domain.RoomRepository
    dbRepo              domain.DBRepository
    redis               *cRedis.Client
    robotSchedulerRedis *redis.RobotSchedulerRedis
    robotPool           *redis.RobotPoolService
    config              *config.RobotConfig
    ctx                 context.Context
    cancel              context.CancelFunc
}
```

`SetBehaviorEngine(engine *RobotBehaviorEngine)` 在构造后调用，打破 `RobotPlayer ↔ RobotBehaviorEngine` 的循环依赖。

### 19.2 Start / Stop（优雅关闭）

```go
func (s *RobotSchedulerService) Start() {
    s.ctx, s.cancel = context.WithCancel(context.Background())
    go s.scanLoop()
}

func (s *RobotSchedulerService) Stop() {
    if s.cancel != nil { s.cancel() }
}
```

**注意**：`Stop` 仅调用 `cancel()` 取消 context，不等待 `scanLoop` goroutine 退出（无 `WaitGroup`）。`scanLoop` 在下一次 `select` 检查到 `<-s.ctx.Done()` 时才返回。若 `scanRooms` 正在执行，需要等本次扫描执行完毕才会真正退出。Stop 多次调用安全（判空 `cancel != nil`）。

`scanLoop` 内有保护：若 `ScanInterval <= 0`，直接返回不启动循环。

### 19.3 scanRooms 扫描算法（主流程）

```go
func (s *RobotSchedulerService) scanRooms() {
    budget := time.Duration(float64(ScanInterval) * 0.8)  // 80% 时间预算
    deadline := time.Now().Add(budget)

    rooms := s.getWaitingRooms(ctx)                       // 1. 取 Waiting 房间
    candidates := s.filterRoomsNeedingRobots(ctx, rooms)  // 2. 过滤
    s.sortRoomsByPriority(candidates)                     // 3. 排序
    for _, room := range candidates {
        if time.Now().After(deadline) { break }           // 4. 在预算内分配
        s.assignRobotsToRoom(ctx, room)
    }
    s.checkPoolReserve(ctx)         // 5. 池水位检查
    s.cleanupEndedRooms(ctx)        // 6. 兜底清理
}
```

**关键设计**：
1. **时间预算保护**：用 80% 扫描间隔作为 deadline，避免本轮未完成时下一轮 tick 到来造成重叠堆积
2. **六步顺序**：取房间 → 过滤 → 排序 → 限额分配 → 池监控 → 兜底清理

### 19.4 getWaitingRooms（房间发现）

```go
const scanPattern = KeyRoomHashPrefix + ":*"  // cashparty:room:hash:*
const scanCount = 200
```

- 通过 `s.redis.Scan` 游标式扫描 Redis 所有房间 hash 键
- 用 `strings.LastIndex(key, ":")` 截取最后一段作为 `roomID`
- 对每个 roomID：
  - `repo.GetRoomStateData(ctx, roomID)` 拿房间状态
  - 只保留 `state.Status == RoomStatusWaiting`
  - 统计 `SeatedCount`（`SeatNo > 0`）与 `readyRealPlayers`（`SeatNo > 0 && !IsRobot`）
  - 取 `state.Meta.StartedAt` 作为 `WaitingSince`，缺失时退化为 `time.Now()`

### 19.5 filterRoomsNeedingRobots（过滤条件）

判断条件（两个都需满足）：
1. `room.ReadyPlayerCount >= MinRealPlayers`（房间内已就绪真实玩家达到最小阈值）
2. `room.SeatedCount < MaxPlayers`（房间尚有空位）

任一不满足直接 skip。

### 19.6 sortRoomsByPriority（优先级排序）

```go
sort.SliceStable(rooms, func(i, j int) bool {
    if rooms[i].ReadyPlayerCount != rooms[j].ReadyPlayerCount {
        return rooms[i].ReadyPlayerCount > rooms[j].ReadyPlayerCount
    }
    return rooms[i].WaitingSince.Before(rooms[j].WaitingSince)
})
```

- **主键**：已就绪真实玩家数 **降序**（优先把快开局的房间补满）
- **次键**：等待起始时间 **升序**（等得最久的先补）
- 使用 `SliceStable` 保持等优先级房间的插入顺序稳定

### 19.7 assignRobotsToRoom（分配逻辑）

```
assignRobotsToRoom(ctx, room):
  1. AcquireRoomAssignLock(roomID, RoomAssignLockTTL)  // 房间级限流锁
     └── 失败 → 直接返回 (避免多实例并发处理)
  2. existingRobots = GetRoomRobots(roomID)
     └── 非空 → recycleZombieRobots (清理僵尸)
  3. 计算 needed:
     needed = MaxPlayers - room.SeatedCount
     maxAllowed = MaxRobotsPerRoom - len(existingRobots)
     needed = min(needed, maxAllowed)
     若 needed <= 0 → return
  4. 循环 needed 次:
     a. robot = accountSvc.GetAvailableRobot(roomFee)  // 取空闲且余额够用的机器人
        └── 取不到 → break
     b. AcquireAssignLock(robot.UserID, roomID, RobotAssignLockTTL)  // 机器人级锁
        └── 失败 → continue
     c. MarkRobotInGame(userID)  // Idle → InGame, 移出 available pool
        └── 失败 → 释放锁 continue
     d. AddRobotToRoom + AddToActiveSet
     e. robotPlayer.JoinAndReady(roomID, FormatID(robot.UserID))
        └── 失败 → 回滚 (MarkRobotIdle, RemoveRobotFromRoom, RemoveFromActiveSet, ReleaseAssignLock)
        └── 成功 → Info 日志 "robot assigned to room"
```

### 19.8 recycleZombieRobots（僵尸回收）

"僵尸机器人"定义：已加入 `room robot set` 但实际未在房间 `Players` 列表中入座（`SeatNo > 0`）的机器人。

```
recycleZombieRobots(ctx, roomID, robotIDs):
  1. stateData = repo.GetRoomStateData(roomID)
  2. 构建 seatedUserIDs map: SeatNo > 0 的所有玩家 UserID
  3. 对每个 robotID:
     ├── FormatID(robotID) 在 seatedUserIDs 中 → 跳过 (已入座,非僵尸)
     └── 否则:
         ├── Warn 日志 "zombie robot detected, recycling"
         ├── RemoveRobotFromRoom + RemoveFromActiveSet
         ├── MarkRobotIdle (失败 Error 但不中断)
         └── robotPlayer.LeaveRoom (兜底取消座位+离房)
```

### 19.9 cleanupEndedRooms（兜底清理）

处理"漏掉 `OnGameEnd` 事件"的情况：

```
cleanupEndedRooms(ctx):
  1. SCAN 所有 "robot:room:*" 键, 提取 roomID
  2. 对每个 roomID:
     ├── robotIDs = GetRoomRobots(roomID)
     ├── 无机器人 → 跳过
     ├── stateData = GetRoomStateData(roomID)
     └── 仅 Status == Idle || Status == Interrupted 时执行清理:
         └── recycleRoomRobots(roomID, robotIDs)
             └── 对每个机器人: behaviorEngine.LeaveRoomNow(...)
                 (behaviorEngine 为空 → 退化为 robotPlayer.ScheduleLeave(...,0))
```

### 19.10 OnGameEnd 事件钩子

```go
func (s *RobotSchedulerService) OnGameEnd(ctx, roomID)
```

游戏正常结束时被调用：取出该房间所有机器人，逐个 `LeaveRoomNow`（或 `ScheduleLeave(...,0)`）。这是 `cleanupEndedRooms` 的"主路径"，`cleanupEndedRooms` 只是它的兜底。

### 19.11 checkPoolReserve（水位监控）

```go
availableCount, _ := s.robotPool.GetAvailableCount(ctx)
if ReserveCount > 0 && availableCount < ReserveCount {
    logger.Warn("robot pool reserve low", ...)
}
```

仅告警，不自动扩容。

### 19.12 ValidateReserveRatio

启动期校验：`ReserveCount / total_robots > ratioMax`（默认 0.5）时 Warn，避免预留水位设置过高占用过多机器人。

---

## 20. 机器人玩家与账号服务实现

### 20.1 RobotPlayer（单机器人动作驱动）

> 代码：`game/application/robot_player.go`
> 驱动单个机器人走完整房间生命周期：`Join → Seat → Ready → Grab / Send → Leave`。

#### 20.1.1 结构

```go
type RobotActionScheduler interface {
    ScheduleAction(roomID string, robotUserID string, action string, delay time.Duration)
}

var ErrNoEmptySeat = errors.New("no empty seat available")

type RobotPlayer struct {
    seatAppService *SeatAppService
    gameAppService *GameAppService
    roomAppService *RoomAppService
    accountSvc     *RobotAccountService
    grabSvc        *GrabService
    repo           domain.RoomRepository
    behaviorEngine RobotActionScheduler
    config         *config.RobotConfig
}
```

`SetBehaviorEngine(engine RobotActionScheduler)` 用于打破循环依赖。

#### 20.1.2 JoinAndReady 流程

```
JoinAndReady(ctx, roomID, robotUserID):
  1. roomAppService.JoinRoom (作为旁观者加入)
  2. roomState = repo.GetRoomStateData
  3. hasEmptySeat(roomState) → 无空座返回 ErrNoEmptySeat
  4. delay = randomDelay(SeatDelayMin, SeatDelayMax)
  5. behaviorEngine.ScheduleAction(roomID, robotUserID, "seat", delay)
  // 不直接 Ready: Ready 是 SelectSeat 成功后再链式调度的, 避免"机器感"
```

#### 20.1.3 SelectSeat（含重试）

```
SelectSeat(ctx, roomID, robotUserID):
  maxRetries = config.ActionRetryMax (默认 2)
  for attempt := 0..maxRetries:
    1. state = repo.GetRoomStateData (每次重试拿最新状态)
    2. seatNo = pickRandomEmptySeat(state)
       └── 无空座 → return ErrNoEmptySeat
    3. seatAppService.SelectSeat
       ├── 成功 → 调度 "ready" 动作 (ReadyDelayMin..ReadyDelayMax), return
       └── 失败 → Warn 日志, 继续重试
  return ErrNoEmptySeat
```

#### 20.1.4 Ready / GrabPacket / SendPacket

```go
func (p *RobotPlayer) Ready(ctx, roomID, robotUserID) error {
    _, err := p.seatAppService.PlayerReady(...)
    return err
}

func (p *RobotPlayer) GrabPacket(ctx, roomID, roundID, robotUserID) error {
    result, err := p.grabSvc.RobotGrabPacket(ctx, roomID, roundID, robotUserID)  // 单 Lua 原子抢
    if err != nil { return err }
    p.gameAppService.OnRobotGrabbed(ctx, roomID, roundID, robotUserID, result)
    return nil
}

func (p *RobotPlayer) SendPacket(ctx, roomID, robotUserID) error {
    _, err := p.gameAppService.SendPacket(...)
    return err
}
```

注释特别说明：`GrabPacket` 通过 `grabSvc.RobotGrabPacket` 一次 Lua 调用完成"选包 + 抢包"，避免传统两步法的竞态。

#### 20.1.5 LeaveRoom / ScheduleLeave

```go
func (p *RobotPlayer) LeaveRoom(ctx, roomID, robotUserID) error {
    _, _ = p.seatAppService.CancelSeat(...)   // 先尝试取消座位（忽略错误）
    _, err := p.roomAppService.LeaveRoom(...) // 再离房
    return err
}

func (p *RobotPlayer) ScheduleLeave(ctx, roomID, robotUserID, delay) {
    if p.behaviorEngine == nil { Warn; return }
    p.behaviorEngine.ScheduleAction(roomID, robotUserID, "leave", delay)
}
```

**注意**：`LeaveRoomNow` 方法位于 `RobotBehaviorEngine` 上，不在 `RobotPlayer` 中。调度服务在 `cleanupEndedRooms` 与 `OnGameEnd` 中调用 `s.behaviorEngine.LeaveRoomNow(...)`。

#### 20.1.6 座位辅助算法

**`hasEmptySeat(state)`**：
- `MaxPlayers <= 0` 时按 5 处理
- 构建 `playerSeats map[int]bool`，所有 `SeatNo > 0` 的玩家占用
- 遍历 1..MaxPlayers，找到任一座位未被玩家占用且 `SeatOwners[i]` 为空/"0" 即返回 true

**`pickRandomEmptySeat(state)`**：
- 同样的空座判定，但收集所有空座到 `emptySeats` 切片
- `rand.Intn(len(emptySeats))` 随机返回其一
- 无空座返回 0

判定空座的规则：`!playerSeats[i] && (owner 不存在 || owner == "" || owner == "0")`。座位被 spectator 选但未确认入座时仍被视作"空"（可被机器人抢），对应 spectator 选座后还未 ready 的中间态。

### 20.2 RobotAccountService（机器人账号管理服务）

> 代码：`game/application/robot_account_service.go`

#### 20.2.1 常量与余额门槛

```go
const defaultRoomFee = 1000       // 默认房费
const minRoomFee = 100             // 机器人可参与最低房费
const maxRoomFee = 50000           // 机器人可参与最高房费
```

**余额门槛公式**：`balanceRequired := roomFee/5 + roomFee*9`（即 9.2 倍房费，覆盖 9 局发红包 + 1/5 的额外缓冲）。

#### 20.2.2 服务结构

```go
type RobotAccountService struct {
    repo           *mysql.RobotAccountRepository
    userSvc        *UserService
    virtualBalance *redis.VirtualBalanceService
    robotPool      *redis.RobotPoolService
    avatarCfg      *config.AvatarConfig
}
```

#### 20.2.3 BatchCreateRobotsWithBalance（核心创建逻辑）

对每个 i（1..count）：
1. 生成 `robotUserID = "robot_%05d"`、`nickname = "Robot_%d"`
2. 从 `avatarCfg` 取随机头像（base URL + DefaultCount）
3. `userSvc.SaveUser` 落库用户表，拿到 `formattedID` 与可能更新的 avatar
4. `strconv.ParseInt` 把 formattedID 转 int64
5. `SetUserIsRobot(userID)` 标记用户为机器人
6. 构造 `model.RobotAccount{ UserID, Status: Idle, VirtualBalance: initialBalance, MinRoomFee: 100, MaxRoomFee: 50000 }`，`repo.Create`
7. `virtualBalance.AddToRobotSet` 加入机器人集合
8. `virtualBalance.SetBalance` 设置虚拟余额
9. `robotPool.AddToAvailablePool` 加入可用池

**注意**：任一步出错返回错误，**非原子**：失败时前面已写的 Redis/DB 状态不会自动回滚。

#### 20.2.4 GetAvailableRobot

```go
func (s *RobotAccountService) GetAvailableRobot(ctx, roomFee int) (*model.RobotAccount, error)
```

- 计算 `balanceRequired = roomFee/5 + roomFee*9`
- `repo.GetAvailableRobots(roomFee, roomFee)` 查 MinRoomFee ≤ roomFee ≤ MaxRoomFee 且状态 Idle 的机器人列表
- 逐个查 `virtualBalance.GetBalance`，找到第一个 `balance >= balanceRequired` 的返回
- 找不到返回 `(nil, nil)`（不是 error）

#### 20.2.5 状态转换方法

| 方法 | 动作 |
|------|------|
| `MarkRobotInGame(userID)` | `UpdateStatus(InGame)` + `RemoveFromAvailablePool` |
| `MarkRobotIdle(userID)` | `UpdateStatus(Idle)` + `AddToAvailablePool` + `UpdateLastActiveAt` |
| `CheckLowBalance(userID, roomFee, threshold)` | `balance < balanceRequired * threshold` 时返回 true |
| `DisableRobot(userID)` | `UpdateStatus(Disabled)` + `RemoveFromAvailablePool` |
| `RechargeVirtualBalance(userID, amount)` | `virtualBalance.Credit` + 若 Disabled 则 `UpdateStatus(Idle)` + `AddToAvailablePool` |

#### 20.2.6 状态机

```
[创建]  → Idle
Idle    → InGame    (MarkRobotInGame)
InGame  → Idle      (MarkRobotIdle)
任意    → Disabled   (DisableRobot)
Disabled → Idle     (RechargeVirtualBalance 充值时自动复活)
```

---

## 21. 游戏状态领域模型与阶段枚举

> 代码：`game/domain/game_state.go`

### 21.1 GamePhase 枚举

```go
type GamePhase int
const (
    PhaseWaiting    GamePhase = iota + 1  // 1 等待开局
    PhaseCountdown                        // 2 倒计时
    PhaseRoundStart                       // 3 单局开始
    PhaseGrabbing                         // 4 抢红包中
    PhaseSettling                         // 5 结算中
    PhaseWaitSend                         // 6 等待发红包
    PhaseGameEnd                          // 7 游戏结束
)
```

`String()` 返回大写字符串（`"WAITING"` 等），`ParseGamePhase(s)` 反向解析。

> **注意**：`ParseGamePhase` 未知字符串默认返回 `PhaseWaiting`，解析失败被静默降级，可能掩盖错误。

### 21.2 GameState 结构

```go
type GameState struct {
    RoomID         string
    Phase          GamePhase
    CurrentRound   int
    MaxRounds      int
    NextSenderID   string    // 下一轮发红包者
    PhaseEnteredAt int64     // 进入当前阶段的时间戳
    PhaseDeadline  int64     // 当前阶段截止时间戳
}
```

### 21.3 RoundInfo（单局信息）

```go
type RoundInfo struct {
    RoundNo                int
    RoundID                string  // 单局唯一 ID
    SenderID, SenderType   string  // 发包人 ID 与类型（玩家/系统）
    TotalAmount            int64   // 红包总金额（分）
    Commission             int64   // 佣金
    ActualAmount           int64   // 实际可抢金额 = TotalAmount - Commission
    PacketCount            int     // 红包个数
    Status                 int     // 单局状态
    GrabEndTime            int64   // 抢包截止时间
    CreatedAt              int64
}
```

### 21.4 CommissionConfig（佣金配置）

```go
type CommissionConfig struct { Rate float64 }

func DefaultCommissionConfig() *CommissionConfig {
    return &CommissionConfig{Rate: 0.05}  // 默认 5%
}

func (c *CommissionConfig) Calculate(totalAmount int64) int64 {
    return int64(float64(totalAmount) * c.Rate)
}
```

---

## 22. 关键流程时序图

### 22.1 玩家完整入房时序

```
玩家                Gateway              Game 服务             Redis              MySQL
 │                     │                     │                  │                    │
 │── join_room ───────►│                     │                  │                    │
 │                     │── gRPC Forward ────►│                  │                    │
 │                     │                     │── GetUserById ──►│                    │
 │                     │                     │◄── user ─────────│                    │
 │                     │                     │── GetRoomMeta ──►│                    │
 │                     │                     │◄── (miss) ───────│                    │
 │                     │                     │── GetRoom ───────┼───────────────────►│
 │                     │                     │◄── room ─────────┼────────────────────│
 │                     │                     │── InitRoom ─────►│                    │
 │                     │                     │── JoinAsSpectator (Lua) ─►│           │
 │                     │                     │◄── result ───────│                    │
 │                     │                     │── Publish Event ─┼── Kafka ────►     │
 │                     │                     │── Broadcast ─────┼── Kafka ────►     │
 │                     │◄── JoinRoomResult ──│                  │                    │
 │◄── room_state ──────│                     │                  │                    │
```

### 22.2 一轮游戏完整时序（玩家发包）

```
玩家A           Game 服务         GrabService       Redis         Scheduler       Kafka
  │                │                  │              │               │              │
  │── send_packet ►│                  │              │               │              │
  │                │── GetRoomMeta ──►│              │               │              │
  │                │── GetPlayer ────►│              │               │              │
  │                │── initLaterRoundAndDeduct (扣费)               │              │
  │                │── sendPacketPipeline             │              │              │
  │                │  ├── packetGenerator.Generate ──►│              │              │
  │                │  └── InitRoundPackets (LuaSendPacket) ─►│      │              │
  │                │◄── packetIDs ────│              │               │              │
  │                │── go postSendPacketAsync         │              │              │
  │                │  ├── Publish PacketCreated ──────┼──────────────┼─────────────►│
  │                │  ├── SetTimeout(Grab) ───────────┼──────────────│              │
  │                │  └── Broadcast RoundStart ───────┼─────────────►│              │
  │◄── round_start │                  │              │               │              │
  │                │                  │              │               │              │
  │── grab_packet ►│                  │              │               │              │
  │                │── GrabPacket (LuaGrabPacket) ───►│              │              │
  │                │◄── result(含 isLast) ────────────│              │              │
  │                │── Broadcast PacketGrabbed ───────┼─────────────►│              │
  │                │── if isLast:                     │              │              │
  │                │     ClearTimeout(Grab) ──────────┼──────────────│              │
  │                │     go settleRound ──►           │              │              │
  │◄── grabbed ────│                  │              │               │              │
```

### 22.3 结算与下一轮触发时序

```
settleRound goroutine          Redis          Settlement           Kafka
      │                          │                │                    │
      │── Acquire SettleLock ───►│                │                    │
      │── LuaSettleRound ───────►│                │                    │
      │◄── result(含 minPlayerID,│                │                    │
      │    isGameEnd, results)  │                │                    │
      │── Broadcast RoundEnd ───┼───────────────►│                    │
      │── Publish RoundSettle ──┼────────────────┼───────────────────►│
      │                         │                │                    │
      ├── if isGameEnd:         │                │                    │
      │    endGameWithOptions ──►│                │                    │
      │    (LuaEndGame)         │                │                    │
      │    Broadcast RoomState ─┼───────────────►│                    │
      │    Publish SessionEnd ──┼────────────────┼───────────────────►│
      │    gameEndCallback ─────┼────────────────│ (机器人回收)        │
      │                         │                │                    │
      └── else:                 │                │                    │
           SetTimeout(Send, minPlayerID) ────────►│                    │
                                                                        
                        Kafka Consumer (异步)
                              │
                              ├── tryAcquire(traceID) SetNX ──► Redis
                              ├── DB Transaction:
                              │   ├── Update Round status=Ended
                              │   ├── Create GrabRecords
                              │   └── Update SessionPlayer stats
                              ├── SettleRound (settlement service)
                              │   ├── creditRound (创建 bill_type=3 grab 收入)
                              │   └── SettleReward (若触发奖励, bill_type=10/11)
                              └── robotBehaviorEngine.OnRoundSettle
                                  └── 若 minPlayerID 是机器人 → 调度 send 动作
```

### 22.4 中断与恢复时序

```
玩家断线/被踢        Game 服务           Redis              Scheduler
     │                  │                  │                   │
     │── KickPlayer ───►│                  │                   │
     │                  │── KickPlayerAndInterrupt (Lua) ──►   │
     │                  │◄── result(SeatNo, RoomStatus=4) ─────│
     │                  │── SetTimeout(Replace, 30s) ──────────►│
     │                  │── tryAutoSubstitute:                   │
     │                  │  ├── GetQueueList                      │
     │                  │  ├── 余额过滤                           │
     │                  │  └── AutoSubstitute (Lua)              │
     │                  │      └── 成功 → handleCountdownAfterSeat
     │                  │           └── ShouldStartCountdown==2
     │                  │               └── go resumeGameCallback
     │                  │                   └── GameAppService.ResumeGame
     │                  │                       ├── ClearTimeout(Replace)
     │                  │                       ├── Broadcast PushGameResumed
     │                  │                       └── systemSendRound (ResumeInterrupt)
     │                  │                                       │
     │                  │── (无替补) OnReplaceTimeout ◄──────────┘  30s 后
     │                  │   ├── DistributePenalty (均分离开者 roomFee)
     │                  │   └── endGameWithOptions(ReasonReplacementTimeout)
```

### 22.5 机器人调度时序

```
RobotScheduler (5s tick)       Redis              RobotPlayer        BehaviorEngine
       │                         │                    │                   │
       │── scanRooms:            │                    │                   │
       │  ├── SCAN room:hash:* ─►│                    │                   │
       │  ├── filter (≥2 real players, <max)          │                   │
       │  └── sort (real desc, waiting asc)          │                   │
       │                         │                    │                   │
       │── assignRobotsToRoom:   │                    │                   │
       │  ├── AcquireRoomAssignLock ──►               │                   │
       │  ├── GetAvailableRobot ──►                   │                   │
       │  ├── AcquireAssignLock (UUID token) ──►      │                   │
       │  ├── MarkRobotInGame ──►                     │                   │
       │  └── robotPlayer.JoinAndReady:               │                   │
       │      ├── JoinRoom ──►                        │                   │
       │      └── ScheduleAction("seat", 2-5s) ───────┼───────────────────►│
       │                                                │                   │
       │                         │                    │ (2-5s 后)         │
       │                         │ HandleRobotTimeout("seat") ─────────────►│
       │                         │                    │ ├── SelectSeat ──►│
       │                         │                    │ └── ScheduleAction("ready", delay)
       │                         │                    │                   │
       │                         │                    │ (ready 后)        │
       │                         │                    │ HandleRobotTimeout("ready") ──►│
       │                         │                    │ └── PlayerReady ──►│
       │                         │                    │                   │
       │                         │ (满员倒计时 → 开始游戏)                  │
       │                         │                    │                   │
       │                         │ OnPacketCreated (Kafka consumer 回调) ──►│
       │                         │                    │ ├── 对每个机器人:  │
       │                         │                    │ │   ├── shouldSkipGrab?
       │                         │                    │ │   └── ScheduleAction("grab", 1-8s)
       │                         │                    │                   │
       │                         │                    │ (1-8s 后)         │
       │                         │                    │ HandleRobotTimeout("grab") ───►│
       │                         │                    │ └── GrabPacket (RobotGrabPacket Lua)
       │                         │                    │                   │
       │                         │ OnRoundSettle (Kafka consumer 回调) ────►│
       │                         │                    │ └── 若 minPlayerID 是机器人:
       │                         │                    │     ScheduleAction("send", 2-5s)
       │                         │                    │                   │
       │                         │ OnGameEnd ─────────┼───────────────────►│
       │                         │                    │ └── LeaveRoomNow (立即离场+清理)
```

---

## 23. 错误码与错误处理

### 23.1 Lua 错误码清单

> 代码：`game/domain/lua_errors.go`

| 错误码 | 常量 | 含义 |
|--------|------|------|
| 0 | `LuaSuccess` | 成功 |
| 1 | `LuaErrRoomNotFound` | 房间不存在 |
| 2 | `LuaErrRoomFull` | 房间已满 |
| 3 | `LuaErrAlreadyInRoom` | 已在房间中 |
| 4 | `LuaErrNotInRoom` | 不在房间中 |
| 5 | `LuaErrGameStarted` | 游戏已开始 |
| 6 | `LuaErrGameNotStarted` | 游戏未开始 |
| 7 | `LuaErrSeatOccupied` | 座位已被占用 |
| 8 | `LuaErrInvalidSeatNo` | 无效座位号 |
| 9 | `LuaErrAlreadyReady` | 已准备 |
| 10 | `LuaErrNotReady` | 未准备 |
| 11 | `LuaErrPacketAlreadyExist` | 红包已存在 |
| 12 | `LuaErrNoPacket` | 无可抢红包 |
| 13 | `LuaErrAlreadyGrabbed` | 已抢过 |
| 14 | `LuaErrNotYourTurn` | 未轮到你 |
| 15 | `LuaErrInsufficientBalance` | 余额不足 |
| 16 | `LuaErrPlayerCannotLeave` | 玩家不能离开（须被踢） |
| 17 | `LuaErrQueueEmpty` | 队列为空 |
| 18 | `LuaErrSubstituteFail` | 替补失败 |
| 19 | `LuaErrNoEmptySeat` | 无空座位 |
| 20 | `LuaErrRoundSettled` | 轮次已结算（幂等） |
| 32 | `LuaErrSenderKicked` | 发包人被踢 |

`MapLuaError(code int)` 将 Lua 错误码映射为 `message.Error` 业务错误。

### 23.2 业务错误码

> 代码：`common/message/`

业务错误使用 `message.NewError(code)` 构造，常见错误码：
- `CodeRoomNotFound` — 房间不存在
- `CodeGameNotStarted` — 游戏未开始
- `CodeGameInProgress` — 游戏进行中
- `CodeNotInRoom` — 不在房间
- `CodeNotYourTurn` — 未轮到你
- `CodeNoPacket` — 无可抢红包
- `CodePacketsAlreadyExist` — 红包已存在
- `CodeInvalidSeatNo` — 无效座位号
- `CodeInsufficientBalance` — 余额不足
- `CodeSystemError` — 系统错误
- `CodeSystemBusy` — 系统繁忙
- `CodeOperationInProgress` — 操作进行中（锁占用）
- `CodePlayerNotInSession` — 玩家不在该会话
- `CodeSessionNotFound` — 会话不存在

`message.NewErrorWithMsg(code, msg)` 允许自定义消息。
`message.IsGameError(err)` 判断是否为游戏业务错误。

### 23.3 gRPC 错误处理约定

> 代码：`game/server/generic_service.go`

- **业务错误不返回 gRPC error**，编码在 `ForwardResponse.Code` 中
- `handleError` 提取 `GameError` code/msg，否则 `CodeSystemError`
- 防止内部错误泄漏给客户端
- `recoveryUnaryInterceptor` 兜底 panic → `codes.Internal`
- `loggingUnaryInterceptor` 记录请求耗时

### 23.4 扣费失败错误处理

扣费失败（首轮流摊/后续轮最小者/惩罚）采用 `handleDeductFailure` 统一处理：
- 发布中断原因（`ReasonFirstRoundDeductFailed` / `ReasonLaterRoundDeductFailed` / `ReasonPenaltyDeductFailed`）
- 调用 `endGameWithOptions` 结束游戏（状态为 `Abnormal`）
- 返回 `CodeSystemError` + 中断消息给调用方

---

## 24. 并发控制与锁策略汇总

### 24.1 锁清单

| 锁 | Key 模式 | TTL | 用途 | 释放方式 |
|----|---------|-----|------|---------|
| 发包锁 | `lock:send_packet:{roomID}:{userID}` | 10s | 防止玩家并发发包 | 自动过期 |
| 结算锁 | `lock:settle:{roomID}:{roundID}` | 30s | 防止结算重复执行 | 自动过期 |
| 开始游戏锁 | `lock:game_start:{roomID}` | - | 防止重复开始游戏 | 自动过期 |
| 结束游戏锁 | `lock:game_end:{roomID}` | - | 防止重复结束游戏 | 自动过期 |
| 替补超时锁 | `lock:replace_timeout:{roomID}:{userID}` | 30s | 防止替补超时重复触发 | 自动过期 |
| 机器人分配锁 | `robot:assign:{userID}` | 10s | 防止机器人被重复分配 | UUID token + Lua 释放 |
| 房间限流锁 | `robot:room_assign:{roomID}` | 30s | 防止多实例并发处理同一房间 | 自动过期 |

### 24.2 Lua 原子性保证

所有 Redis 多键变更操作通过 Lua 脚本保证原子性（Redis 单线程执行 Lua）：

| 操作 | Lua 脚本 | 原子保证 |
|------|---------|---------|
| 加入旁观者 | `LuaJoinAsSpectator` | 容量检查 + 写入 + 设 userRoomKey |
| 选座 | `LuaSelectSeat` | 座位检查 + SETBIT + HSET |
| 自动入座+ready | `LuaAutoSeatAndReady` | 找空座 + 旁观者→玩家 + ready + 倒计时触发 |
| 自动替补 | `LuaAutoSubstitute` | 队列弹出 + 旁观者→玩家 + 占座 |
| 踢人+中断 | `LuaKickPlayerAndInterrupt` | 释放座位 + 状态变更 + userRoom 清除 |
| 开始游戏 | `LuaTryStartGame` | 幂等检查 + 设 started_at |
| 发包 | `LuaSendPacket` | 创建 packet keys + 设 roundState + phase=GRABBING |
| 抢包 | `LuaGrabPacket` | 校验 + 标记包 + 最后一人触发 SETTLING |
| 机器人抢包 | `LuaRobotGrabPacket` | 随机选包 + 抢包（原子） |
| 结算 | `LuaSettleRound` | 幂等 + 构建结果 + 找最小抢者 + 更新累计 + phase=SETTLED |
| 结束游戏 | `LuaEndGame` | 幂等 + 状态重置 + 玩家→旁观者 |

### 24.3 幂等保障机制

**双层幂等**：

1. **Redis SetNX 前置去重**（事件消费层）：
   - `game:event:processed:{traceID}` — TTL 7 天，GameEvent 消费幂等
   - `room:event:processed:{eventID}` — TTL 24h，RoomEvent 消费幂等
   - Redis 故障时 fail-open，依赖业务侧幂等兜底

2. **DB 后置幂等检查**：
   - `RoundStatusCredited` 检查 — `SettleRound` 入口
   - `GetBillByRoundTypeAndUser` 查重 — `SettleReward` 入口
   - `allSettled` 检查 — `SettleGame` 入口
   - `FirstOrCreate` grab_record — Kafka consumer 层
   - Lua 脚本内部状态检查（如 `phase==SETTLED` 则返回 code=2 跳过）

### 24.4 异步任务约束

所有异步 goroutine 须遵守（见 `GAME_SERVICE_BUG_ANALYSIS.md` 问题 1）：
- 使用 app-level context（`AsyncTaskRunner` 派生），而非 `context.Background()`
- 每任务设超时（5-30s），防止永久阻塞
- `defer recover()` 防崩溃
- `AsyncTaskRunner` 含 closed 状态保护，防止 `Wait` 后添加任务 panic
- `WaitGroup` 跟踪，优雅关闭时等待完成

### 24.5 机器人分配锁安全（Task 26 修复）

**问题**：原 `AcquireAssignLock` 用 `SetNX` + `Del(key)` 释放，TTL 过期后可能误删他人锁。

**修复**：
- `AcquireAssignLock` 生成 UUID token 作为 SetNX value
- `ReleaseAssignLock` 用 Lua 脚本 `if GET key == token then DEL key end`
- 调用方保存 `lockToken`，释放时传入

```go
// Acquire
token := uuid.New().String()
ok := redis.SetNX(key, token, ttl)

// Release (Lua)
script := "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end"
redis.Eval(script, []string{key}, token)
```

### 24.6 PacketGenerator 配置并发安全

**问题**：nacos 配置更新时，`PacketGenerator` 的 config/generator 字段被并发读写，导致数据竞争。

**修复**：
- config/generator 字段使用 `atomic.Pointer`
- `UpdateConfig` 时 `atomic.Pointer.Store` 原子替换
- `Generate` 方法每次调用 `atomic.Load` 拿一致快照
- 一次 Generate 调用内 config 一致性保证

### 24.7 Repository 聚合对象并发安全

**问题**：`DBRepositoryImpl`、`GormTransactionImpl` 的子仓储字段若懒初始化，并发访问会数据竞争。

**修复**：
- 构造函数中 eager 初始化所有子仓储
- 构造后字段只读，无锁并发安全
- `WithTransaction` 返回的 `Transaction` 接口实现同样 eager 初始化
