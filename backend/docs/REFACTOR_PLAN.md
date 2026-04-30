# 红包游戏系统完整重构方案

## 一、架构总览

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Gateway Layer                               │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  限流中间件 | 认证中间件 | 连接管理 | 消息路由 | 广播服务         │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┼───────────────┐
                    ▼               ▼               ▼
┌───────────────────────┐ ┌─────────────────┐ ┌─────────────────┐
│     Game Service      │ │ Settlement Svc  │ │  Other Services │
│  ┌─────────────────┐  │ │  ┌───────────┐  │ │                 │
│  │  GameAppService │  │ │  │Calculator │  │ │                 │
│  │  (协调器)       │  │ │  │RecordMgr  │  │ │                 │
│  └─────────────────┘  │ │  │RewardHdl  │  │ │                 │
│  ┌─────────────────┐  │ │  └───────────┘  │ │                 │
│  │  RoundManager   │  │ │                 │ │                 │
│  │  PenaltyService │  │ │                 │ │                 │
│  └─────────────────┘  │ │                 │ │                 │
│  ┌─────────────────┐  │ │                 │ │                 │
│  │  GrabService    │  │ │                 │ │                 │
│  │  (Lua原子操作)  │  │ │                 │ │                 │
│  └─────────────────┘  │ │                 │ │                 │
└───────────────────────┘ └─────────────────┘ └─────────────────┘
                    │               │
                    └───────┬───────┘
                            ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        Infrastructure Layer                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │
│  │ Redis (Lua)  │  │   MySQL      │  │ MessageQueue │                  │
│  │  原子操作    │  │   持久化     │  │   事件驱动   │                  │
│  └──────────────┘  └──────────────┘  └──────────────┘                  │
└─────────────────────────────────────────────────────────────────────────┘
```

## 二、模块职责划分

| 模块 | 职责 | 关键文件 |
|------|------|----------|
| **Gateway** | 限流、认证、连接管理、消息路由 | `middleware/ratelimit.go`, `server/server.go` |
| **Game** | 游戏逻辑、状态机、抢红包、惩罚 | `application/game_app_service.go`, `application/grab_service.go` |
| **Settlement** | 结算、抽佣、奖励分发、记录 | `service/settlement_service.go`, `service/calculator.go` |

## 三、并发控制策略

| 场景 | 控制方式 | 原因 |
|------|----------|------|
| 抢红包 | **Lua 脚本** | 高并发，需要原子性 |
| 发红包 | **Lua 脚本** | 需要原子性扣款和生成红包 |
| 状态转换 | **Lua 脚本** | 需要原子性检查和更新 |
| 玩家加入/离开 | **Lua 脚本** | 已实现，继续使用 |
| 跨房间操作 | **分布式锁** | 跨多个 Redis Key 的操作 |
| 结算处理 | **分布式锁** | 涉及数据库事务 |

---

## 四、文件结构

### 4.1 需要删除的文件

```
game/
├── application/
│   └── grab_service.go          # 删除，重构为新版本
├── state/
│   └── state_manager.go         # 删除，状态机逻辑合并到 GameAppService
└── scheduler/
    └── timeout_scheduler.go     # 删除，重构为新版本
```

### 4.2 需要新增/修改的文件

```
common/
└── message/
    ├── types.go                 # 更新：新增推送类型常量
    ├── payload.go               # 更新：新增推送数据结构
    └── errors.go                # 更新：新增错误码

gateway/
└── middleware/
    └── ratelimit.go             # 重构：增强限流器

game/
├── application/
│   ├── game_app_service.go      # 重构：核心游戏逻辑
│   ├── penalty_service.go       # 新增：惩罚服务
│   └── grab_service.go          # 重构：Lua原子操作
├── domain/
│   ├── game_state.go            # 新增：游戏状态定义
│   ├── round.go                 # 新增：回合实体
│   └── penalty.go               # 新增：惩罚实体
├── scheduler/
│   └── timeout_scheduler.go     # 重构：超时调度
└── infrastructure/
    └── persistence/
        └── redis/
            ├── keys.go          # 更新：新增Key
            ├── lua_scripts.go   # 保留：房间管理相关脚本
            └── lua_game.go      # 新增：游戏相关脚本

settlement/
├── service/
│   └── settlement_service.go    # 重构：增强结算
└── model/
    ├── request.go               # 更新：新增请求类型
    └── result.go                # 更新：新增结果类型
```

---

## 五、消息体系更新

### 5.1 类型常量更新 (`common/message/types.go`)

```go
package message

// ==================== 命令类型 ====================
const (
    CmdPing            = "ping"
    CmdJoinRoom        = "join_room"
    CmdAutoMatch       = "auto_match"
    CmdLeaveRoom       = "leave_room"
    CmdConfirmLeave    = "confirm_leave"
    CmdRoomState       = "room_state"
    CmdSelectSeat      = "select_seat"
    CmdCancelSeat      = "cancel_seat"
    CmdPlayerReady     = "player_ready"
    CmdSendPacket      = "send_packet"
    CmdGrabPacket      = "grab_packet"
    CmdGetRoomList     = "get_room_list"
    CmdGetRoomTypeList = "get_room_type_list"
)

// ==================== 推送类型 ====================
const (
    PushRoomState = "room_state"

    PushSpectatorJoined    = "spectator_joined"
    PushSpectatorLeft      = "spectator_left"
    PushPlayerJoined       = "player_joined"
    PushPlayerLeft         = "player_left"
    PushSeatSelected       = "seat_selected"
    PushSeatCancelled      = "seat_cancelled"
    PushPlayerReady        = "player_ready"
    PushPlayerDisconnected = "player_disconnected"
    PushPlayerReconnected  = "player_reconnected"

    PushGameStart       = "game_start"
    PushCountdown       = "countdown"
    PushRoundStart      = "round_start"
    PushRoundEnd        = "round_end"
    PushGameEnd         = "game_end"
    PushAutoDistribute  = "auto_distribute"
    PushPenalty         = "penalty"
    PushWaitReplacement = "wait_replacement"
    PushGameInterrupted = "game_interrupted"

    PushKicked = "kicked"
    PushError  = "error"
)

// ==================== 房间状态常量 ====================
const (
    RoomStatusWaiting = 1
    RoomStatusPlaying = 2
)

// ==================== 玩家状态常量 ====================
const (
    PlayerStatusWaiting  = 1
    PlayerStatusReady    = 2
    PlayerStatusPlaying  = 3
    PlayerStatusOffline  = 4
)

// ==================== 广播目标类型 ====================
const (
    TargetTypeRoom = "room"
    TargetTypeUser = "user"
    TargetTypeAll  = "all"
)

// ==================== 游戏阶段常量 ====================
const (
    GamePhaseWaiting    = "WAITING"
    GamePhaseCountdown  = "COUNTDOWN"
    GamePhaseRoundStart = "ROUND_START"
    GamePhaseGrabbing   = "GRABBING"
    GamePhaseSettling   = "SETTLING"
    GamePhaseWaitSend   = "WAIT_SEND"
    GamePhaseGameEnd    = "GAME_END"
)

// ==================== 惩罚类型常量 ====================
const (
    PenaltyTypeSendTimeout      = "send_timeout"
    PenaltyTypeLeaveDuringGame  = "leave_during_game"
    PenaltyTypeDisconnectTimeout = "disconnect_timeout"
)
```

### 5.2 推送数据结构更新 (`common/message/payload.go`)

在现有文件基础上新增以下结构体：

```go
// ==================== 推送数据结构 ====================

// ... 保留现有结构体 ...

// RoundStartPush 回合开始推送
type RoundStartPush struct {
    RoomID          string `json:"room_id"`
    RoundID         string `json:"round_id"`
    CurrentRound    int32  `json:"current_round"`
    SenderID        string `json:"sender_id"`
    SenderNickname  string `json:"sender_nickname"`
    SenderType      string `json:"sender_type"`
    TotalAmount     int64  `json:"total_amount"`
    Commission      int64  `json:"commission"`
    ActualAmount    int64  `json:"actual_amount"`
    AmountPerPlayer int64  `json:"amount_per_player,omitempty"`
    PacketCount     int32  `json:"packet_count"`
    GrabTimeout     int32  `json:"grab_timeout"`
}

