# 会话玩家快照（SessionPlayerSnapshot）重构方案

> **文档版本**: v1.0
> **创建日期**: 2026-07-17
> **状态**: 方案设计阶段（未实施）
> **范围**: game 模块（含 history / room / grab / settlement 交叉点）
> **目标**: 解决 `session_players` 表无法表达玩家座位流转、状态变更、替补关系的根本性数据不一致问题，建立可扩展的轮次玩家事实表

---

## 一、背景与问题概述

### 1.1 问题现象

2026-07-17 07:34 用户 905（user_id `203651623757549568`）调用 `get_player_session_detail` 查询历史详情，服务返回 `code=6003, msg="Error desconocido"`（未知错误）。

日志 `logs/game.log:1151-1155` 显示：

```
2026-07-17T07:34:19.484  received request  cmd=get_player_session_detail user_id=203651623757549568
2026-07-17T07:34:19.486  sending response  code=6003 msg="Error desconocido"
2026-07-17T07:34:24.645  received request  cmd=get_player_session_detail user_id=203651623757549568
2026-07-17T07:34:24.650  sending response  code=6003 msg="Error desconocido"
```

### 1.2 实际数据状态

session_id `203653389521784832` 的 `session_players` 表查询结果：

| seat_no | user_id            | nickname | joined_at              |
|---------|--------------------|----------|------------------------|
| 1       | 203494551426437120 | 901      | 2026-07-17 07:25:51.962 |
| 2       | 203494860089462784 | 903      | 2026-07-17 07:25:51.962 |
| 3       | 203650888387006464 | 904      | 2026-07-17 07:25:51.962 |
| 4       | 203494812731576320 | 902      | 2026-07-17 07:25:51.962 |
| 5       | 203492958488498176 | Robot_2  | 2026-07-17 07:25:51.962 |

**但日志实际流转**：

- 07:28:17 902 被 penalty kick 出 seat 4
- 07:28:17.569 905 自动替补入 seat 4（auto substitute success）
- 07:29:42 904 被 kick，902 替补入 seat 3
- 07:31:04 904 重新加入，select seat 1

**结论**：905 实际抢了 6 个红包（rounds 5/6/7/8/9/10），运行时 Redis 状态正确，但 `session_players` 表完全没有反映任何替补与重入座变化，905 在该表中根本无记录。

### 1.3 用户影响

1. **被替补玩家**（902、904）历史详情无法正确展示其后的座位变化
2. **替补玩家**（905）历史详情直接报错 `6003 玩家不在会话中`
3. **前端展示错位**：round 列表中按 `session_players.seat_no` 关联展示，会出现"902 在 seat 4 抢红包"的误导信息（实际 seat 4 已被 905 占据）
4. **审计缺失**：无法事后追溯某会话中玩家何时被踢、何时替补、何时重新入座

---

## 二、根因分析

### 2.1 根因 1：`session_players` 表结构无法表达流转

[DESCRIBE session_players] 输出：

```
Field         Type          Key     Default
id            bigint        PRI     auto_increment
session_id    bigint        MUL
room_id       bigint        MUL
user_id       bigint        MUL
nickname      varchar(50)
avatar        varchar(255)
seat_no       bigint
send_count    bigint
grab_count    bigint
total_send    bigint
total_grab    bigint
total_profit  bigint
ip            varchar(45)
device_id     varchar(100)
joined_at     datetime(3)
created_at    datetime(3)
```

**缺陷**：
- 无 `left_at` 字段：无法表达玩家何时离开
- 无 `status` 字段：无法区分 active / left / kicked / substituted
- 无 `replaced_by` 字段：无法追溯替补链
- `WHERE session_id=? AND user_id=?` 假设一人一行，但同一玩家可在同会话内先后坐多个座位（902 先 seat 4 后 seat 3）

### 2.2 根因 2：替补流程仅更新 Redis，不持久化到 MySQL

