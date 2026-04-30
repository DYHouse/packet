# 惩罚系统重构方案

## 一、需求背景

### 现有问题

1. **惩罚记录没有持久化**：只记录到 Redis（24小时过期），没有数据库记录
2. **没有实际扣款**：惩罚只记录了计数，没有调用 Settlement 服务执行扣款
3. **没有分红逻辑**：第2次惩罚后，扣款金额没有分给其他玩家
4. **惩罚金额用途不明确**：没有区分第1次和第2次惩罚的不同用途

### 惩罚规则

| 惩罚次数 | 扣款 | 用途 | 后续 |
|---------|------|------|------|
| 第1次 | 房费 | 系统发红包 | 继续游戏 |
| 第2次 | 房费 | 等待补位结果决定 | 踢出玩家 |

### 第2次惩罚的两种场景

```
第2次惩罚
    ├─ 扣款房费
    ├─ 踢出玩家
    └─ 等待补位（30秒）
        ├─ 补位成功，游戏恢复 → 扣款金额用来发恢复回合的红包
        └─ 补位超时，游戏结束 → 扣款金额分给其他4个玩家
```

---

## 二、完整流程

### 第1次惩罚

```
玩家超时未发红包
    ↓
PenaltyService.ApplyPenalty (count=1)
    ├─ Redis 记录惩罚计数
    └─ Settlement.HandlePenalty
        ├─ 创建惩罚账单 (BillTypePenalty)
        ├─ 扣款房费
        └─ 创建红包账单 (BillTypePenaltyPacket)
    ↓
GameAppService.handleFirstPenalty
    └─ 系统代发红包
    ↓
继续游戏
```

### 第2次惩罚 - 补位成功

```
玩家再次超时未发红包
    ↓
PenaltyService.ApplyPenalty (count=2)
    ├─ Redis 记录惩罚计数
    └─ Settlement.HandlePenalty
        ├─ 创建惩罚账单 (BillTypePenalty)
        ├─ 扣款房费
        └─ 创建冻结账单 (BillTypePenaltyFrozen)
    ↓
GameAppService.handleKickAndReplace
    ├─ 踢出玩家
    ├─ 房间状态 → 中断
    └─ 保存惩罚信息到 Redis
    ↓
等待补位（30秒）
    ↓
补位成功
    ↓
获取分布式锁（防止并发）
    ↓
PenaltyService.SettlePenalty (resumed=true)
    └─ Settlement.SettlePenalty
        ├─ 创建红包账单 (BillTypePenaltyPacket)
        └─ 更新冻结账单状态为成功
    ↓
释放分布式锁
    ↓
GameAppService.handleGameResumed
    └─ 系统发恢复回合的红包
    ↓
继续游戏
```

### 第2次惩罚 - 补位超时

```
玩家再次超时未发红包
    ↓
PenaltyService.ApplyPenalty (count=2)
    ├─ Redis 记录惩罚计数
    └─ Settlement.HandlePenalty
        ├─ 创建惩罚账单 (BillTypePenalty)
        ├─ 扣款房费
        └─ 创建冻结账单 (BillTypePenaltyFrozen)
    ↓
GameAppService.handleKickAndReplace
    ├─ 踢出玩家
    ├─ 房间状态 → 中断
    └─ 保存惩罚信息到 Redis
    ↓
等待补位（30秒超时）
    ↓
获取分布式锁（防止并发）
    ↓
PenaltyService.SettlePenalty (resumed=false)
    └─ Settlement.SettlePenalty
        ├─ 创建分红账单 (BillTypePenaltyShare) × 4
        ├─ 给其他4个玩家打款
        └─ 更新冻结账单状态为成功
    ↓
释放分布式锁
    ↓
GameAppService.handleGameEnd
    └─ 游戏结束
```

---

## 三、详细设计

### 1. 新增账单类型

**文件**：`settlement/model/bill.go`

```go
const (
    BillTypeSendPacket      = 1  // 发红包扣款
    BillTypeGrabPacket      = 2  // 抢红包收入
    BillTypePenalty         = 3  // 惩罚扣款
    BillTypePenaltyShare    = 4  // 惩罚分红（第2次惩罚补位超时）
    BillTypePenaltyPacket   = 5  // 惩罚发红包（第1次惩罚 + 第2次惩罚补位成功）
    BillTypePenaltyFrozen   = 6  // 惩罚冻结（第2次惩罚，等待补位结果）
)
```