// RoundEndPush 回合结束推送
type RoundEndPush struct {
    RoomID          string        `json:"room_id"`
    RoundID         string        `json:"round_id"`
    CurrentRound    int32         `json:"current_round"`
    SenderID        string        `json:"sender_id"`
    TotalAmount     int64         `json:"total_amount"`
    Commission      int64         `json:"commission"`
    Results         []RoundResult `json:"results"`
    MinAmountPlayer string        `json:"min_amount_player"`
    NextSenderID    string        `json:"next_sender_id"`
    IsGameEnd       bool          `json:"is_game_end"`
}

// AutoDistributePush 自动分配推送
type AutoDistributePush struct {
    RoomID           string          `json:"room_id"`
    RoundID          string          `json:"round_id"`
    DistributedCount int32           `json:"distributed_count"`
    Results          []DistributeResult `json:"results"`
}

// DistributeResult 分配结果
type DistributeResult struct {
    UserID   string `json:"user_id"`
    Amount   int64  `json:"amount"`
    Position int32  `json:"position"`
}

// PenaltyPush 惩罚推送
type PenaltyPush struct {
    RoomID        string `json:"room_id"`
    UserID        string `json:"user_id"`
    PenaltyType   string `json:"penalty_type"`
    PenaltyAmount int64  `json:"penalty_amount"`
    PenaltyCount  int32  `json:"penalty_count"`
    KickRequired  bool   `json:"kick_required"`
    Reason        string `json:"reason"`
}

// WaitReplacementPush 等待补位推送
type WaitReplacementPush struct {
    RoomID     string `json:"room_id"`
    VacantSeat int32  `json:"vacant_seat"`
    LeftUserID string `json:"left_user_id"`
    WaitTime   int32  `json:"wait_time"`
}

// GameInterruptedPush 游戏中断推送
type GameInterruptedPush struct {
    RoomID       string `json:"room_id"`
    Reason       string `json:"reason"`
    PenaltyShare int64  `json:"penalty_share"`
    Recipients   []string `json:"recipients"`
}

// GameEndPush 游戏结束推送
type GameEndPush struct {
    RoomID       string       `json:"room_id"`
    TotalRounds  int32        `json:"total_rounds"`
    FinalResults []GameResult `json:"final_results"`
}
```

### 5.3 错误码更新 (`common/message/errors.go`)

在现有文件基础上新增以下错误码：

```go
const (
    // ... 保留现有错误码 ...

    // 游戏相关错误码 40-59
    CodeNotInGrabbingPhase = 40
    CodeGrabTimeout        = 41
    CodePacketNotExist     = 42
    CodeRoundNotExist      = 43

    // 发红包相关错误码 50-59
    CodeInvalidRoundNumber    = 50
    CodeNotYourTurnToSend     = 51
    CodeNoPlayers             = 52
    CodeInsufficientBalance   = 53
    CodePacketsAlreadyExist   = 54

    // 惩罚相关错误码 60-69
    CodePenaltyApplied    = 60
    CodePlayerKicked      = 61
    CodeReplacementFailed = 62
)
```

---

## 六、Redis Key 设计

### 6.1 Key 定义 (`game/infrastructure/persistence/redis/keys.go`)

```go
package redis

import "fmt"

const (
    keyPrefix = "cashparty"

    // ==================== 房间相关 ====================
    KeyRoomHash       = keyPrefix + ":room:hash:%s"
    KeyRoomPlayers    = keyPrefix + ":room:players:%s"
    KeyRoomSpectators = keyPrefix + ":room:spectators:%s"
    KeyRoomSeats      = keyPrefix + ":room:seats:%s"
    KeyRoomSeatOwner  = keyPrefix + ":room:seat:owner:%s"
    KeyPlayerRoom     = keyPrefix + ":player:room:%s"
    KeyCandidateRooms = keyPrefix + ":room:candidate:%d"

    // ==================== 回合相关 ====================
    KeyRoundState           = keyPrefix + ":round:state:%s"
    KeyRoundPackets         = keyPrefix + ":round:packets:%s"
    KeyRoundAvailablePackets = keyPrefix + ":round:available_packets:%s"
    KeyRoundGrabbers        = keyPrefix + ":round:grabbers:%s"
    KeyRoundReward          = keyPrefix + ":round:reward:%s"
    KeyUserGrabbed          = keyPrefix + ":round:grabbed:%s:%s"
    KeyPacketInfo           = keyPrefix + ":packet:info:%s"

    // ==================== 玩家相关 ====================
    KeyRoomPlayer = keyPrefix + ":room:player:%s:%s"

    // ==================== 倒计时相关 ====================
    KeyCountdownStarted = keyPrefix + ":countdown:started:%s"
    KeyCountdownEndTime = keyPrefix + ":countdown:end_time:%s"

    // ==================== 惩罚相关 ====================
    KeyPenaltyCount  = keyPrefix + ":penalty:count:%s:%s"
    KeyPenaltyRecord = keyPrefix + ":penalty:record:%s:%s"

    // ==================== 超时相关 ====================
    KeyTimeout = keyPrefix + ":timeout:%s"

    // ==================== 结算相关 ====================
    KeySettlementDone = keyPrefix + ":settle:done:%d"
)

// ==================== 房间相关 Key ====================

func RoomHashKey(roomID string) string {
    return fmt.Sprintf(KeyRoomHash, roomID)
}

func RoomPlayersKey(roomID string) string {
    return fmt.Sprintf(KeyRoomPlayers, roomID)
}

func RoomSpectatorsKey(roomID string) string {
    return fmt.Sprintf(KeyRoomSpectators, roomID)
}

func RoomSeatsKey(roomID string) string {
    return fmt.Sprintf(KeyRoomSeats, roomID)
}

func RoomSeatOwnerKey(roomID string) string {
    return fmt.Sprintf(KeyRoomSeatOwner, roomID)
}

func PlayerRoomKey(userID string) string {
    return fmt.Sprintf(KeyPlayerRoom, userID)
}

func CandidateRoomsKey(roomType int) string {
    return fmt.Sprintf(KeyCandidateRooms, roomType)
}

// ==================== 回合相关 Key ====================

func RoundStateKey(roomID string) string {
    return fmt.Sprintf(KeyRoundState, roomID)
}

func RoundPacketsKey(roundID string) string {
    return fmt.Sprintf(KeyRoundPackets, roundID)
}

func RoundAvailablePacketsKey(roundID string) string {
    return fmt.Sprintf(KeyRoundAvailablePackets, roundID)
}

func RoundGrabbersKey(roundID string) string {
    return fmt.Sprintf(KeyRoundGrabbers, roundID)
}

func RoundRewardKey(roundID string) string {
    return fmt.Sprintf(KeyRoundReward, roundID)
}

func UserGrabbedKey(roundID, userID string) string {
    return fmt.Sprintf(KeyUserGrabbed, roundID, userID)
}

func PacketInfoKey(packetID string) string {
    return fmt.Sprintf(KeyPacketInfo, packetID)
}

// ==================== 玩家相关 Key ====================

func RoomPlayerKey(roomID, userID string) string {
    return fmt.Sprintf(KeyRoomPlayer, roomID, userID)
}

// ==================== 倒计时相关 Key ====================

func CountdownStartedKey(roomID string) string {
    return fmt.Sprintf(KeyCountdownStarted, roomID)
}

func CountdownEndTimeKey(roomID string) string {
    return fmt.Sprintf(KeyCountdownEndTime, roomID)
}

// ==================== 惩罚相关 Key ====================

func PenaltyCountKey(roomID, userID string) string {
    return fmt.Sprintf(KeyPenaltyCount, roomID, userID)
}

func PenaltyRecordKey(roomID, userID string) string {
    return fmt.Sprintf(KeyPenaltyRecord, roomID, userID)
}

// ==================== 超时相关 Key ====================

func TimeoutKey(timeoutType string) string {
    return fmt.Sprintf(KeyTimeout, timeoutType)
}

// ==================== 结算相关 Key ====================

func SettlementDoneKey(roundID int64) string {
    return fmt.Sprintf(KeySettlementDone, roundID)
}
```

---

## 七、Lua 脚本设计

### 7.1 游戏相关脚本 (`game/infrastructure/persistence/redis/lua_game.go`)

```go
package redis

