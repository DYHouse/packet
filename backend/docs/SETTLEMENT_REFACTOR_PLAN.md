# 结算系统重构方案

## 一、问题分析

### 1.1 核心问题

当前系统中存在两个严重的资金流转缺失问题：

| 问题 | 描述 | 影响 |
|------|------|------|
| **发红包未扣款** | 玩家发送红包时，只计算了佣金并生成红包，但没有从玩家账户扣除相应金额 | 平台资金损失 |
| **结算未入账** | 回合结算时，只更新了 Redis 中的游戏数据，但没有给抢到红包的玩家账户加钱 | 玩家资金损失 |
| **记录缺失** | `commission_record`、`settlement_record`、`player_settlement_record` 三张表没有数据 | 无法对账和审计 |

### 1.2 当前流程缺陷

```
发红包流程（当前）：
玩家A发红包 → 计算佣金 → 生成红包包 → ❌ 缺失：扣款和记录佣金

抢红包流程（当前）：
玩家抢红包 → Redis原子操作 → ❌ 缺失：账户余额未更新

结算流程（当前）：
回合结束 → Lua脚本结算 → 广播结果 → ❌ 缺失：账户入账和记录结算
```

### 1.3 数据库表状态

| 表名 | 当前状态 | 期望状态 |
|------|---------|---------|
| `commission_record` | 0 条记录 | 每次发红包时插入一条 |
| `settlement_record` | 0 条记录 | 每次回合结算时插入一条 |
| `player_settlement_record` | 0 条记录 | 每次回合结算时插入多条（每个玩家一条） |
| `rounds` | 10 条记录 | 正常 |
| `round_grab_records` | 50 条记录 | 正常 |

---

## 二、重构目标

### 2.1 目标流程

```
发红包流程（目标）：
玩家A发红包 → 从账户扣款 → 计算佣金 → 记录佣金 → 生成红包包

抢红包流程（目标）：
玩家抢红包 → Redis原子操作 → 记录抢红包数据

结算流程（目标）：
回合结束 → Lua脚本结算 → 计算盈亏 → 给玩家账户加钱 → 记录结算数据
```

### 2.2 资金流转示例

#### 场景：玩家A发送500分红包，5个玩家抢

**发红包时：**
```
玩家A账户余额：10000分
操作：
  1. 从玩家A账户扣除500分
     平台.Deduct(userID=A, amount=500)
     余额变为：9500分

  2. 计算佣金（5%）
     commission = 500 * 0.05 = 25分

  3. 记录佣金
     INSERT INTO commission_record (
       room_id, round_id, room_type, 
       room_fee=500, commission=25, packet_total=475
     )

  4. 生成红包包（总额475分）
     [100, 95, 95, 95, 90]
```

**抢红包时：**
```
玩家B抢到100分
玩家C抢到95分
玩家D抢到95分
玩家E抢到95分
玩家F抢到90分（最小，下轮发红包）

操作：
  Redis原子操作记录抢红包结果
  暂不更新账户余额（等结算时统一处理）
```

**结算时：**
```
操作：
  1. 计算每个玩家盈亏
     玩家A: -500分（发红包）
     玩家B: +100分
     玩家C: +95分
     玩家D: +95分
     玩家E: +95分
     玩家F: +90分

  2. 给玩家账户加钱
     平台.Credit(userID=B, amount=100)
     平台.Credit(userID=C, amount=95)
     平台.Credit(userID=D, amount=95)
     平台.Credit(userID=E, amount=95)
     平台.Credit(userID=F, amount=90)

  3. 记录结算数据
     INSERT INTO settlement_record (
       trace_id, room_id, round_id, round_no,
       total_amount=500, commission=25,
       player_count=5, ...
     )

     INSERT INTO player_settlement_record (
       trace_id, room_id, round_id, user_id,
       grab_amount, profit, balance_after, ...
     ) VALUES 
       (..., user_id=B, grab_amount=100, profit=100, ...),
       (..., user_id=C, grab_amount=95, profit=95, ...),
       ...
```

---

## 三、技术方案

### 3.1 架构设计