### 2. 修改请求结构

**文件**：`settlement/model/request.go`

```go
type PenaltyRequest struct {
    TraceID      string
    RoomID       int64
    SessionID    int64
    RoundID      int64
    UserID       int64
    PenaltyType  string
    Amount       int64
    PenaltyCount int       // 惩罚次数
}

type PenaltySettleRequest struct {
    TraceID      string
    RoomID       int64
    SessionID    int64
    RoundID      int64
    UserID       int64
    Amount       int64
    PenaltyCount int
    Resumed      bool      // 是否恢复游戏
    Recipients   []int64   // 补位超时时，分红给其他玩家
}
```

### 3. 新增惩罚信息 Redis 存储

**文件**：`game/infrastructure/persistence/redis/keys.go`

```go
KeyPendingPenalty = keyPrefix + ":penalty:pending:%s:%s"  // roomID:userID
```

**文件**：`game/infrastructure/persistence/redis/repository.go`

```go
type PendingPenalty struct {
    TraceID      string `json:"trace_id"`
    RoomID       string `json:"room_id"`
    UserID       string `json:"user_id"`
    SessionID    int64  `json:"session_id"`
    RoundID      int64  `json:"round_id"`
    Amount       int64  `json:"amount"`
    PenaltyCount int    `json:"penalty_count"`
    PenaltyType  string `json:"penalty_type"`
    CreatedAt    int64  `json:"created_at"`
}

func (r *RoomRepository) SavePendingPenalty(ctx context.Context, roomID, userID string, penalty *PendingPenalty) error {
    key := PendingPenaltyKey(roomID, userID)
    data, _ := json.Marshal(penalty)
    return r.client.Set(ctx, key, data, 2*time.Minute).Err()
}

func (r *RoomRepository) GetPendingPenalty(ctx context.Context, roomID, userID string) (*PendingPenalty, error) {
    key := PendingPenaltyKey(roomID, userID)
    data, err := r.client.Get(ctx, key).Bytes()
    if err != nil {
        if errors.Is(err, redis.Nil) {
            return nil, nil
        }
        return nil, err
    }
    var penalty PendingPenalty
    if err := json.Unmarshal(data, &penalty); err != nil {
        return nil, err
    }
    return &penalty, nil
}

func (r *RoomRepository) DeletePendingPenalty(ctx context.Context, roomID, userID string) error {
    key := PendingPenaltyKey(roomID, userID)
    return r.client.Del(ctx, key).Err()
}
```

### 4. 新增分布式锁 Key

**文件**：`settlement/infrastructure/persistence/redis/keys.go`

```go
func PenaltySettleLockKey(roomID string, userID int64) string {
    return fmt.Sprintf("cashparty:lock:penalty:settle:%s:%d", roomID, userID)
}
```

### 5. 修改 Settlement 服务

**文件**：`settlement/service/settlement_service.go`

