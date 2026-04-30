# 回合奖励结算重构方案

## 一、需求概述

| 需求项 | 描述 |
|--------|------|
| **奖励判定** | 每个回合结束之后，判断该回合是否触发奖励 |
| **奖励结算** | 如果触发奖励，结算时需要额外结算系统奖励的金额 |
| **奖励类型** | 顺子奖励、豹子奖励 |

---

## 二、架构设计

### 2.1 整体流程

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              游戏流程                                        │
└─────────────────────────────────────────────────────────────────────────────┘

┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────────────────────┐
│  发红包   │ ─→ │  抢红包   │ ─→ │ 回合结束  │ ─→ │       结算流程           │
│ (正常生成)│    │ (正常抢)  │    │          │    │  ┌────────────────────┐  │
└──────────┘    └──────────┘    └──────────┘    │  │ 1. 判断是否触发奖励 │  │
                                                │  │ 2. 结算玩家金额     │  │
                                                │  │ 3. 结算系统奖励金额 │  │
                                                │  └────────────────────┘  │
                                                └──────────────────────────┘
```

### 2.2 奖励判定时机

**在回合结束时判断**，不是在生成红包时：

| 阶段 | 说明 |
|------|------|
| 生成红包 | 正常生成红包，不涉及奖励判定 |
| 抢红包 | 正常抢红包，不涉及奖励判定 |
| 回合结束 | 判断是否触发奖励，触发则额外发放奖励金额 |

### 2.3 组件架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                       GameAppService                                 │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │                    结算流程 (同步执行)                          │  │
│  │  ┌────────────────┐  ┌────────────────┐  ┌──────────────────┐ │  │
│  │  │ 1. 奖励判定    │  │ 2. 结算玩家金额│  │ 3. 结算系统奖励  │ │  │
│  │  │ (判断是否触发) │  │ (正常结算)     │  │ (平台→玩家)      │ │  │
│  │  └────────────────┘  └────────────────┘  └──────────────────┘ │  │
│  │                           ↓                                    │  │
│  │  ┌───────────────────────────────────────────────────────────┐│  │
│  │  │ 4. 广播奖励消息 (如果触发奖励)                             ││  │
│  │  └───────────────────────────────────────────────────────────┘│  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.4 关键变更：同步奖励判定 + 异步数据结算

**原流程（有问题）**：
```
回合结束 → 发布事件 → 消费者异步处理 → 继续下一步
                          ↑
                     问题：奖励广播也被异步了，用户看不到
```

**新流程（优化后）**：
```
回合结束 
  ├── 同步：判断是否触发奖励
  ├── 同步：广播奖励消息 (如果触发)
  └── 异步：发布事件处理数据更新和资金结算
           ├── 更新 round 状态
           ├── 创建 grab record
           ├── 更新 session player 统计
           ├── 更新 session current_round
           └── 调用 settlementService.SettleRound()
```

**优点**：
1. 奖励判定和广播是实时的，用户能立即看到
2. 数据更新和资金结算可以异步处理，不阻塞主流程
3. 即使异步处理失败，用户已经知道中奖了，后续可以重试

### 2.5 数据处理逻辑重构

**原设计（有问题）**：
```
game_app_service.settleRound()
  └── 异步发布事件 PublishRoundSettle()

game_event_consumer.handleRoundSettle() 
  ├── 更新 round 状态
  ├── 创建 grab record
  ├── 更新 session player 统计
  ├── 更新 session current_round
  └── 调用 settlementService.SettleRound()
```

**新设计（优化后）**：
```
game_app_service.settleRound()  // 同步部分
  ├── 1. 判断是否触发奖励
  ├── 2. 广播奖励消息 (如果触发)
  └── 3. 异步发布事件 PublishRoundSettle()

game_event_consumer.handleRoundSettle()  // 异步部分
  ├── 1. 更新 round 状态
  ├── 2. 创建 grab record
  ├── 3. 更新 session player 统计
  ├── 4. 更新 session current_round
  └── 5. 调用 settlementService.SettleRound()