// LuaGrabPacketV2 抢红包脚本（增强版）
// KEYS: [availablePacketsKey, userGrabKey, grabbersKey, roundStateKey, playerStatsKey]
// ARGV: [userID, now, grabTimeout, roomID, keyPrefix]
// 返回: {code, packetID, amount, position, errMsg, isLast}
const LuaGrabPacketV2 = `
local availablePacketsKey = KEYS[1]
local userGrabKey = KEYS[2]
local grabbersKey = KEYS[3]
local roundStateKey = KEYS[4]
local playerStatsKey = KEYS[5]

local userID = ARGV[1]
local now = tonumber(ARGV[2])
local grabTimeout = tonumber(ARGV[3])
local roomID = ARGV[4]
local keyPrefix = ARGV[5]

-- 1. 检查游戏阶段
local phase = redis.call('HGET', roundStateKey, 'phase')
if not phase or phase ~= 'GRABBING' then
    return {40, 0, 0, 0, 'not in grabbing phase', 0}
end

-- 2. 检查抢红包是否超时
local grabEndTime = tonumber(redis.call('HGET', roundStateKey, 'grab_end_time') or 0)
if grabEndTime > 0 and now > grabEndTime then
    return {41, 0, 0, 0, 'grab timeout', 0}
end

-- 3. 检查是否已抢
if redis.call('EXISTS', userGrabKey) == 1 then
    return {21, 0, 0, 0, 'already grabbed', 0}
end

-- 4. 获取可用红包
local packetIDStr = redis.call('LPOP', availablePacketsKey)
if not packetIDStr then
    return {22, 0, 0, 0, 'no available packet', 0}
end

-- 5. 获取红包详情
local packetKey = keyPrefix .. ':packet:info:' .. packetIDStr
local packetData = redis.call('GET', packetKey)
if not packetData then
    return {23, 0, 0, 0, 'packet info not found', 0}
end

local packet = cjson.decode(packetData)
local amount = packet.amount
local position = packet.position

-- 6. 更新抢红包状态
redis.call('SET', userGrabKey, '1', 'EX', 86400)
redis.call('SADD', grabbersKey, userID)

-- 7. 更新玩家统计
if playerStatsKey and playerStatsKey ~= '' then
    redis.call('HINCRBY', playerStatsKey, 'total_grab', amount)
    redis.call('HINCRBY', playerStatsKey, 'grab_count', 1)
end

-- 8. 更新红包状态
packet.is_grabbed = true
packet.grabber_id = userID
packet.grabbed_at = now
redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)

-- 9. 检查是否最后一个
local grabbedCount = redis.call('SCARD', grabbersKey)
local totalPackets = tonumber(redis.call('HGET', roundStateKey, 'packet_count') or 5)
local isLast = 0
if grabbedCount >= totalPackets then
    isLast = 1
    redis.call('HSET', roundStateKey, 'phase', 'SETTLING')
end

return {0, tonumber(packetIDStr), amount, position, '', isLast}
`

// LuaAutoDistributePackets 自动分配未抢红包
// KEYS: [availablePacketsKey, grabbersKey, roundStateKey, playersKey, roomHashKey]
// ARGV: [now, keyPrefix, roundID]
// 返回: {code, distributedCount, results}
const LuaAutoDistributePackets = `
local availablePacketsKey = KEYS[1]
local grabbersKey = KEYS[2]
local roundStateKey = KEYS[3]
local playersKey = KEYS[4]
local roomHashKey = KEYS[5]

local now = tonumber(ARGV[1])
local keyPrefix = ARGV[2]
local roundID = ARGV[3]

-- 1. 获取所有玩家
local allPlayers = redis.call('HKEYS', playersKey)
if not allPlayers or #allPlayers == 0 then
    return {0, 0, {}}
end

-- 2. 获取已抢玩家
local grabbedPlayers = redis.call('SMEMBERS', grabbersKey)
local grabbedSet = {}
for _, uid in ipairs(grabbedPlayers) do
    grabbedSet[uid] = true
end

-- 3. 找出未抢玩家
local ungrabbedPlayers = {}
for _, uid in ipairs(allPlayers) do
    if not grabbedSet[uid] then
        table.insert(ungrabbedPlayers, uid)
    end
end

if #ungrabbedPlayers == 0 then
    return {0, 0, {}}
end

-- 4. 获取剩余红包
local packets = redis.call('LRANGE', availablePacketsKey, 0, -1)
if not packets or #packets == 0 then
    return {0, 0, {}}
end

-- 5. 随机分配
math.randomseed(now)
local results = {}

for i, packetIDStr in ipairs(packets) do
    local playerIdx = ((i - 1) % #ungrabbedPlayers) + 1
    local userID = ungrabbedPlayers[playerIdx]
    
    local packetKey = keyPrefix .. ':packet:info:' .. packetIDStr
    local packetData = redis.call('GET', packetKey)
    
    if packetData then
        local packet = cjson.decode(packetData)
        
        packet.is_grabbed = true
        packet.grabber_id = userID
        packet.grabbed_at = now
        packet.auto_assigned = true
        
        redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)
        redis.call('SADD', grabbersKey, userID)
        
        local userGrabKey = keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID
        redis.call('SET', userGrabKey, '1', 'EX', 86400)
        
        -- 更新玩家统计
        local roomID = roomHashKey:match(':(%d+)$')
        if roomID then
            local playerStatsKey = keyPrefix .. ':room:player:' .. roomID .. ':' .. userID
            redis.call('HINCRBY', playerStatsKey, 'total_grab', packet.amount)
        end
        
        table.insert(results, {userID, packet.amount, packet.position})
    end
end

-- 6. 清空可用红包队列
redis.call('DEL', availablePacketsKey)

-- 7. 更新状态为结算中
redis.call('HSET', roundStateKey, 'phase', 'SETTLING')

return {0, #results, results}
`

// LuaSendPacketV2 发红包脚本（增强版）
// KEYS: [roomHashKey, playersKey, roundStateKey, availablePacketsKey, grabbersKey]
// ARGV: [senderID, totalAmount, commission, actualAmount, roundNo, now, grabTimeout, keyPrefix, packetAmountsJson]
// 返回: {code, roundID, errMsg}
const LuaSendPacketV2 = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local roundStateKey = KEYS[3]
local availablePacketsKey = KEYS[4]
local grabbersKey = KEYS[5]

local senderID = ARGV[1]
local totalAmount = tonumber(ARGV[2])
local commission = tonumber(ARGV[3])
local actualAmount = tonumber(ARGV[4])
local roundNo = tonumber(ARGV[5])
local now = tonumber(ARGV[6])
local grabTimeout = tonumber(ARGV[7])
local keyPrefix = ARGV[8]
local packetAmountsJson = ARGV[9]

-- 1. 检查房间状态
local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status ~= 2 then
    return {6, 0, 'game not in playing status'}
end

-- 2. 检查当前轮次
local currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
if roundNo ~= currentRound + 1 then
    return {50, 0, 'invalid round number'}
end

-- 3. 检查发送者（非第一局）
if senderID ~= '0' and roundNo > 1 then
    local nextSender = redis.call('HGET', roomHashKey, 'next_sender_id') or ''
    if nextSender ~= senderID then
        return {51, 0, 'not your turn to send'}
    end
end

-- 4. 检查是否已有红包
local existingPackets = redis.call('LLEN', availablePacketsKey)
if existingPackets > 0 then
    return {20, 0, 'packets already exist for this round'}
end

-- 5. 解析红包金额
local amounts = cjson.decode(packetAmountsJson)
local packetCount = #amounts

-- 6. 生成红包
local roundID = redis.call('INCR', 'global:round_id')
local roundIDStr = tostring(roundID)
local packetIDs = {}

for i, amount in ipairs(amounts) do
    local packetID = redis.call('INCR', 'global:packet_id')
    local packetKey = keyPrefix .. ':packet:info:' .. packetID
    
    local packet = {
        packet_id = packetID,
        round_id = roundID,
        sender_id = senderID,
        amount = amount,
        position = i,
        is_grabbed = false,
        created_at = now
    }
    
    redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)
    redis.call('RPUSH', availablePacketsKey, packetID)
    table.insert(packetIDs, packetID)