```go
// HandlePenalty 处理惩罚扣款
func (s *SettlementService) HandlePenalty(ctx context.Context, req *model.PenaltyRequest) error {
    // 1. 检查是否已处理
    if s.billMgr.ExistsByRoundTypeAndUser(ctx, req.RoundID, model.BillTypePenalty, req.UserID) {
        logger.Info("penalty already processed",
            "round_id", req.RoundID,
            "user_id", req.UserID,
        )
        return nil
    }

    // 2. 创建惩罚账单
    traceID := idgen.GenerateString()
    if req.TraceID != "" {
        traceID = req.TraceID
    }

    penaltyBill := &model.BillRecord{
        TraceID:   traceID,
        BillType:  model.BillTypePenalty,
        RoomID:    req.RoomID,
        SessionID: req.SessionID,
        RoundID:   req.RoundID,
        UserID:    req.UserID,
        Amount:    -req.Amount,
        Status:    model.BillStatusProcessing,
        Remark:    fmt.Sprintf("惩罚罚款,类型:%s,次数:%d", req.PenaltyType, req.PenaltyCount),
    }

    if err := s.billMgr.CreateBill(ctx, penaltyBill); err != nil {
        return err
    }

    // 3. 执行扣款
    if err := s.executeDeduct(ctx, penaltyBill, req.Amount, req.RoundID); err != nil {
        return err
    }

    // 4. 根据惩罚次数决定用途
    if req.PenaltyCount == 1 {
        // 第1次惩罚：金额用于系统发红包
        packetBill := &model.BillRecord{
            TraceID:   traceID,
            BillType:  model.BillTypePenaltyPacket,
            RoomID:    req.RoomID,
            SessionID: req.SessionID,
            RoundID:   req.RoundID,
            UserID:    req.UserID,
            Amount:    req.Amount,
            Status:    model.BillStatusSuccess,
            Remark:    fmt.Sprintf("惩罚发红包,用户:%d,惩罚次数:1", req.UserID),
        }
        return s.billMgr.CreateBill(ctx, packetBill)
    }

    // 第2次惩罚：先冻结金额，等补位结果出来后再决定用途
    frozenBill := &model.BillRecord{
        TraceID:   traceID,
        BillType:  model.BillTypePenaltyFrozen,
        RoomID:    req.RoomID,
        SessionID: req.SessionID,
        RoundID:   req.RoundID,
        UserID:    req.UserID,
        Amount:    req.Amount,
        Status:    model.BillStatusProcessing,
        Remark:    fmt.Sprintf("惩罚冻结,用户:%d,惩罚次数:2,等待补位结果", req.UserID),
    }
    return s.billMgr.CreateBill(ctx, frozenBill)
}

// SettlePenalty 补位结果出来后，决定惩罚金额用途
func (s *SettlementService) SettlePenalty(ctx context.Context, req *model.PenaltySettleRequest) error {
    // 1. 获取分布式锁，防止补位成功和补位超时并发触发
    lockKey := redis.PenaltySettleLockKey(
        fmt.Sprintf("%d", req.RoomID),
        req.UserID,
    )
    return lock.WithRedisLock(ctx, s.redis, lockKey, 10, func() error {
        // 2. 检查是否已结算
        if s.billMgr.ExistsByRoundTypeAndUser(ctx, req.RoundID, model.BillTypePenaltyPacket, req.UserID) ||
            s.billMgr.ExistsByRoundTypeAndUser(ctx, req.RoundID, model.BillTypePenaltyShare, req.UserID) {
            logger.Info("penalty already settled",
                "round_id", req.RoundID,
                "user_id", req.UserID,
            )
            return nil
        }

        // 3. 根据补位结果决定用途
        if req.Resumed {
            // 补位成功，游戏恢复：金额用于发恢复回合的红包
            packetBill := &model.BillRecord{
                TraceID:   req.TraceID,
                BillType:  model.BillTypePenaltyPacket,
                RoomID:    req.RoomID,
                SessionID: req.SessionID,
                RoundID:   req.RoundID,
                UserID:    req.UserID,
                Amount:    req.Amount,
                Status:    model.BillStatusSuccess,
                Remark:    fmt.Sprintf("惩罚发红包(游戏恢复),用户:%d,惩罚次数:%d", req.UserID, req.PenaltyCount),
            }
            if err := s.billMgr.CreateBill(ctx, packetBill); err != nil {
                return err
            }
        } else {
            // 补位超时，游戏结束：金额分给其他玩家
            if len(req.Recipients) > 0 && req.Amount > 0 {
                shareAmount := req.Amount / int64(len(req.Recipients))

                for _, recipientID := range req.Recipients {
                    shareBill := &model.BillRecord{
                        TraceID:   req.TraceID,
                        BillType:  model.BillTypePenaltyShare,
                        RoomID:    req.RoomID,
                        SessionID: req.SessionID,
                        RoundID:   req.RoundID,
                        UserID:    recipientID,
                        Amount:    shareAmount,
                        Status:    model.BillStatusProcessing,
                        Remark:    fmt.Sprintf("惩罚分红,来自用户:%d", req.UserID),
                    }

                    if err := s.billMgr.CreateBill(ctx, shareBill); err != nil {
                        logger.Error("create penalty share bill failed", "error", err)
                        continue
                    }

                    if err := s.executeCredit(ctx, shareBill, shareAmount, req.RoundID); err != nil {
                        logger.Error("credit penalty share failed", "error", err)
                    }
                }
            }
        }

        // 4. 更新冻结账单状态为成功
        return s.billMgr.UpdateBillStatusByTraceID(ctx, req.TraceID, model.BillStatusSuccess, "")
    })
}
```

