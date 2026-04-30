# Round 模型重构方案

## 一、当前问题分析

### 1.1 持久化时机错误

| 问题           | 说明                                                         |
| ------------ | ---------------------------------------------------------- |
| Round 数据未持久化 | `model.Round` 虽然有 GORM 标签，但实际代码中没有任何地方创建或保存 `Round` 实例到数据库 |
| 持久化时机错误      | 真正持久化的是 `RoundRecord`，且在结算时才创建，应该在红包生成时就持久化                |

### 1.2 表结构冗余

| 表             | 问题               |
| ------------- | ---------------- |
| `Round`       | 有表结构但未使用         |
| `RoundRecord` | 与 Round 功能重复，应删除 |

### 1.3 字段冗余

`Round` 结构体中以下字段不需要：

| 字段            | 原因                                          |
| ------------- | ------------------------------------------- |
| `TotalAmount` | 可通过 `Packet` 表聚合计算，或从 `CommissionRecord` 获取 |
| `PacketCount` | 可通过 `Packet` 表 count 获取                     |
| `Packets`     | 运行时数据，不需要持久化，通过 `packets` 表关联查询             |

### 1.4 RoundID 生成方式错误

当前代码中 RoundID 的生成方式：

```go
roundID := fmt.Sprintf("%s_%s_%d", req.RoomID, meta.CurrentSessionID, nextRound)
```

**问题：**

- RoundID 应该使用雪花算法生成的唯一字符串，而不是组合字符串
- 传给 `packetGenerator.Generate` 的 `RoundID` 参数使用的是 `int64(nextRound)`，这是错误的

### 1.5 佣金存储位置

佣金已在 `CommissionRecord` 表存储，`RoundRecord` 中的 `Commission` 字段冗余。

***

## 二、当前数据流

```
红包生成时:
  GameAppService → PacketCreated 事件 → Consumer → 创建 Packet 表

结算时:
  SettlementService → RoundSettle 事件 → Consumer → 创建 RoundRecord + RoundGrabRecord
                                              → 更新 SessionPlayer 统计
```

***

## 三、目标数据流

```
红包生成时:
  GameAppService → PacketCreated 事件 → Consumer → 创建 Round 表 + Packet 表

结算时:
  SettlementService → RoundSettle 事件 → Consumer → 更新 Round(ended_at, status)
                                              + 创建 RoundGrabRecord
                                              + 更新 SessionPlayer 统计
```

***

## 四、重构方案

### 4.1 修改 Round 结构体

**文件：** `game/model/round.go`

```
type Round struct {
    RoundID   int64      `json:"round_id" gorm:"primaryKey"`
    SessionID int64       `json:"session_id" gorm:"index;not null"`
    RoomID    int64       `json:"room_id" gorm:"index;not null"`
    RoundNo   int         `json:"round_no" gorm:"not null"`
    Status    RoundStatus `json:"status" gorm:"default:0"`
    SenderID  int64       `json:"sender_id" gorm:"not null"`
    StartedAt *time.Time  `json:"started_at"`
    EndedAt   *time.Time  `json:"ended_at"`
    CreatedAt time.Time   `json:"created_at" gorm:"autoCreateTime"`
}

func (Round) TableName() string { return "rounds" }
```

**变更说明：**

- `RoundID` 使用雪花算法生成，数据库存储为 `int64`，应用程序使用 `string` 类型
  - 数据库字段类型为 int64，避免 Redis/JSON 序列化时大整数精度丢失
  - 应用程序中使用 `string` 类型，便于与前端交互（JavaScript 大整数精度问题）
- 保留 `RoundNo` 字段，用于显示当前是第几局（1、2、3...）
- 移除 `TotalAmount`、`PacketCount`、`Packets` 字段
- 保留核心字段：`RoundID`、`SessionID`、`RoomID`、`RoundNo`、`Status`、`SenderID`、`StartedAt`、`EndedAt`

### 4.2 删除 RoundRecord 结构体

**文件：** `game/model/round.go`

删除 `RoundRecord` 结构体及其 `TableName` 方法。

### 4.3 修改 RoundID 生成方式

**文件：** `game/application/game_app_service.go`