```

---

## 三、奖励规则

### 3.1 奖励类型

| 奖励类型 | 触发条件 | 奖励金额 | 发放对象 |
|----------|----------|----------|----------|
| **顺子奖励** | 回合结束时判定触发 | `totalAmount × multiplier` | 当前所有参与抢红包的玩家，每人发相同金额 |
| **豹子奖励** | 回合结束时判定触发 | `totalAmount × multiplier` | 当前所有参与抢红包的玩家，每人发相同金额 |

### 3.2 奖励金额计算

```go
// 顺子奖励金额
straightRewardAmount = totalAmount * straightRewardMultiplier

// 豹子奖励金额
leopardRewardAmount = totalAmount * leopardRewardMultiplier
```

### 3.3 奖励发放流程

```
┌─────────────────────────────────────────────────────────────────┐
│                     奖励发放流程                                 │
└─────────────────────────────────────────────────────────────────┘

1. 平台账户扣款
   └── platform.Deduct(platformAccount, rewardAmount × playerCount)

2. 给所有参与抢红包的玩家入账 (每人发相同金额)
   └── for each player:
         platform.Credit(playerID, rewardAmount)
```

---

## 四、配置结构设计

### 4.1 配置文件 (YAML)

```yaml
# config/game.yaml
reward_settlement:
  # 奖励金额倍数配置
  straight_reward_multiplier: 1.0   # 顺子奖励倍数 (相对于红包总额)
  leopard_reward_multiplier: 10.0   # 豹子奖励倍数 (相对于红包总额)
  
  # 开关配置
  enabled: true                      # 是否开启奖励结算
```

### 4.2 Go 配置结构

```go
type RewardSettlementConfig struct {
    Enabled                  bool    `yaml:"enabled"`
    StraightRewardMultiplier float64 `yaml:"straight_reward_multiplier"`
    LeopardRewardMultiplier  float64 `yaml:"leopard_reward_multiplier"`
}
```

---

## 五、核心逻辑流程

### 5.1 回合结束结算流程

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         回合结束结算流程                                     │
└─────────────────────────────────────────────────────────────────────────────┘

                              ┌─────────────────┐
                              │   回合结束       │
                              └────────┬────────┘
                                       │
                                       ▼
                              ┌─────────────────┐
                              │ 判断是否触发奖励 │
                              │ (根据红包结果)   │
                              └────────┬────────┘
                                       │
                    ┌──────────────────┼──────────────────┐
                    │                  │                  │
                    ▼                  ▼                  ▼
           ┌──────────────┐   ┌──────────────┐   ┌──────────────┐
           │ 顺子奖励      │   │ 豹子奖励      │   │ 无奖励       │
           └──────┬───────┘   └──────┬───────┘   └──────┬───────┘
                  │                  │                  │
                  ▼                  ▼                  ▼
           ┌──────────────┐   ┌──────────────┐   ┌──────────────┐
           │ 计算奖励金额  │   │ 计算奖励金额  │   │ 正常结算     │
           │ totalAmount×1│   │ totalAmount×10│   │              │
           └──────┬───────┘   └──────┬───────┘   └──────────────┘
                  │                  │
                  ▼                  ▼
           ┌──────────────────────────────────────────────────┐
           │              结算玩家抢到的金额                    │
           │         (正常流程，调用 platform.Credit)          │
           └──────────────────────┬───────────────────────────┘
                                  │
                                  ▼
           ┌──────────────────────────────────────────────────┐
           │              结算系统奖励金额                      │
           │         (从平台账户发放给所有玩家)                 │
           │         1. platform.Deduct(总额)                  │
           │         2. 每个玩家发相同奖励金额                  │
           │            platform.Credit(playerID, rewardAmount)│
           └──────────────────────────────────────────────────┘
```

### 5.2 奖励判定逻辑

```go
// 判断是否触发奖励 (根据红包结果)
func DetermineRewardType(packetAmounts []int64) int {
    // 检查是否是豹子 (所有金额相同)
    if isLeopard(packetAmounts) {
        return RewardTypeLeopard
    }
    
    // 检查是否是顺子 (金额连续递增)
    if isStraight(packetAmounts) {
        return RewardTypeStraight
    }
    
    return RewardTypeNone
}

func isLeopard(amounts []int64) bool {
    if len(amounts) < 2 {
        return false
    }
    first := amounts[0]
    for _, amount := range amounts[1:] {
        if amount != first {
            return false
        }
    }
    return true
}

func isStraight(amounts []int64) bool {
    if len(amounts) < 2 {
        return false
    }
    
    // 排序
    sorted := make([]int64, len(amounts))
    copy(sorted, amounts)
    sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
    
    // 检查是否连续
    for i := 1; i < len(sorted); i++ {
        if sorted[i] != sorted[i-1]+1 {
            return false
        }
    }
    return true
}
```