### 6. 新增 BillManager 方法

**文件**：`settlement/service/bill_manager.go`

```go
func (m *BillManager) UpdateBillStatusByTraceID(ctx context.Context, traceID string, status int, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	if status == model.BillStatusSuccess {
		now := time.Now()
		updates["settled_at"] = &now
	}
	return m.db.Model(&model.BillRecord{}).
		Where("trace_id = ?", traceID).
		Updates(updates).Error
}
```

### 7. 修改 Game 服务的 PenaltyService

**文件**：`game/application/penalty_service.go`

```go
package application

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	settlementModel "github.com/cashparty/backend/settlement/model"
)

type SettlementClient interface {
	HandlePenalty(ctx context.Context, req *settlementModel.PenaltyRequest) error
	SettlePenalty(ctx context.Context, req *settlementModel.PenaltySettleRequest) error
}

type PenaltyService struct {
	redis      *cRedis.Client
	policy     *domain.PenaltyPolicy
	settlement SettlementClient
}

func NewPenaltyService(
	redis *cRedis.Client,
	policy *domain.PenaltyPolicy,
	settlement SettlementClient,
) *PenaltyService {
	if policy == nil {
		policy = domain.DefaultPenaltyPolicy()
	}
	return &PenaltyService{
		redis:      redis,
		policy:     policy,
		settlement: settlement,
	}
}

func (s *PenaltyService) ApplyPenalty(
	ctx context.Context,
	roomID, userID string,
	penaltyType domain.PenaltyType,
	roomFee int64,
	sessionID, roundID int64,
) (*domain.PenaltyResult, error) {
	// 1. Redis 中记录惩罚计数
	keys := []string{
		redis.PenaltyCountKey(roomID, userID),
		redis.RoomHashKey(roomID),
		redis.RoomPlayersKey(roomID),
		redis.RoundStateKey(roomID),
		redis.PenaltyRecordKey(roomID, userID),
	}

	args := []interface{}{
		userID,
		roomFee,
		time.Now().Unix(),
		penaltyType.String(),
	}

	res, err := s.redis.Eval(ctx, redis.LuaHandlePenalty, keys, args...).Slice()
	if err != nil {
		logger.Error("apply penalty lua failed", "error", err, "room_id", roomID, "user_id", userID)
		return nil, err
	}

	code := parseInt(res[0])
	if code != 0 {
		return nil, fmt.Errorf("handle penalty failed: code=%d", code)
	}

	count := parseInt(res[1])
	amount := parseInt64(res[2])
	kickRequired := parseInt(res[3]) == 1

	logger.Info("penalty applied in redis",
		"room_id", roomID,
		"user_id", userID,
		"penalty_type", penaltyType,
		"count", count,
		"amount", amount,
		"kick_required", kickRequired,
	)

	// 2. 调用 settlement 服务执行扣款
	traceID := idgen.GenerateString()
	penaltyReq := &settlementModel.PenaltyRequest{
		TraceID:      traceID,
		RoomID:       parseInt64(roomID),
		SessionID:    sessionID,
		RoundID:      roundID,
		UserID:       parseInt64(userID),
		PenaltyType:  penaltyType.String(),
		Amount:       amount,
		PenaltyCount: count,
	}

	if err := s.settlement.HandlePenalty(ctx, penaltyReq); err != nil {
		logger.Error("settlement handle penalty failed",
			"error", err,
			"room_id", roomID,
			"user_id", userID,
			"trace_id", traceID,
		)
	}

	return &domain.PenaltyResult{
		Applied:      true,
		Amount:       amount,
		Count:        count,
		KickRequired: kickRequired,
		Reason:       penaltyType.String(),
		TraceID:      traceID,
	}, nil
}

// SettlePenalty 补位结果出来后，决定惩罚金额用途
func (s *PenaltyService) SettlePenalty(
	ctx context.Context,
	roomID, userID string,
	sessionID, roundID int64,
	amount int64,
	penaltyCount int,
	traceID string,
	resumed bool,
	recipients []int64,
) error {
	return s.settlement.SettlePenalty(ctx, &settlementModel.PenaltySettleRequest{
		TraceID:      traceID,
		RoomID:       parseInt64(roomID),
		SessionID:    sessionID,
		RoundID:      roundID,
		UserID:       parseInt64(userID),
		Amount:       amount,
		PenaltyCount: penaltyCount,
		Resumed:      resumed,
		Recipients:   recipients,
	})
}
```