```go
roundID := idgen.GenerateString()
amount := meta.RoomFee

commission := s.commissionCfg.Calculate(amount)
actualAmount := amount - commission

genResult, err := s.packetGenerator.Generate(ctx, &algorithm.GenerateRequest{
    TotalAmount: actualAmount,
    PacketCount: s.config.MaxPlayersPerRoom,
    RoomID:      parseInt64(req.RoomID),
    RoundID:     parseInt64(roundID),
    RoomType:    int(amount / 100),
})
```

**变更说明：**

- 使用 `idgen.GenerateString()` 生成雪花算法 ID
- 需要修改 `algorithm.GenerateRequest` 中 `RoundID` 的类型或转换逻辑

### 4.4 修改事件数据结构

**文件：** `game/domain/events.go`

```go
type GameEvent struct {
    EventType GameEventType `json:"event_type"`
    RoomID    int64         `json:"room_id"`
    SessionID string        `json:"session_id"`
    RoundID   string        `json:"round_id,omitempty"`
    Timestamp int64         `json:"timestamp"`
    Data      interface{}   `json:"data"`
    TraceID   string        `json:"trace_id"`
}

type PacketCreatedData struct {
    RoomID      int64         `json:"room_id"`
    SessionID   string        `json:"session_id"`
    RoundID     string        `json:"round_id"`
    RoundNo     int           `json:"round_no"`
    SenderID    int64         `json:"sender_id"`
    SenderType  string        `json:"sender_type"`
    TotalAmount int64         `json:"total_amount"`
    Commission  int64         `json:"commission"`
    Packets     []*PacketData `json:"packets"`
}

type PacketData struct {
    PacketID int64  `json:"packet_id"`
    RoomID   int64  `json:"room_id"`
    RoundID  string `json:"round_id"`
    Amount   int64  `json:"amount"`
    Position int    `json:"position"`
}
```

**变更说明：**

- `GameEvent.RoundID` 类型从 `int64` 改为 `string`
- `PacketCreatedData.RoundID` 类型从 `int64` 改为 `string`
- `PacketData.RoundID` 类型从 `int64` 改为 `string`

### 4.5 修改 handlePacketCreated

**文件：** `game/infrastructure/messaging/game_event_consumer.go`

```go
func (c *GameEventConsumer) handlePacketCreated(ctx context.Context, event *domain.GameEvent) error {
    dataBytes, err := json.Marshal(event.Data)
    if err != nil {
        return fmt.Errorf("marshal packet created data failed: %w", err)
    }

    var data domain.PacketCreatedData
    if err := json.Unmarshal(dataBytes, &data); err != nil {
        return fmt.Errorf("unmarshal packet created data failed: %w", err)
    }

    sessionIDInt64, err := strconv.ParseInt(event.SessionID, 10, 64)
    if err != nil {
        return fmt.Errorf("invalid session_id: %w", err)
    }

    now := time.Now()
    
    return c.db.Transaction(func(tx *gorm.DB) error {
        round := &model.Round{
            RoundID:   event.RoundID,
            SessionID: sessionIDInt64,
            RoomID:    event.RoomID,
            RoundNo:   data.RoundNo,
            Status:    model.RoundStatusSending,
            SenderID:  data.SenderID,
            StartedAt: &now,
        }
        if err := tx.Create(round).Error; err != nil {
            return fmt.Errorf("create round failed: %w", err)
        }

        for _, p := range data.Packets {
            packet := &model.Packet{
                PacketID: p.PacketID,
                RoundID:  parseInt64(p.RoundID),
                RoomID:   p.RoomID,
                Amount:   p.Amount,
                Position: p.Position,
            }
            if err := tx.Create(packet).Error; err != nil {
                return fmt.Errorf("create packet failed: %w", err)
            }
        }

        logger.Info("round and packets created",
            "room_id", event.RoomID,
            "round_id", event.RoundID,
            "session_id", sessionIDInt64,
            "packet_count", len(data.Packets))
        return nil
    })
}
```

### 4.6 修改 handleRoundSettle

**文件：** `game/infrastructure/messaging/game_event_consumer.go`