---

## 六、代码实现

### 6.1 新增 `reward_settler.go`

```go
package settlement

import (
    "context"
    "fmt"
    
    "github.com/cashparty/backend/api/platform"
    "github.com/cashparty/backend/settlement/dto"
    "github.com/cashparty/backend/settlement/model"
)

const (
    RewardTypeNone     = 0
    RewardTypeStraight = 1
    RewardTypeLeopard  = 2
)

type RewardSettler struct {
    config   *RewardSettlementConfig
    platform platform.Client
    billMgr  *BillManager
}

func NewRewardSettler(config *RewardSettlementConfig, platform platform.Client, billMgr *BillManager) *RewardSettler {
    return &RewardSettler{
        config:   config,
        platform: platform,
        billMgr:  billMgr,
    }
}

// DetermineRewardType 判断奖励类型
func (s *RewardSettler) DetermineRewardType(packetAmounts []int64) int {
    if !s.config.Enabled {
        return RewardTypeNone
    }
    
    // 优先检查豹子
    if s.isLeopard(packetAmounts) {
        return RewardTypeLeopard
    }
    
    // 检查顺子
    if s.isStraight(packetAmounts) {
        return RewardTypeStraight
    }
    
    return RewardTypeNone
}

// CalculateRewardAmount 计算奖励金额
func (s *RewardSettler) CalculateRewardAmount(rewardType int, totalAmount int64) int64 {
    switch rewardType {
    case RewardTypeStraight:
        return int64(float64(totalAmount) * s.config.StraightRewardMultiplier)
    case RewardTypeLeopard:
        return int64(float64(totalAmount) * s.config.LeopardRewardMultiplier)
    default:
        return 0
    }
}

// SettleReward 结算奖励 (每个玩家发相同金额)
func (s *RewardSettler) SettleReward(ctx context.Context, settlement *model.RoundSettlement, players []*dto.PlayerSettleInfo) error {
    if settlement.RewardType == 0 || settlement.RewardAmount == 0 {
        return nil
    }
    
    playerCount := int64(len(players))
    if playerCount == 0 {
        return nil
    }
    
    // 每个玩家发相同金额，平台总扣款 = rewardAmount × playerCount
    totalDeductAmount := settlement.RewardAmount * playerCount
    
    // 1. 平台账户扣款
    platformBill := &model.BillRecord{
        RoundTraceID: settlement.RoundTraceID,
        BizOrderNo:   fmt.Sprintf("REWARD_OUT_%d", settlement.RoundID),
        BillType:     dto.BillTypeSystemReward,
        RoomID:       settlement.RoomID,
        SessionID:    settlement.SessionID,
        RoundID:      settlement.RoundID,
        RoundNo:      settlement.RoundNo,
        UserID:       dto.PlatformAccountID,
        Amount:       -totalDeductAmount,
        Status:       dto.BillStatusProcessing,
        Remark:       fmt.Sprintf("系统奖励支出,类型:%d,局ID:%d,玩家数:%d,每人金额:%d", settlement.RewardType, settlement.RoundID, playerCount, settlement.RewardAmount),
    }
    
    if err := s.billMgr.CreateBill(ctx, platformBill); err != nil {
        return fmt.Errorf("create platform reward bill failed: %w", err)
    }
    
    deductReq := &platform.DeductRequest{
        UserID:  dto.PlatformAccountID,
        Amount:  totalDeductAmount,
        RoundID: settlement.RoundID,
        Remark:  platformBill.Remark,
    }
    
    result, err := s.platform.Deduct(ctx, deductReq)
    if err != nil {
        s.billMgr.UpdateBillStatus(ctx, platformBill.ID, dto.BillStatusFailed, err.Error())
        return fmt.Errorf("deduct reward from platform failed: %w", err)
    }
    
    s.billMgr.UpdateBillSuccess(ctx, platformBill.ID, 0, result.BalanceAfter)
    
    // 2. 给所有参与抢红包的玩家入账 (每人发相同金额)
    for _, player := range players {
        playerBill := &model.BillRecord{
            RoundTraceID: settlement.RoundTraceID,
            BizOrderNo:   fmt.Sprintf("REWARD_IN_%d_%d", settlement.RoundID, player.UserID),
            BillType:     dto.BillTypeSystemReward,
            RoomID:       settlement.RoomID,
            SessionID:    settlement.SessionID,
            RoundID:      settlement.RoundID,
            RoundNo:      settlement.RoundNo,
            UserID:       player.UserID,
            Amount:       settlement.RewardAmount,
            Status:       dto.BillStatusProcessing,
            Remark:       fmt.Sprintf("系统奖励收入,类型:%d,局ID:%d", settlement.RewardType, settlement.RoundID),
        }
        
        if err := s.billMgr.CreateBill(ctx, playerBill); err != nil {
            logger.Error("create player reward bill failed", "user_id", player.UserID, "error", err)
            continue
        }
        
        creditReq := &platform.CreditRequest{
            UserID:  player.UserID,
            Amount:  settlement.RewardAmount,
            RoundID: settlement.RoundID,
            Remark:  playerBill.Remark,
        }
        
        creditResult, err := s.platform.Credit(ctx, creditReq)
        if err != nil {
            s.billMgr.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusFailed, err.Error())
            logger.Error("credit reward to player failed", "user_id", player.UserID, "error", err)
            continue
        }
        
        s.billMgr.UpdateBillSuccess(ctx, playerBill.ID, 0, creditResult.BalanceAfter)
    }
    
    return nil
}

func (s *RewardSettler) isLeopard(amounts []int64) bool {
    if len(amounts) < 2 {
        return false
    }
    first := amounts[0]
    for _, amount := range amounts[1:] {
        if amount != first {
            return false
        }
    }
    return true
}

func (s *RewardSettler) isStraight(amounts []int64) bool {
    if len(amounts) < 2 {
        return false
    }
    
    sorted := make([]int64, len(amounts))
    copy(sorted, amounts)
    
    for i := 0; i < len(sorted)-1; i++ {
        for j := i + 1; j < len(sorted); j++ {
            if sorted[i] > sorted[j] {
                sorted[i], sorted[j] = sorted[j], sorted[i]
            }
        }
    }
    
    for i := 1; i < len(sorted); i++ {
        prevInt := sorted[i-1] / 100
        currInt := sorted[i] / 100
        if currInt != prevInt+1 {
            return false
        }
    }
    return true
}
```

