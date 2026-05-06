# 待入账金额实时展示

## 需求

当前玩家在游戏中，扣款（Debit）回合开始时立即从平台钱包扣除，但入账（Credit）要等 10 回合全部结束后才一次性到账。玩家期间查余额只能看到扣完款的余额，体验差。

需要增加"待入账金额"，让玩家在每回合结束时能看到累计抢红包 + 奖励的待入账金额。

### 待入账金额定义

**待入账 = 累计抢红包金额 + 累计奖励金额**（不减扣款，因为扣款已经从钱包扣了）

前端展示：
- 可用余额 = 平台真实余额（`get_user_balance` 返回的 balance）
- 待入账 = 累计抢红包 + 累计奖励

## 方案：`get_user_balance` 响应增加 `pending_credit` 字段

### 核心思路

不改资金入账时机（仍会话级统一入账），在 `get_user_balance` 响应中增加 `pending_credit` 字段（累计抢红包+奖励的待入账金额）。前端收到 `round_end` 后调一次 `get_user_balance` 即可获取最新待入账金额。

### 可利用的现有数据

| 数据 | 来源 | 说明 |
|------|------|------|
| 累计抢到+奖励金额 | Redis `session:{sid}:player:totals` | Lua 脚本 `LuaSettleRound` 每回合更新 |

注意：Redis `session:{sid}:player:totals` 已包含抢红包 + 奖励的累计总额（Lua 脚本中 reward 也会 HINCRBY 到该 hash），因此**直接读这个 hash 即可获取待入账金额**，无需额外计算。

### 关键问题：入账后 player:totals 仍残留旧数据

时间线：
1. 会话 10 回合结束 → `creditSessionPayouts` 调用 `platform.Credit(payout)` → 玩家真实余额已包含入账
2. 但 Redis `session:{sid}:player:totals` **仍然保留着**累计金额
3. `room:{roomID}:meta` 的 `current_session_id` **也没有被清除**（`LuaEndGame` 未清除该字段）
4. 此时玩家调 `get_user_balance`：`pending_credit` 仍返回旧数据 → **重复计算**

解决方案：检查房间状态。查 `room:{roomID}:meta` 时一并读取 `status`，如果 `status != Playing(2)`（游戏未开始或已结束），`pending_credit` 返回 0。房间状态是可靠的"游戏是否进行中"标志，不需要额外清理 Redis 数据。

### 改动范围

#### 1. `game/application/user_service.go` — 新增 `GetPendingCredit` 方法

在 `UserService` 中封装 pending_credit 查询逻辑。UserService 已有 `redis` 依赖，可直接使用 Redis key 函数。

```go
// GetPendingCredit 获取玩家当前游戏的待入账金额（累计抢红包+奖励）
func (s *UserService) GetPendingCredit(ctx context.Context, userID string) int64 {
    // 1. 通过 PlayerRoomKey(userID) 获取当前房间 ID
    roomID := s.redis.Get(ctx, redis.PlayerRoomKey(userID)).Val()
    if roomID == "" || roomID == "0" {
        return 0
    }

    // 2. 通过 RoomHashKey(roomID) HGETALL 获取房间 meta
    roomData := s.redis.HGetAll(ctx, redis.RoomHashKey(roomID)).Val()
    if len(roomData) == 0 {
        return 0
    }

    // 3. 检查房间状态，非 Playing(2) 返回 0
    status, _ := strconv.Atoi(roomData["status"])
    if status != 2 { // RoomStatusPlaying = 2
        return 0
    }

    // 4. 获取 sessionID
    sessionID := roomData["current_session_id"]
    if sessionID == "" {
        return 0
    }

    // 5. 从 session:{sid}:player:totals HGET 玩家累计金额
    val := s.redis.HGet(ctx, redis.SessionPlayerTotalsKey(sessionID), userID).Val()
    if val == "" {
        return 0
    }
    amount, _ := strconv.ParseInt(val, 10, 64)
    return amount
}
```

Redis 查询链路：`PlayerRoomKey(userID)` → roomID → `RoomHashKey(roomID)` → sessionID + status → `SessionPlayerTotalsKey(sessionID)` → 累计金额

#### 2. `game/server/generic_service.go` — handleGetUserBalance 调用 UserService

```go
func (s *GenericServiceServer) handleGetUserBalance(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    // ... 现有的 balance 获取逻辑 ...

    // 新增：获取 pending_credit
    var pendingCredit int64
    if s.userSvc != nil {
        pendingCredit = s.userSvc.GetPendingCredit(ctx, req.UserId)
    }

    return s.successResponse(req, map[string]interface{}{
        "balance":        currency.NewMoneyFromFen(balance),
        "pending_credit": currency.NewMoneyFromFen(pendingCredit),
    }), nil
}
```

#### 3. 无需新增 Redis key

`PlayerRoomKey`、`RoomHashKey`、`SessionPlayerTotalsKey` 均已存在。

### 不变更的部分

1. **资金入账时机**：仍会话级统一入账，不改 `creditSessionPayouts` 逻辑
2. **Lua 脚本**：不改 `LuaSettleRound`（累计数据已在维护）
3. **Kafka consumer**：不改 `handleRoundSettle`
4. **BillRecord / 结算逻辑**：无变更
5. **扣款逻辑**：无变更
6. **`round_end` 推送**：不改推送协议

### 边界条件

1. **首回合开始前**：player:totals 为空，`pending_credit` = 0
2. **会话进行中**：每回合 player:totals 更新，`pending_credit` 随之增长
3. **会话结束后（入账完成）**：房间状态变为 Waiting，`pending_credit` = 0（即使 player:totals 未清理）
4. **下一局开始**：新 sessionID，新 player:totals，从 0 开始累计
5. **系统发红包（senderID=0）**：sender 不需要待入账，player:totals 中不含 senderID=0
6. **断线重连**：调 `get_user_balance` 即可获取 `pending_credit`
7. **HGET 返回空**：说明 player:totals 中无该玩家记录，`pending_credit` = 0
8. **玩家不在游戏中**：无法获取 roomID 或 sessionID，`pending_credit` 返回 0

### 数据流

```
前端调用 get_user_balance
  │
  ├─ platform.GetBalance() → 可用余额（真实余额）
  │
  ├─ userSvc.GetPendingCredit(userID)：
  │    ├─ PlayerRoomKey(userID) → roomID（无 → 0）
  │    ├─ RoomHashKey(roomID) HGETALL → sessionID + status
  │    ├─ status != Playing(2) → 0（游戏未开始或已结束）
  │    ├─ SessionPlayerTotalsKey(sessionID) HGET userID → 累计抢红包+奖励金额
  │    └─ hash 中无记录 → 0
  │
  └─ 返回 { balance, pending_credit }
```