### 8. 修改 domain.PenaltyResult

**文件**：`game/domain/penalty.go`

```go
type PenaltyResult struct {
	Applied      bool
	Amount       int64
	Count        int
	KickRequired bool
	Reason       string
	TraceID      string  // 新增：用于后续结算
}
```

### 9. 修改 GameAppService

**文件**：`game/application/game_app_service.go`

```go
func (s *GameAppService) OnSendTimeout(ctx context.Context, roomID, userID string) {
	meta, err := s.repo.GetRoomMeta(ctx, roomID)
	if err != nil {
		logger.Error("failed to get room meta", "room_id", roomID, "error", err)
		return
	}

	result, err := s.penaltyService.ApplyPenalty(
		ctx,
		roomID,
		userID,
		domain.PenaltyTypeSendTimeout,
		meta.RoomFee,
		meta.SessionID,
		meta.CurrentRoundID,
	)
	if err != nil {
		logger.Error("failed to apply penalty", "room_id", roomID, "user_id", userID, "error", err)
		return
	}

	if result.Count == 1 {
		// 第1次惩罚：系统发红包
		s.handleFirstPenalty(ctx, roomID, userID, meta)
	} else if result.KickRequired {
		// 第2次惩罚：踢出玩家，保存惩罚信息，等待补位
		s.handleKickAndReplace(ctx, roomID, userID, meta.RoomFee)

		// 保存惩罚信息到 Redis，等补位结果出来后使用
		s.repo.SavePendingPenalty(ctx, roomID, userID, &redis.PendingPenalty{
			TraceID:      result.TraceID,
			RoomID:       roomID,
			UserID:       userID,
			SessionID:    meta.SessionID,
			RoundID:      meta.CurrentRoundID,
			Amount:       result.Amount,
			PenaltyCount: result.Count,
			PenaltyType:  result.Reason,
			CreatedAt:    time.Now().Unix(),
		})
	}
}

func (s *GameAppService) handleFirstPenalty(ctx context.Context, roomID, userID string, meta *domain.RoomMeta) {
	// 系统发红包逻辑（已有实现）
	logger.Info("first penalty, system will send packet",
		"room_id", roomID,
		"user_id", userID,
		"amount", meta.RoomFee,
	)
}

func (s *GameAppService) OnReplaceTimeout(ctx context.Context, roomID, userID string) {
	// 1. 获取惩罚信息
	penalty, err := s.repo.GetPendingPenalty(ctx, roomID, userID)
	if err != nil || penalty == nil {
		logger.Warn("no pending penalty found for replace timeout",
			"room_id", roomID,
			"user_id", userID,
		)
	} else {
		// 2. 获取其他玩家作为分红接收者
		recipients := s.getPenaltyRecipients(ctx, roomID, userID)

		// 3. 调用 SettlePenalty，补位失败，金额分给其他玩家
		if err := s.penaltyService.SettlePenalty(
			ctx,
			roomID, userID,
			penalty.SessionID, penalty.RoundID,
			penalty.Amount, penalty.PenaltyCount,
			penalty.TraceID,
			false,
			recipients,
		); err != nil {
			logger.Error("settle penalty failed",
				"room_id", roomID,
				"user_id", userID,
				"error", err,
			)
		}

		// 4. 删除惩罚信息
		s.repo.DeletePendingPenalty(ctx, roomID, userID)
	}

	// 5. 处理游戏结束
	s.handleGameEnd(ctx, roomID)
}

func (s *GameAppService) OnReplaceSuccess(ctx context.Context, roomID, oldUserID, newUserID string) {
	// 1. 获取惩罚信息
	penalty, err := s.repo.GetPendingPenalty(ctx, roomID, oldUserID)
	if err != nil || penalty == nil {
		logger.Warn("no pending penalty found for replace success",
			"room_id", roomID,
			"user_id", oldUserID,
		)
	} else {
		// 2. 调用 SettlePenalty，补位成功，金额用于发红包
		if err := s.penaltyService.SettlePenalty(
			ctx,
			roomID, oldUserID,
			penalty.SessionID, penalty.RoundID,
			penalty.Amount, penalty.PenaltyCount,
			penalty.TraceID,
			true,
			nil,
		); err != nil {
			logger.Error("settle penalty failed",
				"room_id", roomID,
				"user_id", oldUserID,
				"error", err,
			)
		}

		// 3. 删除惩罚信息
		s.repo.DeletePendingPenalty(ctx, roomID, oldUserID)
	}

	// 4. 处理游戏恢复
	s.handleGameResumed(ctx, roomID, newUserID)
}

func (s *GameAppService) getPenaltyRecipients(ctx context.Context, roomID, excludeUserID string) []int64 {
	stateData, err := s.repo.GetRoomStateData(ctx, roomID)
	if err != nil {
		return nil
	}

	var recipients []int64
	for _, player := range stateData.Players {
		if player.UserID != excludeUserID {
			recipients = append(recipients, parseInt64(player.UserID))
		}
	}

	return recipients
}
```