### 6.2 修改 `settlement_service.go`

```go
type SettlementService struct {
    platform       platform.Client
    billMgr        *BillManager
    redis          *cRedis.Client
    traceIDGen     *TraceIDGenerator
    deductSvc      *DeductService
    refundSvc      *RefundService
    rewardSettler  *RewardSettler  // 新增
}

// SettleRoundResult 结算结果 (新增返回值)
type SettleRoundResult struct {
    Success       bool
    RewardType    int    // 奖励类型
    RewardAmount  int64  // 每个玩家的奖励金额
    PlayerCount   int    // 参与玩家数
    TotalReward   int64  // 平台总奖励支出 = RewardAmount × PlayerCount
}

func (s *SettlementService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) (*SettleRoundResult, error) {
    // ... 原有结算逻辑 ...

    // === 新增：结算系统奖励 ===
    if req.RewardType > 0 && req.RewardAmount > 0 {
        settlement := &model.RoundSettlement{
            RewardType:   req.RewardType,
            RewardAmount: req.RewardAmount,
            // ... 其他字段 ...
        }
        
        if err := s.rewardSettler.SettleReward(ctx, settlement, req.Players); err != nil {
            logger.Error("settle system reward failed", "round_id", req.RoundID, "error", err)
            // 奖励结算失败记录，后续重试
        }
    }

    return &SettleRoundResult{
        Success:      true,
        RewardType:   req.RewardType,
        RewardAmount: req.RewardAmount,
        PlayerCount:  len(req.Players),
        TotalReward:  req.RewardAmount * int64(len(req.Players)),
    }, nil
}
```

### 6.3 修改 `game_app_service.go` (关键修改)