```
┌─────────────────────────────────────────────────────────────┐
│                     GameAppService                          │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  SendPacket()                                        │  │
│  │  ├─ 1. 验证玩家和房间状态                             │  │
│  │  ├─ 2. 调用 SettlementService.DeductForSendPacket()  │  │
│  │  │     ├─ 扣款（platform.Deduct）                    │  │
│  │  │     ├─ 计算佣金                                   │  │
│  │  │     └─ 记录佣金（commission_record）              │  │
│  │  ├─ 3. 生成红包包                                    │  │
│  │  └─ 4. 广播回合开始                                  │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  settleRound()                                       │  │
│  │  ├─ 1. Lua脚本结算（Redis原子操作）                  │  │
│  │  ├─ 2. 调用 SettlementService.Settle()               │  │
│  │  │     ├─ 计算每个玩家盈亏                           │  │
│  │  │     ├─ 给玩家账户加钱（platform.Credit）          │  │
│  │  │     ├─ 记录结算汇总（settlement_record）          │  │
│  │  │     └─ 记录玩家明细（player_settlement_record）   │  │
│  │  ├─ 3. 广播回合结束                                  │  │
│  │  └─ 4. 发布事件                                      │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 关键设计决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| **扣款时机** | 发红包时立即扣款 | 避免玩家余额不足，保证资金安全 |
| **入账时机** | 回合结算时统一入账 | 简化逻辑，减少平台API调用次数 |
| **佣金记录时机** | 发红包时记录 | 与扣款同步，便于对账 |
| **结算记录时机** | 回合结算时记录 | 与入账同步，保证数据一致性 |
| **分布式锁** | 结算时使用 | 防止重复结算 |

---

## 四、代码修改

### 4.1 修改文件列表

| 文件 | 修改类型 | 说明 |
|------|---------|------|
| `game/application/game_app_service.go` | 修改 | 集成结算服务调用 |
| `settlement/service/settlement_service.go` | 保持 | 已有完整实现 |
| `settlement/service/record_manager.go` | 保持 | 已有完整实现 |
| `settlement/model/record.go` | 保持 | 已有完整定义 |

### 4.2 详细代码修改

#### 4.2.1 修改 SendPacket 方法

**文件：** `game/application/game_app_service.go`

**修改位置：** 第 82-184 行

**修改前：**
```go
func (s *GameAppService) SendPacket(ctx context.Context, req *SendPacketRequest) (*SendPacketResponse, error) {
    meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
    if err != nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeRoomNotFound,
            Message: message.GetErrorMsg(message.CodeRoomNotFound),
        }, nil
    }

    if meta.Status != domain.RoomStatusPlaying {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeGameNotStarted,
            Message: message.GetErrorMsg(message.CodeGameNotStarted),
        }, nil
    }

    player, err := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
    if err != nil || player == nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeNotInRoom,
            Message: message.GetErrorMsg(message.CodeNotInRoom),
        }, nil
    }

    nextRound := int(meta.CurrentRound) + 1
    roundID := idgen.GenerateString()
    amount := meta.RoomFee

    commission := s.commissionCfg.Calculate(amount)
    actualAmount := amount - commission

    genResult, err := s.packetGenerator.Generate(ctx, &algorithm.GenerateRequest{
        TotalAmount: actualAmount,
        PacketCount: s.config.MaxPlayersPerRoom,
        RoomID:      req.RoomID,
        RoundID:     roundID,
        RoomType:    int(amount / 100),
    })
    if err != nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeSystemError,
            Message: message.GetErrorMsg(message.CodeSystemError),
        }, nil
    }

    _, packetIDs, err := s.grabService.InitRoundPackets(ctx, req.RoomID, roundID, req.UserID, amount, genResult.PacketAmounts, nextRound)
    if err != nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeSystemError,
            Message: message.GetErrorMsg(message.CodeSystemError),
        }, nil
    }

    s.publishPacketCreatedEvent(ctx, req.RoomID, roundID, packetIDs, req.UserID, amount, commission, nextRound)

    if s.scheduler != nil {
        s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSend, req.RoomID, req.UserID)
        s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeGrab, req.RoomID, roundID)
    }

    packets := make([]message.PacketInfo, len(packetIDs))
    for i, packetID := range packetIDs {
        packets[i] = message.PacketInfo{
            PacketID: packetID,
            Position: int32(i + 1),
        }
    }

    if s.broadcaster != nil {
        s.broadcaster.Broadcast(req.RoomID, message.PushRoundStart, &message.RoundStartPush{
            RoomID:         req.RoomID,
            RoundID:        roundID,
            CurrentRound:   int32(nextRound),
            SenderID:       req.UserID,
            SenderNickname: player.Nickname,
            TotalAmount:    actualAmount,
            Commission:     commission,
            PacketCount:    int32(len(genResult.PacketAmounts)),
            GrabTimeout:    10,
            Packets:        packets,
        }, "")
    }

    logger.Info("packet sent",
        "room_id", req.RoomID,
        "user_id", req.UserID,
        "amount", amount,
        "round_id", roundID,
        "packet_count", len(genResult.PacketAmounts),
    )

    return &SendPacketResponse{
        Success:     true,
        Code:        message.CodeSuccess,
        Message:     message.GetErrorMsg(message.CodeSuccess),
        PacketCount: len(genResult.PacketAmounts),
    }, nil
}
```

**修改后：**
```go
func (s *GameAppService) SendPacket(ctx context.Context, req *SendPacketRequest) (*SendPacketResponse, error) {
    meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
    if err != nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeRoomNotFound,
            Message: message.GetErrorMsg(message.CodeRoomNotFound),
        }, nil
    }

    if meta.Status != domain.RoomStatusPlaying {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeGameNotStarted,
            Message: message.GetErrorMsg(message.CodeGameNotStarted),
        }, nil
    }

    player, err := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
    if err != nil || player == nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeNotInRoom,
            Message: message.GetErrorMsg(message.CodeNotInRoom),
        }, nil
    }

    nextRound := int(meta.CurrentRound) + 1
    roundID := idgen.GenerateString()
    amount := meta.RoomFee

    // ========== 新增：调用结算服务扣款并记录佣金 ==========
    deductReq := &settlementModel.SendPacketRequest{
        UserID:  parseInt64(req.UserID),
        RoomID:  parseInt64(req.RoomID),
        RoundID: parseInt64(roundID),
        RoomFee: amount,
    }
    
    deductResult, err := s.settlementService.DeductForSendPacket(ctx, deductReq)
    if err != nil {
        logger.Error("deduct for send packet failed", 
            "room_id", req.RoomID,
            "user_id", req.UserID,
            "amount", amount,
            "error", err)
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeInsufficientBalance,
            Message: "余额不足",
        }, nil
    }
    // ========================================================

    genResult, err := s.packetGenerator.Generate(ctx, &algorithm.GenerateRequest{
        TotalAmount: deductResult.PacketTotal,  // 使用实际红包总额
        PacketCount: s.config.MaxPlayersPerRoom,
        RoomID:      req.RoomID,
        RoundID:     roundID,
        RoomType:    int(amount / 100),
    })
    if err != nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeSystemError,
            Message: message.GetErrorMsg(message.CodeSystemError),
        }, nil
    }

    _, packetIDs, err := s.grabService.InitRoundPackets(ctx, req.RoomID, roundID, req.UserID, amount, genResult.PacketAmounts, nextRound)
    if err != nil {
        return &SendPacketResponse{
            Success: false,
            Code:    message.CodeSystemError,
            Message: message.GetErrorMsg(message.CodeSystemError),
        }, nil
    }

    s.publishPacketCreatedEvent(ctx, req.RoomID, roundID, packetIDs, req.UserID, amount, deductResult.Commission, nextRound)

    if s.scheduler != nil {
        s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSend, req.RoomID, req.UserID)
        s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeGrab, req.RoomID, roundID)
    }

    packets := make([]message.PacketInfo, len(packetIDs))
    for i, packetID := range packetIDs {
        packets[i] = message.PacketInfo{
            PacketID: packetID,
            Position: int32(i + 1),
        }
    }

    if s.broadcaster != nil {
        s.broadcaster.Broadcast(req.RoomID, message.PushRoundStart, &message.RoundStartPush{
            RoomID:         req.RoomID,
            RoundID:        roundID,
            CurrentRound:   int32(nextRound),
            SenderID:       req.UserID,
            SenderNickname: player.Nickname,
            TotalAmount:    deductResult.PacketTotal,  // 使用实际红包总额
            Commission:     deductResult.Commission,    // 使用实际佣金
            PacketCount:    int32(len(genResult.PacketAmounts)),
            GrabTimeout:    10,
            Packets:        packets,
        }, "")
    }

    logger.Info("packet sent",
        "room_id", req.RoomID,
        "user_id", req.UserID,
        "amount", amount,
        "commission", deductResult.Commission,
        "packet_total", deductResult.PacketTotal,
        "round_id", roundID,
        "packet_count", len(genResult.PacketAmounts),
    )

    return &SendPacketResponse{
        Success:     true,
        Code:        message.CodeSuccess,
        Message:     message.GetErrorMsg(message.CodeSuccess),
        PacketCount: len(genResult.PacketAmounts),
    }, nil
}
```

**关键变更：**
1. 新增导入：`settlementModel "github.com/cashparty/backend/settlement/model"`
2. 调用 `settlementService.DeductForSendPacket()` 进行扣款和记录佣金
3. 使用 `deductResult.PacketTotal` 和 `deductResult.Commission` 替代本地计算
4. 增加错误处理和日志记录

#### 4.2.2 修改 settleRound 方法

**文件：** `game/application/game_app_service.go`

**修改位置：** 第 792-918 行

**修改前：**
```go
func (s *GameAppService) settleRound(ctx context.Context, roomID, roundID string) {
    keys := []string{
        redis.RoundStateKey(roundID),
        redis.RoundGrabbersKey(roundID),
        redis.RoomPlayersKey(roomID),
        redis.RoomHashKey(roomID),
    }

    args := []interface{}{
        roundID,
        time.Now().Unix(),
        "cashparty",
    }

    res, err := s.redis.Eval(ctx, redis.LuaSettleRound, keys, args...).Slice()
    if err != nil {
        logger.Error("settle round failed", "room_id", roomID, "round_id", roundID, "error", err)
        return
    }

    code := parseInt(res[0])
    if code != 0 {
        logger.Error("settle round lua failed", "room_id", roomID)
        return
    }

    roundNo := parseInt(res[1])
    senderID := fmt.Sprintf("%v", res[2])
    totalAmount := parseInt64(res[3])
    minAmountPlayer := fmt.Sprintf("%v", res[4])
    isGameEnd := parseInt(res[5]) == 1

    var results []message.RoundResult
    if len(res) > 6 {
        if arr, ok := res[6].([]interface{}); ok {
            for _, item := range arr {
                if tuple, ok := item.([]interface{}); ok && len(tuple) >= 7 {
                    isAutoAssigned := parseInt(tuple[5]) == 1
                    packetID := parseInt64(tuple[6])
                    results = append(results, message.RoundResult{
                        UserID:         fmt.Sprintf("%v", tuple[0]),
                        Amount:         parseInt64(tuple[1]),
                        Nickname:       fmt.Sprintf("%v", tuple[2]),
                        Position:       int32(parseInt(tuple[3])),
                        Avatar:         fmt.Sprintf("%v", tuple[4]),
                        IsAutoAssigned: isAutoAssigned,
                        PacketID:       fmt.Sprintf("%d", packetID),
                    })
                }
            }
        }
    }

    sort.Slice(results, func(i, j int) bool {
        return results[i].Position < results[j].Position
    })

    meta, _ := s.repo.GetRoomMeta(ctx, roomID)
    commission := int64(0)
    if meta != nil {
        commission = s.commissionCfg.Calculate(totalAmount)
    }

    if s.broadcaster != nil {
        s.broadcaster.Broadcast(roomID, message.PushRoundEnd, &message.RoundEndPush{
            RoomID:          roomID,
            RoundID:         roundID,
            CurrentRound:    int32(roundNo),
            SenderID:        senderID,
            TotalAmount:     totalAmount,
            Results:         results,
            MinAmountPlayer: minAmountPlayer,
            NextSenderID:    minAmountPlayer,
            IsGameEnd:       isGameEnd,
        }, "")
    }

    if s.eventPublisher != nil && meta != nil {
        roundResults := make([]*domain.RoundResult, 0, len(results))
        for _, r := range results {
            roundResults = append(roundResults, &domain.RoundResult{
                UserID:        r.UserID,
                PacketID:      r.PacketID,
                Position:      int(r.Position),
                Amount:        r.Amount,
                IsAutoAssigned: r.IsAutoAssigned,
            })
        }

        senderType := "player"
        if senderID == "0" {
            senderType = "system"
        }

        event := &domain.GameEvent{
            RoomID:    roomID,
            SessionID: meta.CurrentSessionID,
            RoundID:   roundID,
            Timestamp: time.Now().Unix(),
            TraceID:   fmt.Sprintf("evt_%s_%d", roomID, time.Now().UnixNano()),
            Data: &domain.RoundSettleData{
                RoundNo:     roundNo,
                SenderID:    senderID,
                SenderType:  senderType,
                TotalAmount: totalAmount,
                Commission:  commission,
                PacketCount: len(results),
                Results:     roundResults,
                MinPlayerID: minAmountPlayer,
                IsGameEnd:   isGameEnd,
            },
        }
        go func() {
            if err := s.eventPublisher.PublishRoundSettle(context.Background(), event); err != nil {
                logger.Error("publish round settle event failed", "error", err)
            }
        }()
    }

    if isGameEnd {
        s.endGame(ctx, roomID)
    } else {
        if s.scheduler != nil {
            s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSend, roomID, minAmountPlayer)
        }
    }
}
```

**修改后：**
```go
func (s *GameAppService) settleRound(ctx context.Context, roomID, roundID string) {
    keys := []string{
        redis.RoundStateKey(roundID),
        redis.RoundGrabbersKey(roundID),
        redis.RoomPlayersKey(roomID),
        redis.RoomHashKey(roomID),
    }

    args := []interface{}{
        roundID,
        time.Now().Unix(),
        "cashparty",
    }

    res, err := s.redis.Eval(ctx, redis.LuaSettleRound, keys, args...).Slice()
    if err != nil {
        logger.Error("settle round failed", "room_id", roomID, "round_id", roundID, "error", err)
        return
    }

    code := parseInt(res[0])
    if code != 0 {
        logger.Error("settle round lua failed", "room_id", roomID)
        return
    }

    roundNo := parseInt(res[1])
    senderID := fmt.Sprintf("%v", res[2])
    totalAmount := parseInt64(res[3])
    minAmountPlayer := fmt.Sprintf("%v", res[4])
    isGameEnd := parseInt(res[5]) == 1

    var results []message.RoundResult
    if len(res) > 6 {
        if arr, ok := res[6].([]interface{}); ok {
            for _, item := range arr {
                if tuple, ok := item.([]interface{}); ok && len(tuple) >= 7 {
                    isAutoAssigned := parseInt(tuple[5]) == 1
                    packetID := parseInt64(tuple[6])
                    results = append(results, message.RoundResult{
                        UserID:         fmt.Sprintf("%v", tuple[0]),
                        Amount:         parseInt64(tuple[1]),
                        Nickname:       fmt.Sprintf("%v", tuple[2]),
                        Position:       int32(parseInt(tuple[3])),
                        Avatar:         fmt.Sprintf("%v", tuple[4]),
                        IsAutoAssigned: isAutoAssigned,
                        PacketID:       fmt.Sprintf("%d", packetID),
                    })
                }
            }
        }
    }

    sort.Slice(results, func(i, j int) bool {
        return results[i].Position < results[j].Position
    })

    meta, _ := s.repo.GetRoomMeta(ctx, roomID)
    commission := int64(0)
    if meta != nil {
        commission = s.commissionCfg.Calculate(totalAmount)
    }

    // ========== 新增：调用结算服务给玩家账户加钱 ==========
    if s.settlementService != nil && meta != nil && len(results) > 0 {
        settleReq := &settlementModel.SettlementRequest{
            RoomID:        parseInt64(roomID),
            RoundID:       parseInt64(roundID),
            RoundNo:       roundNo,
            RoomFee:       totalAmount,
            RewardType:    0,  // 根据实际业务设置
            RewardAmount:  0,
            JackpotAmount: 0,
            Players:       make([]*settlementModel.PlayerSettleInfo, 0, len(results)),
        }

        for _, r := range results {
            settleReq.Players = append(settleReq.Players, &settlementModel.PlayerSettleInfo{
                UserID:   parseInt64(r.UserID),
                Nickname: r.Nickname,
                SeatNo:   int(r.Position),
            })
        }

        settleResult, err := s.settlementService.Settle(ctx, settleReq)
        if err != nil {
            logger.Error("settlement failed", 
                "room_id", roomID, 
                "round_id", roundID, 
                "error", err)
        } else {
            logger.Info("settlement completed",
                "room_id", roomID,
                "round_id", roundID,
                "trace_id", settleResult.TraceID,
                "player_count", len(settleResult.PlayerResults))
        }
    }
    // ============================================================

    if s.broadcaster != nil {
        s.broadcaster.Broadcast(roomID, message.PushRoundEnd, &message.RoundEndPush{
            RoomID:          roomID,
            RoundID:         roundID,
            CurrentRound:    int32(roundNo),
            SenderID:        senderID,
            TotalAmount:     totalAmount,
            Results:         results,
            MinAmountPlayer: minAmountPlayer,
            NextSenderID:    minAmountPlayer,
            IsGameEnd:       isGameEnd,
        }, "")
    }

    if s.eventPublisher != nil && meta != nil {
        roundResults := make([]*domain.RoundResult, 0, len(results))
        for _, r := range results {
            roundResults = append(roundResults, &domain.RoundResult{
                UserID:        r.UserID,
                PacketID:      r.PacketID,
                Position:      int(r.Position),
                Amount:        r.Amount,
                IsAutoAssigned: r.IsAutoAssigned,
            })
        }

        senderType := "player"
        if senderID == "0" {
            senderType = "system"
        }

        event := &domain.GameEvent{
            RoomID:    roomID,
            SessionID: meta.CurrentSessionID,
            RoundID:   roundID,
            Timestamp: time.Now().Unix(),
            TraceID:   fmt.Sprintf("evt_%s_%d", roomID, time.Now().UnixNano()),
            Data: &domain.RoundSettleData{
                RoundNo:     roundNo,
                SenderID:    senderID,
                SenderType:  senderType,
                TotalAmount: totalAmount,
                Commission:  commission,
                PacketCount: len(results),
                Results:     roundResults,
                MinPlayerID: minAmountPlayer,
                IsGameEnd:   isGameEnd,
            },
        }
        go func() {
            if err := s.eventPublisher.PublishRoundSettle(context.Background(), event); err != nil {
                logger.Error("publish round settle event failed", "error", err)
            }
        }()
    }

    if isGameEnd {
        s.endGame(ctx, roomID)
    } else {
        if s.scheduler != nil {
            s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSend, roomID, minAmountPlayer)
        }
    }
}
```

**关键变更：**
1. 新增导入：`settlementModel "github.com/cashparty/backend/settlement/model"`
2. 在 Lua 结算后，调用 `settlementService.Settle()` 进行账户入账和记录结算
3. 构造 `SettlementRequest` 参数
4. 增加错误处理和日志记录

#### 4.2.3 添加辅助函数

**文件：** `game/application/game_app_service.go`

**在文件末尾添加：**
```go
func parseInt64(v interface{}) int64 {
    switch val := v.(type) {
    case int64:
        return val
    case float64:
        return int64(val)
    case int:
        return int64(val)
    case string:
        var result int64
        fmt.Sscanf(val, "%d", &result)
        return result
    }
    return 0
}
```

**说明：** 如果文件中已有此函数，则无需添加。

---

## 五、测试方案

### 5.1 单元测试

#### 5.1.1 测试发红包扣款

**测试用例：**
```go
func TestSendPacket_DeductBalance(t *testing.T) {
    // 准备
    userID := int64(100001)
    initialBalance := int64(10000)
    roomFee := int64(500)
    
    mockClient := platform.NewMockClient()
    mockClient.SetBalance(userID, initialBalance)
    
    // 执行
    deductReq := &settlementModel.SendPacketRequest{
        UserID:  userID,
        RoomID:  1,
        RoundID: 1,
        RoomFee: roomFee,
    }
    
    result, err := settlementService.DeductForSendPacket(ctx, deductReq)
    
    // 验证
    assert.NoError(t, err)
    assert.Equal(t, int64(25), result.Commission)  // 500 * 0.05
    assert.Equal(t, int64(475), result.PacketTotal)  // 500 - 25
    assert.Equal(t, initialBalance-roomFee, result.BalanceAfter)
    
    // 验证数据库记录
    var commissionRecord model.CommissionRecord
    db.First(&commissionRecord, "room_id = ?", 1)
    assert.Equal(t, roomFee, commissionRecord.RoomFee)
    assert.Equal(t, int64(25), commissionRecord.Commission)
}
```

#### 5.1.2 测试结算入账

**测试用例：**
```go
func TestSettle_CreditPlayers(t *testing.T) {
    // 准备
    players := []*settlementModel.PlayerSettleInfo{
        {UserID: 100001, Nickname: "Player1", SeatNo: 1},
        {UserID: 100002, Nickname: "Player2", SeatNo: 2},
    }
    
    mockClient := platform.NewMockClient()
    mockClient.SetBalance(100001, 9500)  // 发红包后余额
    mockClient.SetBalance(100002, 10000)
    
    // 执行
    settleReq := &settlementModel.SettlementRequest{
        RoomID:   1,
        RoundID:  1,
        RoundNo:  1,
        RoomFee:  500,
        Players:  players,
    }
    
    result, err := settlementService.Settle(ctx, settleReq)
    
    // 验证
    assert.NoError(t, err)
    assert.NotEmpty(t, result.TraceID)
    assert.Len(t, result.PlayerResults, 2)
    
    // 验证账户余额
    balance1 := mockClient.GetBalance(100001)
    balance2 := mockClient.GetBalance(100002)
    assert.Greater(t, balance1, int64(9500))  // 应该增加了
    assert.Greater(t, balance2, int64(10000)) // 应该增加了
    
    // 验证数据库记录
    var settlementRecord model.SettlementRecord
    db.First(&settlementRecord, "round_id = ?", 1)
    assert.Equal(t, 1, settlementRecord.PlayerCount)
    
    var playerRecords []model.PlayerSettlementRecord
    db.Find(&playerRecords, "round_id = ?", 1)
    assert.Len(t, playerRecords, 2)
}
```

### 5.2 集成测试

#### 5.2.1 完整游戏流程测试

**测试步骤：**
1. 创建房间并加入5个玩家
2. 所有玩家准备，游戏开始
3. 系统发送第一轮红包
4. 所有玩家抢红包
5. 验证回合结算
6. 重复步骤3-5直到游戏结束
7. 验证最终账户余额和数据库记录

**验证点：**
- [ ] 发红包时，发送者账户余额正确扣除
- [ ] 发红包时，`commission_record` 表有记录
- [ ] 抢红包时，Redis 数据正确
- [ ] 结算时，所有玩家账户余额正确增加
- [ ] 结算时，`settlement_record` 表有记录
- [ ] 结算时，`player_settlement_record` 表有记录
- [ ] 游戏结束时，所有玩家盈亏总和为0（扣除佣金后）

#### 5.2.2 异常场景测试

**测试场景：**
1. **余额不足发红包**
   - 设置玩家余额 < 房间费用
   - 尝试发红包
   - 验证返回错误码和消息

2. **重复结算**
   - 对同一回合调用两次结算
   - 验证幂等性（第二次应该被忽略）

3. **并发抢红包**
   - 多个玩家同时抢同一个红包
   - 验证只有一个玩家能抢到

### 5.3 数据验证

#### 5.3.1 数据一致性检查

**SQL 查询：**
```sql
-- 检查佣金记录
SELECT 
    cr.room_id,
    cr.round_id,
    cr.room_fee,
    cr.commission,
    cr.packet_total,
    cr.created_at