### 10. 修改 Bootstrap 容器

**文件**：`game/bootstrap/container.go`

```go
// PenaltyService 依赖 SettlementClient，需要通过 gRPC 或直接调用
// 方案1：直接引用 settlement 包（单体架构）
// 方案2：通过 gRPC 调用（微服务架构）

// 当前项目是单体架构，直接引用 settlement 包
penaltyService := application.NewPenaltyService(
    redis,
    nil,
    settlementService,  // settlement.SettlementService 实现了 SettlementClient 接口
)
```

---

## 四、需要修改的文件清单

### Settlement 服务

| 文件 | 修改内容 |
|------|---------|
| `settlement/model/bill.go` | 新增 `BillTypePenaltyPacket` 和 `BillTypePenaltyFrozen` 常量 |
| `settlement/model/request.go` | 修改 `PenaltyRequest`（新增 `PenaltyCount`），新增 `PenaltySettleRequest` |
| `settlement/service/settlement_service.go` | 修改 `HandlePenalty`，新增 `SettlePenalty` |
| `settlement/service/bill_manager.go` | 新增 `UpdateBillStatusByTraceID` 方法 |
| `settlement/infrastructure/persistence/redis/keys.go` | 新增 `PenaltySettleLockKey` |

### Game 服务

| 文件 | 修改内容 |
|------|---------|
| `game/domain/penalty.go` | `PenaltyResult` 新增 `TraceID` 字段 |
| `game/application/penalty_service.go` | 新增 `SettlementClient` 接口，修改 `ApplyPenalty`，新增 `SettlePenalty` |
| `game/application/game_app_service.go` | 修改 `OnSendTimeout`，修改 `OnReplaceTimeout`，新增 `OnReplaceSuccess` |
| `game/infrastructure/persistence/redis/keys.go` | 新增 `KeyPendingPenalty` |
| `game/infrastructure/persistence/redis/repository.go` | 新增 `SavePendingPenalty`、`GetPendingPenalty`、`DeletePendingPenalty` |
| `game/bootstrap/container.go` | 修改 `PenaltyService` 初始化，注入 `SettlementClient` |

---

## 五、注意事项

### 1. 惩罚信息的保存

- **存储位置**：Redis
- **Key 格式**：`cashparty:penalty:pending:{roomID}:{userID}`
- **TTL**：2 分钟（补位超时 30 秒 + 缓冲时间）
- **数据格式**：JSON

```json
{
    "trace_id": "abc123",
    "room_id": "12345",
    "user_id": "67890",
    "session_id": 100,
    "round_id": 200,
    "amount": 500,
    "penalty_count": 2,
    "penalty_type": "send_timeout",
    "created_at": 1775820492
}
```

