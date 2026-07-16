# Round 预创建与罚款关联重构方案

> **文档版本**: v1.0
> **创建日期**: 2026-07-16
> **状态**: 方案设计阶段（未实施）
> **范围**: game / settlement 模块（不含 stats 模块）
> **目标**: 解决罚款/罚款分发与 Round 脱钩问题，提供完整的数据可追溯性

---

## 一、背景与问题概述

### 1.1 问题现象

当前系统中，罚款（Penalty）与罚款分发（Penalty Distribution）在以下场景下产生：

1. **OnSendTimeout**：玩家在 inter-round 窗口期内未发包触发超时
2. **OnReplaceTimeout**：游戏 Interrupted 状态下玩家离开无替补触发超时

这些罚款账目（`bill_record` 中的 `bill_type=8` 罚款收入、`bill_type=10` 罚款分发）的 `round_id` 字段大量为 `0`，**无法关联到具体 round**，导致：

- 后期数据分析无法按 round 维度聚合罚款
- 无法对账：罚款扣款总额 vs 分发总额 + 平台收入
- 无法追溯某个 round 产生的罚款明细
- 无法分析用户的罚款行为模式

### 1.2 根因分析

#### 根因 1：罚款发生在 inter-round 过渡期

**时序**（[packet.lua.go:404](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/packet.lua.go#L404) 的反向操作）：

```
上一轮结算 → Lua HDEL current_round_id → inter-round 窗口期
                                          ↓
                                  玩家应在窗口期内发包
                                          ↓
                              SendPacket → createRoundRecord 创建下一轮 round
                                          ↓
                                  玩家未发包超时 → OnSendTimeout 触发罚款
                                          ↓
                              此时下一轮 round DB 记录尚未创建
                                          ↓
                              CurrentRoundID = "" → roundID = 0
```

罚款的**根因是下一轮（roundNo=N+1）未发包**，应该关联到下一轮。但罚款触发时下一轮 round **尚未创建**，`meta.CurrentRoundID` 已被上一轮结算清除。

#### 根因 2：PenaltyRecord 仅 Redis 持久化

[penalty.lua.go:25-32](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/penalty.lua.go#L25-L32) 把罚款记录 `RPUSH` 到 `penalty:record:{roomID}:{userID}`，TTL 过期即丢失。**事后无法审计、无法按 round 维度分析罚款**。

#### 根因 3：PenaltyDistribution 完全未持久化

[penalty.go:81-88](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/round/penalty.go#L81-L88) 的 `PenaltyDistribution` 仅作为函数返回值在内存传递，**未持久化到任何表**。

#### 根因 4：TraceID 生成器存在幂等性 bug

[trace_id_generator.go:62-64](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go#L62-L64)：

```go
func (g *TraceIDGenerator) GeneratePenaltyDistTraceID(roomID, sessionID int64) string {
    return fmt.Sprintf("PENALTY_DIST_%d_%d", roomID, sessionID)  // ❌ 不含 roundNo/序号
}
```

同一 session 内多次「替补超时」会生成相同 traceID，而 [penalty_settlement_service.go:254-257](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/penalty_settlement_service.go#L254-L257) 的幂等检查：

```go
existingBill, err := billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, domain.BillTypePenaltyDistribute, dto.PlatformAccountID)
if err == nil && existingBill != nil && existingBill.Status == domain.BillStatusSuccess {
    return nil  // ❌ 第二次罚款分发直接被吞掉！
}
```

**潜在资金流失**：第二次替补超时的罚款分红账目不会创建。

#### 根因 5：OnSendTimeout 语义错乱

[game_lifecycle_timeout.go:101-106](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_timeout.go#L101-L106)：

```go
var roundID int64
if meta.CurrentRoundID != "" {
    roundID = converter.ParseID(meta.CurrentRoundID)
}
result, err := s.penaltyService.ApplyPenalty(ctx, roomID, userID, round.PenaltyTypeSendTimeout, 
    meta.RoomFee, sessionID, int(meta.CurrentRound), roundID)
```

当 `CurrentRoundID == ""`（发红包超时场景下的常态）时：
- `roundID = 0` 传给 `PenaltyDeductRequest.RoundID`，最终 `BillRecord.RoundID = 0` ❌
- 但 `roundNo = meta.CurrentRound`（上一轮已结束的编号）传给 `GeneratePenaltyDeductTraceID`
- **同一笔罚款：roundID=0（无关联），roundNo=N（指向上轮）——语义完全错乱**

---

## 二、现状代码全景

### 2.1 数据模型分布（分散在 5 个包）

| 位置 | 类型 | 职责 |
|---|---|---|
| [game/domain/round/round_info.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/round/round_info.go) | `RoundInfo` | Redis 状态投影 |
| [game/domain/round/penalty.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/round/penalty.go) | `PenaltyRecord`/`PenaltyDistribution`/`PenaltyPolicy` | 罚款领域模型（**仅 Redis，无 DB 持久化**） |
| [game/domain/round/grab.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/round/grab.go) | `GrabResult`/`PlayerResult`/`SpecialReward` | 抢包模型 |
| [game/model/round.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/round.go) | `Round`/`RoundGrabRecord` | DB 持久化 |
| [settlement/domain/round_settlement.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/domain/round_settlement.go) | `RoundSettlement` | 结算聚合根 |
| [settlement/model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go) | `model.RoundSettlement`/`model.BillRecord` | 结算持久化 |

### 2.2 双状态机并存且无显式映射

- `model.RoundStatus`：Pending(0) → Sending(1) → Grabbing(2) → Ended(3) / Failed(4) — 由 [game_event_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_event_handler.go) 驱动
- `domain.RoundStatus`：Deducting(0) → Deducted(1) → Credited(6) / Failed(5) — 由 [round_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/round_settle_service.go) 驱动
- 两个状态机由不同的模块、不同的事务、不同的 Kafka 消息驱动，**无跨表一致性保证**，仅靠业务流程隐式对齐

### 2.3 当前 round 状态语义

[model/round.go:8-13](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/round.go#L8-L13)：

```go
const (
    RoundStatusPending = 0  // 已创建未发包
    RoundStatusSending = 1  // 已发包抢包中
    RoundStatusGrabbing = 2 // 抢包中（当前未使用）
    RoundStatusEnded = 3    // 正常结束
    RoundStatusFailed = 4   // 失败
)
```

---

## 三、方案选型

### 3.1 针对 roundID=0 的方案对比

| 方案 | 侵入性 | 业务语义 | 分析能力 | 对账影响 | 推荐度 |
|---|---|---|---|---|---|
| A: Meta 扩展 LastRoundID | 中（改 Lua） | 一般（关联上一轮，语义错） | 好 | 无 | ⭐⭐ |
| B: 占位 Round | 高（新状态） | 一般 | 好 | 无 | ⭐⭐ |
| C: Session 级（不关联 round） | 低 | 一般 | 差 | 无 | ⭐ |
| D: roundID=0+标记字段 | 低 | 清晰 | 中 | 无 | ⭐⭐ |
| E: 延迟绑定+回填 | 中 | 延迟正确 | 中 | 无 | ⭐⭐⭐ |
| **F: 预创建 Pending Round + 复用** | **低** | **正确** | **强** | **无** | **⭐⭐⭐⭐⭐** |

### 3.2 选型结论：方案 F

**核心思路**：罚款时提前创建下一轮 Pending round，后续 `ForceSendPacketForPlayer` / `SendPacket` 通过查询复用同一个 round。

**选择理由**：
1. 业务语义最正确：罚款关联到下一轮 roundID（根因 = 下一轮未发包）
2. 不修改 Lua 脚本，侵入可控
3. `createRoundRecord` 改动小（加一个 GetRoundBySessionAndRoundNo 查询）
4. Pending 状态已存在，语义契合"已创建但未发包"
5. 不需要定时器、不需要新状态、不需要回填

---

## 四、详细设计：方案 F 预创建 Pending Round

### 4.1 核心设计

#### 4.1.1 状态语义明确

Pending 状态语义为"已创建但未发包"，完全契合预创建场景：

| 状态 | 语义 | 场景 |
|---|---|---|
| Pending(0) | 已创建未发包 | 预创建（罚款触发）/ 首轮初始 |
| Sending(1) | 已发包抢包中 | 正常发包 |
| Grabbing(2) | 抢包中 | （当前未使用） |
| Ended(3) | 正常结束 | 结算完成 |
| Failed(4) | 失败 | 扣款/发包失败 |

#### 4.1.2 各场景下的 round 状态流转

**正常流程**：
```
OnSendTimeout 预创建 Pending 
    → ForceSendPacketForPlayer 复用 
    → UpdateRoundStatus(Sending) 
    → 结算 Ended
```

**踢人+替补流程**：
```
OnSendTimeout 预创建 Pending 
    → handleKickAndReplace 
    → 替补玩家 SendPacket 复用 
    → Sending → Ended
```

**踢人+无替补+游戏中断**：
```
OnSendTimeout 预创建 Pending 
    → handleKickAndReplace 
    → 无替补 
    → EndGameWithOptions
    → round 保持 Pending（✅ 语义清晰：预创建但未发包，游戏已结束）
```

**OnReplaceTimeout 场景**（游戏已 Interrupted，无下一轮发包）：
```
OnReplaceTimeout 预创建 Pending 
    → DistributePenalty 
    → EndGameWithOptions
    → round 保持 Pending（✅ 同上）
```

### 4.2 数据模型变更

#### 4.2.1 新增表：penalty_records

```go
// PenaltyRecord 罚款记录持久化模型
type PenaltyRecord struct {
    ID            int64     `gorm:"primaryKey;autoIncrement"`
    RoomID        int64     `gorm:"index;not null"`
    SessionID     int64     `gorm:"index;not null"`
    RoundID       int64     `gorm:"index;not null"`           // ✅ 关联预创建的下一轮 round
    RoundNo       int       `gorm:"not null"`                 // ✅ 下一轮编号
    UserID        int64     `gorm:"index;not null"`           // 被罚用户
    PenaltyType   string    `gorm:"size:32;not null"`         // send_timeout/leave_during_game/disconnect_timeout
    Amount        int64     `gorm:"not null"`
    Count         int       `gorm:"not null"`                 // 累计罚款次数
    KickRequired  bool      `gorm:"not null"`
    DeductStatus  int       `gorm:"default:0;index"`          // 0=Processing,1=Success,2=Failed
    BillID        int64     `gorm:"index"`                    // 关联 bill_record.id
    DeductError   string    `gorm:"size:512"`
    CreatedAt     time.Time `gorm:"autoCreateTime;index"`
    UpdatedAt     time.Time `gorm:"autoUpdateTime"`
}
```

**索引**：
- `idx_penalty_session_round`: (session_id, round_id)
- `idx_penalty_user`: (user_id, created_at)
- `idx_penalty_room`: (room_id, created_at)

#### 4.2.2 新增表：penalty_distributions

```go
// PenaltyDistribution 罚款分发记录持久化模型
type PenaltyDistribution struct {
    ID             int64     `gorm:"primaryKey;autoIncrement"`
    RoomID         int64     `gorm:"index;not null"`
    SessionID      int64     `gorm:"index;not null"`
    RoundID        int64     `gorm:"index;not null"`          // ✅ 关联预创建的下一轮 round
    RoundNo        int       `gorm:"not null"`                // ✅ 下一轮编号
    TriggerType    string    `gorm:"size:32;not null"`        // replacement_timeout/leave_during_game/...
    TotalAmount    int64     `gorm:"not null"`
    ShareAmount    int64     `gorm:"not null"`
    RecipientCount int       `gorm:"not null"`
    ExcludeUsers   string    `gorm:"type:text"`               // JSON 数组
    PlatformBillID int64     `gorm:"index"`                   // 关联平台支出的 bill_record.id
    CreatedAt      time.Time `gorm:"autoCreateTime;index"`
}
```

#### 4.2.3 新增表：penalty_distribution_recipients

```go
// PenaltyDistributionRecipient 罚款分发接收方明细
type PenaltyDistributionRecipient struct {
    ID             int64  `gorm:"primaryKey;autoIncrement"`
    DistributionID int64  `gorm:"index;not null"`             // 关联 penalty_distributions.id
    UserID         int64  `gorm:"index;not null"`
    Amount         int64  `gorm:"not null"`
    BillID         int64  `gorm:"index"`                      // 关联 bill_record.id
}
```

#### 4.2.4 BillRecord 增加关联字段

```go
type BillRecord struct {
    // ... 现有字段 ...
    
    // ✅ 新增关联字段
    PenaltyType            string `gorm:"size:32;index"`      // 仅 BillType=8/10 时填充
    PenaltyRecordID        int64  `gorm:"index"`              // 关联 penalty_records.id
    SpecialRewardID        int64  `gorm:"index"`              // 关联 special_rewards.id（BillType=11 时）
    PenaltyDistributionID  int64  `gorm:"index"`              // 关联 penalty_distributions.id（BillType=10 时）
}
```

#### 4.2.5 PenaltyDistributeRequest DTO 增强

[dto/request.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/request.go) 的 `PenaltyDistributeRequest` 增加：

```go
type PenaltyDistributeRequest struct {
    RoomID       int64
    SessionID    int64
    RoundID      int64
    RoundNo      int      // ✅ 新增
    Amount       int64
    Recipients   []int64
    Reason       string
    TriggerPhase string   // ✅ 新增：inter_round / in_round
}
```

### 4.3 代码变更点

#### 4.3.1 RoundDBRepository 新增方法

[game/domain/repository/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/repository/db_repository.go) 的 `RoundDBRepository` interface 增加：

```go
// GetRoundBySessionAndRoundNo 按 session+roundNo 查询 round
// 利用现有 idx_session_roundno 复合索引，用于 inter-round 场景补全 roundID
GetRoundBySessionAndRoundNo(ctx context.Context, sessionID int64, roundNo int) (*model.Round, error)
```

[round_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/round_repository.go) 实现：

```go
func (r *gormRoundRepository) GetRoundBySessionAndRoundNo(ctx context.Context, sessionID int64, roundNo int) (*model.Round, error) {
    var round model.Round
    err := r.db.WithContext(ctx).
        Where("session_id = ? AND round_no = ?", sessionID, roundNo).
        First(&round).Error
    if err != nil {
        return nil, err
    }
    return &round, nil
}
```

#### 4.3.2 OnSendTimeout 修改

[game_lifecycle_timeout.go:101-106](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_timeout.go#L101-L106)：

```go
// 旧：
// var roundID int64
// if meta.CurrentRoundID != "" {
//     roundID = converter.ParseID(meta.CurrentRoundID)
// }
// result, err := s.penaltyService.ApplyPenalty(ctx, roomID, userID, round.PenaltyTypeSendTimeout,
//     meta.RoomFee, sessionID, int(meta.CurrentRound), roundID)

// 新：
nextRoundNo := int(meta.CurrentRound) + 1
sessionID := converter.ParseID(meta.CurrentSessionID)

// 预创建下一轮 Pending round（罚款根因 = 下一轮未发包）
roundID, err := s.ensureNextRound(ctx, roomIDInt, sessionID, nextRoundNo)
if err != nil {
    logger.Error("ensure next round failed", "error", err)
    // 降级：roundID=0，不阻塞罚款流程
    roundID = 0
}

result, err := s.penaltyService.ApplyPenalty(ctx, roomID, userID, round.PenaltyTypeSendTimeout,
    meta.RoomFee, sessionID, nextRoundNo, roundID)
```

#### 4.3.3 OnReplaceTimeout 修改

[game_lifecycle_timeout.go:184-196](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_timeout.go#L184-L196)：

```go
// 旧：
// roundID := int64(0)
// if meta.CurrentRoundID != "" {
//     roundID = converter.ParseID(meta.CurrentRoundID)
// }

// 新：
nextRoundNo := int(meta.CurrentRound) + 1
sessionID := converter.ParseID(meta.CurrentSessionID)

roundID, err := s.ensureNextRound(ctx, roomIDInt, sessionID, nextRoundNo)
if err != nil {
    logger.Error("ensure next round failed", "error", err)
    roundID = 0
}

distReq := &settlementDto.PenaltyDistributeRequest{
    RoomID:       roomIDInt,
    SessionID:    sessionID,
    RoundID:      roundID,
    RoundNo:      nextRoundNo,        // ✅ 下一轮编号
    Amount:       meta.RoomFee,
    Recipients:   recipientIDs,
    Reason:       "replacement_timeout",
    TriggerPhase: "inter_round",      // ✅ 新增标记
}
```

#### 4.3.4 新增 ensureNextRound 辅助方法

在 [game_lifecycle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_service.go) 增加：

```go
// ensureNextRound 确保下一轮 round 存在，用于 inter-round 罚款场景。
// 罚款根因是下一轮未发包，故应关联到下一轮 roundID。
// 如果下一轮 round 已存在（SendPacket 已创建），则复用；
// 如果不存在，则预创建 Pending round，后续 SendPacket 时复用。
// 查询/创建失败时返回 error，调用方降级为 roundID=0，不阻塞罚款流程。
func (s *GameLifecycleService) ensureNextRound(ctx context.Context, roomID, sessionID int64, roundNo int) (int64, error) {
    // 先查询是否已有 round
    existing, err := s.dbRepo.RoundDBRepo().GetRoundBySessionAndRoundNo(ctx, sessionID, roundNo)
    if err == nil && existing != nil {
        return existing.RoundID, nil
    }
    if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
        return 0, fmt.Errorf("query round failed: %w", err)
    }
    
    // 预创建 Pending round
    roundID, err := s.idGen.GenerateInt64()
    if err != nil {
        return 0, fmt.Errorf("generate round id: %w", err)
    }
    round := &model.Round{
        RoundID:   roundID,
        SessionID: sessionID,
        RoomID:    roomID,
        RoundNo:   roundNo,
        Status:    model.RoundStatusPending,
    }
    if err := s.dbRepo.RoundDBRepo().CreateRound(ctx, round); err != nil {
        return 0, fmt.Errorf("create round failed: %w", err)
    }
    return roundID, nil
}
```

#### 4.3.5 createRoundRecord 修改（复用预创建 round）

[packet_round_init.go:98-119](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/packet_round_init.go#L98-L119)：

```go
func (p *PacketOrchestrator) createRoundRecord(ctx context.Context, roomID, sessionID int64, roundNo int) (*model.Round, error) {
    // ✅ 先查询是否已有 Pending round（罚款时预创建的）
    existing, err := p.dbRepo.RoundDBRepo().GetRoundBySessionAndRoundNo(ctx, sessionID, roundNo)
    if err == nil && existing != nil {
        // 复用预创建的 round，保留原 roundID 和 created_at
        return existing, nil
    }
    // 原逻辑：创建新 round
    roundID, err := p.idGen.GenerateInt64()
    if err != nil {
        return nil, fmt.Errorf("generate round id: %w", err)
    }
    round := &model.Round{
        RoundID:   roundID,
        SessionID: sessionID,
        RoomID:    roomID,
        RoundNo:   roundNo,
        Status:    model.RoundStatusPending,
    }
    if err := p.dbRepo.RoundDBRepo().CreateRound(ctx, round); err != nil {
        return nil, err
    }
    return round, nil
}
```

#### 4.3.6 ApplyPenalty 持久化 PenaltyRecord

[penalty_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/penalty_service.go) 的 `ApplyPenalty` 方法在 Lua 成功后同步创建 `PenaltyRecord`：

```go
// Lua 成功后，同步持久化到 DB（与 DeductPenaltyToPlatform 调用同事务）
penaltyRecord := &model.PenaltyRecord{
    RoomID:       roomID,
    SessionID:    sessionID,
    RoundID:      roundID,
    RoundNo:      roundNo,
    UserID:       userID,
    PenaltyType:  penaltyType,
    Amount:       result.Amount,
    Count:        result.Count,
    KickRequired: result.KickRequired,
    DeductStatus: model.PenaltyDeductProcessing,
}
if err := s.dbRepo.PenaltyRecordRepo().Create(ctx, penaltyRecord); err != nil {
    logger.Error("persist penalty record failed", "error", err)
    // 不阻塞主流程，记录已写入 Redis，后续对账补偿
}
```

#### 4.3.7 DistributePenalty 持久化 PenaltyDistribution

[penalty_settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/penalty_settlement_service.go) 的 `DistributePenalty` 方法在创建 bill 后同步持久化：

```go
// 创建分发记录
distribution := &model.PenaltyDistribution{
    RoomID:         req.RoomID,
    SessionID:      req.SessionID,
    RoundID:        req.RoundID,
    RoundNo:        req.RoundNo,
    TriggerType:    req.Reason,
    TotalAmount:    req.Amount,
    ShareAmount:    shareAmount,
    RecipientCount: len(req.Recipients),
    ExcludeUsers:   toJSON(req.ExcludeUsers),
    PlatformBillID: platformBill.ID,
}
if err := penaltyDistRepo.Create(ctx, distribution); err != nil {
    logger.Error("persist penalty distribution failed", "error", err)
}

// 创建接收方明细
for _, recipient := range recipients {
    recipientRecord := &model.PenaltyDistributionRecipient{
        DistributionID: distribution.ID,
        UserID:         recipient.UserID,
        Amount:         recipient.Amount,
        BillID:         recipient.BillID,
    }
    penaltyDistRecipientRepo.Create(ctx, recipientRecord)
}
```

#### 4.3.8 修复 TraceIDGenerator 幂等性 bug

[trace_id_generator.go:62-64](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go#L62-L64)：

```go
// 旧：
// func (g *TraceIDGenerator) GeneratePenaltyDistTraceID(roomID, sessionID int64) string {
//     return fmt.Sprintf("PENALTY_DIST_%d_%d", roomID, sessionID)
// }

// 新：增加 roundNo + seq，保证同 session 多次分发不冲突
func (g *TraceIDGenerator) GeneratePenaltyDistTraceID(roomID, sessionID int64, roundNo int, seq int64) string {
    return fmt.Sprintf("PENALTY_DIST_%d_%d_%d_%d", roomID, sessionID, roundNo, seq)
}
```

**seq 生成方式**：Redis INCR `cashparty:penalty:dist:seq:{roomID}:{sessionID}:{roundNo}`，需在 [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) 注册常量。

**兼容性**：旧 bill 的 `PENALTY_DIST_{roomID}_{sessionID}` 格式查询路径需保留，新格式通过版本号区分。

### 4.4 并发安全分析

#### 4.4.1 锁覆盖

- `OnSendTimeout` 持有 `SendPacketLockKey(roomID, userID)` 锁（[game_lifecycle_timeout.go:62](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_timeout.go#L62)）
- `SendPacket` 也持有同一把锁（[packet_orchestrator.go:163](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/packet_orchestrator.go#L163)）
- `ForceSendPacketForPlayer` 在 `OnSendTimeout` 锁内调用，所以也在锁内
- **不会并发创建 round**

#### 4.4.2 边界场景

| 场景 | 锁持有方 | 是否安全 |
|---|---|---|
| OnSendTimeout 预创建 round | SendPacketLockKey | ✅ |
| ForceSendPacketForPlayer 复用 round | SendPacketLockKey（同上） | ✅ |
| SendPacket 复用 round | SendPacketLockKey | ✅ |
| OnReplaceTimeout 预创建 round | ReplaceTimeoutLockKey | ⚠️ 需验证是否与 SendPacket 锁互斥 |

**OnReplaceTimeout 场景补充分析**：
- OnReplaceTimeout 触发时游戏已 Interrupted，不会有 SendPacket 调用
- 但需确认 `ReplaceTimeoutLockKey` 是否覆盖所有可能的并发路径

#### 4.4.3 数据库唯一约束兜底

为防止极端并发场景，`rounds` 表增加唯一约束：
```go
type Round struct {
    // ...
    SessionID int64 `gorm:"uniqueIndex:idx_session_roundno;not null"`
    RoundNo   int   `gorm:"uniqueIndex:idx_session_roundno;not null"`
}
```
创建时遇到 duplicate key error 则转为查询复用。

### 4.5 可选的分析查询示例

> **说明**：以下 SQL 查询示例不在本次重构代码范围内（stats 模块另行规划），仅用于说明本次数据模型变更后具备的分析能力，供后续 stats 模块重构或临时数据分析参考。

#### 4.5.1 按 round 维度的罚款钻取

```sql
-- 某会话每个 round 的罚款汇总
SELECT 
    r.round_id, r.round_no, r.status,
    COALESCE(SUM(pr.amount), 0) as penalty_deduct_total,
    COUNT(DISTINCT pr.id) as penalty_count,
    COALESCE(SUM(pd.total_amount), 0) as penalty_distribute_total
FROM rounds r
LEFT JOIN penalty_records pr ON r.round_id = pr.round_id
LEFT JOIN penalty_distributions pd ON r.round_id = pd.round_id
WHERE r.session_id = ?
GROUP BY r.round_id, r.round_no, r.status
ORDER BY r.round_no;
```

#### 4.5.2 罚款类型分布分析

```sql
SELECT 
    penalty_type,
    COUNT(*) as count,
    COALESCE(SUM(amount), 0) as total_amount,
    COALESCE(AVG(amount), 0) as avg_amount
FROM penalty_records
WHERE created_at >= ? AND created_at < ?
GROUP BY penalty_type;
```

#### 4.5.3 用户罚款排行

```sql
SELECT 
    user_id,
    COUNT(*) as penalty_count,
    COALESCE(SUM(amount), 0) as total_penalty
FROM penalty_records
WHERE created_at >= ? AND created_at < ?
GROUP BY user_id
ORDER BY total_penalty DESC
LIMIT 100;
```

#### 4.5.4 罚款分发接收方分布

```sql
SELECT 
    pdr.user_id,
    COUNT(*) as receive_count,
    COALESCE(SUM(pdr.amount), 0) as total_received
FROM penalty_distribution_recipients pdr
JOIN penalty_distributions pd ON pdr.distribution_id = pd.id
WHERE pd.created_at >= ? AND pd.created_at < ?
GROUP BY pdr.user_id
ORDER BY total_received DESC
LIMIT 100;
```

#### 4.5.5 对账查询：扣款总额 vs 分发总额 + 平台收入

```sql
-- 对账检查：每个 round 的罚款扣款总额应等于分发总额 + 平台收入
SELECT 
    r.round_id, r.round_no,
    deduct.total as penalty_deduct_total,
    COALESCE(dist.total, 0) as penalty_distribute_total,
    COALESCE(deduct.total, 0) - COALESCE(dist.total, 0) as platform_income
FROM rounds r
JOIN (
    SELECT round_id, SUM(amount) as total 
    FROM penalty_records 
    WHERE deduct_status = 1 
    GROUP BY round_id
) deduct ON r.round_id = deduct.round_id
LEFT JOIN (
    SELECT round_id, SUM(total_amount) as total 
    FROM penalty_distributions 
    GROUP BY round_id
) dist ON r.round_id = dist.round_id
WHERE r.session_id = ?
ORDER BY r.round_no;
```

#### 4.5.6 Pending round 分析（识别预创建未发包）

```sql
-- Pending round 的罚款分析（预创建但未发包的 round）
SELECT 
    r.round_id, r.round_no, r.session_id, r.status,
    COUNT(pr.id) as penalty_count,
    COALESCE(SUM(pr.amount), 0) as penalty_total
FROM rounds r
LEFT JOIN penalty_records pr ON r.round_id = pr.round_id
WHERE r.status = 0  -- Pending
GROUP BY r.round_id, r.round_no, r.session_id;
```

### 4.6 Dead Code 清理

[penalty.go:8-11](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/round/penalty.go#L8-L11) 定义了 3 种罚款类型，但全局搜索后只有 `PenaltyTypeSendTimeout` 在 [game_lifecycle_timeout.go:106](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_timeout.go#L106) 被调用一次。

`PenaltyTypeLeaveDuringGame` 和 `PenaltyTypeDisconnectTimeout` **无任何调用点**，属于死代码。

**处理方式**：
- 如果近期不会启用：直接删除，保持代码整洁
- 如果未来可能启用：保留常量定义，但注释标注 `// reserved, not implemented yet`

---

## 五、实施优先级

| 优先级 | 阶段 | 内容 | 价值 |
|---|---|---|---|
| **P0** | 4.3.8 | 修复 `GeneratePenaltyDistTraceID` 幂等 bug | 防止资金流失 |
| **P0** | 4.3.2-4.3.5 | 预创建 Pending round + 复用（核心方案） | 数据正确性 |
| **P0** | 4.2.1 | `PenaltyRecord` 持久化到 DB | 数据可分析 |
| **P0** | 4.2.2-4.2.3 | `PenaltyDistribution` 持久化 + 关联 Round | 数据可分析 |
| **P0** | 4.3.6-4.3.7 | ApplyPenalty/DistributePenalty 持久化 | 数据完整性 |
| **P1** | 4.2.4 | `BillRecord` 增加关联字段 | 跨表查询简化 |
| **P2** | 4.6 | 移除死代码罚款类型 | 代码整洁 |

> **注**：stats 模块的查询增强不在本次重构范围内，4.5 节仅提供分析查询示例供后续参考。

---

## 六、数据迁移

### 6.1 新增表

新增 3 张表（`penalty_records`、`penalty_distributions`、`penalty_distribution_recipients`）通过 GORM AutoMigrate 自动创建。

### 6.2 BillRecord 字段新增

`bill_record` 表新增 4 个字段（`penalty_type`、`penalty_record_id`、`special_reward_id`、`penalty_distribution_id`），通过 GORM AutoMigrate 自动添加。

### 6.3 历史数据处理

**现有 `bill_record` 中 `round_id=0` 的罚款记录**：
- 无法回填真实 roundID（罚款时未记录 roundNo）
- 在 `penalty_records` 表中标记 `legacy=true`（可选字段）
- 分析时通过 `created_at` 时间戳模糊匹配

**现有 Redis `penalty:record:*` 中的存量数据**：
- 一次性脚本回填到 `penalty_records` 表
- 回填时 `round_id=0`，`legacy=true`

### 6.4 唯一约束添加

`rounds` 表添加 `idx_session_roundno` 唯一约束前，需检查并清理可能的重复数据：

```sql
-- 检查重复
SELECT session_id, round_no, COUNT(*) 
FROM rounds 
GROUP BY session_id, round_no 
HAVING COUNT(*) > 1;
```

---

## 七、风险与注意事项

### 7.1 核心风险

1. **发包流程侵入**：4.3.5 修改 `createRoundRecord` 是核心发包流程改动，需充分测试
2. **并发边界**：OnReplaceTimeout 的 `ReplaceTimeoutLockKey` 是否覆盖所有并发路径，需验证
3. **TraceID 兼容性**：4.3.8 修改 TraceID 格式后，旧 bill 的查询路径仍需保留

### 7.2 CODING_STANDARD 合规

所有新代码须遵循 [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)：

- **§13**：GORM struct tag 定义索引（已在数据模型中体现）
- **§5.4**：短事务原则，罚款扣款 RPC 在事务外
- **§6.4**：金额使用 crypto/rand（罚款金额由配置决定，不涉及随机）
- **§17.1**：代码注释用中文，标识符保留原代码形式
- **§16**：字符串拼接规约
  - TraceID 通过 `TraceIDGenerator` 生成（SC-10）
  - Redis key 通过 `common/rediskeys` 注册（SC-7）
  - 错误包装用 `%w`（SC-6）

### 7.3 Lua 脚本规约

4.3.8 引入的 `penalty:dist:seq` 自增序号：
- 需在 [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) 注册常量
- 禁止内联 key 前缀

### 7.4 降级策略

- `ensureNextRound` 失败时降级为 `roundID=0`，不阻塞罚款流程
- `PenaltyRecord` 持久化失败时记录日志，不阻塞主流程（已写入 Redis，后续对账补偿）
- `PenaltyDistribution` 持久化失败时记录日志，不阻塞主流程（bill 已创建）

### 7.5 监控告警

- `ensureNextRound` 失败率告警（阈值：> 1%）
- `PenaltyRecord` 持久化失败告警
- 罚款对账不一致告警（扣款总额 ≠ 分发总额 + 平台收入）

---

## 八、测试要点

### 8.1 单元测试

- `ensureNextRound`：
  - round 不存在时预创建成功
  - round 已存在时复用成功
  - 查询失败时降级为 roundID=0
  - 创建失败时降级为 roundID=0
- `createRoundRecord`：
  - 无预创建 round 时创建新 round
  - 有预创建 round 时复用
- `GeneratePenaltyDistTraceID`：
  - 同 session 多次调用生成不同 traceID

### 8.2 集成测试

- **正常流程**：OnSendTimeout 预创建 → ForceSendPacketForPlayer 复用 → 结算
- **踢人+替补**：OnSendTimeout 预创建 → handleKickAndReplace → 替补 SendPacket 复用 → 结算
- **踢人+无替补**：OnSendTimeout 预创建 → round 保持 Pending
- **OnReplaceTimeout**：预创建 → round 保持 Pending
- **并发场景**：模拟 OnSendTimeout 与 SendPacket 并发，验证不产生重复 round

### 8.3 数据分析验证

- 按 round 维度查询罚款汇总
- 罚款扣款总额 vs 分发总额 + 平台收入对账
- Pending round 的罚款分析

---

## 九、变更清单汇总

### 9.1 新增文件

| 文件 | 说明 |
|---|---|
| `game/model/penalty_record.go` | PenaltyRecord GORM 模型 |
| `settlement/model/penalty_distribution.go` | PenaltyDistribution / Recipient GORM 模型 |
| `game/infrastructure/persistence/mysql/penalty_record_repository.go` | PenaltyRecord 仓储实现 |
| `settlement/infrastructure/persistence/mysql/penalty_distribution_repository.go` | PenaltyDistribution 仓储实现 |

### 9.2 修改文件

| 文件 | 变更 |
|---|---|
| `game/domain/repository/db_repository.go` | RoundDBRepository 新增 `GetRoundBySessionAndRoundNo` |
| `game/infrastructure/persistence/mysql/round_repository.go` | 实现 `GetRoundBySessionAndRoundNo` |
| `game/application/game_lifecycle_timeout.go` | OnSendTimeout / OnReplaceTimeout 改用 `ensureNextRound` |
| `game/application/game_lifecycle_service.go` | 新增 `ensureNextRound` 辅助方法 |
| `game/application/packet_round_init.go` | `createRoundRecord` 增加复用逻辑 |
| `game/application/penalty_service.go` | `ApplyPenalty` 增加持久化 |
| `settlement/service/penalty_settlement_service.go` | `DistributePenalty` 增加持久化 |
| `settlement/service/trace_id_generator.go` | 修复 `GeneratePenaltyDistTraceID` 幂等 bug |
| `settlement/dto/request.go` | `PenaltyDistributeRequest` 增加 `RoundNo` / `TriggerPhase` |
| `settlement/model/bill.go` | `BillRecord` 增加关联字段 |
| `common/rediskeys/keys.go` | 注册 `penalty:dist:seq` 常量 |
| `game/domain/round/penalty.go` | 删除死代码罚款类型（可选） |

### 9.3 新增 Redis Key

| Key 模式 | 用途 |
|---|---|
| `cashparty:penalty:dist:seq:{roomID}:{sessionID}:{roundNo}` | 罚款分发 TraceID 序号生成 |

---

## 十、待确认决策点

1. **死代码罚款类型**：`PenaltyTypeLeaveDuringGame`/`PenaltyTypeDisconnectTimeout` 是否近期会启用？还是直接删除？
2. **TriggerPhase 字段类型**：用 `inter_round`/`in_round` 字符串，还是 int 枚举（0=in_round, 1=inter_round）？
3. **历史数据回填**：现有 `bill_record` 中 `round_id=0` 的罚款记录是否需要编写迁移脚本尽量回填，还是接受历史数据缺失？
4. **OnReplaceTimeout 并发**：`ReplaceTimeoutLockKey` 是否覆盖所有可能的并发路径？需进一步验证。
5. **created_at 保留策略**：复用预创建 round 时，是否需要更新 `created_at`？还是保留罚款时的创建时间（更准确地反映 round 的真实生命周期）？

---

## 附录 A：相关文件索引

### 核心文件

- [game/application/game_lifecycle_timeout.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_lifecycle_timeout.go) — OnSendTimeout / OnReplaceTimeout
- [game/application/packet_round_init.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/packet_round_init.go) — createRoundRecord
- [game/application/penalty_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/penalty_service.go) — ApplyPenalty
- [settlement/service/penalty_settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/penalty_settlement_service.go) — DistributePenalty
- [settlement/service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) — TraceID 生成
- [game/model/round.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/round.go) — Round 模型
- [game/domain/round/penalty.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/round/penalty.go) — Penalty 领域模型
- [game/infrastructure/persistence/redis/scripts/penalty.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/penalty.lua.go) — 罚款 Lua 脚本

### 仓储文件

- [game/domain/repository/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/repository/db_repository.go) — 仓储接口
- [game/infrastructure/persistence/mysql/round_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/round_repository.go) — Round 仓储实现
- [settlement/model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go) — BillRecord 模型

### 规约文件

- [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — 编码规范
- [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) — Redis Key 注册

---

**文档结束**
