# 玩家历史记录对账数据修正方案

> 文档版本：v1.0
> 创建日期：2026-06-26
> 涉及工程：`RedPacket-master/backend`（Go 后端）、`gogain/packages/gift-box`（PixiJS 前端）
> 关联文档：`PLAYER_HISTORY_DESIGN.md`

---

## 一、问题背景

玩家历史记录功能上线后，对账时发现以下数据不一致：

1. **统计概览区域**：胜率、总盈亏、总抢到、总发出 数据不准确
2. **列表项盈亏**：与实际资金流水对不上
3. **详情页个人结果卡片**：盈亏、抢到、发出 数据不准确
4. **详情页回合明细列表**：缺少"玩家发出红包金额"字段，无法逐回合对账

经系统化分析后端代码，**根本原因**是当前历史查询依赖 `session_players` 表的累计字段（`total_send` / `total_grab`），而该表的更新逻辑存在 Bug，且该表语义与"资金流水"并不完全对齐。`bill_record` 表才是资金流水的权威数据源。

---

## 二、现状分析

### 2.1 当前数据来源

| 接口 | 当前数据源 | 仓储方法 |
|------|-----------|----------|
| `get_player_history`（列表） | `session_players` 关联 `game_sessions` | `ListPlayerSessions` |
| `get_player_session_detail`（详情） | `session_players` + `rounds` + `round_grab_records` | `GetPlayerSession` + `GetSessionRounds` + `ListPlayerGrabRecords` |
| `get_player_stats`（统计概览） | `session_players` 聚合 | `AggregatePlayerStats` |

所有盈亏均由 `total_grab - total_send` 计算得出，胜率由 `(total_grab - total_send) > 0` 判定。

### 2.2 关键 Bug：`session_players.total_send` 更新错位