### 2. 分布式锁

- **目的**：防止补位成功和补位超时并发触发 `SettlePenalty`
- **锁 Key**：`cashparty:lock:penalty:settle:{roomID}:{userID}`
- **锁超时**：10 秒
- **实现方式**：使用现有的 `lock.WithRedisLock`

```go
lockKey := redis.PenaltySettleLockKey(roomID, userID)
return lock.WithRedisLock(ctx, s.redis, lockKey, 10, func() error {
    // 检查是否已结算
    // 执行结算逻辑
})
```

### 3. 幂等性

- **HandlePenalty**：通过 `ExistsByRoundTypeAndUser` 检查是否已处理
- **SettlePenalty**：通过分布式锁 + `ExistsByRoundTypeAndUser` 双重检查

### 4. 补位超时时间

- 当前设置为 30 秒
- 惩罚信息的 TTL 为 2 分钟，远大于补位超时时间
- 确保在补位超时后，惩罚信息仍然可用

### 5. 错误处理

- **扣款失败**：不影响惩罚计数，后续可以重试
- **分红失败**：记录错误日志，不影响其他玩家的分红
- **结算失败**：记录错误日志，后续可以重试

### 6. 账单记录查询

- 所有惩罚相关的账单都记录在 `bill_record` 表中
- 可以通过 `bill_type` 区分不同类型的账单
- 可以通过 `trace_id` 关联同一次惩罚的所有账单

---

## 六、账单类型说明

| 账单类型 | 值 | 说明 | 金额 | 触发场景 |
|---------|---|------|------|---------|
| BillTypePenalty | 3 | 惩罚扣款 | 负数 | 每次惩罚都会创建 |
| BillTypePenaltyPacket | 5 | 惩罚发红包 | 正数 | 第1次惩罚 / 第2次惩罚补位成功 |
| BillTypePenaltyFrozen | 6 | 惩罚冻结 | 正数 | 第2次惩罚，等待补位结果 |
| BillTypePenaltyShare | 4 | 惩罚分红 | 正数 | 第2次惩罚补位超时 |

### 账单关联关系

```
第1次惩罚：
  BillTypePenalty (trace_id=abc, amount=-500)
  BillTypePenaltyPacket (trace_id=abc, amount=500)

第2次惩罚（补位成功）：
  BillTypePenalty (trace_id=def, amount=-500)
  BillTypePenaltyFrozen (trace_id=def, amount=500, status=processing)
  → 补位成功后
  BillTypePenaltyPacket (trace_id=def, amount=500)
  BillTypePenaltyFrozen (trace_id=def, status=success)

第2次惩罚（补位超时）：
  BillTypePenalty (trace_id=ghi, amount=-500)
  BillTypePenaltyFrozen (trace_id=ghi, amount=500, status=processing)
  → 补位超时后
  BillTypePenaltyShare (trace_id=ghi, user_id=1, amount=125)
  BillTypePenaltyShare (trace_id=ghi, user_id=2, amount=125)
  BillTypePenaltyShare (trace_id=ghi, user_id=3, amount=125)
  BillTypePenaltyShare (trace_id=ghi, user_id=4, amount=125)
  BillTypePenaltyFrozen (trace_id=ghi, status=success)
```

---

## 七、实施步骤

1. 修改 `settlement/model/bill.go`，新增账单类型
2. 修改 `settlement/model/request.go`，修改请求结构
3. 修改 `settlement/service/bill_manager.go`，新增方法
4. 修改 `settlement/service/settlement_service.go`，修改和新增方法
5. 修改 `game/domain/penalty.go`，新增 TraceID 字段
6. 修改 `game/infrastructure/persistence/redis/keys.go`，新增 Key
7. 修改 `game/infrastructure/persistence/redis/repository.go`，新增方法
8. 修改 `game/application/penalty_service.go`，重构惩罚服务
9. 修改 `game/application/game_app_service.go`，修改调用逻辑
10. 修改 `game/bootstrap/container.go`，修改依赖注入
11. 数据库迁移，确保 bill_record 表支持新的 bill_type
12. 测试验证