[room_app_service.go:339](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/room_app_service.go#L339) 的 `tryAutoSubstitute` 仅调用：

```go
subResult, err := s.repo.AutoSubstitute(ctx, roomID, seatNo)
```

[repository.go:429](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go#L429) 的 `AutoSubstitute` 仅执行 Lua 脚本更新 Redis 状态，**完全不写 `session_players` 表**。

### 2.3 根因 3：Kafka 消费者无 DB 落地

[room_event_consumer.go:93-103](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go#L93-L103)：

```go
switch event.EventType {
case events.RoomEventSpectatorJoin,
    events.RoomEventSpectatorLeave,
    events.RoomEventPlayerReady,
    events.RoomEventSeatCancel,
    events.RoomEventSpectatorKick,
    events.RoomEventQueueJoin,
    events.RoomEventQueueLeave,
    events.RoomEventSubstitute:
    // 所有已知房间事件类型均触发同步房间计数，无需按类型分发到独立 handler
    handleErr = c.syncRoomCounts(ctx, event)
}
```

7 种房间事件（含替补）一律走 `syncRoomCounts`，仅更新 `rooms.player_count/spectator_count`，**完全无 `session_players` 写入**。

### 2.4 根因 4：i18n 翻译缺失

[messages_es.go:13-89](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/i18n/messages_es.go#L13-L89) 的 `codeMessages` map 缺失 6001-6004 四个错误码的西班牙语映射：

```go
// common/message/errors.go
CodeHistoryQueryFailed  = 6001 // 历史查询失败
CodeSessionNotFound     = 6002 // 会话不存在
CodePlayerNotInSession  = 6003 // 玩家不在该会话中
CodeHistoryParamInvalid = 6004 // 参数校验失败
```

导致用户看到 `"Error desconocido"`（未知错误）而非有意义的提示。

### 2.5 根因 5：历史详情查询逻辑过于简单

[history_service.go:66-68](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go#L66-L68)：

```go
if player == nil {
    return nil, message.NewError(message.CodePlayerNotInSession)
}
```

`GetPlayerSession` 查不到记录即返回 6003，未考虑"玩家曾被替补但确实参与过该会话"的情况。

---

## 三、方案选型

### 3.1 三种方案对比

| 维度 | 方案 A：单表加状态字段 | 方案 B：拆分聚合表+座位历史表 | 方案 C：轮次玩家快照表 |
|------|---------------------|--------------------------|---------------------|
| 表数量 | 1（改造 session_players） | 2（session_players + session_seat_history） | 2（session_players 聚合 + round_player_snapshot） |
| 当前状态查询 | 需 `status='active'` 过滤 | 直接查 session_players | 直接查 session_players |
| 历史流转查询 | 同表多行 ORDER BY joined_at | join 历史表 | 单表 WHERE round_id=? |
| 对 round_grab_records 的影响 | 无需改动 | 可选冗余 seat_no | 可选冗余 seat_no |
| 扩展新维度成本 | 加表 + 迁移 + 改消费者 | 加表 | 加字段即可 |
| 查询复杂度 | 多行 + 过滤 | 多表 join | 单表 WHERE |
| 数据量（10 轮 × 5 玩家） | ~10 行 | ~10 行 | ~50 行 |
| 内聚性 | 低（聚合与状态耦合） | 中（座位流转内聚） | 高（所有玩家状态流转内聚） |

### 3.2 方案 A 的问题

- 聚合字段（`total_profit` 等）会随每次入座事件被多行冗余，需要在应用层聚合去重
- 同一玩家多次入座会有多行，`WHERE session_id=? AND user_id=?` 不再唯一，需要 `ORDER BY joined_at DESC LIMIT 1`，破坏现有查询契约

### 3.3 方案 B 的局限

- 只解决"座位流转"一个维度，其他维度（在线状态、准备状态、每轮角色等）仍需散落多表 join
- 扩展时每加一个维度就要加一张表，违反"内聚"原则

### 3.4 选定方案 C：轮次玩家快照表

**核心理由**：
1. **维度内聚**：一个表覆盖座位、角色、替补、离座、断线等所有玩家状态流转，未来加维度只需加字段
2. **查询高效**：历史详情的"每轮座位表"查询从多表 join 降为单表 WHERE
3. **事实表模式**：符合数据仓库最佳实践，snapshot 是不可变的事实记录，适合审计与统计分析
4. **向前兼容**：`session_players` 聚合表保留，现有查询不受影响

---

## 四、方案 C 详细设计

### 4.1 核心思路：以 Round 为锚点，记录每轮完整快照

每轮游戏开始时（发包前），为当前所有活跃玩家 + 旁观者各插入一行 `RoundPlayerSnapshot`。中途发生状态变更（替补/踢出/离座/加入旁观）时 UPDATE 对应行或 INSERT 新行。

### 4.2 业务场景覆盖

| 场景 | 方案 C 如何支持 |
|------|---------------|
| 座位流转（入座/离座/替补） | `seat_no` + `left_at` + `left_reason` + `replaced_by` |
| 玩家在线状态变更 | 加 `online_status` 字段（预留扩展） |
| 玩家准备状态变更 | 加 `ready_status` 字段（预留扩展） |
| 玩家在每轮的角色 | `is_sender` + `role` |
| 玩家被惩罚的历史 | join `penalty_records`，或冗余 `penalty_amount` 到 snapshot |
| 玩家每轮的盈亏 | join `round_grab_records`，或冗余 `round_pnl` |
| 玩家成为旁观者/离开旁观 | `role='spectator'` + `active_start/end` |
| 历史详情前端展示"每轮谁在哪个座位抢了多少" | 一次 WHERE round_id=? 完成 |
| 审计玩家从旁观者替补到玩家的过程 | WHERE session_id=? AND user_id=? AND source='substitute' |
| 跨会话统计"某玩家历史被踢率" | COUNT(*) WHERE user_id=? AND left_reason='kicked' 跨 session 查询 |

### 4.3 未来扩展场景

| 未来需求 | 方案 C 如何支持 |
|---------|---------------|
| 统计玩家在某会话中"坐过多少个不同座位" | `SELECT COUNT(DISTINCT seat_no) FROM round_player_snapshot WHERE session_id=? AND user_id=? AND seat_no IS NOT NULL` |
| 统计玩家"被替补了几次" | `SELECT COUNT(*) FROM round_player_snapshot WHERE session_id=? AND user_id=? AND left_reason='substituted'` |
| 查某轮某座位的玩家是谁 | `WHERE session_id=? AND round_no=? AND seat_no=?` |
| 排行榜按"每轮盈亏"展示 | join `round_grab_records` + snapshot，或冗余 `round_pnl` |
| 玩家断线/重连的轮次范围 | 加 `online_status` 字段到 snapshot |

---

## 五、表结构设计

### 5.1 新增表：RoundPlayerSnapshot

遵循 CODING_STANDARD.md §14 「GORM struct tags 作为 schema 单一真相源」：

```go
// RoundPlayerSnapshot 会话轮次玩家快照，记录每轮开始时所有座位 + 旁观者的完整状态。
// 一轮一玩家一行，是该轮的"事实表"，覆盖座位流转、角色变更、替补关系等所有玩家状态。
type RoundPlayerSnapshot struct {
    ID            int64      `gorm:"primaryKey;autoIncrement" json:"id"`
    SessionID     string     `gorm:"column:session_id;type:varchar(32);index:idx_round_session,priority:1;index:idx_session_user_round,priority:1" json:"session_id"`
    RoundID       string     `gorm:"column:round_id;type:varchar(32);index:idx_round_session,priority:2" json:"round_id"`
    RoundNo       int        `gorm:"column:round_no;index:idx_round_session,priority:3" json:"round_no"`
    UserID        string     `gorm:"column:user_id;type:varchar(32);index:idx_session_user_round,priority:2" json:"user_id"`
    Role          string     `gorm:"column:role;size:20;default:'player'" json:"role"`           // player / spectator
    SeatNo        *int       `gorm:"column:seat_no" json:"seat_no,omitempty"`                    // spectator 为 null
    IsSender      bool       `gorm:"column:is_sender;default:0" json:"is_sender"`               // 本轮是否发红包者
    JoinedAt      time.Time  `gorm:"column:joined_at;type:datetime(3)" json:"joined_at"`          // 加入房间时间
    ActiveStart   time.Time  `gorm:"column:active_start;type:datetime(3)" json:"active_start"`    // 本轮成为活跃玩家/旁观者的时刻
    ActiveEnd     *time.Time `gorm:"column:active_end;type:datetime(3);index" json:"active_end,omitempty"` // 本轮离开时刻
    LeftReason    string     `gorm:"column:left_reason;size:30" json:"left_reason"`              // kicked / substituted / user_request / timeout / disconnect
    ReplacedBy    string     `gorm:"column:replaced_by;type:varchar(32)" json:"replaced_by,omitempty"` // 替补者 user_id
    Source        string     `gorm:"column:source;size:20;default:'initial'" json:"source"`      // initial / substitute / rejoin
    CreatedAt     time.Time  `gorm:"column:created_at;type:datetime(3);autoCreateTime" json:"created_at"`
    UpdatedAt     time.Time  `gorm:"column:updated_at;type:datetime(3);autoUpdateTime" json:"updated_at"`
}

// TableName 指定表名
func (RoundPlayerSnapshot) TableName() string {
    return "round_player_snapshot"
}

// 索引说明：
// idx_round_session(session_id, round_id, round_no) - 按轮查所有玩家
// idx_session_user_round(session_id, user_id)       - 按玩家查所有参与轮次
// active_end 索引 - 支持查"当前活跃入座"（active_end IS NULL）
```

### 5.2 改造表：SessionPlayer

将原 `session_players` 重命名为 `session_player`（遵循 CODING_STANDARD.md 单数表名约定），并移除 `seat_no` 字段（座位流转下沉到 `RoundPlayerSnapshot`）：

```go
// SessionPlayer 会话玩家聚合统计，一人一行，跨所有轮次汇总。
// 状态字段 left_at/status 表示该玩家在会话中的最终状态。
type SessionPlayer struct {
    ID          int64      `gorm:"primaryKey;autoIncrement" json:"id"`
    SessionID   string     `gorm:"column:session_id;type:varchar(32);index:idx_session_user,priority:1,unique" json:"session_id"`
    UserID      string     `gorm:"column:user_id;type:varchar(32);index:idx_session_user,priority:2,unique" json:"user_id"`
    Nickname    string     `gorm:"column:nickname;size:50" json:"nickname"`
    Avatar      string     `gorm:"column:avatar;size:255" json:"avatar"`
    JoinedAt    time.Time  `gorm:"column:joined_at;type:datetime(3)" json:"joined_at"`
    LeftAt      *time.Time `gorm:"column:left_at;type:datetime(3);index" json:"left_at,omitempty"`
    Status      string     `gorm:"column:status;size:20;index:idx_session_status;default:'active'" json:"status"` // active / left / kicked / substituted
    SendCount   int64      `gorm:"column:send_count;default:0" json:"send_count"`
    GrabCount   int64      `gorm:"column:grab_count;default:0" json:"grab_count"`
    TotalSend   int64      `gorm:"column:total_send;default:0" json:"total_send"`
    TotalGrab   int64      `gorm:"column:total_grab;default:0" json:"total_grab"`
    TotalProfit int64      `gorm:"column:total_profit;default:0" json:"total_profit"`
    IP          string     `gorm:"column:ip;size:45" json:"ip"`
    DeviceID    string     `gorm:"column:device_id;size:100" json:"device_id"`
    CreatedAt   time.Time  `gorm:"column:created_at;type:datetime(3);autoCreateTime" json:"created_at"`
    UpdatedAt   time.Time  `gorm:"column:updated_at;type:datetime(3);autoUpdateTime" json:"updated_at"`
}

// TableName 指定表名
func (SessionPlayer) TableName() string {
    return "session_player"
}

// 唯一约束 idx_session_user(session_id, user_id) 保证一人一行
// status 索引便于按状态过滤当前活跃玩家
```

### 5.3 表关系

```
sessions (1) ──< round_player_snapshot (N)     每轮每人一行（事实表）
sessions (1) ──< session_player (N)            聚合统计，一人一行
sessions (1) ──< rounds (N)                   轮次元数据
rounds (1)   ──< round_grab_records (N)       抢红包明细
sessions (1) ──< penalty_records (N)          惩罚记录（已存在）
```

---

## 六、代码改造点

### 6.1 新增 Repository 接口

[game/domain/repository/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/repository/) 新增 `snapshot_repository.go`：

```go
package repository

import (
    "context"
    "time"
)

// SnapshotRepository 轮次玩家快照仓储接口
type SnapshotRepository interface {
    // BatchCreateOnRoundStart round 开始时批量插入所有玩家快照
    BatchCreateOnRoundStart(ctx context.Context, snapshots []*RoundPlayerSnapshot) error

    // MarkPlayerLeft 标记玩家在某轮离开（被踢/离座/替补）
    MarkPlayerLeft(ctx context.Context, sessionID, roundID, userID string, leftAt time.Time, reason, replacedBy string) error

    // AddPlayerMidRound 中途加入（替补/重新入座/成为旁观者）
    AddPlayerMidRound(ctx context.Context, snapshot *RoundPlayerSnapshot) error

    // ListByRound 查询某轮的所有玩家快照
    ListByRound(ctx context.Context, sessionID, roundID string) ([]*RoundPlayerSnapshot, error)

    // ListByUser 查询某玩家在某会话的所有参与轮次
    ListByUser(ctx context.Context, sessionID, userID string) ([]*RoundPlayerSnapshot, error)

    // ListActiveSeats 查询某轮当前活跃的座位（active_end IS NULL）
    ListActiveSeats(ctx context.Context, sessionID, roundID string) ([]*RoundPlayerSnapshot, error)
}

// RoundPlayerSnapshot 快照实体
type RoundPlayerSnapshot struct {
    ID          int64
    SessionID   string
    RoundID     string
    RoundNo     int
    UserID      string
    Role        string
    SeatNo      *int
    IsSender    bool
    JoinedAt    time.Time
    ActiveStart time.Time
    ActiveEnd   *time.Time
    LeftReason  string
    ReplacedBy  string
    Source      string
}
```

### 6.2 新增 Snapshot 消费者分支

改造 [room_event_consumer.go:93-103](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go#L93-L103)：

```go
switch event.EventType {
case events.RoomEventSubstitute:
    handleErr = c.handleSubstitute(ctx, event)
case events.RoomEventSpectatorJoin:
    handleErr = c.handleSpectatorJoin(ctx, event)
case events.RoomEventSpectatorLeave:
    handleErr = c.handleSpectatorLeave(ctx, event)
case events.RoomEventSpectatorKick:
    handleErr = c.handleSpectatorKick(ctx, event)
case events.RoomEventSeatCancel:
    handleErr = c.handleSeatCancel(ctx, event)
case events.RoomEventQueueJoin, events.RoomEventQueueLeave:
    handleErr = c.syncRoomCounts(ctx, event)
default:
    return fmt.Errorf("unknown event type: %s", event.EventType)
}

// handleSubstitute 处理替补事件：
// 1. UPDATE round_player_snapshot SET active_end=now, left_reason='substituted', replaced_by=?
//    WHERE session_id=? AND round_id=? AND user_id=? AND active_end IS NULL
// 2. INSERT INTO round_player_snapshot (替补者新行, source='substitute')
// 3. UPDATE session_player SET status='substituted', left_at=now WHERE session_id=? AND user_id=被替者
// 4. INSERT INTO session_player (替补者) ON CONFLICT DO NOTHING
func (c *RoomEventConsumer) handleSubstitute(ctx context.Context, event *events.RoomEvent) error {
    payload, err := event.ParseSubstitutePayload()
    if err != nil {
        return fmt.Errorf("parse substitute payload failed: %w", err)
    }

    now := time.Now()
    currentRoundID, err := c.getCurrentRoundID(ctx, event.RoomID)
    if err != nil {
        return fmt.Errorf("get current round id failed: %w", err)
    }

    // 事务边界遵循 CODING_STANDARD.md §15：应用层编排
    return c.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        // 1. 标记被替者的当前 snapshot 为已离开
        if err := tx.SnapshotRepo().MarkPlayerLeft(ctx, event.SessionID, currentRoundID, event.UserID,
            now, "substituted", payload.SubstituteUserID); err != nil {
            return fmt.Errorf("mark player left failed: %w", err)
        }

        // 2. 插入替补者的新 snapshot
        snapshot := &repository.RoundPlayerSnapshot{
            SessionID:   event.SessionID,
            RoundID:     currentRoundID,
            UserID:      payload.SubstituteUserID,
            Role:        "player",
            SeatNo:      &payload.SeatNo,
            JoinedAt:    now,
            ActiveStart: now,
            Source:      "substitute",
        }
        if err := tx.SnapshotRepo().AddPlayerMidRound(ctx, snapshot); err != nil {
            return fmt.Errorf("add substitute snapshot failed: %w", err)
        }

        // 3. 更新被替者的 session_player 状态
        if err := tx.SessionPlayerRepo().UpdateStatus(ctx, event.SessionID, event.UserID,
            "substituted", now); err != nil {
            return fmt.Errorf("update session player status failed: %w", err)
        }

        // 4. 替补者首次入会话则插入 session_player
        if err := tx.SessionPlayerRepo().Upsert(ctx, &repository.SessionPlayer{
            SessionID: event.SessionID,
            UserID:    payload.SubstituteUserID,
            Nickname:  payload.Nickname,
            Avatar:    payload.Avatar,
            JoinedAt:  now,
            Status:    "active",
        }); err != nil {
            return fmt.Errorf("upsert substitute session player failed: %w", err)
        }

        return nil
    })
}
```

### 6.3 Round 创建时批量插入 Snapshot

在 [game_event_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_event_handler.go) `handleRoundStarted` 中调用：

```go
func (h *GameEventHandler) handleRoundStarted(ctx context.Context, event *events.GameEvent) error {
    // ... 现有 round 创建逻辑 ...

    // 新增：批量插入 round_player_snapshot
    snapshots, err := h.buildSnapshotsFromRedis(ctx, event.RoomID, event.SessionID, roundID, roundNo, senderID)
    if err != nil {
        return fmt.Errorf("build snapshots failed: %w", err)
    }

    if err := h.snapshotRepo.BatchCreateOnRoundStart(ctx, snapshots); err != nil {
        return fmt.Errorf("batch create snapshots failed: %w", err)
    }

    return nil
}

// buildSnapshotsFromRedis 从 Redis 读取当前所有玩家 + 旁观者状态，构建快照
func (h *GameEventHandler) buildSnapshotsFromRedis(ctx context.Context, roomID, sessionID, roundID string, roundNo int, senderID string) ([]*repository.RoundPlayerSnapshot, error) {
    stateData, err := h.repo.GetRoomStateData(ctx, roomID)
    if err != nil {
        return nil, err
    }

    var snapshots []*repository.RoundPlayerSnapshot
    now := time.Now()

    // 玩家快照
    for _, player := range stateData.Players {
        seatNo := player.SeatNo
        snapshot := &repository.RoundPlayerSnapshot{
            SessionID:   sessionID,
            RoundID:     roundID,
            RoundNo:     roundNo,
            UserID:      player.UserID,
            Role:        "player",
            SeatNo:      &seatNo,
            IsSender:    player.UserID == senderID,
            JoinedAt:    player.JoinedAt,
            ActiveStart: now,
            Source:      "initial",
        }
        snapshots = append(snapshots, snapshot)
    }

    // 旁观者快照
    for _, spectator := range stateData.Spectators {
        snapshot := &repository.RoundPlayerSnapshot{
            SessionID:   sessionID,
            RoundID:     roundID,
            RoundNo:     roundNo,
            UserID:      spectator.UserID,
            Role:        "spectator",
            SeatNo:      nil,
            JoinedAt:    spectator.JoinedAt,
            ActiveStart: now,
            Source:      "initial",
        }
        snapshots = append(snapshots, snapshot)
    }

    return snapshots, nil
}
```

### 6.4 历史详情查询改造

[history_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go) 改造：

```go
func (s *HistoryService) GetPlayerSessionDetail(ctx context.Context, userID, sessionID string) (*PlayerSessionDetail, error) {
    // 查询 1：玩家在该会话的聚合统计（即使 status != 'active' 也返回）
    player, err := s.dbRepo.GetPlayerSession(ctx, userID, sessionID)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }

    // 关键改造：player == nil 时不再直接返回 6003
    // 改为查 snapshot 表，如果玩家在该会话有任何 snapshot 记录，说明确实参与过
    if player == nil {
        snapshots, _ := s.dbRepo.SnapshotRepo().ListByUser(ctx, sessionID, userID)
        if len(snapshots) == 0 {
            return nil, message.NewError(message.CodePlayerNotInSession)
        }
        // 玩家曾参与但 session_player 未记录（历史数据兼容），用 snapshot 构造临时 player
        player = &repository.SessionPlayer{
            SessionID: sessionID,
            UserID:    userID,
            JoinedAt:  snapshots[0].JoinedAt,
            Status:    "active", // 容错
        }
    }

    // 查询 2：session 基本信息
    session, err := s.dbRepo.GetSession(ctx, sessionID)
    if err != nil {
        return nil, message.NewError(message.CodeSessionNotFound)
    }

    // 查询 3：所有 round 列表
    rounds, err := s.dbRepo.GetSessionRounds(ctx, sessionID)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }

    // 查询 4：玩家在该会话的所有参与轮次快照（用于关联座位信息）
    seatSnapshots, err := s.dbRepo.SnapshotRepo().ListByUser(ctx, sessionID, userID)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }

    // 查询 5：玩家所有抢红包记录
    grabRecords, err := s.dbRepo.ListPlayerGrabRecords(ctx, userID, sessionID)
    if err != nil {
        return nil, message.NewError(message.CodeHistoryQueryFailed)
    }

    // 组装响应：用 snapshot 关联 round 与座位
    return s.buildDetailResponse(player, session, rounds, seatSnapshots, grabRecords), nil
}
```

### 6.5 i18n 翻译补全

[messages_es.go:13-89](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/i18n/messages_es.go#L13-L89) 补全：

```go
var codeMessages = map[int]string{
    // ... 现有映射 ...
    6001: "Error al consultar el historial",           // CodeHistoryQueryFailed
    6002: "La sesión no existe",                       // CodeSessionNotFound
    6003: "El jugador no está en la sesión",           // CodePlayerNotInSession
    6004: "Parámetros del historial inválidos",        // CodeHistoryParamInvalid
}
```

### 6.6 TraceID 透传修复

[room_event.go:218-221](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/events/room_event.go#L218-L221) 的 `NewSubstituteEvent` 改为接收 traceID：

```go
func NewSubstituteEvent(roomID, userID string, seatNo int, nickname, avatar, traceID string) *RoomEvent {
    event := &RoomEvent{
        EventHeader: message.NewEventHeader(traceID),
        EventType:   RoomEventSubstitute,
        RoomID:      roomID,
        UserID:      userID,
    }
    _ = event.SetPayload(SubstitutePayload{
        SeatNo:   seatNo,
        Nickname: nickname,
        Avatar:   avatar,
    })
    return event
}
```

[room_app_service.go:352](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/room_app_service.go#L352) 调用方透传：

```go
if s.publisher != nil && subResult.Player != nil {
    traceID := trace.FromContext(ctx)
    if err := s.publisher.PublishRoomEvent(ctx, events.NewSubstituteEvent(
        roomID, subResult.SubstituteUserID, subResult.SeatNo,
        subResult.Player.Nickname, subResult.Player.Avatar, traceID)); err != nil {
        // ...
    }
}
```

---

## 七、迁移步骤

### 7.1 阶段 1：表结构准备（不影响线上）

1. 新增 `round_player_snapshot` 表（GORM AutoMigrate）
2. `session_players` 重命名为 `session_player`，加列 `left_at` / `status` / `updated_at`
3. 保留 `session_player.seat_no` 字段（兼容期，后续删除）

### 7.2 阶段 2：写入双跑（Dual Write）

1. 实现新 Repository 接口 `SnapshotRepository`
2. [game_event_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_event_handler.go) round 创建时双跑写入 `round_player_snapshot`
3. [room_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go) 加 `handleSubstitute` / `handleSpectatorKick` 等分支双跑
4. 监控双跑成功率，对比 Redis 状态与 DB snapshot 一致性

### 7.3 阶段 3：查询切换

1. [history_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go) 改用新查询逻辑
2. 前端联调历史详情展示（每轮座位表）
3. 灰度切换：先 10% 流量用新查询，逐步放量到 100%

### 7.4 阶段 4：历史数据回填

```sql
-- 从 round_grab_records 反推每轮的座位占用
INSERT INTO round_player_snapshot (session_id, round_id, round_no, user_id, role, seat_no, is_sender, joined_at, active_start, source)
SELECT
    r.session_id,
    r.round_id,
    r.round_no,
    gr.user_id,
    'player' AS role,
    NULL AS seat_no,  -- 历史数据无座位信息，置 NULL
    (r.sender_id = gr.user_id) AS is_sender,
    r.created_at AS joined_at,
    r.created_at AS active_start,
    'initial' AS source
FROM rounds r
JOIN round_grab_records gr ON r.round_id = gr.round_id
WHERE NOT EXISTS (
    SELECT 1 FROM round_player_snapshot s
    WHERE s.session_id = r.session_id AND s.round_id = r.round_id AND s.user_id = gr.user_id
);
```

### 7.5 阶段 5：清理

1. 删除 `session_player.seat_no` 字段
2. 删除双跑代码
3. 删除旧 `session_players` 表名（若已重命名）

---

## 八、验证方案

### 8.1 单元测试

新增 `snapshot_repository_test.go`，覆盖：
- `BatchCreateOnRoundStart` 批量插入 5 玩家 + 2 旁观者
- `MarkPlayerLeft` 标记被替者离开
- `AddPlayerMidRound` 插入替补者
- `ListByRound` 查询某轮所有玩家
- `ListByUser` 查询某玩家所有参与轮次
- `ListActiveSeats` 查询当前活跃座位

### 8.2 集成测试

模拟本次问题场景：
1. 创建会话，5 玩家入座
2. 触发 902 被 penalty kick
3. 验证 905 替补后 `round_player_snapshot` 有 905 的记录
4. 验证 902 的 snapshot 行 `left_reason='substituted'`, `replaced_by=905`
5. 调用 `get_player_session_detail` for 905，应返回 `code=0` 而非 `6003`

### 8.3 数据一致性校验

```sql
-- 校验：当前活跃玩家数（snapshot）与 Redis HLEN 一致
SELECT COUNT(*) FROM round_player_snapshot
WHERE session_id = ? AND round_id = ? AND active_end IS NULL AND role = 'player';
-- 应等于 Redis HLEN room:players:{roomID}

-- 校验：session_player.status='active' 数量与当前活跃玩家一致
SELECT COUNT(*) FROM session_player
WHERE session_id = ? AND status = 'active';
-- 应等于 Redis HLEN room:players:{roomID}
```

### 8.4 性能测试

- 单轮批量插入 5 行 < 10ms
- 历史详情查询（10 轮 × 5 玩家）< 50ms
- 单会话 snapshot 表行数：10 轮 × 5 玩家 = 50 行，索引覆盖下查询性能无瓶颈

---

## 九、风险与对策

| 风险 | 评估 | 对策 |
|------|------|------|
| 数据量增长 | 10 轮 × 5 玩家 = 50 行/会话，1 万会话 = 50 万行 | 加 `idx_round_session` 索引；按 `created_at` 分区可选 |
| 写入性能 | 每轮开始批量插入 5 行 | 用 `INSERT ... VALUES (...), (...), ...` 批量插入 |
| 一致性 | round 创建时批量插入，中途变更需 UPDATE | 消费者幂等：先查再 UPDATE，靠 `event_id` 去重 |
| 双跑期间数据不一致 | Redis 成功但 DB 失败 | 失败时记录告警日志，不影响游戏运行；定时任务校对 |
| 历史数据回填不完整 | 无座位信息 | `seat_no` 置 NULL，前端展示"未知座位" |
| 事务边界 | 跨 snapshot + session_player 两表写入 | 应用层 `WithTransaction` 编排（遵循 CODING_STANDARD.md §15） |
| Kafka 消费者事务 | 替补事件涉及多表 | 事务内完成所有 DB 操作，失败则 Kafka 重试 |

---

## 十、执行优先级与里程碑

### 10.1 P0（修复核心 Bug）

| 任务 | 工作量 | 依赖 |
|------|--------|------|
| i18n 补全 6001-6004 翻译 | 0.5h | 无 |
| 新增 `round_player_snapshot` 表 + GORM 模型 | 1h | 无 |
| 实现 `SnapshotRepository` 接口 | 3h | 表结构 |
| [room_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go) 加 `handleSubstitute` 等分支 | 4h | Repository |
| [game_event_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_event_handler.go) round 创建时写入 snapshot | 3h | Repository |
| TraceID 透传修复 | 1h | 无 |

### 10.2 P1（查询切换）

| 任务 | 工作量 | 依赖 |
|------|--------|------|
| [history_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go) 改用 snapshot 查询 | 4h | P0 完成 |
| 前端联调历史详情展示 | 2h | 后端切换 |
| 集成测试 + 数据一致性校验 | 3h | 联调完成 |

### 10.3 P2（优化与清理）

| 任务 | 工作量 | 依赖 |
|------|--------|------|
| 历史数据回填脚本 | 2h | P1 稳定运行 1 周 |
| `round_grab_records` 冗余 `seat_no` 字段 | 2h | 无 |
| 删除 `session_player.seat_no` 字段 | 1h | 兼容期结束 |
| 删除双跑代码 | 1h | 兼容期结束 |

---

## 十一、附录

### 11.1 相关文件清单

| 文件 | 改造类型 |
|------|---------|
| [common/i18n/messages_es.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/i18n/messages_es.go) | 新增 4 条翻译 |
| [common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go) | 无需改动（错误码已定义） |
| [game/domain/events/room_event.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/events/room_event.go) | `NewSubstituteEvent` 加 traceID 参数 |
| [game/domain/repository/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/repository/) | 新增 `snapshot_repository.go` 接口 |
| [game/domain/repository/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/repository/db_repository.go) | `DBRepository` 接口加 `SnapshotRepo()` 访问器 |
| [game/domain/repository/transaction.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/repository/transaction.go) | `Transaction` 接口加 `SnapshotRepo()` 访问器 |
| [game/infrastructure/persistence/mysql/snapshot_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/) | 新增实现 |
| [game/infrastructure/messaging/room_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go) | 按事件类型分发到独立 handler |
| [game/application/game_event_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_event_handler.go) | round 创建时批量写入 snapshot |
| [game/application/history_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go) | 改用 snapshot 查询 |
| [game/application/room_app_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/room_app_service.go) | 透传 traceID 给 `NewSubstituteEvent` |

### 11.2 相关代码位置

- 替补流程入口：[room_app_service.go:293](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/room_app_service.go#L293) `tryAutoSubstitute`
- 替补 Lua 脚本：[repository.go:429](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/repository.go#L429) `AutoSubstitute`
- 踢出流程入口：[game_lifecycle_endgame.go:167](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_endgame.go#L167) `handleKickAndReplace`
- 踢出 Lua 脚本：[room_seat.lua.go:273](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/room_seat.lua.go#L273) `luaKickPlayerAndInterrupt`
- 抢红包玩家校验：[packet.lua.go:42-45](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/packet.lua.go#L42-L45) `HGET playersKey userID`
- 历史详情入口：[generic_service.go:599-625](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go#L599-L625) `handleGetPlayerSessionDetail`

### 11.3 遵循的 CODING_STANDARD.md 规则

| 规则 | 章节 | 应用点 |
|------|------|--------|
| GORM struct tags 作为 schema 单一真相源 | §14 | `RoundPlayerSnapshot` / `SessionPlayer` 模型定义 |
| 复合索引显式声明 priority | §14 | `idx_round_session` / `idx_session_user_round` |
| 事务边界在应用层 | §15 | `RoomEventConsumer.handleSubstitute` 用 `WithTransaction` |
| 短事务原则：禁止 RPC/Kafka/耗时计算在事务内 | §15 | snapshot 写入仅 DB 操作 |
| 乐观锁优先：状态机 UPDATE 加 WHERE status=? | §15 | `MarkPlayerLeft` 加 `WHERE active_end IS NULL` |
| TraceID 必须由消息发布者设置并校验 | §5.4 | `NewSubstituteEvent` 透传 traceID |
| Repository 聚合对象必须 eager 初始化 | §15.3 | `DBRepositoryImpl` 构造时初始化 `SnapshotRepo` |
| `domain.Transaction` 接口扩展子 repo 访问器 | §15.4 | `Transaction` 加 `SnapshotRepo()` 方法 |
| Lua 脚本错误码处理 | §13 | 无需改动 Lua，仅 DB 层新增 |
| 代码注释中文 | §17.1 | 所有 godoc 注释中文 |
| 错误日志英文（第三人称过去时） | §5.4 | 日志保持英文 |
| 字符串拼接规范 | §16 | SQL 用 ? 占位符，错误用 %w 包装 |

### 11.4 数据量估算

| 场景 | 单会话行数 | 日均会话数 | 日均行数 | 月均行数 |
|------|-----------|-----------|---------|---------|
| 小型（10 轮 × 5 玩家） | 50 | 100 | 5,000 | 150,000 |
| 中型（20 轮 × 5 玩家） | 100 | 500 | 50,000 | 1,500,000 |
| 大型（30 轮 × 5 玩家） | 150 | 1,000 | 150,000 | 4,500,000 |

按月 450 万行估算，单表可承载（MySQL 单表 1000 万行内性能良好），必要时按 `created_at` 月度分区。

---

## 十二、总结

本方案以 **轮次玩家快照表（RoundPlayerSnapshot）** 为核心，建立可扩展的轮次玩家事实表，根本性解决：

1. **数据一致性**：替补/踢出/重入座等状态流转完整持久化到 MySQL
2. **查询效率**：历史详情"每轮座位表"从多表 join 降为单表 WHERE
3. **扩展性**：未来新增维度（在线状态、准备状态、每轮盈亏等）只需加字段，无需加表
4. **审计能力**：完整的玩家状态流转记录，支持事后追溯与统计分析

同时修复 i18n 翻译缺失、TraceID 透传断链等附属问题，遵循 CODING_STANDARD.md 全部相关规则。