```go
// 回合结算 - 同步处理奖励判定和广播，异步处理数据更新和资金结算
func (s *GameAppService) settleRound(ctx context.Context, roomID string, sessionID string, roundID string, roundNo int, results []*GrabResult, minPlayerID string, totalAmount int64, commission int64) error {
    // === 同步部分：判断是否触发奖励 ===
    packetAmounts := make([]int64, 0, len(results))
    for _, r := range results {
        packetAmounts = append(packetAmounts, r.Amount)
    }

    rewardType := s.rewardSettler.DetermineRewardType(packetAmounts)
    var rewardAmount int64
    if rewardType > 0 {
        rewardAmount = s.rewardSettler.CalculateRewardAmount(rewardType, totalAmount)
    }

    // === 同步部分：广播奖励消息 (如果触发) ===
    if rewardType > 0 && rewardAmount > 0 {
        s.broadcastRewardMessage(ctx, roomID, rewardType, rewardAmount, len(results))
    }

    // === 异步部分：发布事件处理数据更新和资金结算 ===
    event := &domain.GameEvent{
        Type:      "round_settle",
        RoomID:    roomID,
        SessionID: sessionID,
        RoundID:   roundID,
        TraceID:   fmt.Sprintf("%d", time.Now().UnixNano()),
        Data: &domain.RoundSettleData{
            RoundNo:      roundNo,
            SenderID:     results[0].SenderID,
            SenderType:   results[0].SenderType,
            TotalAmount:  totalAmount,
            Commission:   commission,
            MinPlayerID:  minPlayerID,
            Results:      results,
            RewardType:   rewardType,      // 新增
            RewardAmount: rewardAmount,    // 新增
        },
    }

    go func() {
        if err := s.eventPublisher.PublishRoundSettle(context.Background(), event); err != nil {
            logger.Error("publish round settle event failed", "error", err)
        }
    }()

    return nil
}

// 广播奖励消息 (延迟2秒发送，避免与回合结束消息间隔太短)
func (s *GameAppService) broadcastRewardMessage(ctx context.Context, roomID string, rewardType int, rewardAmount int64, playerCount int) {
    msg := &message.RewardPush{
        RewardType: rewardType,
        Amount:     rewardAmount,
    }

    // 延迟2秒发送
    go func() {
        time.Sleep(2 * time.Second)
        
        if s.broadcaster != nil {
            s.broadcaster.Broadcast(roomID, message.PushReward, msg, "")
            logger.Info("reward message broadcasted",
                "room_id", roomID,
                "reward_type", rewardType,
                "amount", rewardAmount,
                "player_count", playerCount)
        }
    }()
}
```

### 6.4 修改 `game_event_consumer.go` 中的 `handleRoundSettle`

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
    traceID := parseInt64(event.TraceID)

    return c.db.Transaction(func(tx *gorm.DB) error {
        // 1. 更新 round 状态
        if err := tx.Model(&model.Round{}).
            Where("round_id = ?", parseInt64(event.RoundID)).
            Updates(map[string]interface{}{
                "status":          model.RoundStatusEnded,
                "ended_at":        &now,
                "settle_trace_id": traceID,
            }).Error; err != nil {
            return fmt.Errorf("update round failed: %w", err)
        }

        // 2. 创建 grab record
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
                PacketID:       parseInt64(r.PacketID),
                SessionID:      sessionIDInt64,
                UserID:         parseInt64(r.UserID),
                Amount:         r.Amount,
                IsMin:          isMin,
                IsAutoAssigned: isAutoAssigned,
                GrabbedAt:      now,
            }
            if err := tx.Create(grabRecord).Error; err != nil {
                return fmt.Errorf("create grab record failed: %w", err)
            }
        }

        // 3. 更新 session player 统计
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

        // 4. 更新 session current_round
        if err := tx.Model(&model.GameSession{}).
            Where("session_id = ?", sessionIDInt64).
            Update("current_round", data.RoundNo).Error; err != nil {
            return fmt.Errorf("update session current_round failed: %w", err)
        }

        logger.Info("round data updated",
            "session_id", sessionIDInt64,
            "round_id", event.RoundID,
            "round_no", data.RoundNo)

        // 5. 调用资金结算服务
        players := make([]*settlementDto.PlayerSettleInfo, 0, len(data.Results))
        for _, r := range data.Results {
            players = append(players, &settlementDto.PlayerSettleInfo{
                UserID: parseInt64(r.UserID),
                Amount: r.Amount,
                IsMin:  r.UserID == data.MinPlayerID,
            })
        }

        settleReq := &settlementDto.RoundSettleRequest{
            RoomID:       parseInt64(event.RoomID),
            SessionID:    sessionIDInt64,
            RoundID:      parseInt64(event.RoundID),
            RoundNo:      data.RoundNo,
            SenderID:     parseInt64(data.SenderID),
            SenderType:   data.SenderType,
            TotalAmount:  data.TotalAmount,
            Commission:   data.Commission,
            MinPlayerID:  parseInt64(data.MinPlayerID),
            Players:      players,
            RewardType:   data.RewardType,      // 新增
            RewardAmount: data.RewardAmount,    // 新增
        }

        if err := c.settlementService.SettleRound(ctx, settleReq); err != nil {
            logger.Error("settlement failed",
                "session_id", sessionIDInt64,
                "round_id", event.RoundID,
                "error", err)
        }

        return nil
    })
}
```

### 6.5 修改 `model/bill.go`

```go
type RoundSettlement struct {
    // ... 原有字段 ...
    
    RewardType   int   `gorm:"default:0" json:"reward_type"`    // 奖励类型: 0无, 1顺子, 2豹子
    RewardAmount int64 `gorm:"default:0" json:"reward_amount"`  // 奖励金额
}