FROM commission_record cr
ORDER BY cr.created_at DESC
LIMIT 10;

-- 检查结算记录
SELECT 
    sr.trace_id,
    sr.room_id,
    sr.round_id,
    sr.total_amount,
    sr.commission,
    sr.player_count,
    sr.settled_at
FROM settlement_record sr
ORDER BY sr.settled_at DESC
LIMIT 10;

-- 检查玩家结算明细
SELECT 
    psr.trace_id,
    psr.room_id,
    psr.round_id,
    psr.user_id,
    psr.grab_amount,
    psr.profit,
    psr.balance_after
FROM player_settlement_record psr
ORDER BY psr.created_at DESC
LIMIT 20;

-- 验证资金平衡（所有玩家盈亏 + 佣金 = 0）
SELECT 
    sr.round_id,
    sr.total_amount,
    sr.commission,
    SUM(psr.grab_amount) as total_grab,
    (sr.total_amount - sr.commission - SUM(psr.grab_amount)) as balance_check
FROM settlement_record sr
JOIN player_settlement_record psr ON sr.trace_id = psr.trace_id
GROUP BY sr.round_id, sr.total_amount, sr.commission
HAVING balance_check != 0;
```

**预期结果：**
- `balance_check` 应该为 0，表示资金平衡

---

## 六、部署方案

### 6.1 部署前检查

**检查清单：**
- [ ] 数据库表结构正确
- [ ] SettlementService 已正确初始化
- [ ] Platform Client 已正确配置（Mock 或 HTTP）
- [ ] Redis 连接正常
- [ ] Kafka 连接正常

### 6.2 灰度发布

**阶段一：测试环境验证**
1. 部署到测试环境
2. 执行完整测试用例
3. 验证数据库记录
4. 验证账户余额变化

**阶段二：预发布环境验证**
1. 部署到预发布环境
2. 使用真实平台接口（非Mock）
3. 小范围用户测试
4. 监控日志和错误

**阶段三：生产环境发布**
1. 选择低峰期发布
2. 逐步替换实例
3. 实时监控关键指标
4. 准备回滚方案

### 6.3 监控指标

**关键指标：**
- 发红包成功率
- 发红包扣款失败次数
- 结算成功率
- 结算失败次数
- 数据库写入延迟
- 平台 API 调用延迟
- 账户余额异常告警

**告警规则：**
```
# 发红包扣款失败率 > 1%
rate(send_packet_deduct_failed_total[5m]) / rate(send_packet_total[5m]) > 0.01