end

-- 7. 更新轮次状态
local grabEndTime = now + grabTimeout
redis.call('HMSET', roundStateKey,
    'round_id', roundIDStr,
    'round_no', roundNo,
    'sender_id', senderID,
    'total_amount', totalAmount,
    'commission', commission,
    'actual_amount', actualAmount,
    'packet_count', packetCount,
    'phase', 'GRABBING',
    'grab_end_time', grabEndTime,
    'created_at', now
)
redis.call('EXPIRE', roundStateKey, 86400)

-- 8. 清空抢红包记录
redis.call('DEL', grabbersKey)

-- 9. 更新房间轮次
redis.call('HSET', roomHashKey, 'current_round', roundNo)

-- 10. 更新发送者统计
if senderID ~= '0' then
    local roomID = roomHashKey:match(':(%d+)$')
    if roomID then
        local playerStatsKey = keyPrefix .. ':room:player:' .. roomID .. ':' .. senderID
        redis.call('HINCRBY', playerStatsKey, 'total_send', totalAmount)
        redis.call('HINCRBY', playerStatsKey, 'send_count', 1)
    end
end

return {0, roundID, 'success'}
`

// LuaSystemSendFirstRound 系统发送第一局红包
// KEYS: [roomHashKey, playersKey, roundStateKey, availablePacketsKey, grabbersKey]
// ARGV: [totalAmount, commission, actualAmount, now, grabTimeout, keyPrefix, packetAmountsJson]
// 返回: {code, roundID, amountPerPlayer}
const LuaSystemSendFirstRound = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local roundStateKey = KEYS[3]
local availablePacketsKey = KEYS[4]
local grabbersKey = KEYS[5]

local totalAmount = tonumber(ARGV[1])
local commission = tonumber(ARGV[2])
local actualAmount = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local grabTimeout = tonumber(ARGV[5])
local keyPrefix = ARGV[6]
local packetAmountsJson = ARGV[7]

-- 1. 检查房间状态
local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status ~= 2 then
    return {6, 0, 'game not in playing status'}
end

-- 2. 检查是否第一局
local currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
if currentRound ~= 0 then
    return {50, 0, 'not first round'}
end

-- 3. 获取玩家数量
local playerCount = tonumber(redis.call('HGET', roomHashKey, 'player_count') or 0)
if playerCount == 0 then
    return {52, 0, 'no players'}
end

-- 4. 计算每个玩家应付金额
local amountPerPlayer = math.floor(totalAmount / playerCount)

-- 5. 解析红包金额
local amounts = cjson.decode(packetAmountsJson)
local packetCount = #amounts

-- 6. 生成红包
local roundID = redis.call('INCR', 'global:round_id')
local roundIDStr = tostring(roundID)
local packetIDs = {}

for i, amount in ipairs(amounts) do
    local packetID = redis.call('INCR', 'global:packet_id')
    local packetKey = keyPrefix .. ':packet:info:' .. packetID
    
    local packet = {
        packet_id = packetID,
        round_id = roundID,
        sender_id = '0',
        sender_type = 'system',
        amount = amount,
        position = i,
        is_grabbed = false,
        created_at = now
    }
    
    redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)
    redis.call('RPUSH', availablePacketsKey, packetID)
    table.insert(packetIDs, packetID)
end

-- 7. 更新轮次状态
local grabEndTime = now + grabTimeout
redis.call('HMSET', roundStateKey,
    'round_id', roundIDStr,
    'round_no', 1,
    'sender_id', '0',
    'sender_type', 'system',
    'total_amount', totalAmount,
    'commission', commission,
    'actual_amount', actualAmount,
    'amount_per_player', amountPerPlayer,
    'packet_count', packetCount,
    'phase', 'GRABBING',
    'grab_end_time', grabEndTime,
    'created_at', now
)
redis.call('EXPIRE', roundStateKey, 86400)

-- 8. 清空抢红包记录
redis.call('DEL', grabbersKey)

-- 9. 更新房间轮次
redis.call('HSET', roomHashKey, 'current_round', 1)

return {0, roundID, amountPerPlayer}
`

// LuaSettleRound 结算回合
// KEYS: [roundStateKey, grabbersKey, playersKey, roomHashKey]
// ARGV: [roundID, now]
// 返回: {code, roundNo, senderID, totalAmount, minAmountPlayer, isGameEnd, results}
const LuaSettleRound = `
local roundStateKey = KEYS[1]
local grabbersKey = KEYS[2]
local playersKey = KEYS[3]
local roomHashKey = KEYS[4]

local roundID = ARGV[1]
local now = tonumber(ARGV[2])

-- 1. 获取轮次信息
local roundInfo = redis.call('HGETALL', roundStateKey)
if not roundInfo or #roundInfo == 0 then
    return {1, 'round not found'}
end

local roundData = {}
for i = 1, #roundInfo, 2 do
    roundData[roundInfo[i]] = roundInfo[i + 1]
end

local roundNo = tonumber(roundData['round_no'] or 0)
local senderID = roundData['sender_id']
local totalAmount = tonumber(roundData['total_amount'] or 0)

-- 2. 获取所有抢红包记录
local grabberIDs = redis.call('SMEMBERS', grabbersKey)
local results = {}
local minAmount = -1
local minAmountPlayer = ''

for _, userID in ipairs(grabberIDs) do
    local playerData = redis.call('HGET', playersKey, userID)
    if playerData then
        local player = cjson.decode(playerData)
        local grabAmount = player.total_grab or 0
        
        table.insert(results, {userID, grabAmount, player.nickname or ''})
        
        if minAmount < 0 or grabAmount < minAmount then
            minAmount = grabAmount
            minAmountPlayer = userID
        end
    end
end

-- 3. 更新状态为等待发送
redis.call('HSET', roundStateKey, 'phase', 'WAIT_SEND')
redis.call('HSET', roundStateKey, 'settled_at', now)

-- 4. 设置下一轮发送者
if minAmountPlayer ~= '' then
    redis.call('HSET', roomHashKey, 'next_sender_id', minAmountPlayer)
end

-- 5. 检查是否游戏结束
local maxRounds = tonumber(redis.call('HGET', roomHashKey, 'max_rounds') or 10)
local isGameEnd = 0
if roundNo >= maxRounds then
    isGameEnd = 1
    redis.call('HSET', roundStateKey, 'phase', 'GAME_END')
    redis.call('HSET', roomHashKey, 'status', 3)
end

return {0, roundNo, senderID, totalAmount, minAmountPlayer, isGameEnd, results}
`

// LuaHandlePenalty 处理惩罚
// KEYS: [penaltyCountKey, roomHashKey, playersKey, roundStateKey]
// ARGV: [userID, roomFee, now, penaltyType]
// 返回: {code, newCount, penaltyAmount, kickRequired}
const LuaHandlePenalty = `
local penaltyCountKey = KEYS[1]
local roomHashKey = KEYS[2]
local playersKey = KEYS[3]
local roundStateKey = KEYS[4]

local userID = ARGV[1]
local roomFee = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local penaltyType = ARGV[4]

-- 1. 获取惩罚次数
local penaltyCount = tonumber(redis.call('GET', penaltyCountKey) or 0)

-- 2. 增加惩罚次数
local newCount = redis.call('INCR', penaltyCountKey)
redis.call('EXPIRE', penaltyCountKey, 86400)

-- 3. 记录惩罚
local roomID = roomHashKey:match(':(%d+)$')
if roomID then
    local penaltyRecordKey = 'cashparty:penalty:record:' .. roomID .. ':' .. userID
    redis.call('RPUSH', penaltyRecordKey, cjson.encode({
        user_id = userID,
        penalty_type = penaltyType,
        count = newCount,
        amount = roomFee,
        created_at = now
    }))
    redis.call('EXPIRE', penaltyRecordKey, 86400)
end

-- 4. 判断是否需要踢出
local kickRequired = 0
if newCount >= 2 then
    kickRequired = 1
end

-- 5. 更新玩家状态
local playerData = redis.call('HGET', playersKey, userID)
if playerData then
    local player = cjson.decode(playerData)
    player.penalty_count = newCount
    player.last_penalty_at = now
    redis.call('HSET', playersKey, userID, cjson.encode(player))
end

return {0, newCount, roomFee, kickRequired}
`