const (
    // ... 原有类型 ...
    BillTypeSystemReward = 10  // 系统奖励
)
```

### 6.6 修改 `dto/request.go`

```go
type RoundSettleRequest struct {
    // ... 原有字段 ...
    RewardType   int   // 新增：奖励类型
    RewardAmount int64 // 新增：奖励金额
}
```

### 6.7 修改 `common/message/types.go` 添加事件常量

```go
// 在推送类型常量块中添加
const (
    // ... 原有常量 ...
    PushRoundEnd        = "round_end"
    PushReward          = "reward"           // 新增：奖励推送
    PushGameEnd         = "game_end"
    // ...
)
```

### 6.8 修改 `common/message/payload.go` 添加消息结构

```go
// RewardPush 奖励推送
type RewardPush struct {
    RewardType int   `json:"reward_type"` // 奖励类型: 1顺子, 2豹子
    Amount     int64 `json:"amount"`      // 每个玩家获得的奖励金额
}
```

### 6.9 奖励落库处理

#### 6.9.1 修改 `model/reward.go`

```go
package model

import "time"

const (
    RewardTypeStraight = 1 // 顺子
    RewardTypeLeopard  = 2 // 豹子
)

const (
    TriggerTypeGuarantee   = 1 // 保底触发
    TriggerTypeProbability = 2 // 概率触发
)