```go
func (c *GameEventConsumer) handleRoundSettle(ctx context.Context, event *domain.GameEvent) error {
    dataBytes, err := json.Marshal(event.Data)
    if err != nil {
        return fmt.Errorf("marshal round settle data failed: %w", err)
    }

    var data domain.RoundSettleData
    if err := json.Unmarshal(dataBytes, &data); err != nil {
        return fmt.Errorf("unmarshal round settle data failed: %w", err)
    }

    sessionIDInt64, err := strconv.ParseInt(event.SessionID, 10, 64)
    if err != nil {
        return fmt.Errorf("invalid session_id: %w", err)
    }

    now := time.Now()

    return c.db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Model(&model.Round{}).
            Where("round_id = ?", event.RoundID).
            Updates(map[string]interface{}{
                "status":   model.RoundStatusEnded,
                "ended_at": &now,
            }).Error; err != nil {
            return fmt.Errorf("update round failed: %w", err)
        }

        for _, r := range data.Results {
            isMin := 0
            if r.UserID == data.MinPlayerID {
                isMin = 1
            }
            isAutoAssigned := 0
            if r.IsAutoAssigned {
                isAutoAssigned = 1
            }
            grabRecord := &model.RoundGrabRecord{
                RoundID:        parseInt64(event.RoundID),
                PacketID:       r.PacketID,
                SessionID:      sessionIDInt64,
                UserID:         r.UserID,
                Amount:         r.Amount,
                IsMin:          isMin,
                IsAutoAssigned: isAutoAssigned,
                GrabbedAt:      now,
            }
            if err := tx.Create(grabRecord).Error; err != nil {
                return fmt.Errorf("create grab record failed: %w", err)
            }
        }

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
                Where("session_id = ? AND user_id = ?", sessionIDInt64, r.UserID).
                Updates(updates).Error; err != nil {
                return fmt.Errorf("update session player failed: %w", err)
            }
        }

        if err := tx.Model(&model.GameSession{}).
            Where("session_id = ?", sessionIDInt64).
            Update("current_round", data.RoundNo).Error; err != nil {
            return fmt.Errorf("update session current_round failed: %w", err)
        }

        logger.Info("round settled",
            "session_id", sessionIDInt64,
            "round_id", event.RoundID,
            "round_no", data.RoundNo)
        return nil
    })
}
```

### 4.7 修改 AutoMigrate

**文件：** `scripts/init_rooms.go`

```go
if err := db.AutoMigrate(
    &model.RoomConfig{},
    &model.Room{},
    &model.GameSession{},
    &model.SessionPlayer{},
    &model.Round{},
    &model.RoundGrabRecord{},
    &model.Packet{},
    &model.SpecialReward{},
    &model.User{},
    &settlementModel.SettlementRecord{},
    &settlementModel.PlayerSettlementRecord{},
    &settlementModel.CommissionRecord{},
); err != nil {
    log.Fatalf("migrate tables failed: %v", err)
}
```

**变更说明：** 移除 `&model.RoundRecord{}`

***

## 五、数据存储职责划分

| 表                        | 职责                  | 创建时机  | 更新时机         |
| ------------------------ | ------------------- | ----- | ------------ |
| `Round`                  | 局次基础信息（ID、局号、状态、时间） | 红包生成时 | 结算时更新状态和结束时间 |
| `Packet`                 | 红包明细                | 红包生成时 | -            |
| `RoundGrabRecord`        | 抢红包记录               | 结算时   | -            |
| `CommissionRecord`       | 佣金计算记录              | 扣款时   | -            |
| `SettlementRecord`       | 结算汇总记录              | 结算时   | -            |
| `PlayerSettlementRecord` | 玩家结算明细              | 结算时   | -            |

***

## 六、影响范围

| 文件                                                     | 变更内容                                                               |
| ------------------------------------------------------ | ------------------------------------------------------------------ |
| `game/model/round.go`                                  | 修改 Round 结构（RoundID 数据库 int64，应用 string，保留 RoundNo），删除 RoundRecord |
| `game/domain/events.go`                                | GameEvent、PacketCreatedData、PacketData 的 RoundID 类型改为 string       |
| `game/application/game_app_service.go`                 | RoundID 生成方式改为雪花算法                                                 |
| `game/infrastructure/messaging/game_event_consumer.go` | 修改 handlePacketCreated、handleRoundSettle                           |
| `scripts/init_rooms.go`                                | AutoMigrate 移除 RoundRecord                                         |

***