# 结算失败率 > 1%
rate(settlement_failed_total[5m]) / rate(settlement_total[5m]) > 0.01

# 数据库写入延迟 > 1秒
histogram_quantile(0.95, rate(db_write_duration_seconds_bucket[5m])) > 1
```

---

## 七、回滚方案

### 7.1 快速回滚

**触发条件：**
- 发红包扣款失败率 > 5%
- 结算失败率 > 5%
- 出现资金异常

**回滚步骤：**
1. 停止新版本服务
2. 部署上一版本代码
3. 验证服务正常
4. 检查数据一致性

### 7.2 数据修复

**场景一：扣款成功但未记录佣金**
```sql
-- 补录佣金记录
INSERT INTO commission_record (room_id, round_id, room_type, room_fee, commission, packet_total, created_at)
SELECT 
    r.room_id,
    r.round_id,
    rc.room_type,
    rc.room_fee,
    FLOOR(rc.room_fee * 0.05) as commission,
    rc.room_fee - FLOOR(rc.room_fee * 0.05) as packet_total,
    NOW() as created_at
FROM rounds r
JOIN room_configs rc ON r.room_id = rc.room_id
WHERE NOT EXISTS (
    SELECT 1 FROM commission_record cr 
    WHERE cr.round_id = r.round_id
);
```

**场景二：结算成功但未记录结算数据**
```sql
-- 补录结算记录（需要根据实际业务逻辑调整）
-- 建议联系开发人员手动处理
```

---

## 八、FAQ

### Q1: 为什么发红包时立即扣款，而不是结算时统一扣款？

**A:** 
- 避免玩家余额不足，保证资金安全
- 简化结算逻辑，结算时只需要处理入账
- 与实际业务场景一致（发红包时钱就出去了）

### Q2: 如果平台扣款失败怎么办？

**A:**
- 返回错误码 `CodeInsufficientBalance`
- 玩家无法发送红包
- 根据超时规则进行惩罚处理

### Q3: 如果结算时平台入账失败怎么办？

**A:**
- 记录错误日志
- 标记结算状态为失败
- 后台任务重试
- 人工介入处理

### Q4: 如何保证结算的幂等性？

**A:**
- SettlementService 内部使用分布式锁
- 检查 `settlement_record` 表是否已存在该 `round_id`
- Redis 中标记已结算的 round_id

### Q5: 如何处理并发抢红包？

**A:**
- 使用 Lua 脚本保证原子性
- Redis 单线程处理，保证顺序
- 已在现有代码中实现

---

## 九、总结

### 9.1 修改影响范围

| 影响范围 | 说明 |
|---------|------|
| **代码修改** | 仅修改 `game_app_service.go`，其他文件保持不变 |
| **数据库变更** | 无需变更表结构，只是开始写入数据 |
| **接口变更** | 无接口变更 |
| **配置变更** | 无配置变更 |

### 9.2 风险评估

| 风险 | 等级 | 缓解措施 |
|------|------|---------|
| 平台API调用失败 | 中 | 重试机制 + 错误处理 |
| 数据库写入失败 | 低 | 事务保证 + 日志记录 |
| 并发问题 | 低 | 已有Lua脚本保证 |
| 资金不一致 | 高 | 完整测试 + 监控告警 |

### 9.3 预期收益

- ✅ 修复资金流转缺失问题
- ✅ 保证平台资金安全
- ✅ 保证玩家资金正确
- ✅ 完善数据记录，便于对账和审计
- ✅ 为后续财务报表提供数据支持

---

## 附录

### A. 相关文件路径

```
game/
├── application/
│   └── game_app_service.go          # 主要修改文件
├── domain/
│   └── events.go                     # 事件定义
└── infrastructure/
    └── messaging/
        └── game_event_consumer.go    # 事件消费者