// LuaDistributePenalty 分配惩罚金额
// KEYS: [roomHashKey, playersKey]
// ARGV: [penaltyAmount, excludeUserIDsJson]
// 返回: {code, shareAmount, recipientCount, recipients}
const LuaDistributePenalty = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]

local penaltyAmount = tonumber(ARGV[1])
local excludeUserIDsJson = ARGV[2]

-- 1. 解析排除玩家
local excludeSet = {}
if excludeUserIDsJson and excludeUserIDsJson ~= '' then
    local excludeList = cjson.decode(excludeUserIDsJson)
    for _, uid in ipairs(excludeList) do
        excludeSet[uid] = true
    end
end

-- 2. 获取剩余玩家
local allPlayers = redis.call('HKEYS', playersKey)
local remainingPlayers = {}
for _, uid in ipairs(allPlayers) do
    if not excludeSet[uid] then
        table.insert(remainingPlayers, uid)
    end
end

if #remainingPlayers == 0 then
    return {0, 0, 0, {}}
end

-- 3. 计算分配金额
local shareAmount = math.floor(penaltyAmount / #remainingPlayers)

-- 4. 分配金额
local recipients = {}
for _, uid in ipairs(remainingPlayers) do
    local playerData = redis.call('HGET', playersKey, uid)
    if playerData then
        local player = cjson.decode(playerData)
        player.total_grab = (player.total_grab or 0) + shareAmount
        redis.call('HSET', playersKey, uid, cjson.encode(player))
        table.insert(recipients, uid)
    end
end

return {0, shareAmount, #remainingPlayers, recipients}
`

// LuaTransitionPhase 状态转换
// KEYS: [roomHashKey, roundStateKey]
// ARGV: [fromPhase, toPhase, now, deadline]
// 返回: {code, errMsg, currentPhase}
const LuaTransitionPhase = `
local roomHashKey = KEYS[1]
local roundStateKey = KEYS[2]

local fromPhase = ARGV[1]
local toPhase = ARGV[2]
local now = tonumber(ARGV[3])
local deadline = tonumber(ARGV[4])

-- 1. 检查当前阶段
local currentPhase = redis.call('HGET', roundStateKey, 'phase') or ''
if currentPhase ~= fromPhase then
    return {1, 'phase mismatch', currentPhase}
end

-- 2. 检查是否已超时
if deadline > 0 and now > deadline then
    return {2, 'phase timeout', currentPhase}
end

-- 3. 转换阶段
redis.call('HSET', roundStateKey, 'phase', toPhase)
redis.call('HSET', roundStateKey, 'phase_entered_at', now)
if deadline > 0 then
    redis.call('HSET', roundStateKey, 'phase_deadline', deadline)
end

-- 4. 更新房间状态（如果需要）
if toPhase == 'GAME_END' then
    redis.call('HSET', roomHashKey, 'status', 3)
elseif toPhase == 'GRABBING' then
    redis.call('HSET', roomHashKey, 'status', 2)
end

return {0, 'success', toPhase}
`

// LuaGetRoundResults 获取回合结果
// KEYS: [roundStateKey, grabbersKey, playersKey]
// ARGV: [roundID]
// 返回: {code, results}
const LuaGetRoundResults = `
local roundStateKey = KEYS[1]
local grabbersKey = KEYS[2]
local playersKey = KEYS[3]

local roundID = ARGV[1]

-- 1. 获取所有抢红包玩家
local grabberIDs = redis.call('SMEMBERS', grabbersKey)
local results = {}

for _, userID in ipairs(grabberIDs) do
    local playerData = redis.call('HGET', playersKey, userID)
    if playerData then
        local player = cjson.decode(playerData)
        table.insert(results, {
            user_id = userID,
            nickname = player.nickname or '',
            grab_amount = player.total_grab or 0,
            send_amount = player.total_send or 0
        })
    end
end

return {0, cjson.encode(results)}
`
```

### 7.2 保留原有脚本 (`game/infrastructure/persistence/redis/lua_scripts.go`)

保留房间管理相关的脚本，删除游戏相关的脚本（`LuaGrabPacket`、`LuaInitRoundPackets`等移到 `lua_game.go`）：

```go
package redis

// 保留以下脚本：
// - LuaJoinAsSpectator
// - LuaPickAndJoinRoom
// - LuaSelectSeat
// - LuaCancelSeat
// - LuaSetReady
// - LuaLeaveRoom
// - LuaProcessSeatTimeout
// - LuaProcessReadyTimeout
// - LuaProcessDisconnectTimeout
// - LuaHandleReconnect
// - LuaUpdatePlayerStatus
// - LuaTryStartCountdown
// - LuaTryStartGame

// 删除以下脚本（移到 lua_game.go）：
// - LuaInitRoundPackets
// - LuaGrabPacket
```

---

## 八、核心服务实现

### 8.1 游戏状态定义 (`game/domain/game_state.go`)

```go
package domain

import "time"

type GamePhase int

const (
    PhaseWaiting GamePhase = iota + 1
    PhaseCountdown
    PhaseRoundStart
    PhaseGrabbing
    PhaseSettling
    PhaseWaitSend
    PhaseGameEnd
)

func (p GamePhase) String() string {
    switch p {
    case PhaseWaiting:
        return "WAITING"
    case PhaseCountdown:
        return "COUNTDOWN"
    case PhaseRoundStart:
        return "ROUND_START"
    case PhaseGrabbing:
        return "GRABBING"
    case PhaseSettling:
        return "SETTLING"
    case PhaseWaitSend:
        return "WAIT_SEND"
    case PhaseGameEnd:
        return "GAME_END"
    default:
        return "UNKNOWN"
    }
}

type GameState struct {
    RoomID         string    `json:"room_id"`
    Phase          GamePhase `json:"phase"`
    CurrentRound   int       `json:"current_round"`
    MaxRounds      int       `json:"max_rounds"`
    NextSenderID   string    `json:"next_sender_id"`
    PhaseEnteredAt int64     `json:"phase_entered_at"`
    PhaseDeadline  int64     `json:"phase_deadline"`
}

type RoundInfo struct {
    RoundNo      int    `json:"round_no"`
    RoundID      string `json:"round_id"`
    SenderID     string `json:"sender_id"`
    SenderType   string `json:"sender_type"`
    TotalAmount  int64  `json:"total_amount"`
    Commission   int64  `json:"commission"`
    ActualAmount int64  `json:"actual_amount"`
    PacketCount  int    `json:"packet_count"`
    Status       int    `json:"status"`
    GrabEndTime  int64  `json:"grab_end_time"`
    CreatedAt    int64  `json:"created_at"`
}

type GrabResult struct {
    PacketID string `json:"packet_id"`
    Amount   int64  `json:"amount"`
    Position int    `json:"position"`
    IsLast   bool   `json:"is_last"`
    IsAuto   bool   `json:"is_auto"`
}

type CommissionConfig struct {
    Rate float64 `json:"rate"`
}

func DefaultCommissionConfig() *CommissionConfig {
    return &CommissionConfig{Rate: 0.05}
}

func (c *CommissionConfig) Calculate(totalAmount int64) int64 {
    return int64(float64(totalAmount) * c.Rate)
}
```

### 8.2 惩罚实体 (`game/domain/penalty.go`)

```go
package domain

type PenaltyType int

const (
    PenaltyTypeSendTimeout PenaltyType = iota + 1
    PenaltyTypeLeaveDuringGame
    PenaltyTypeDisconnectTimeout
)

func (t PenaltyType) String() string {
    switch t {
    case PenaltyTypeSendTimeout:
        return "send_timeout"
    case PenaltyTypeLeaveDuringGame:
        return "leave_during_game"
    case PenaltyTypeDisconnectTimeout:
        return "disconnect_timeout"
    default:
        return "unknown"
    }
}

type PenaltyRecord struct {
    UserID      string      `json:"user_id"`
    RoomID      string      `json:"room_id"`
    RoundNo     int         `json:"round_no"`
    PenaltyType PenaltyType `json:"penalty_type"`
    Amount      int64       `json:"amount"`
    Count       int         `json:"count"`
    CreatedAt   int64       `json:"created_at"`
}

type PenaltyResult struct {
    Applied      bool   `json:"applied"`
    Amount       int64  `json:"amount"`
    Count        int    `json:"count"`
    KickRequired bool   `json:"kick_required"`
    Reason       string `json:"reason"`
}

type PenaltyPolicy struct {
    FirstPenaltyAmount  int64
    SecondPenaltyAmount int64
    KickOnSecond        bool
}

func DefaultPenaltyPolicy() *PenaltyPolicy {
    return &PenaltyPolicy{
        FirstPenaltyAmount:  0,
        SecondPenaltyAmount: 0,
        KickOnSecond:        true,
    }
}
```

### 8.3 超时调度器 (`game/scheduler/timeout_scheduler.go`)

```go
package scheduler

import (
    "context"
    "fmt"
    "time"

    "github.com/cashparty/backend/common/logger"
    cRedis "github.com/cashparty/backend/common/redis"
)

type TimeoutType string

const (
    TimeoutTypeCountdown TimeoutType = "countdown"
    TimeoutTypeGrab      TimeoutType = "grab"
    TimeoutTypeSend      TimeoutType = "send"
    TimeoutTypeReplace   TimeoutType = "replace"
)

type TimeoutConfig struct {
    Duration      time.Duration
    CheckInterval time.Duration
}

var TimeoutConfigs = map[TimeoutType]TimeoutConfig{
    TimeoutTypeCountdown: {Duration: 3 * time.Second, CheckInterval: 100 * time.Millisecond},
    TimeoutTypeGrab:      {Duration: 10 * time.Second, CheckInterval: 500 * time.Millisecond},
    TimeoutTypeSend:      {Duration: 30 * time.Second, CheckInterval: 1 * time.Second},
    TimeoutTypeReplace:   {Duration: 30 * time.Second, CheckInterval: 1 * time.Second},
}

type TimeoutHandler func(ctx context.Context, roomID string, data string)

type TimeoutScheduler struct {
    redis    *cRedis.Client
    handlers map[TimeoutType]TimeoutHandler
    ctx      context.Context
    cancel   context.CancelFunc
}

func NewTimeoutScheduler(redis *cRedis.Client) *TimeoutScheduler {
    ctx, cancel := context.WithCancel(context.Background())
    return &TimeoutScheduler{
        redis:    redis,
        handlers: make(map[TimeoutType]TimeoutHandler),
        ctx:      ctx,
        cancel:   cancel,
    }
}

func (s *TimeoutScheduler) RegisterHandler(timeoutType TimeoutType, handler TimeoutHandler) {
    s.handlers[timeoutType] = handler
}

func (s *TimeoutScheduler) Start() {
    for timeoutType, config := range TimeoutConfigs {
        go s.runChecker(timeoutType, config)
    }
    logger.Info("timeout scheduler started")
}

func (s *TimeoutScheduler) Stop() {
    s.cancel()
    logger.Info("timeout scheduler stopped")
}

func (s *TimeoutScheduler) SetTimeout(ctx context.Context, timeoutType TimeoutType, roomID, data string, customDuration ...time.Duration) {
    config := TimeoutConfigs[timeoutType]
    duration := config.Duration
    if len(customDuration) > 0 {
        duration = customDuration[0]
    }

    expireAt := time.Now().Add(duration).Unix()
    key := s.getTimeoutKey(timeoutType)
    member := fmt.Sprintf("%s:%s", roomID, data)

    s.redis.ZAdd(ctx, key, cRedis.Z{
        Score:  float64(expireAt),
        Member: member,
    })
}

func (s *TimeoutScheduler) ClearTimeout(ctx context.Context, timeoutType TimeoutType, roomID, data string) {
    key := s.getTimeoutKey(timeoutType)
    member := fmt.Sprintf("%s:%s", roomID, data)
    s.redis.ZRem(ctx, key, member)
}

func (s *TimeoutScheduler) ClearRoomTimeouts(ctx context.Context, timeoutType TimeoutType, roomID string) {
    key := s.getTimeoutKey(timeoutType)
    members, _ := s.redis.ZRange(ctx, key, 0, -1).Result()
    for _, member := range members {
        if len(member) > len(roomID) && member[:len(roomID)] == roomID {
            s.redis.ZRem(ctx, key, member)
        }
    }
}

func (s *TimeoutScheduler) getTimeoutKey(timeoutType TimeoutType) string {
    return fmt.Sprintf("cashparty:timeout:%s", timeoutType)
}

func (s *TimeoutScheduler) runChecker(timeoutType TimeoutType, config TimeoutConfig) {
    ticker := time.NewTicker(config.CheckInterval)
    defer ticker.Stop()

    for {
        select {
        case <-s.ctx.Done():
            return
        case <-ticker.C:
            s.checkTimeouts(timeoutType)
        }
    }
}

func (s *TimeoutScheduler) checkTimeouts(timeoutType TimeoutType) {
    key := s.getTimeoutKey(timeoutType)
    now := time.Now().Unix()

    members, err := s.redis.ZRangeByScore(s.ctx, key, &cRedis.ZRangeBy{
        Min: "-inf",
        Max: fmt.Sprintf("%d", now),
    }).Result()

    if err != nil || len(members) == 0 {
        return
    }

    for _, member := range members {
        s.redis.ZRem(s.ctx, key, member)

        roomID, data := s.parseMember(member)
        if roomID == "" {
            continue
        }

        if handler, ok := s.handlers[timeoutType]; ok {
            logger.Info("timeout triggered", "type", timeoutType, "room_id", roomID, "data", data)
            go handler(s.ctx, roomID, data)
        }
    }
}

func (s *TimeoutScheduler) parseMember(member string) (string, string) {
    for i := 0; i < len(member); i++ {
        if member[i] == ':' {
            return member[:i], member[i+1:]
        }
    }
    return member, ""
}
```

### 8.4 抢红包服务 (`game/application/grab_service.go`)

```go
package application

import (
    "context"
    "fmt"
    "time"

    "github.com/cashparty/backend/common/message"
    cRedis "github.com/cashparty/backend/common/redis"
    "github.com/cashparty/backend/game/domain"
    "github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

type GrabService struct {
    redis *cRedis.Client
}

func NewGrabService(redis *cRedis.Client) *GrabService {
    return &GrabService{redis: redis}
}

func (s *GrabService) GrabPacket(ctx context.Context, roomID, roundID, userID string) (*domain.GrabResult, error) {
    keys := []string{
        redis.RoundAvailablePacketsKey(roundID),
        redis.UserGrabbedKey(roundID, userID),
        redis.RoundGrabbersKey(roundID),
        redis.RoundStateKey(roomID),
        redis.RoomPlayerKey(roomID, userID),
    }

    args := []interface{}{
        userID,
        time.Now().Unix(),
        int64(10),
        roomID,
        "cashparty",
    }

    res, err := s.redis.Eval(ctx, redis.LuaGrabPacketV2, keys, args...).Slice()
    if err != nil {
        return nil, err
    }

    code := parseInt(res[0])
    if code != 0 {
        errMsg := ""
        if len(res) > 4 {
            errMsg = fmt.Sprintf("%v", res[4])
        }
        return nil, message.NewErrorWithMsg(code, errMsg)
    }

    return &domain.GrabResult{
        PacketID: fmt.Sprintf("%v", res[1]),
        Amount:   parseInt64(res[2]),
        Position: parseInt(res[3]),
        IsLast:   parseInt(res[5]) == 1,
    }, nil
}

func (s *GrabService) AutoDistribute(ctx context.Context, roomID, roundID string) (int, error) {
    keys := []string{
        redis.RoundAvailablePacketsKey(roundID),
        redis.RoundGrabbersKey(roundID),
        redis.RoundStateKey(roomID),
        redis.RoomPlayersKey(roomID),
        redis.RoomHashKey(roomID),
    }

    args := []interface{}{
        time.Now().Unix(),
        "cashparty",
        roundID,
    }

    res, err := s.redis.Eval(ctx, redis.LuaAutoDistributePackets, keys, args...).Slice()
    if err != nil {
        return 0, err
    }

    return parseInt(res[1]), nil
}

func parseInt(v interface{}) int {
    switch val := v.(type) {
    case int64:
        return int(val)
    case float64:
        return int(val)
    }
    return 0
}

func parseInt64(v interface{}) int64 {
    switch val := v.(type) {
    case int64:
        return val
    case float64:
        return int64(val)
    }
    return 0
}
```

### 8.5 惩罚服务 (`game/application/penalty_service.go`)

```go
package application

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/cashparty/backend/common/logger"
    cRedis "github.com/cashparty/backend/common/redis"
    "github.com/cashparty/backend/game/domain"
    "github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

type PenaltyService struct {
    redis  *cRedis.Client
    policy *domain.PenaltyPolicy
}

func NewPenaltyService(redis *cRedis.Client, policy *domain.PenaltyPolicy) *PenaltyService {
    if policy == nil {
        policy = domain.DefaultPenaltyPolicy()
    }
    return &PenaltyService{
        redis:  redis,
        policy: policy,
    }
}

func (s *PenaltyService) ApplyPenalty(ctx context.Context, roomID, userID string, penaltyType domain.PenaltyType, roomFee int64) (*domain.PenaltyResult, error) {
    keys := []string{
        redis.PenaltyCountKey(roomID, userID),
        redis.RoomHashKey(roomID),
        redis.RoomPlayersKey(roomID),
        redis.RoundStateKey(roomID),
    }

    args := []interface{}{
        userID,
        roomFee,
        time.Now().Unix(),
        penaltyType.String(),
    }

    res, err := s.redis.Eval(ctx, redis.LuaHandlePenalty, keys, args...).Slice()
    if err != nil {
        return nil, err
    }

    code := parseInt(res[0])
    if code != 0 {
        return nil, fmt.Errorf("handle penalty failed")
    }

    count := parseInt(res[1])
    amount := parseInt64(res[2])
    kickRequired := parseInt(res[3]) == 1

    logger.Info("penalty applied",
        "room_id", roomID,
        "user_id", userID,
        "penalty_type", penaltyType,
        "count", count,
        "amount", amount,
        "kick_required", kickRequired,
    )

    return &domain.PenaltyResult{
        Applied:      true,
        Amount:       amount,
        Count:        count,
        KickRequired: kickRequired,
        Reason:       penaltyType.String(),
    }, nil
}

func (s *PenaltyService) GetPenaltyCount(ctx context.Context, roomID, userID string) int {
    key := redis.PenaltyCountKey(roomID, userID)
    val, _ := s.redis.Get(ctx, key).Int()
    return val
}

func (s *PenaltyService) DistributePenalty(ctx context.Context, roomID string, penaltyAmount int64, excludeUserIDs []string) (int64, []string, error) {
    keys := []string{
        redis.RoomHashKey(roomID),
        redis.RoomPlayersKey(roomID),
    }

    excludeJSON, _ := json.Marshal(excludeUserIDs)

    args := []interface{}{
        penaltyAmount,
        string(excludeJSON),
    }

    res, err := s.redis.Eval(ctx, redis.LuaDistributePenalty, keys, args...).Slice()
    if err != nil {
        return 0, nil, err
    }

    shareAmount := parseInt64(res[1])
    recipientCount := parseInt(res[2])
    recipients := res[3]

    logger.Info("penalty distributed",
        "room_id", roomID,
        "penalty_amount", penaltyAmount,
        "share_amount", shareAmount,
        "recipient_count", recipientCount,
    )

    return shareAmount, recipients.([]string), nil
}
```

---

## 九、Gateway 限流实现

### 9.1 限流中间件 (`gateway/middleware/ratelimit.go`)

```go
package middleware

import (
    "context"
    "fmt"
    "time"

    "github.com/cashparty/backend/common/logger"
    cRedis "github.com/cashparty/backend/common/redis"
    "github.com/gin-gonic/gin"
)

type RateLimitConfig struct {
    IPRequestsPerSecond   int           `yaml:"ip_requests_per_second"`
    IPBurstSize           int           `yaml:"ip_burst_size"`
    UserRequestsPerSecond int           `yaml:"user_requests_per_second"`
    UserBurstSize         int           `yaml:"user_burst_size"`
    GlobalRequestsPerSec  int           `yaml:"global_requests_per_sec"`
    CleanupInterval       time.Duration `yaml:"cleanup_interval"`
}

func DefaultRateLimitConfig() *RateLimitConfig {
    return &RateLimitConfig{
        IPRequestsPerSecond:   100,
        IPBurstSize:           200,
        UserRequestsPerSecond: 50,
        UserBurstSize:         100,
        GlobalRequestsPerSec:  10000,
        CleanupInterval:       time.Minute,
    }
}

type RateLimiter struct {
    redis  *cRedis.Client
    config *RateLimitConfig
}

func NewRateLimiter(redis *cRedis.Client, config *RateLimitConfig) *RateLimiter {
    if config == nil {
        config = DefaultRateLimitConfig()
    }
    return &RateLimiter{
        redis:  redis,
        config: config,
    }
}

func (rl *RateLimiter) AllowIP(ctx context.Context, ip string) (bool, error) {
    return rl.slidingWindowAllow(ctx,
        fmt.Sprintf("ratelimit:ip:%s", ip),
        rl.config.IPRequestsPerSecond,
        time.Second)
}

func (rl *RateLimiter) AllowUser(ctx context.Context, userID string) (bool, error) {
    return rl.slidingWindowAllow(ctx,
        fmt.Sprintf("ratelimit:user:%s", userID),
        rl.config.UserRequestsPerSecond,
        time.Second)
}

func (rl *RateLimiter) AllowGlobal(ctx context.Context) (bool, error) {
    return rl.slidingWindowAllow(ctx,
        "ratelimit:global",
        rl.config.GlobalRequestsPerSec,
        time.Second)
}

func (rl *RateLimiter) AllowCommand(ctx context.Context, userID, cmd string, limit int, window time.Duration) (bool, error) {
    key := fmt.Sprintf("ratelimit:cmd:%s:%s", cmd, userID)
    return rl.slidingWindowAllow(ctx, key, limit, window)
}

func (rl *RateLimiter) slidingWindowAllow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
    now := time.Now().UnixNano()
    windowNano := int64(window)

    script := `
        local key = KEYS[1]
        local limit = tonumber(ARGV[1])
        local window = tonumber(ARGV[2])
        local now = tonumber(ARGV[3])
        local windowStart = now - window

        redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStart)
        
        local count = redis.call('ZCARD', key)
        
        if count < limit then
            redis.call('ZADD', key, now, now)
            redis.call('PEXPIRE', key, window / 1000000)
            return 1
        end
        
        return 0
    `

    result, err := rl.redis.Eval(ctx, script, []string{key}, limit, windowNano, now).Int()
    if err != nil {
        logger.Error("rate limiter error", "key", key, "error", err)
        return true, nil
    }

    return result == 1, nil
}

func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := c.Request.Context()
        ip := c.ClientIP()

        allowed, err := limiter.AllowIP(ctx, ip)
        if err != nil {
            logger.Error("ip rate limit check failed", "error", err)
            c.Next()
            return
        }

        if !allowed {
            logger.Warn("ip rate limit exceeded", "ip", ip, "path", c.Request.URL.Path)
            c.JSON(429, gin.H{
                "success": false,
                "code":    429,
                "msg":     "rate limit exceeded",
            })
            c.Abort()
            return
        }

        c.Next()
    }
}

func UserRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := c.Request.Context()

        userID, exists := c.Get("user_id")
        if !exists {
            c.Next()
            return
        }

        allowed, err := limiter.AllowUser(ctx, fmt.Sprintf("%v", userID))
        if err != nil {
            logger.Error("user rate limit check failed", "error", err)
            c.Next()
            return
        }

        if !allowed {
            logger.Warn("user rate limit exceeded", "user_id", userID, "path", c.Request.URL.Path)
            c.JSON(429, gin.H{
                "success": false,
                "code":    429,
                "msg":     "rate limit exceeded",
            })
            c.Abort()
            return
        }

        c.Next()
    }
}

func CommandRateLimitMiddleware(limiter *RateLimiter, cmd string, limit int, window time.Duration) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := c.Request.Context()

        userID, exists := c.Get("user_id")
        if !exists {
            c.Next()
            return
        }

        allowed, err := limiter.AllowCommand(ctx, fmt.Sprintf("%v", userID), cmd, limit, window)
        if err != nil {
            logger.Error("command rate limit check failed", "error", err)
            c.Next()
            return
        }

        if !allowed {
            logger.Warn("command rate limit exceeded", "user_id", userID, "cmd", cmd)
            c.JSON(429, gin.H{
                "success": false,
                "code":    429,
                "msg":     "command rate limit exceeded",
            })
            c.Abort()
            return
        }

        c.Next()
    }
}
```

---

## 十、游戏流程时序图

### 10.1 完整游戏流程

```
┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐
│ Player │     │ Gateway│     │  Game  │     │ Redis  │     │Settle- │
│        │     │        │     │Service │     │        │     │ ment   │
└───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘
    │              │              │              │              │
    │  1. Ready    │              │              │              │
    │─────────────>│              │              │              │
    │              │  2. OnReady  │              │              │
    │              │─────────────>│              │              │
    │              │              │  3. Check All Ready (Lua)   │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │              │              │              │
    │              │              │  4. Start Countdown         │
    │              │              │─────────────>│              │
    │              │<─────────────│              │              │
    │  5. Push Countdown         │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │              │  6. Timeout  │              │              │
    │              │─────────────>│              │              │
    │              │              │  7. Start Game (Lua)        │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │<─────────────│              │              │
    │  8. Push GameStart         │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │              │              │  9. First Round (Lua)       │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │<─────────────│              │              │
    │  10. Push RoundStart       │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │  11. Grab    │              │              │              │
    │─────────────>│              │              │              │
    │              │  12. Grab    │              │              │
    │              │─────────────>│              │              │
    │              │              │  13. Grab (Lua)             │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │<─────────────│              │              │
    │  14. Result  │              │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │              │  15. Timeout │              │              │
    │              │─────────────>│              │              │
    │              │              │  16. Auto Distribute (Lua)  │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │<─────────────│              │              │
    │  17. Push AutoDistribute    │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │              │              │  18. Settle (Lua)           │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │              │              │              │
    │              │              │  19. Do Settlement          │
    │              │              │─────────────────────────────>│
    │              │              │<─────────────────────────────│
    │              │<─────────────│              │              │
    │  20. Push RoundEnd         │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │  21. Send Packet           │              │              │
    │─────────────>│              │              │              │
    │              │  22. Send    │              │              │
    │              │─────────────>│              │              │
    │              │              │  23. Send (Lua)             │
    │              │              │─────────────>│              │
    │              │              │<─────────────│              │
    │              │<─────────────│              │              │
    │  24. Push RoundStart       │              │              │
    │<─────────────│              │              │              │
    │              │              │              │              │
    │              │  ... Repeat Round 2-10 ... │              │
    │              │              │              │              │
    │              │              │  25. Game End               │
    │              │              │─────────────>│              │
    │              │<─────────────│              │              │
    │  26. Push GameEnd          │              │              │
    │<─────────────│              │              │              │
```

### 10.2 惩罚流程

```
┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐
│ Player │     │ Gateway│     │  Game  │     │ Redis  │
│        │     │        │     │Service │     │        │
└───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘
    │              │              │              │
    │              │  1. Send Timeout            │
    │              │─────────────>│              │
    │              │              │  2. Apply Penalty (Lua)     │
    │              │              │─────────────>│
    │              │              │<─────────────│
    │              │              │              │
    │              │              │  3. Check Penalty Count     │
    │              │              │─────────────>│
    │              │              │<─────────────│
    │              │              │              │
    │              │<─────────────│              │
    │  4. Push Penalty          │              │
    │<─────────────│              │              │
    │              │              │              │
    │              │  [If Kick Required]         │
    │              │              │  5. Kick Player             │
    │              │              │─────────────>│
    │              │              │              │
    │              │              │  6. Wait Replacement        │
    │              │              │─────────────>│
    │              │<─────────────│              │
    │  7. Push WaitReplacement   │              │
    │<─────────────│              │              │
    │              │              │              │
    │              │  8. Replacement Timeout     │
    │              │─────────────>│              │
    │              │              │  9. Distribute Penalty (Lua)│
    │              │              │─────────────>│
    │              │              │<─────────────│
    │              │<─────────────│              │
    │  10. Push GameInterrupted  │              │
    │<─────────────│              │              │
    │              │              │              │
    │              │  [If No Kick]               │
    │              │              │  11. System Send            │
    │              │              │─────────────>│
    │              │              │<─────────────│
    │              │<─────────────│              │
    │  12. Push RoundStart       │              │
    │<─────────────│              │              │
```

---

## 十一、实施计划

### 第一阶段：基础设施（1-2天）

| 任务 | 文件 | 说明 |
|------|------|------|
| 新增游戏Lua脚本 | `lua_game.go` | 抢红包、发红包、结算等脚本 |
| 更新消息类型 | `types.go` | 新增推送类型常量 |
| 更新消息结构 | `payload.go` | 新增推送数据结构 |
| 更新错误码 | `errors.go` | 新增游戏相关错误码 |
| 更新Redis Key | `keys.go` | 新增游戏相关Key |

### 第二阶段：核心服务（3-4天）

| 任务 | 文件 | 说明 |
|------|------|------|
| 新增游戏状态定义 | `domain/game_state.go` | 游戏阶段、状态结构 |
| 新增惩罚实体 | `domain/penalty.go` | 惩罚类型、记录、结果 |
| 重构超时调度器 | `scheduler/timeout_scheduler.go` | 支持多种超时类型 |
| 重构抢红包服务 | `application/grab_service.go` | 使用新Lua脚本 |
| 新增惩罚服务 | `application/penalty_service.go` | 惩罚处理逻辑 |

### 第三阶段：Gateway限流（1天）

| 任务 | 文件 | 说明 |
|------|------|------|
| 重构限流中间件 | `middleware/ratelimit.go` | 滑动窗口限流 |

### 第四阶段：测试验证（2-3天）

| 任务 | 说明 |
|------|------|
| 单元测试 | 各服务单元测试 |
| 集成测试 | 完整游戏流程测试 |
| 压力测试 | 高并发场景测试 |
| 边界测试 | 异常场景测试 |

---

## 十二、总结

### 12.1 架构优势

| 优势 | 说明 |
|------|------|
| **原子性** | 所有关键操作使用Lua脚本保证原子性 |
| **高性能** | 限流在Gateway层实现，减少后端压力 |
| **可扩展** | 模块职责清晰，易于扩展 |
| **可维护** | Lua脚本分离，消息体系规范 |

### 12.2 并发控制总结

| 场景 | 控制方式 | 文件 |
|------|----------|------|
| 抢红包 | Lua脚本 | `lua_game.go` - LuaGrabPacketV2 |
| 发红包 | Lua脚本 | `lua_game.go` - LuaSendPacketV2 |
| 第一局发红包 | Lua脚本 | `lua_game.go` - LuaSystemSendFirstRound |
| 自动分配 | Lua脚本 | `lua_game.go` - LuaAutoDistributePackets |
| 结算 | Lua脚本 | `lua_game.go` - LuaSettleRound |
| 惩罚处理 | Lua脚本 | `lua_game.go` - LuaHandlePenalty |
| 惩罚分配 | Lua脚本 | `lua_game.go` - LuaDistributePenalty |
| 状态转换 | Lua脚本 | `lua_game.go` - LuaTransitionPhase |
| 限流 | Lua脚本 | `middleware/ratelimit.go` |
| 结算持久化 | 分布式锁 | `settlement_service.go` |

### 12.3 文件变更清单

| 操作 | 文件路径 |
|------|----------|
| **新增** | `game/infrastructure/persistence/redis/lua_game.go` |
| **新增** | `game/domain/game_state.go` |
| **新增** | `game/domain/penalty.go` |
| **新增** | `game/application/penalty_service.go` |
| **修改** | `common/message/types.go` |
| **修改** | `common/message/payload.go` |
| **修改** | `common/message/errors.go` |
| **修改** | `game/infrastructure/persistence/redis/keys.go` |
| **修改** | `game/infrastructure/persistence/redis/lua_scripts.go` |
| **重构** | `game/scheduler/timeout_scheduler.go` |
| **重构** | `game/application/grab_service.go` |
| **重构** | `gateway/middleware/ratelimit.go` |
| **删除** | `game/state/state_manager.go` |