在 [game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go#L269-L283) 的 `handleRoundSettle` 中：

```go
for _, r := range data.Results {
    updates := map[string]interface{}{
        "grab_count": gorm.Expr("grab_count + 1"),
        "total_grab": gorm.Expr("total_grab + ?", r.Amount),
    }
    if r.UserID == data.MinPlayerID {           // ← Bug 在这里
        updates["send_count"] = gorm.Expr("send_count + 1")
        updates["total_send"] = gorm.Expr("total_send + ?", data.TotalAmount)
    }
    ...
}
```

**问题**：`data.MinPlayerID` 是"本轮抢到最小金额的玩家"，他将成为**下一回合的发包者**；而**本轮的红包实际上是由 `data.SenderID` 发出的**。当前代码把 `total_send` 累加到了下一回合发包者身上，导致每位玩家的 `total_send` 整体错位一回合。

**游戏语义回顾**（见 [game_app_service.go#L955-L1005](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L955-L1005)）：

| 字段 | 含义 | 时机 |
|------|------|------|
| `data.SenderID` | 本轮红包的实际发出者 | 本轮结算时已知 |
| `data.MinPlayerID` | 本轮抢到最少的玩家 | 本轮结算时已知 |
| `MinPlayerID` 的角色 | **下一轮**的发包者 | 下一轮 `SendPacket` 时才扣款 |

**资金流佐证**：在 [deduct_service.go#L394-L408](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go#L394-L408) 的 `DeductForLaterRound` 中，扣款账户是 `req.MinPlayerID`，但该参数在 [game_app_service.go#L1595](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L1595) `initLaterRoundAndDeduct` 调用时传入的是 `senderIDInt`（即本轮将要发包的玩家）。也就是说：

- `bill_record` 中 `BillType=4 (LaterRoundDeduct)` 的 `user_id` 是**真正发包的玩家**
- `session_players.total_send` 却记在了**下轮发包者**名下

两者口径不一致，必然对账不上。

### 2.3 `session_players` 表的其它局限

| 局限 | 影响 |
|------|------|
| 仅记录 `total_send`/`total_grab` 两个聚合值，未区分资金类型 | 无法分离房费、罚款、系统奖励等 |
| `total_profit` 由 `handleSessionEnd` 事件覆盖写入，与 `total_grab - total_send` 不一定一致 | 盈亏口径混乱 |
| 由事件异步消费写入，存在秒级延迟 | 刚结束对局查询可能不准 |
| `total_grab` 包含发包者自己抢自己红包的金额 | 与"净抢到"语义不符 |

### 2.4 详情页回合明细缺失"我的发出"

当前 [history_dto.go#L72-L82](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_dto.go#L72-L82) 的 `RoundDetail` 只有 `MyGrab`：

```go
type RoundDetail struct {
    RoundID     string         `json:"round_id"`
    RoundNo     int            `json:"round_no"`
    SenderID    string         `json:"sender_id"`
    SenderType  string         `json:"sender_type"`
    TotalAmount currency.Money `json:"total_amount"`
    ...
    MyGrab      *GrabDetail    `json:"my_grab,omitempty"`   // 只有"我抢到"
    // 缺少 MySend：我作为发包者发出了多少
}
```

前端 [HistoryRoundItem.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/widgets/history-round-item/HistoryRoundItem.ts) 也只展示"抢到金额"和"红包总额"，**当玩家自己是发包者时，看不到自己发出了多少**，导致逐回合对账缺失一项关键数据。

---

## 三、`bill_record` 表口径梳理

`bill_record` 是资金流水的权威数据源，每条记录对应一次实际资金变动。关键字段：

| 字段 | 说明 |
|------|------|
| `bill_type` | 业务类型（见下表） |
| `user_id` | 资金变动归属玩家（`0` 表示平台账户） |
| `amount` | 资金变动金额（分），**负数=玩家支出，正数=玩家收入** |
| `status` | `1=Success` 才是有效流水 |
| `session_id` / `round_id` | 关联会话与回合 |

### 3.1 BillType 语义对照

| 常量 | 值 | 方向 | 含义 | 是否计入玩家对账 |
|------|----|------|------|------------------|
| `BillTypeFirstRoundDeduct` | 2 | 玩家支出 | 首轮房费分摊（系统发包） | ✅ 计入"支出" |
| `BillTypeGrabPacket` | 3 | 玩家收入 | 抢红包所得 | ✅ 计入"抢到" |
| `BillTypeLaterRoundDeduct` | 4 | 玩家支出 | 后续回合房费（=玩家发包金额） | ✅ 计入"发出" |
| `BillTypeCommission` | 7 | 平台收入 | 佣金（归属平台账户） | ❌ 不计入玩家 |
| `BillTypePenaltyIncome` | 8 | 玩家支出 | 罚款扣款 | ✅ 计入"支出" |
| `BillTypeSystemPacket` | 9 | 平台支出 | 系统发红包成本（归属平台账户） | ❌ 不计入玩家 |
| `BillTypePenaltyDistribute` | 10 | 玩家收入 | 罚款分红 | ✅ 计入"其它收入" |
| `BillTypeSystemReward` | 11 | 玩家收入 | 系统奖励 | ✅ 计入"其它收入" |
| `BillTypeSessionCredit` | 12 | 玩家收入 | 会话级实际入账（与 3/10/11 重复） | ❌ 必须排除，否则重复计算 |

### 3.2 对账口径定义

基于 `bill_record` 重新定义对账字段（所有金额均以"分"为单位，`status=1` 且 `user_id != 0`）：

```sql
-- 总抢到（严格口径：仅抢红包所得）
SUM(CASE WHEN bill_type = 3 THEN amount ELSE 0 END)

-- 总发出（玩家作为发包者付出的红包金额）
SUM(CASE WHEN bill_type = 4 THEN ABS(amount) ELSE 0 END)

-- 总支出（含房费分摊、罚款）
SUM(CASE WHEN bill_type IN (2, 4, 8) AND amount < 0 THEN ABS(amount) ELSE 0 END)

-- 总收入（含抢红包、罚款分红、系统奖励，排除会话级入账）
SUM(CASE WHEN bill_type IN (3, 10, 11) AND amount > 0 THEN amount ELSE 0 END)

-- 总盈亏 = 总收入 - 总支出
-- 等价于：SUM(amount) WHERE bill_type != 12 AND user_id != 0 AND status = 1
```

**胜率口径**：以"会话级盈亏 > 0"为胜，需按 `session_id` 分组聚合后再统计。

---

## 四、修正方案

### 4.1 方案选型

| 方案 | 描述 | 优点 | 缺点 | 决策 |
|------|------|------|------|------|
| A. 修复 consumer + 继续用 `session_players` | 修正 `MinPlayerID` → `SenderID` 的错位 | 改动小 | 仍无法解决"无资金类型区分"、"对账口径单一"问题；存量数据需回补 | ❌ |
| B. 全量改用 `bill_record` 聚合 | 历史查询全部走 `bill_record` | 对账权威、口径灵活、天然支持多维度 | 查询稍复杂、需新增索引 | ✅ **采用** |
| C. A + B 混合 | 修 Bug 保运行时正确，历史查询走 bill | 双保险 | 维护两套口径 | 作为 B 的补充 |

**决策**：采用 **方案 B**，同时**附带方案 A 的 Bug 修复**（避免 `session_players` 继续被写脏，影响其它消费方）。

### 4.2 整体架构

```
┌─────────────────────────────────────────────────────────┐
│  HistoryService（应用服务层）                            │
│  ├─ GetPlayerHistory    ──┐                              │
│  ├─ GetPlayerSessionDetail ──┼─→ 走 bill_record 聚合      │
│  └─ GetPlayerStats      ──┘    + rounds + round_grab_records │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│  HistoryDBRepository（扩展接口）                          │
│  ├─ ListPlayerSessionsWithBill   (列表+盈亏)              │
│  ├─ GetPlayerSessionBillSummary  (单局个人结果)           │
│  ├─ AggregatePlayerStatsFromBill (累计统计)               │
│  ├─ GetPlayerSendRounds          (玩家发包回合列表)        │
│  └─ ListPlayerGrabRecords        (保留)                   │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│  bill_record / rounds / round_grab_records / game_sessions │
└─────────────────────────────────────────────────────────┘
```

---

## 五、详细设计

### 5.1 修复 `session_players` 写入 Bug（附带修复）

**文件**：[game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go#L269-L283)

**修改前**：
```go
for _, r := range data.Results {
    updates := map[string]interface{}{
        "grab_count": gorm.Expr("grab_count + 1"),
        "total_grab": gorm.Expr("total_grab + ?", r.Amount),
    }
    if r.UserID == data.MinPlayerID {
        updates["send_count"] = gorm.Expr("send_count + 1")
        updates["total_send"] = gorm.Expr("total_send + ?", data.TotalAmount)
    }
    if err := tx.Model(&model.SessionPlayer{}).
        Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(r.UserID)).
        Updates(updates).Error; err != nil {
        return fmt.Errorf("update session player failed: %w", err)
    }
}
```

**修改后**：
```go
// 1. 抢包累加：对所有抢到红包的玩家
for _, r := range data.Results {
    if err := tx.Model(&model.SessionPlayer{}).
        Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(r.UserID)).
        Updates(map[string]interface{}{
            "grab_count": gorm.Expr("grab_count + 1"),
            "total_grab": gorm.Expr("total_grab + ?", r.Amount),
        }).Error; err != nil {
        return fmt.Errorf("update session player grab failed: %w", err)
    }
}

// 2. 发包累加：仅对本轮实际发包者（SenderID），系统发包不计入玩家
if data.SenderType != domain.SenderTypeSystem &&
   data.SenderType != domain.SenderTypeSystemForced &&
   data.SenderID != "" && data.SenderID != "0" {
    if err := tx.Model(&model.SessionPlayer{}).
        Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(data.SenderID)).
        Updates(map[string]interface{}{
            "send_count": gorm.Expr("send_count + 1"),
            "total_send": gorm.Expr("total_send + ?", data.TotalAmount),
        }).Error; err != nil {
        return fmt.Errorf("update session player send failed: %w", err)
    }
}
```

**注意**：此修复仅保证后续新数据正确，**存量错位数据需通过方案 B 的 bill 聚合绕过**，无需回补 `session_players`。

### 5.2 扩展 `HistoryDBRepository` 接口

**文件**：[game/domain/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/db_repository.go#L105-L113)

新增方法：

```go
type HistoryDBRepository interface {
    // 保留现有方法...
    ListPlayerSessions(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]PlayerSessionRow, int64, error)
    GetSessionRounds(sessionID int64) ([]model.Round, error)
    ListPlayerGrabRecords(sessionID, userID int64) ([]model.RoundGrabRecord, error)
    GetPlayerSession(userID, sessionID int64) (*model.SessionPlayer, error)
    GetSession(sessionID int64) (*model.GameSession, error)

    // 新增：基于 bill_record 的对账查询
    ListPlayerSessionsWithBill(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]PlayerSessionBillRow, int64, error)
    GetPlayerSessionBillSummary(userID, sessionID int64) (*PlayerSessionBillSummary, error)
    AggregatePlayerStatsFromBill(userID int64) (*PlayerStatsBillAggregate, error)
    GetPlayerSendRounds(sessionID, userID int64) ([]model.Round, error)
}

// PlayerSessionBillRow 列表项（含 bill 聚合的盈亏数据）
type PlayerSessionBillRow struct {
    // game_sessions 字段
    SessionID    int64
    RoomNo       string
    ConfigName   string
    RoomFee      int64
    MaxRounds    int
    ActualRounds int
    Status       int
    StartedAt    *time.Time
    EndedAt      *time.Time
    EndReason    string
    // bill_record 聚合字段（分）
    TotalGrab    int64 // bill_type=3 求和
    TotalSend    int64 // bill_type=4 求和（ABS）
    TotalBet     int64 // bill_type IN (2,4,8) 且 amount<0 求和（ABS）
    TotalIncome  int64 // bill_type IN (3,10,11) 且 amount>0 求和
    Profit       int64 // TotalIncome - TotalBet
    GrabCount    int64 // bill_type=3 计数
    SendCount    int64 // bill_type=4 计数
}

// PlayerSessionBillSummary 单局个人结果卡片
type PlayerSessionBillSummary struct {
    TotalGrab   int64
    TotalSend   int64
    TotalBet    int64
    TotalIncome int64
    Profit      int64
    GrabCount   int64
    SendCount   int64
}

// PlayerStatsBillAggregate 累计统计
type PlayerStatsBillAggregate struct {
    TotalGames     int64 // 不同 session_id 计数
    WinCount       int64 // 盈亏>0 的 session 计数
    TotalGrab      int64
    TotalSend      int64
    TotalBet       int64
    TotalIncome    int64
    TotalProfit    int64
    TotalSendCount int64
    TotalGrabCount int64
}
```

### 5.3 仓储层 SQL 实现

**文件**：`game/infrastructure/persistence/mysql/history_repository.go`（扩展）

#### 5.3.1 列表查询 `ListPlayerSessionsWithBill`

```sql
SELECT
    gs.session_id, gs.room_no, gs.config_name, gs.room_fee,
    gs.max_rounds, gs.actual_rounds, gs.status,
    gs.started_at, gs.ended_at, gs.end_reason,
    COALESCE(SUM(CASE WHEN b.bill_type = 3 AND b.amount > 0 THEN b.amount ELSE 0 END), 0) AS total_grab,
    COALESCE(SUM(CASE WHEN b.bill_type = 4 AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS total_send,
    COALESCE(SUM(CASE WHEN b.bill_type IN (2,4,8) AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS total_bet,
    COALESCE(SUM(CASE WHEN b.bill_type IN (3,10,11) AND b.amount > 0 THEN b.amount ELSE 0 END), 0) AS total_income,
    COALESCE(SUM(CASE WHEN b.bill_type IN (3,10,11) AND b.amount > 0 THEN b.amount ELSE 0 END), 0)
        - COALESCE(SUM(CASE WHEN b.bill_type IN (2,4,8) AND b.amount < 0 THEN ABS(b.amount) ELSE 0 END), 0) AS profit,
    COALESCE(SUM(CASE WHEN b.bill_type = 3 THEN 1 ELSE 0 END), 0) AS grab_count,
    COALESCE(SUM(CASE WHEN b.bill_type = 4 THEN 1 ELSE 0 END), 0) AS send_count
FROM game_sessions gs
INNER JOIN bill_record b ON b.session_id = gs.session_id AND b.user_id = ? AND b.status = 1
WHERE gs.status = 1
  AND b.user_id != 0
  AND b.bill_type != 12   -- 排除会话级入账，避免重复
  /* 可选时间/配置过滤 */
GROUP BY gs.session_id
ORDER BY gs.started_at DESC
LIMIT ? OFFSET ?
```

**总数查询**：
```sql
SELECT COUNT(DISTINCT gs.session_id) AS total
FROM game_sessions gs
INNER JOIN bill_record b ON b.session_id = gs.session_id AND b.user_id = ? AND b.status = 1
WHERE gs.status = 1 AND b.user_id != 0 AND b.bill_type != 12
```

#### 5.3.2 单局个人结果 `GetPlayerSessionBillSummary`

```sql
SELECT
    COALESCE(SUM(CASE WHEN bill_type = 3 AND amount > 0 THEN amount ELSE 0 END), 0) AS total_grab,
    COALESCE(SUM(CASE WHEN bill_type = 4 AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS total_send,
    COALESCE(SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END), 0) AS total_bet,
    COALESCE(SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END), 0) AS total_income,
    COALESCE(SUM(CASE WHEN bill_type = 3 THEN 1 ELSE 0 END), 0) AS grab_count,
    COALESCE(SUM(CASE WHEN bill_type = 4 THEN 1 ELSE 0 END), 0) AS send_count
FROM bill_record
WHERE session_id = ? AND user_id = ? AND status = 1 AND user_id != 0 AND bill_type != 12
```

#### 5.3.3 累计统计 `AggregatePlayerStatsFromBill`

```sql
SELECT
    COUNT(DISTINCT b.session_id) AS total_games,
    SUM(CASE WHEN b.profit > 0 THEN 1 ELSE 0 END) AS win_count,
    SUM(b.total_grab) AS total_grab,
    SUM(b.total_send) AS total_send,
    SUM(b.total_bet) AS total_bet,
    SUM(b.total_income) AS total_income,
    SUM(b.profit) AS total_profit,
    SUM(b.grab_count) AS total_grab_count,
    SUM(b.send_count) AS total_send_count
FROM (
    SELECT
        session_id,
        SUM(CASE WHEN bill_type = 3 AND amount > 0 THEN amount ELSE 0 END) AS total_grab,
        SUM(CASE WHEN bill_type = 4 AND amount < 0 THEN ABS(amount) ELSE 0 END) AS total_send,
        SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END) AS total_bet,
        SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END) AS total_income,
        SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END)
            - SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END) AS profit,
        SUM(CASE WHEN bill_type = 3 THEN 1 ELSE 0 END) AS grab_count,
        SUM(CASE WHEN bill_type = 4 THEN 1 ELSE 0 END) AS send_count
    FROM bill_record
    WHERE user_id = ? AND status = 1 AND user_id != 0 AND bill_type != 12
    GROUP BY session_id
) b
```

#### 5.3.4 玩家发包回合 `GetPlayerSendRounds`

```sql
SELECT * FROM rounds
WHERE session_id = ? AND sender_id = ? AND sender_type IN ('player', 'system_resume')
ORDER BY round_no ASC
```

### 5.4 应用服务层改造

**文件**：[history_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go)

#### 5.4.1 列表接口

```go
func (s *HistoryService) GetPlayerHistory(ctx context.Context, userID int64, req PlayerHistoryReq) (*PlayerHistoryResp, error) {
    // ...参数校验同原逻辑...
    rows, total, err := s.dbRepo.HistoryDBRepo().ListPlayerSessionsWithBill(
        userID, req.StartDate, req.EndDate, req.ConfigName, pageSize, offset)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }
    items := make([]PlayerHistoryItem, 0, len(rows))
    for i := range rows {
        items = append(items, playerSessionBillRowToItem(&rows[i]))
    }
    return &PlayerHistoryResp{List: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func playerSessionBillRowToItem(row *domain.PlayerSessionBillRow) PlayerHistoryItem {
    return PlayerHistoryItem{
        SessionID:    converter.FormatID(row.SessionID),
        RoomNo:       row.RoomNo,
        ConfigName:   row.ConfigName,
        RoomFee:      currency.NewMoneyFromFen(row.RoomFee),
        MaxRounds:    row.MaxRounds,
        ActualRounds: row.ActualRounds,
        Status:       row.Status,
        StartedAt:    timeToMs(row.StartedAt),
        EndedAt:      timeToMs(row.EndedAt),
        EndReason:    row.EndReason,
        SendCount:    int(row.SendCount),
        GrabCount:    int(row.GrabCount),
        TotalSend:    currency.NewMoneyFromFen(row.TotalSend), // 来自 bill_type=4
        TotalGrab:    currency.NewMoneyFromFen(row.TotalGrab), // 来自 bill_type=3
        Profit:       currency.NewMoneyFromFen(row.Profit),    // 总收入-总支出
    }
}
```

#### 5.4.2 详情接口

```go
func (s *HistoryService) GetPlayerSessionDetail(ctx context.Context, userID int64, sessionID int64) (*PlayerSessionDetailResp, error) {
    // 1. 校验玩家属于该会话（保留原逻辑）
    player, err := s.dbRepo.HistoryDBRepo().GetPlayerSession(userID, sessionID)
    if err != nil || player == nil {
        return nil, message.NewError(message.CodePlayerNotInSession)
    }

    // 2. 会话基本信息（保留）
    session, err := s.dbRepo.HistoryDBRepo().GetSession(sessionID)
    if err != nil || session == nil {
        return nil, message.NewError(message.CodeSessionNotFound)
    }

    // 3. 个人结果卡片：改用 bill 聚合
    billSummary, err := s.dbRepo.HistoryDBRepo().GetPlayerSessionBillSummary(userID, sessionID)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }

    // 4. 回合明细：抢包记录 + 发包回合
    rounds, _ := s.dbRepo.HistoryDBRepo().GetSessionRounds(sessionID)
    grabRecords, _ := s.dbRepo.HistoryDBRepo().ListPlayerGrabRecords(sessionID, userID)
    sendRounds, _ := s.dbRepo.HistoryDBRepo().GetPlayerSendRounds(sessionID, userID)

    grabByRound := make(map[int64]model.RoundGrabRecord, len(grabRecords))
    for i := range grabRecords {
        grabByRound[grabRecords[i].RoundID] = grabRecords[i]
    }
    sendByRound := make(map[int64]model.Round, len(sendRounds))
    for i := range sendRounds {
        sendByRound[sendRounds[i].RoundID] = sendRounds[i]
    }

    // 5. 组装回合明细（含 MyGrab + MySend）
    roundDetails := make([]RoundDetail, 0, len(rounds))
    for i := range rounds {
        rd := RoundDetail{
            RoundID:     converter.FormatID(rounds[i].RoundID),
            RoundNo:     rounds[i].RoundNo,
            SenderID:    converter.FormatID(rounds[i].SenderID),
            SenderType:  rounds[i].SenderType,
            TotalAmount: currency.NewMoneyFromFen(rounds[i].TotalAmount),
            StartedAt:   timeToMs(rounds[i].StartedAt),
            EndedAt:     timeToMs(rounds[i].EndedAt),
            Status:      int(rounds[i].Status),
        }
        if rec, ok := grabByRound[rounds[i].RoundID]; ok {
            rd.MyGrab = &GrabDetail{
                PacketID:       converter.FormatID(rec.PacketID),
                Amount:         currency.NewMoneyFromFen(rec.Amount),
                IsMin:          rec.IsMin == 1,
                IsAutoAssigned: rec.IsAutoAssigned == 1,
                GrabbedAt:      rec.GrabbedAt.UnixMilli(),
            }
        }
        // 新增：如果玩家是本轮发包者，填充 MySend
        if sr, ok := sendByRound[rounds[i].RoundID]; ok {
            rd.MySend = &SendDetail{
                TotalAmount: currency.NewMoneyFromFen(sr.TotalAmount),
                StartedAt:   timeToMs(sr.StartedAt),
            }
        }
        roundDetails = append(roundDetails, rd)
    }

    // 6. 个人结果卡片改用 bill 聚合数据
    myStats := PlayerHistoryItem{
        SessionID:    converter.FormatID(session.SessionID),
        RoomNo:       session.RoomNo,
        ConfigName:   session.ConfigName,
        RoomFee:      currency.NewMoneyFromFen(session.RoomFee),
        MaxRounds:    session.MaxRounds,
        ActualRounds: session.ActualRounds,
        Status:       int(session.Status),
        StartedAt:    timeToMs(session.StartedAt),
        EndedAt:      timeToMs(session.EndedAt),
        EndReason:    session.EndReason,
        SeatNo:       player.SeatNo,
        SendCount:    int(billSummary.SendCount),
        GrabCount:    int(billSummary.GrabCount),
        TotalSend:    currency.NewMoneyFromFen(billSummary.TotalSend),
        TotalGrab:    currency.NewMoneyFromFen(billSummary.TotalGrab),
        Profit:       currency.NewMoneyFromFen(billSummary.Profit),
        JoinedAt:     player.JoinedAt.UnixMilli(),
        LeftAt:       timeToMs(player.LeftAt),
    }

    return &PlayerSessionDetailResp{
        Session: gameSessionToSessionInfo(session),
        MyStats: myStats,
        Rounds:  roundDetails,
    }, nil
}
```

#### 5.4.3 统计概览接口

```go
func (s *HistoryService) GetPlayerStats(ctx context.Context, userID int64) (*PlayerStatsResp, error) {
    agg, err := s.dbRepo.HistoryDBRepo().AggregatePlayerStatsFromBill(userID)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }
    if agg == nil {
        return &PlayerStatsResp{}, nil
    }

    loseCount := agg.TotalGames - agg.WinCount
    var winRate, avgProfit float64
    if agg.TotalGames > 0 {
        winRate = float64(agg.WinCount) / float64(agg.TotalGames)
        avgProfit = float64(agg.TotalProfit) / float64(agg.TotalGames)
    }

    return &PlayerStatsResp{
        TotalGames:     agg.TotalGames,
        WinCount:       agg.WinCount,
        LoseCount:      loseCount,
        WinRate:        winRate,
        TotalProfit:    currency.NewMoneyFromFen(agg.TotalProfit),
        TotalSend:      currency.NewMoneyFromFen(agg.TotalSend),
        TotalGrab:      currency.NewMoneyFromFen(agg.TotalGrab),
        TotalSendCount: agg.TotalSendCount,
        TotalGrabCount: agg.TotalGrabCount,
        AvgProfit:      currency.NewMoneyFromFen(int64(avgProfit)),
    }, nil
}
```

### 5.5 DTO 扩展

**文件**：[history_dto.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_dto.go)

```go
// SendDetail 发包明细（新增）
type SendDetail struct {
    TotalAmount currency.Money `json:"total_amount"` // 玩家本轮发出的红包总额
    StartedAt   int64          `json:"started_at"`
}

type RoundDetail struct {
    RoundID     string         `json:"round_id"`
    RoundNo     int            `json:"round_no"`
    SenderID    string         `json:"sender_id"`
    SenderType  string         `json:"sender_type"`
    TotalAmount currency.Money `json:"total_amount"`
    StartedAt   int64          `json:"started_at"`
    EndedAt     int64          `json:"ended_at"`
    Status      int            `json:"status"`
    MyGrab      *GrabDetail    `json:"my_grab,omitempty"`
    MySend      *SendDetail    `json:"my_send,omitempty"` // 新增
}
```

### 5.6 前端适配

#### 5.6.1 类型定义

**文件**：[gogain/packages/gift-box/src/history/types.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/types.ts)

```typescript
export type SendDetail = {
  total_amount: number
  started_at: number
}

export type RoundDetail = {
  round_id: string
  round_no: number
  sender_id: string
  sender_type: string
  total_amount: number
  started_at: number
  ended_at: number
  status: number
  my_grab?: GrabDetail
  my_send?: SendDetail  // 新增
}
```

#### 5.6.2 回合明细项展示

**文件**：[HistoryRoundItem.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/widgets/history-round-item/HistoryRoundItem.ts)

布局调整（当 `round.my_send` 存在时，右栏额外展示"发出 X"）：

```
┌──────────────────────────────────────────────┐
│ 第2局                                         │
│ 我发包                            发出 -800   │  ← 红色，玩家发包支出
│                                  抢到 +120   │  ← 金色，玩家抢到
│                                  红包总额 800 │
└──────────────────────────────────────────────┘
```

实现要点：
- 新增 `sendText` 文本节点，颜色用红色 `0xe85050`，前缀 `-`
- 当 `round.my_send` 存在时显示，否则隐藏
- `layoutRight` 中根据是否显示 `sendText` 动态调整 `grabText` 与 `totalAmount` 的 y 坐标

### 5.7 数据库索引

**文件**：`backend/migrations/20260626_add_bill_history_indexes.sql`（新增）

```sql
-- 列表查询：按 user_id + status 聚合 session_id
ALTER TABLE bill_record ADD INDEX idx_user_status_session (user_id, status, session_id);

-- 单局聚合：按 session_id + user_id 过滤
ALTER TABLE bill_record ADD INDEX idx_session_user_type (session_id, user_id, bill_type, status);

-- 玩家发包回合查询
ALTER TABLE rounds ADD INDEX idx_session_sender (session_id, sender_id, round_no);
```

执行后用 `EXPLAIN` 验证上述 SQL 是否命中索引。

---

## 六、对账验证方案

### 6.1 单局对账公式

对任一 `(session_id, user_id)`，以下等式应成立：

```
bill_record 盈亏 = SUM(amount) WHERE bill_type != 12 AND status = 1
                 = 总收入(bill_type IN (3,10,11), amount>0)
                 - 总支出(bill_type IN (2,4,8),  amount<0 的 ABS)

≈ session_players.total_grab - session_players.total_send  (修复 Bug 后的新数据)
```

> 注：由于 `session_players.total_grab` 包含"发包者自抢"金额，而 `bill_record` 中 `bill_type=3` 也包含自抢，两者口径一致。但 `session_players.total_send` 在 Bug 修复前会错位一回合，因此**存量数据只能用 bill 对账**。

### 6.2 验证 SQL

```sql
-- 检查单局某玩家的对账
SELECT
    SUM(CASE WHEN bill_type IN (3,10,11) AND amount > 0 THEN amount ELSE 0 END) AS income,
    SUM(CASE WHEN bill_type IN (2,4,8) AND amount < 0 THEN ABS(amount) ELSE 0 END) AS expense,
    SUM(amount) AS raw_profit
FROM bill_record
WHERE session_id = ? AND user_id = ? AND status = 1 AND bill_type != 12;
-- 期望：raw_profit = income - expense
```

### 6.3 灰度验证

1. 在测试环境跑 1 局完整对局
2. 用上述 SQL 查 `bill_record` 聚合结果
3. 调用 `get_player_history` / `get_player_session_detail` / `get_player_stats` 接口
4. 比对接口返回与 SQL 结果应一致
5. 重点验证：发包者本人在详情页能看到 `my_send`，且金额等于 `rounds.total_amount`

---

## 七、实施步骤

| 步骤 | 内容 | 涉及文件 |
|------|------|----------|
| 1 | 修复 consumer 写入 Bug | `game_event_consumer.go` |
| 2 | 扩展 `HistoryDBRepository` 接口 | `game/domain/db_repository.go` |
| 3 | 实现 bill 聚合查询方法 | `game/infrastructure/persistence/mysql/history_repository.go` |
| 4 | 改造 `HistoryService` 三个方法 | `game/application/history_service.go` |
| 5 | 扩展 DTO（`SendDetail`） | `game/application/history_dto.go` |
| 6 | 新增数据库索引 | `backend/migrations/20260626_add_bill_history_indexes.sql` |
| 7 | 前端类型与回合明细项适配 | `gift-box/src/history/types.ts`、`HistoryRoundItem.ts` |
| 8 | 灰度对账验证 | 见第六节 |

---

## 八、风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| `bill_record` 查询性能 | 列表加载变慢 | 新增组合索引；`session_id` 已有索引，按 user 聚合可走 `idx_user_status_session` |
| `bill_type=12` 误纳入统计 | 盈亏翻倍 | SQL 中显式 `bill_type != 12`，并在单元测试覆盖 |
| 系统发包回合无玩家发包者 | `MySend` 为空 | 前端判空隐藏，文案仍显示"系统发包" |
| 存量 `session_players` 数据错位 | 历史聚合不再使用该表 | 历史查询全走 `bill_record`，不再读 `session_players.total_send/total_grab` |
| `rounds.sender_id` 为 0（系统发包） | `GetPlayerSendRounds` 不会返回 | SQL 已过滤 `sender_type IN ('player','system_resume')` |
| 事件异步落地延迟 | 刚结束对局查询数据不全 | 接口层不补偿；前端加载失败时提示"数据同步中" |

---

## 九、未来扩展

1. **资金流水明细页**：基于 `bill_record` 按 `round_id` 展开每笔流水（P2 需求 US-5）
2. **多维度筛选**：`bill_type` 维度筛选（仅看成包/只看罚款）
3. **每日盈亏趋势**：按 `bill_record.created_at` 日期分组聚合
4. **异常对账监控**：定时任务比对 `bill_record` 与 `session_players`，发现不一致告警
5. **`session_players` 字段重构**：考虑废弃 `total_send`/`total_grab`/`total_profit`，改为运行时只维护 `joined_at`/`left_at`/`seat_no` 等非资金字段，资金数据统一从 bill 查询