settlement/
├── service/
│   ├── settlement_service.go         # 结算服务（已有）
│   ├── calculator.go                 # 计算器（已有）
│   └── record_manager.go             # 记录管理器（已有）
├── model/
│   ├── record.go                     # 记录模型（已有）
│   ├── request.go                    # 请求模型（已有）
│   └── result.go                     # 结果模型（已有）
└── consumer/
    └── kafka_consumer.go             # Kafka消费者（暂不使用）

api/platform/
├── http_client.go                    # HTTP客户端（已有）
└── mock_client.go                    # Mock客户端（已有）
```

### B. 数据库表结构

#### commission_record
```sql
CREATE TABLE `commission_record` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `room_id` bigint(20) NOT NULL,
  `round_id` bigint(20) NOT NULL,
  `room_type` int(11) NOT NULL,
  `room_fee` bigint(20) NOT NULL,
  `commission` bigint(20) NOT NULL,
  `packet_total` bigint(20) NOT NULL,
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_room_id` (`room_id`),
  KEY `idx_round_id` (`round_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

#### settlement_record
```sql
CREATE TABLE `settlement_record` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `trace_id` varchar(64) NOT NULL,
  `room_id` bigint(20) NOT NULL,
  `round_id` bigint(20) NOT NULL,
  `round_no` int(11) NOT NULL,
  `room_type` int(11) NOT NULL,
  `total_amount` bigint(20) NOT NULL,
  `commission` bigint(20) NOT NULL,
  `reward_type` int(11) DEFAULT '0',
  `reward_amount` bigint(20) DEFAULT '0',
  `jackpot_amount` bigint(20) DEFAULT '0',
  `min_player_id` bigint(20) NOT NULL,
  `player_count` int(11) NOT NULL,
  `status` int(11) DEFAULT '0',
  `settled_at` datetime NOT NULL,
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_trace_id` (`trace_id`),
  KEY `idx_room_id` (`room_id`),
  KEY `idx_round_id` (`round_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

#### player_settlement_record
```sql
CREATE TABLE `player_settlement_record` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `trace_id` varchar(64) NOT NULL,
  `room_id` bigint(20) NOT NULL,
  `round_id` bigint(20) NOT NULL,
  `user_id` bigint(20) NOT NULL,
  `grab_amount` bigint(20) DEFAULT '0',
  `is_min` int(11) DEFAULT '0',
  `reward_type` int(11) DEFAULT '0',
  `reward_amount` bigint(20) DEFAULT '0',
  `profit` bigint(20) DEFAULT '0',
  `balance_after` bigint(20) DEFAULT '0',
  `transaction_id` varchar(64) DEFAULT NULL,
  `created_at` datetime DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_trace_id` (`trace_id`),
  KEY `idx_room_id` (`room_id`),
  KEY `idx_round_id` (`round_id`),
  KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### C. 错误码定义

| 错误码 | 常量名 | 说明 |
|--------|--------|------|
| 1001 | CodeRoomNotFound | 房间不存在 |
| 1002 | CodeGameNotStarted | 游戏未开始 |
| 1003 | CodeNotInRoom | 不在房间中 |
| 1004 | CodeInsufficientBalance | 余额不足 |
| 1005 | CodeSystemError | 系统错误 |

### D. 参考文档

- [WebSocket通信协议](./websocket_protocol.md)
- [游戏房间流程](./game_room_flow.md)
- [重构计划](./REFACTOR_PLAN.md)
- [Round模型重构](./round_refactor_plan.md)
