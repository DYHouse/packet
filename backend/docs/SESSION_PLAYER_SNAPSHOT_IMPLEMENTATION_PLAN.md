# 会话玩家快照重构 — 实施计划

> **基于**: `backend/docs/SESSION_PLAYER_SNAPSHOT_REFACTOR_PROPOSAL.md` (方案 C)
> **创建日期**: 2026-07-17
> **状态**: 待审批

## 一、目标

修复替补玩家（905）查询历史详情报 6003 错误，建立 `round_player_snapshot` 事实表持久化玩家流转，补全 i18n 翻译，修复 TraceID 透传断链。

## 二、关键事实修正（与原方案文档差异）

经过代码探索，发现原方案文档中以下假设与实际不符，需修正：

| 原方案假设 | 实际情况 | 修正方案 |
|----------|---------|---------|
| 新建 `SessionPlayerRepository` 接口 | 该接口不存在；现有方法散在 `SessionDBRepository`（写）和 `HistoryDBRepository`（读） | 在 `SessionDBRepository` 接口上扩展 `UpdateStatus`/`Upsert` 方法 |
| `handleRoundStarted` 在 `GameEventHandler` 中 | `GameEventHandler` 只处理 4 个 Kafka 事件（SessionStart/PacketCreated/RoundSettle/SessionEnd），均只做 UPDATES；Round DB 创建入口在 [packet_round_init.go:100](backend/game/application/packet_round_init.go#L100) `PacketOrchestrator.createRoundRecord` | 在 `PacketOrchestrator.createRoundRecord` 后调用 snapshot 写入 |
| `Transaction` 接口在 `transaction.go` | 实际在 [db_repository.go:74-83](backend/game/domain/repository/db_repository.go#L74-L83) 内联定义 | 直接在 `db_repository.go` 扩展接口 |
| `RoomEvent.SessionID` 字段存在 | 不存在；`RoomEvent` 只有 `EventHeader/EventType/RoomID/UserID/Payload` | 通过 Redis `RoomHashKey(roomID)` → `current_session_id` 查询 |
| `NewSubstituteEvent` 接收 traceID | 现签名 `NewSubstituteEvent(roomID, userID string, seatNo int, nickname, avatar string)` 无 traceID 参数 | 修改签名增加 traceID 参数 |
| 生产 AutoMigrate | CODING_STANDARD §14 禁止生产 AutoMigrate；仅在 [scripts/init_rooms.go:72-93](backend/scripts/init_rooms.go#L72-L93) 本地初始化用 | 本地用 AutoMigrate；生产用 SQL migration 脚本 |

## 三、实施任务分解（按优先级）

### P0-A：i18n 翻译补全（独立可发布）

**文件**: [common/i18n/messages_es.go](backend/common/i18n/messages_es.go#L13-L89)

在 `codeMessages` map 末尾（line 89 前）追加：

```go
6001: "Error al consultar el historial",           // CodeHistoryQueryFailed
6002: "La sesión no existe",                       // CodeSessionNotFound
6003: "El jugador no está en la sesión",           // CodePlayerNotInSession
6004: "Parámetros del historial inválidos",        // CodeHistoryParamInvalid
```

**验证**: `go test ./common/i18n/...`；手动调用 `GetErrorMsg(6003)` 应返回西语翻译而非 "Error desconocido"。

---

### P0-B：TraceID 透传修复（独立可发布）

**文件 1**: [game/domain/events/room_event.go:218-231](backend/game/domain/events/room_event.go#L218-L231)

修改 `NewSubstituteEvent` 签名，增加 `traceID string` 参数：

```go
func NewSubstituteEvent(roomID, userID string, seatNo int, nickname, avatar, traceID string) *RoomEvent {
    event := &RoomEvent{
        EventHeader: message.NewEventHeader(traceID),  // 原来是 ""
        ...
    }
    ...
}
```

**文件 2**: [game/application/room_app_service.go:351-361](backend/game/application/room_app_service.go#L351-L361)

调用方传入 traceID：

```go
traceID := trace.FromContext(ctx)
if err := s.publisher.PublishRoomEvent(ctx, events.NewSubstituteEvent(
    roomID, subResult.SubstituteUserID, subResult.SeatNo,
    subResult.Player.Nickname, subResult.Player.Avatar, traceID)); err != nil {
    ...
}
```

**验证**: 检查所有 `NewSubstituteEvent` 调用点（用 grep 确认仅此一处）；触发替补流程，验证日志 `trace_id` 字段非空。

---

### P0-C：新增 `round_player_snapshot` 表 + Repository

#### 1. 新增 GORM 模型

**新文件**: `game/infrastructure/persistence/mysql/model/round_player_snapshot.go`

```go
// RoundPlayerSnapshot 会话轮次玩家快照，记录每轮所有座位+旁观者的完整状态。
// 一轮一玩家一行，是该轮的"事实表"，覆盖座位流转、角色变更、替补关系。
type RoundPlayerSnapshot struct {
    ID            int64      `gorm:"primaryKey;autoIncrement" json:"id"`
    SessionID     int64      `gorm:"column:session_id;index:idx_round_session,priority:1;index:idx_session_user_round,priority:1" json:"session_id"`
    RoundID       int64      `gorm:"column:round_id;index:idx_round_session,priority:2" json:"round_id"`
    RoundNo       int        `gorm:"column:round_no;index:idx_round_session,priority:3" json:"round_no"`
    UserID        int64      `gorm:"column:user_id;index:idx_session_user_round,priority:2" json:"user_id"`
    Role          string     `gorm:"column:role;size:20;default:'player'" json:"role"`
    SeatNo        *int       `gorm:"column:seat_no" json:"seat_no,omitempty"`
    IsSender      bool       `gorm:"column:is_sender;default:0" json:"is_sender"`
    JoinedAt      time.Time  `gorm:"column:joined_at;type:datetime(3)" json:"joined_at"`
    ActiveStart   time.Time  `gorm:"column:active_start;type:datetime(3)" json:"active_start"`
    ActiveEnd     *time.Time `gorm:"column:active_end;type:datetime(3);index" json:"active_end,omitempty"`
    LeftReason    string     `gorm:"column:left_reason;size:30" json:"left_reason"`
    ReplacedBy    int64      `gorm:"column:replaced_by" json:"replaced_by,omitempty"`
    Source        string     `gorm:"column:source;size:20;default:'initial'" json:"source"`
    CreatedAt     time.Time  `gorm:"column:created_at;type:datetime(3);autoCreateTime" json:"created_at"`
    UpdatedAt     time.Time  `gorm:"column:updated_at;type:datetime(3);autoUpdateTime" json:"updated_at"`
}

func (RoundPlayerSnapshot) TableName() string { return "round_player_snapshot" }
```

> 注：参考现有 [model.SessionPlayer](backend/game/infrastructure/persistence/mysql/model/) 字段类型，`session_id/user_id/round_id` 均为 `int64`（雪花 ID），不是 `string`。

#### 2. 新增 Repository 接口

**文件**: [game/domain/repository/db_repository.go](backend/game/domain/repository/db_repository.go)

在 `DBRepository` 接口（line 213-225）追加：

```go
SnapshotDBRepo() SnapshotRepository
```

在 `Transaction` 接口（line 74-83）追加：

```go
SnapshotRepo() SnapshotRepository
```

新增接口定义（同文件，参照现有 sub-repo 风格）：

```go
// SnapshotRepository 轮次玩家快照仓储接口
type SnapshotRepository interface {
    // BatchCreateOnRoundStart round 开始时批量插入玩家快照
    BatchCreateOnRoundStart(ctx context.Context, snapshots []*model.RoundPlayerSnapshot) error
    // MarkPlayerLeft 标记玩家在某轮离开（被踢/离座/替补）
    // 乐观锁：WHERE active_end IS NULL 避免重复标记
    MarkPlayerLeft(ctx context.Context, sessionID, roundID, userID int64, leftAt time.Time, reason string, replacedBy int64) error
    // AddPlayerMidRound 中途加入（替补/重新入座/成为旁观者）
    AddPlayerMidRound(ctx context.Context, snapshot *model.RoundPlayerSnapshot) error
    // ListByRound 查询某轮的所有玩家快照
    ListByRound(ctx context.Context, sessionID, roundID int64) ([]*model.RoundPlayerSnapshot, error)
    // ListByUser 查询某玩家在某会话的所有参与轮次
    ListByUser(ctx context.Context, sessionID, userID int64) ([]*model.RoundPlayerSnapshot, error)
}
```

#### 3. 新增 Repository 实现

**新文件**: `game/infrastructure/persistence/mysql/snapshot_repository.go`

实现 `gormSnapshotRepository` 结构体 + `NewGormSnapshotRepository(db *gorm.DB)` 构造函数。关键 SQL：

- `BatchCreateOnRoundStart`: `db.CreateInBatches(snapshots, 100)` 批量插入
- `MarkPlayerLeft`: `db.Model(&RoundPlayerSnapshot{}).Where("session_id=? AND round_id=? AND user_id=? AND active_end IS NULL").Updates(map{...})` — 用 `active_end IS NULL` 作为乐观锁
- `AddPlayerMidRound`: `db.Create(snapshot)`
- `ListByRound`/`ListByUser`: 简单 SELECT

#### 4. 扩展 `DBRepositoryImpl` 与 `GormTransactionImpl`

**文件**: [game/infrastructure/persistence/mysql/db_repository.go](backend/game/infrastructure/persistence/mysql/db_repository.go#L13-L108)

`DBRepositoryImpl` (line 13-41) 加字段 `snapshotRepo repository.SnapshotRepository` + 构造函数初始化 + `SnapshotDBRepo()` 方法。

`GormTransactionImpl` (line 69-108) 加字段 + 构造函数初始化 + `SnapshotRepo()` 方法。

遵循 CODING_STANDARD §15.3 "Repository 聚合对象必须 eager 初始化"。

#### 5. 扩展 `SessionDBRepository` 接口

**文件**: [game/domain/repository/db_repository.go:19-28](backend/game/domain/repository/db_repository.go#L19-L28)

仅追加 1 个方法（用于替补者首次入会话时 upsert）：

```go
// UpsertPlayer 插入或更新玩家聚合记录（用于替补者首次入会话）
UpsertPlayer(ctx context.Context, player *model.SessionPlayer) error
```

实现：[game/infrastructure/persistence/mysql/session_repository.go](backend/game/infrastructure/persistence/mysql/session_repository.go) 的 `gormSessionRepository` 加该方法。

> **不扩展 `UpdateStatus` 方法**：玩家在会话中的流转状态（kicked/substituted/left）由 `round_player_snapshot` 事实表记录，`session_players` 表仅作为聚合统计表（一人一行，跨整个会话），不再加 status / left_at 字段。

#### 6. `SessionPlayer` 模型保持现状

**文件**: [game/infrastructure/persistence/mysql/model/](backend/game/infrastructure/persistence/mysql/model/) 的 `SessionPlayer` 结构体

**完全保持现状**：[game/model/session.go:34-51](backend/game/model/session.go#L34-L51) 的 `SessionPlayer` 结构体**不做任何修改**。

现状（已有字段）：
- `ID` / `SessionID` / `RoomID` / `UserID` / `Nickname` / `Avatar` / `SeatNo`
- `SendCount` / `GrabCount` / `TotalSend` / `TotalGrab`
- `IP` / `DeviceID` / `JoinedAt` / `CreatedAt`
- `TableName()` 方法已存在，返回 `"session_players"`

**不新增任何字段**（不加 `left_at` / `status` / `updated_at`）：
- `left_at`：在聚合表上无意义（玩家可能多次入座/离开，单一时间戳无法表达）
- `status`：冗余 — `round_player_snapshot.left_reason` 已记录所有流转状态（kicked/substituted/user_request/timeout）
- `updated_at`：现有模型无此字段，聚合统计通过 `IncrementSessionPlayerGrab`/`IncrementSessionPlayerSend` 原子更新，无需追踪更新时间

`SeatNo` 字段保留兼容期，后续 P2 删除（座位流转信息完全下沉到 snapshot 表）。

#### 7. 注册 AutoMigrate

**文件**: [scripts/init_rooms.go:72-93](backend/scripts/init_rooms.go#L72-L93)

在 `db.AutoMigrate(...)` 列表末尾追加 `&model.RoundPlayerSnapshot{}`。

**生产 migration**: 同步在 `migrations/` 目录新增 `YYYYMMDDHHMMSS_create_round_player_snapshot.sql`，仅包含建表 + 索引（**无需修改 `session_players` 表**）。

---

### P0-D：在 Round 创建时写入 Snapshot

**文件**: [game/application/packet_round_init.go:100-124](backend/game/application/packet_round_init.go#L100-L124)

在 `createRoundRecord` 方法 round 创建成功后，调用新方法 `writeRoundSnapshots`：

```go
func (p *PacketOrchestrator) createRoundRecord(ctx context.Context, roomID, sessionID int64, roundNo int) (*model.Round, error) {
    // ... 现有 round 创建逻辑 ...
    round, err := p.dbRepo.RoundDBRepo().CreateOrGetRound(ctx, ...)
    if err != nil { return nil, err }

    // 新增：写入 round_player_snapshot（失败仅告警，不阻断 round 创建）
    if err := p.writeRoundSnapshots(ctx, roomID, sessionID, round.RoundID, roundNo); err != nil {
        logger.Warn("write round snapshots failed",
            "room_id", roomID, "round_id", round.RoundID, "error", err)
    }
    return round, nil
}

// writeRoundSnapshots 从 Redis 读取当前玩家+旁观者状态，批量写入快照
func (p *PacketOrchestrator) writeRoundSnapshots(ctx context.Context, roomID, sessionID, roundID int64, roundNo int) error {
    // 1. 从 RoomStateData 获取当前玩家 + 旁观者
    stateData, err := p.repo.GetRoomStateData(ctx, roomID)  // 已有方法
    if err != nil { return err }

    // 2. 查询当前轮 sender_id（来自 RoomMeta）
    meta, err := p.repo.GetRoomMeta(ctx, roomID)
    if err != nil { return err }
    senderID := meta.CurrentSenderID  // 或从 round 记录获取

    // 3. 构造 snapshots
    now := time.Now()
    var snapshots []*model.RoundPlayerSnapshot
    for _, player := range stateData.Players {
        seatNo := player.SeatNo
        snapshots = append(snapshots, &model.RoundPlayerSnapshot{
            SessionID:   sessionID,
            RoundID:      roundID,
            RoundNo:      roundNo,
            UserID:       player.UserID,
            Role:         "player",
            SeatNo:       &seatNo,
            IsSender:     player.UserID == senderID,
            JoinedAt:     player.JoinedAt,
            ActiveStart:  now,
            Source:       "initial",
        })
    }
    for _, spectator := range stateData.Spectators {
        snapshots = append(snapshots, &model.RoundPlayerSnapshot{
            SessionID:   sessionID,
            RoundID:      roundID,
            RoundNo:      roundNo,
            UserID:       spectator.UserID,
            Role:         "spectator",
            SeatNo:       nil,
            JoinedAt:     spectator.JoinedAt,
            ActiveStart:  now,
            Source:       "initial",
        })
    }

    // 4. 批量写入
    return p.dbRepo.SnapshotDBRepo().BatchCreateOnRoundStart(ctx, snapshots)
}
```

> 注：`PacketOrchestrator` 结构体已有 `dbRepo` 和 `repo`（Redis repo）字段。需确认 `stateData.Players`/`stateData.Spectators` 字段名与现有 `GetRoomStateData` 返回值一致 — 实施时通过读取 [room/state.go](backend/game/domain/room/) 确认。

**关键设计决策**：snapshot 写入失败仅 warn，不阻断 round 创建。理由：snapshot 是辅助事实表，不应影响游戏主流程。

---

### P0-E：RoomEventConsumer 按事件类型分发

**文件**: [game/infrastructure/messaging/room_event_consumer.go:92-111](backend/game/infrastructure/messaging/room_event_consumer.go#L92-L111)

替换 `syncRoomCounts` 一刀切逻辑：

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
```

#### handleSubstitute 实现

```go
func (c *RoomEventConsumer) handleSubstitute(ctx context.Context, event *events.RoomEvent) error {
    payload, err := event.ParseSubstitutePayload()
    if err != nil {
        return fmt.Errorf("parse substitute payload failed: %w", err)
    }

    // 关键：RoomEvent 没有 SessionID，通过 Redis room meta 查询
    meta, err := c.repo.GetRoomMeta(ctx, event.RoomID)
    if err != nil {
        return fmt.Errorf("get room meta failed: %w", err)
    }
    sessionID := meta.CurrentSessionID
    if sessionID == 0 {
        return fmt.Errorf("room %s has no active session", event.RoomID)
    }

    // 查询当前 round_id
    roundID, err := c.getCurrentRoundID(ctx, sessionID)
    if err != nil {
        return fmt.Errorf("get current round id failed: %w", err)
    }

    now := time.Now()
    substituteUserID, _ := strconv.ParseInt(event.UserID, 10, 64)
    replacedUserID := ... // 从 payload 取（注：SubstitutePayload 当前无 UserID 字段，需扩展）

    // 事务编排（遵循 CODING_STANDARD §15）
    return c.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
        // 1. 标记被替者的当前 snapshot 为已离开
        if err := tx.SnapshotRepo().MarkPlayerLeft(ctx, sessionID, roundID, replacedUserID,
            now, "substituted", substituteUserID); err != nil {
            return fmt.Errorf("mark player left failed: %w", err)
        }

        // 2. 插入替补者的新 snapshot
        seatNo := payload.SeatNo
        snapshot := &model.RoundPlayerSnapshot{
            SessionID:   sessionID,
            RoundID:     roundID,
            UserID:      substituteUserID,
            Role:        "player",
            SeatNo:      &seatNo,
            JoinedAt:    now,
            ActiveStart: now,
            Source:      "substitute",
        }
        if err := tx.SnapshotRepo().AddPlayerMidRound(ctx, snapshot); err != nil {
            return fmt.Errorf("add substitute snapshot failed: %w", err)
        }

        // 3. 替补者首次入会话则插入 session_player（聚合表一人一行）
        // 注：session_players 表仅做聚合统计，不加 status/left_at 字段
        // 玩家流转状态由 round_player_snapshot 事实表记录
        if err := tx.SessionDBRepo().UpsertPlayer(ctx, &model.SessionPlayer{
            SessionID: sessionID,
            UserID:    substituteUserID,
            Nickname:  payload.Nickname,
            Avatar:    payload.Avatar,
            JoinedAt:  now,
        }); err != nil {
            return fmt.Errorf("upsert substitute session player failed: %w", err)
        }

        // 4. 同步 rooms 表计数（保留原逻辑）
        return c.syncRoomCounts(ctx, event)
    })
}
```

#### ReplacedUserID 获取：通过 Redis 反查

**不扩展 SubstitutePayload**，保持现有字段 `SeatNo/Nickname/Avatar` 不变。

`handleSubstitute` 中通过 Redis `RoomSeatsKey(roomID)` 反查 seat_no 对应的原 user_id：

```go
// 在 handleSubstitute 内：
// 通过 Redis 反查 seat 当前占用者（被替者）
// 注：AutoSubstitute Lua 执行后 seat 已更新为替补者，故需在事件发布前快照，
// 或通过 RoomSeatOwnerKey 追溯上一个 owner（若 Redis 结构未保留历史，需另寻方案）
replacedUserID, err := c.lookupReplacedUserID(ctx, event.RoomID, payload.SeatNo)
if err != nil {
    return fmt.Errorf("lookup replaced user id failed: %w", err)
}

// lookupReplacedUserID 实现思路：
// 方案 A（推荐）：在 room_app_service.go 发布 Substitute 事件前，先从 Redis HGET seatsKey 取原 owner
// 方案 B：订阅 events.RoomEventSeatCancel 事件（被替者被踢时已发布），通过 seat_no + user_id 关联
// 实施时确认 RoomSeatOwnerKey 是否保留 owner 历史；若无，则改在 publisher 侧把原 owner 写入事件 header metadata
```

#### handleSpectatorJoin / handleSpectatorLeave / handleSpectatorKick / handleSeatCancel

实现模式同 `handleSubstitute`（但更简单）：
- 查询 session_id + round_id
- 事务内仅 INSERT/UPDATE snapshot（无需操作 session_players，因为聚合表不加状态字段）
- 最后调用 `syncRoomCounts` 同步计数

简化版（每个 handler 约 25 行）：

```go
func (c *RoomEventConsumer) handleSeatCancel(ctx context.Context, event *events.RoomEvent) error {
    meta, _ := c.repo.GetRoomMeta(ctx, event.RoomID)
    sessionID := meta.CurrentSessionID
    roundID, _ := c.getCurrentRoundID(ctx, sessionID)
    userID, _ := strconv.ParseInt(event.UserID, 10, 64)
    now := time.Now()

    // 事务内仅操作 snapshot 表（session_players 聚合表不加状态，无需更新）
    if err := c.dbRepo.SnapshotDBRepo().MarkPlayerLeft(ctx, sessionID, roundID, userID,
        now, "user_request", 0); err != nil {
        return err
    }
    // 同步 rooms 表计数（移出事务，遵循 §15 短事务原则）
    return c.syncRoomCounts(ctx, event)
}
```

> **注**：对于 `handleSeatCancel` / `handleSpectatorKick` 这种仅"标记离开"的场景，事务边界很小（仅 snapshot 表 UPDATE），可以不用 `WithTransaction` 包裹，直接调用 `SnapshotDBRepo().MarkPlayerLeft` 即可。只有 `handleSubstitute` 涉及多表（snapshot + session_players Upsert）才需要事务编排。

#### `getCurrentRoundID` 辅助方法

```go
// getCurrentRoundID 从 session 的最新 round 查询 round_id
func (c *RoomEventConsumer) getCurrentRoundID(ctx context.Context, sessionID int64) (int64, error) {
    // 通过 HistoryDBRepo 或 RoundDBRepo 查询 session 的最新 round
    rounds, err := c.dbRepo.RoundDBRepo().ListRoundsBySession(ctx, sessionID)
    if err != nil || len(rounds) == 0 {
        return 0, fmt.Errorf("no rounds found for session %d", sessionID)
    }
    return rounds[len(rounds)-1].RoundID, nil
}
```

> 注：若 `RoundDBRepository` 无 `ListRoundsBySession`，需添加（参考 `HistoryDBRepository.GetSessionRounds` 的实现）。

---

### P1-A：历史详情查询改造（仅修复 6003 错误，不修改响应字段）

**文件**: [game/application/history_service.go:58-243](backend/game/application/history_service.go#L58-L243)

修改 `GetPlayerSessionDetail` 的 line 66-68：

```go
player, err := s.dbRepo.HistoryDBRepo().GetPlayerSession(ctx, userID, sessionID)
if err != nil {
    return nil, message.NewError(message.CodeHistoryQueryFailed)
}

// 关键改造：player == nil 时不再直接返回 6003
// 改为查 snapshot 表，如果玩家在该会话有任何 snapshot 记录，说明确实参与过
if player == nil {
    snapshots, _ := s.dbRepo.SnapshotDBRepo().ListByUser(ctx, sessionID, userID)
    if len(snapshots) == 0 {
        return nil, message.NewError(message.CodePlayerNotInSession)
    }
    // 玩家曾参与但 session_player 未记录（兼容历史数据），用 snapshot 构造临时 player
    player = &model.SessionPlayer{
        SessionID: sessionID,
        UserID:    userID,
        JoinedAt:  snapshots[0].JoinedAt,
        Status:    "active",
    }
}
```

> **保持响应字段不变**：`RoundDetail` / `PlayerSessionDetailResp` 结构体不新增字段，前端无需改动。
> 座位流转信息的关联展示（如前端需要）作为后续迭代，本次仅修复 6003 错误让 905 能正常查询。

---

### P2-A：清理 `session_players.seat_no` 字段

兼容期（建议 2 周线上稳定运行）后：

1. 确认无查询引用 `session_players.seat_no`（用 grep 验证）
2. 在 `model.SessionPlayer` 删除 `SeatNo` 字段
3. 在 migration 脚本中执行 `ALTER TABLE session_players DROP COLUMN seat_no;`
4. 历史详情前端展示改用 `round_player_snapshot.seat_no`

---

## 四、关键文件清单

### 新增文件（3 个）

| 文件路径 | 内容 |
|---------|------|
| `game/infrastructure/persistence/mysql/model/round_player_snapshot.go` | GORM 模型 |
| `game/infrastructure/persistence/mysql/snapshot_repository.go` | Repository 实现 |
| `migrations/YYYYMMDDHHMMSS_create_round_player_snapshot.sql` | 生产 migration（仅建表 + 索引，不改 session_players） |

### 修改文件（8 个）

| 文件路径 | 修改内容 |
|---------|---------|
| [common/i18n/messages_es.go](backend/common/i18n/messages_es.go) | 补 4 条 6001-6004 翻译 |
| [game/domain/events/room_event.go](backend/game/domain/events/room_event.go) | `NewSubstituteEvent` 加 traceID；`SubstitutePayload` **不扩展**（通过 Redis 反查 ReplacedUserID） |
| [game/domain/repository/db_repository.go](backend/game/domain/repository/db_repository.go) | 加 `SnapshotRepository` 接口；`DBRepository`/`Transaction` 加 `SnapshotDBRepo`/`SnapshotRepo` 访问器；`SessionDBRepository` 仅加 `UpsertPlayer` |
| [game/infrastructure/persistence/mysql/db_repository.go](backend/game/infrastructure/persistence/mysql/db_repository.go) | `DBRepositoryImpl`/`GormTransactionImpl` eager 初始化 snapshotRepo |
| [game/infrastructure/persistence/mysql/session_repository.go](backend/game/infrastructure/persistence/mysql/session_repository.go) | 仅实现 `UpsertPlayer` |
| [game/model/session.go](backend/game/model/session.go) | **完全不动**（`SessionPlayer` 模型保持现状，`TableName()` 已存在） |
| [game/application/room_app_service.go](backend/game/application/room_app_service.go) | `NewSubstituteEvent` 调用点加 traceID |
| [game/application/packet_round_init.go](backend/game/application/packet_round_init.go) | `createRoundRecord` 后调用 `writeRoundSnapshots` |
| [game/infrastructure/messaging/room_event_consumer.go](backend/game/infrastructure/messaging/room_event_consumer.go) | 替换 syncRoomCounts 一刀切逻辑；新增 5 个事件 handler |
| [game/application/history_service.go](backend/game/application/history_service.go) | `GetPlayerSessionDetail` 改用 snapshot 兜底（仅修 6003，不改响应字段） |
| [scripts/init_rooms.go](backend/scripts/init_rooms.go) | AutoMigrate 注册 `&model.RoundPlayerSnapshot{}` |

---

## 五、验证方案

### 5.1 单元测试

**新增**: `game/infrastructure/persistence/mysql/snapshot_repository_test.go`

覆盖：
- `BatchCreateOnRoundStart` 批量插入 5 玩家 + 2 旁观者
- `MarkPlayerLeft` 标记离开（含乐观锁测试：active_end IS NULL）
- `AddPlayerMidRound` 插入替补者
- `ListByRound` / `ListByUser` 查询

**新增**: `game/infrastructure/messaging/room_event_consumer_test.go`

覆盖 `handleSubstitute` 事务编排正确性（mock SnapshotRepo + SessionDBRepo）。

### 5.2 集成测试

模拟本次问题场景：
1. 创建会话，5 玩家入座
2. 触发 902 被 penalty kick → 905 替补
3. 验证 `round_player_snapshot` 表中 902 行 `left_reason='substituted'`, `replaced_by=905`
4. 验证 905 行 `source='substitute'`, `seat_no=4`
5. 调用 `get_player_session_detail` for 905 → 应返回 `code=0` 而非 `6003`

### 5.3 端到端验证

1. 启动 game-service + gateway
2. 创建房间，5 玩家入座
3. 模拟玩家发包超时触发 penalty kick
4. 验证替补流程后，替补玩家查询历史详情不报错
5. 查 MySQL `round_player_snapshot` 表确认数据正确
6. 查 `session_players` 表确认 `status='substituted'` 标记正确

### 5.4 数据一致性校验 SQL

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

### 5.5 编译验证

```bash
cd /Users/aaron.pan/Desktop/party/RedPacket-master/backend
go build ./...
go vet ./...
go test ./common/i18n/... ./game/infrastructure/persistence/mysql/... ./game/infrastructure/messaging/...
```

---

## 六、风险与对策

| 风险 | 对策 |
|------|------|
| snapshot 写入失败影响 round 创建 | 失败仅 warn 日志，不阻断主流程 |
| RoomEvent 无 SessionID | 通过 Redis RoomMeta 查询（已有模式） |
| 双写期间数据不一致 | snapshot 是新增表，不影响现有功能；失败时仅告警 |
| SubstitutePayload 缺 ReplacedUserID | 扩展 payload 字段，同步修改事件发布方 |
| 事务内调用 syncRoomCounts（Redis）违反 §15 短事务原则 | 将 syncRoomCounts 移出事务，事务后再调用 |
| 乐观锁 WHERE active_end IS NULL 并发场景 | 若同一玩家在同一轮被并发标记，第二次 UPDATE 影响 0 行，记 warn 即可 |

---

## 七、执行顺序

```
P0-A (i18n) ────────────────────────────────── 可独立发布
P0-B (TraceID) ─────────────────────────────── 可独立发布
P0-C (表+Repo+Model) ──┐
                       ├─ P0-D (Round 创建写入) ──┐
                       │                          ├─ P0-E (Consumer 分发) ──┐
                       │                          │                          ├─ P1-A (查询改造)
                       │                          │                          │
                       └──────────────────────────┴──────────────────────────┘
                       必须按序，因有依赖

P2-A (清理 seat_no) ─────────────────────────── 在兼容期结束后
```

---

## 八、遵循的 CODING_STANDARD.md 规则

| 规则 | 章节 |
|------|------|
| GORM struct tags 作为 schema 单一真相源 | §14 |
| 复合索引显式声明 priority | §14 |
| 事务边界在应用层 | §15 |
| 短事务原则：禁止 RPC/Kafka/耗时计算在事务内 | §15（syncRoomCounts 移出事务） |
| 乐观锁优先 | §15（`WHERE active_end IS NULL`） |
| Repository 聚合对象必须 eager 初始化 | §15.3 |
| TraceID 必须由消息发布者设置并校验 | §5.4 |
| 字符串拼接规范 | §16（SQL 用 ? 占位符，错误用 %w） |
| 代码注释中文，日志英文 | §17.1 / §5.4 |

---

## 九、待审批项（已确认）

| 决策项 | 已确认选择 |
|--------|----------|
| snapshot 写入失败处理 | **仅 warn 不阻断**（保证游戏主流程可用性优先） |
| SubstitutePayload 扩展 | **不扩展**，通过 Redis `RoomSeatsKey` 反查被替者 ID（实施时确认是否需要 publisher 侧快照原 owner） |
| RoundDetail 响应字段 | **保持不变**，不新增座位/角色字段；前端无需改动；本次仅修复 6003 错误 |
| session_players 表名 | **保留原表名 `session_players`**，不重命名为单数；通过 `TableName()` 显式指定避免 GORM 默认规则 |
| session_players 表加 left_at 字段 | **不加**。该字段在聚合表上无意义（玩家可能多次入座/离开，单一 left_at 无法表达）。所有流转时间戳下沉到 `round_player_snapshot.active_end` |
| session_players 表加 status 字段 | **不加**。status 是冗余的 — `round_player_snapshot.left_reason` 已记录所有流转状态（kicked/substituted/user_request/timeout）。聚合表保持纯统计角色 |
| session_players 表加 updated_at 字段 | **不加**。现有模型无此字段；聚合统计通过 `IncrementSessionPlayerGrab`/`IncrementSessionPlayerSend` 原子更新，无需追踪更新时间 |
| SessionPlayer 模型 | **完全保持现状**（[game/model/session.go:34-51](backend/game/model/session.go#L34-L51)），`TableName()` 方法已存在 |
| SessionDBRepository 接口扩展 | 仅加 `UpsertPlayer`（用于替补者首次入会话），**不加 `UpdateStatus`** |

## 十、实施前需进一步确认的技术细节

1. **ReplacedUserID 获取路径**：确认 Redis `RoomSeatOwnerKey` 是否保留 owner 历史。若 AutoSubstitute Lua 执行后旧 owner 信息丢失，需在 `room_app_service.go` 发布事件前先快照原 owner（方案 A），或在 Substitute 事件 payload 中临时附加原 owner（虽不扩展 payload 字段，可考虑通过事件 header metadata 传递）。
2. **RoomStateData 结构**：确认 `stateData.Players` / `stateData.Spectators` 字段名与 `SeatNo` / `UserID` / `JoinedAt` 子字段名（实施时读 [room/state_data.go](backend/game/domain/room/) 确认）。
3. **RoundDBRepository 是否有 ListRoundsBySession**：若无，复用 `HistoryDBRepository.GetSessionRounds` 或在 `RoundDBRepository` 加一个方法。
4. **PacketOrchestrator 是否已注入 `repo`（Redis repo）**：用于 `GetRoomStateData`。若未注入，需通过 [packet_round_init.go](backend/game/application/packet_round_init.go) 现有依赖确认。