type SpecialReward struct {
    ID          int64     `json:"id" gorm:"primaryKey;autoIncrement"`
    RoomID      int64     `json:"room_id" gorm:"index;not null"`
    SessionID   int64     `json:"session_id" gorm:"index;not null"`
    RoundID     int64     `json:"round_id" gorm:"index;not null"`
    RoundNo     int       `json:"round_no" gorm:"not null"`
    RewardType  int       `json:"reward_type" gorm:"not null;comment:1顺子,2豹子"`
    TriggerType int       `json:"trigger_type" gorm:"not null;comment:1保底,2概率"`
    TotalAmount int64     `json:"total_amount" gorm:"not null;comment:红包总额"`
    Amount      int64     `json:"amount" gorm:"not null;comment:每个玩家的奖励金额"`
    PlayerCount int       `json:"player_count" gorm:"not null;comment:参与玩家数"`
    TotalReward int64     `json:"total_reward" gorm:"not null;comment:平台总奖励支出"`
    Details     string    `json:"details" gorm:"type:text"`
    CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (SpecialReward) TableName() string { return "special_rewards" }
```

#### 6.9.2 修改 `domain.RoundSettleData` 添加 TriggerType

```go
type RoundSettleData struct {
    // ... 原有字段 ...
    RewardType   int   // 新增：奖励类型
    RewardAmount int64 // 新增：奖励金额
    TriggerType  int   // 新增：触发类型 (1保底, 2概率)
}
```

#### 6.9.3 修改 `RewardController.DetermineRewardType` 返回触发类型

```go
// 返回值：奖励类型, 触发类型
func (c *RewardController) DetermineRewardType(
    ctx context.Context,
    roomID int64,
    sessionID int64,
    currentRoundNo int,
    maxRounds int,
) (rewardType int, triggerType int) {
    roomConfig := c.getRoomConfig(roomID)
    if roomConfig == nil {
        return RewardTypeNone, 0
    }

    // 1. 检查保底中奖 (返回 TriggerTypeGuarantee)
    if roomConfig.GuaranteeEnabled {
        if rewardType := c.checkGuarantee(ctx, roomID, sessionID, currentRoundNo, maxRounds, roomConfig); rewardType != RewardTypeNone {
            return rewardType, TriggerTypeGuarantee
        }
    }

    // 2. 检查概率触发 (返回 TriggerTypeProbability)
    if roomConfig.ProbabilityEnabled && c.isProbabilityAllowed(ctx) {
        if rewardType := c.checkProbability(roomConfig); rewardType != RewardTypeNone {
            return rewardType, TriggerTypeProbability
        }
    }

    return RewardTypeNone, 0
}
```

#### 6.9.4 修改 `game_app_service.go` 传递 TriggerType

```go
func (s *GameAppService) settleRound(...) error {
    // === 同步部分：判断是否触发奖励 ===
    rewardType, triggerType := s.rewardController.DetermineRewardType(ctx, roomIDInt64, sessionIDInt64, roundNo, maxRounds)
    
    // ... 广播奖励消息 ...

    // === 异步部分：发布事件 ===
    event := &domain.GameEvent{
        // ...
        Data: &domain.RoundSettleData{
            // ...
            RewardType:   rewardType,
            RewardAmount: rewardAmount,
            TriggerType:  triggerType,  // 新增
        },
    }
    // ...
}
```

#### 6.9.5 修改 `game_event_consumer.go` 落库奖励记录

```go
func (c *GameEventConsumer) handleRoundSettle(ctx context.Context, event *domain.GameEvent) error {
    // ... 原有逻辑 ...

    return c.db.Transaction(func(tx *gorm.DB) error {
        // ... 更新 round、创建 grab record、更新统计 ...

        // === 新增：创建奖励记录 ===
        if data.RewardType > 0 && data.RewardAmount > 0 {
            playerCount := len(data.Results)
            totalReward := data.RewardAmount * int64(playerCount)
            
            reward := &model.SpecialReward{
                RoomID:      parseInt64(event.RoomID),
                SessionID:   sessionIDInt64,
                RoundID:     parseInt64(event.RoundID),
                RoundNo:     data.RoundNo,
                RewardType:  data.RewardType,
                TriggerType: data.TriggerType,
                TotalAmount: data.TotalAmount,
                Amount:      data.RewardAmount,
                PlayerCount: playerCount,
                TotalReward: totalReward,
                Details:     fmt.Sprintf("奖励类型:%d,触发类型:%d,玩家数:%d", data.RewardType, data.TriggerType, playerCount),
            }
            if err := tx.Create(reward).Error; err != nil {
                return fmt.Errorf("create special reward record failed: %w", err)
            }
        }

        // === 调用资金结算服务 ===
        // ...

        return nil
    })
}
```

---

## 七、数据库变更

### 7.1 修改 `round_settlement` 表

```sql
ALTER TABLE round_settlement 
ADD COLUMN reward_type INT DEFAULT 0 COMMENT '奖励类型: 0无, 1顺子, 2豹子' AFTER min_player_id,
ADD COLUMN reward_amount BIGINT DEFAULT 0 COMMENT '奖励金额' AFTER reward_type;
```

### 7.2 修改 `special_rewards` 表

```sql
-- 如果表不存在，创建新表
CREATE TABLE IF NOT EXISTS special_rewards (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    room_id BIGINT NOT NULL COMMENT '房间ID',
    session_id BIGINT NOT NULL COMMENT '会话ID',
    round_id BIGINT NOT NULL COMMENT '局ID',
    round_no INT NOT NULL COMMENT '局号',
    reward_type INT NOT NULL COMMENT '奖励类型: 1顺子, 2豹子',
    trigger_type INT NOT NULL DEFAULT 0 COMMENT '触发类型: 1保底, 2概率',
    total_amount BIGINT NOT NULL DEFAULT 0 COMMENT '红包总额',
    amount BIGINT NOT NULL DEFAULT 0 COMMENT '每个玩家的奖励金额',
    player_count INT NOT NULL DEFAULT 0 COMMENT '参与玩家数',
    total_reward BIGINT NOT NULL DEFAULT 0 COMMENT '平台总奖励支出',
    details TEXT COMMENT '详情',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_room_id (room_id),
    INDEX idx_session_id (session_id),
    INDEX idx_round_id (round_id),
    INDEX idx_created_at (created_at)
) COMMENT='特殊奖励记录表';

-- 如果表已存在，修改表结构
ALTER TABLE special_rewards 
    MODIFY COLUMN reward_type INT NOT NULL COMMENT '奖励类型: 1顺子, 2豹子',
    ADD COLUMN IF NOT EXISTS session_id BIGINT NOT NULL DEFAULT 0 COMMENT '会话ID' AFTER room_id,
    ADD COLUMN IF NOT EXISTS round_no INT NOT NULL DEFAULT 0 COMMENT '局号' AFTER round_id,
    ADD COLUMN IF NOT EXISTS trigger_type INT NOT NULL DEFAULT 0 COMMENT '触发类型: 1保底, 2概率' AFTER reward_type,
    ADD COLUMN IF NOT EXISTS total_amount BIGINT NOT NULL DEFAULT 0 COMMENT '红包总额' AFTER trigger_type,
    ADD COLUMN IF NOT EXISTS player_count INT NOT NULL DEFAULT 0 COMMENT '参与玩家数' AFTER amount,
    ADD COLUMN IF NOT EXISTS total_reward BIGINT NOT NULL DEFAULT 0 COMMENT '平台总奖励支出' AFTER player_count,
    ADD INDEX IF NOT EXISTS idx_session_id (session_id);
```

---

## 八、实施步骤

| 步骤 | 任务 | 说明 |
|------|------|------|
| 1 | 新增 `reward_settler.go` | 实现奖励结算逻辑 |
| 2 | 修改 `common/message/types.go` | 添加 `PushReward` 事件常量 |
| 3 | 修改 `common/message/payload.go` | 添加 `RewardPush` 消息结构 |
| 4 | 修改 `model/reward.go` | 修改 `SpecialReward` 结构，添加字段 |
| 5 | 修改 `settlement_service.go` | 集成奖励结算 |
| 6 | 修改 `RewardController.DetermineRewardType` | 返回奖励类型和触发类型 |
| 7 | 修改 `game_app_service.go` | **同步处理奖励判定和广播**，异步发布事件 |
| 8 | 修改 `game_event_consumer.go` | **异步处理数据更新、资金结算、奖励落库** |
| 9 | 修改 `domain.RoundSettleData` | 添加 RewardType、RewardAmount、TriggerType 字段 |
| 10 | 修改 `model/bill.go` | 添加奖励字段 |
| 11 | 修改 `dto/request.go` | 添加奖励字段 |
| 12 | 数据库变更 | 执行 ALTER TABLE |
| 13 | 单元测试 | 编写测试用例 |
| 14 | 集成测试 | 端到端测试 |

---

## 九、注意事项

1. **同步奖励判定**：奖励判定和广播必须在同步部分完成，用户能立即看到
2. **异步数据结算**：数据更新和资金结算可以异步处理，不阻塞主流程
3. **事件数据传递**：奖励类型、金额、触发类型通过事件传递给消费者
4. **失败重试**：异步处理失败时，用户已经知道中奖，后续可以重试
5. **奖励判定时机**：在回合结束时根据红包结果判断，不是生成红包时
6. **奖励发放对象**：当前所有参与抢红包的玩家，每人发相同金额
7. **账务处理**：平台账户扣款总额 = rewardAmount × playerCount
8. **幂等性**：使用 round_trace_id 保证结算幂等
9. **奖励落库**：在事务中创建 `SpecialReward` 记录，用于统计分析和审计追溯
10. **触发类型**：区分保底触发和概率触发，便于运营分析
11. **延迟广播**：奖励消息延迟2秒发送，避免与回合结束消息间隔太短
