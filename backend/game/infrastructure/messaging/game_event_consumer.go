package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	redisKeys "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/model"
	settlementDto "github.com/cashparty/backend/settlement/dto"
	settlementService "github.com/cashparty/backend/settlement/service"
	"gorm.io/gorm"
)

// RobotBehaviorEngineInterface defines the behavior engine methods used by
// the game event consumer. The application.RobotBehaviorEngine implements
// this interface.
type RobotBehaviorEngineInterface interface {
	OnPacketCreated(ctx context.Context, roomID string, roundID string)
	OnRoundSettle(ctx context.Context, roomID string, minPlayerID int64, isGameEnd bool)
}

type GameEventConsumer struct {
	db                  *gorm.DB
	redis               *cRedis.Client
	settlementService   *settlementService.SettlementService
	robotBehaviorEngine RobotBehaviorEngineInterface
}

func NewGameEventConsumer(
	db *gorm.DB,
	redis *cRedis.Client,
	settlementService *settlementService.SettlementService,
	robotBehaviorEngine RobotBehaviorEngineInterface,
) *GameEventConsumer {
	return &GameEventConsumer{
		db:                  db,
		redis:               redis,
		settlementService:   settlementService,
		robotBehaviorEngine: robotBehaviorEngine,
	}
}

func (c *GameEventConsumer) HandleEvent(ctx context.Context, msg kafka.Message) error {
	var event domain.GameEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("unmarshal game event failed", "error", err)
		return fmt.Errorf("unmarshal event failed: %w", err)
	}

	acquired, acquireErr := c.tryAcquire(ctx, event.TraceID)
	if acquireErr != nil {
		// Redis 不可用（fail-closed），返回 error 让 Kafka 重试
		return acquireErr
	}
	if !acquired {
		logger.Warn("event already processed", "trace_id", event.TraceID)
		return nil
	}

	var err error
	switch event.EventType {
	case domain.GameEventSessionStart:
		err = c.handleSessionStart(ctx, &event)
	case domain.GameEventPacketCreated:
		err = c.handlePacketCreated(ctx, &event)
	case domain.GameEventRoundSettle:
		err = c.handleRoundSettle(ctx, &event)
	case domain.GameEventSessionEnd:
		err = c.handleSessionEnd(ctx, &event)
	default:
		err = fmt.Errorf("unknown event type: %s", event.EventType)
	}

	if err != nil {
		c.releaseAcquire(ctx, event.TraceID)
		logger.Error("handle game event failed",
			"event_type", event.EventType,
			"trace_id", event.TraceID,
			"error", err)
		return err
	}

	return nil
}

func (c *GameEventConsumer) handleSessionStart(ctx context.Context, event *domain.GameEvent) error {
	dataBytes, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal session start data failed: %w", err)
	}

	var data domain.SessionStartData
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return fmt.Errorf("unmarshal session start data failed: %w", err)
	}

	sessionIDInt64, err := strconv.ParseInt(event.SessionID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid session_id: %w", err)
	}

	// 幂等检查：session 已存在则跳过（Kafka 重试时主键冲突会导致死循环）
	var existingSession model.GameSession
	if err := c.db.WithContext(ctx).Where("session_id = ?", sessionIDInt64).First(&existingSession).Error; err == nil {
		logger.Info("session already created, skip", "session_id", sessionIDInt64)
		return nil
	}

	now := time.Now()
	session := &model.GameSession{
		SessionID:   sessionIDInt64,
		RoomID:      parseInt64(event.RoomID),
		RoomNo:      data.RoomNo,
		ConfigID:    data.ConfigID,
		ConfigName:  data.ConfigName,
		RoomFee:     data.RoomFee,
		MaxRounds:   data.MaxRounds,
		PlayerCount: len(data.Players),
		Status:      model.SessionStatusPlaying,
		StartedAt:   &now,
	}

	return c.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(session).Error; err != nil {
			return fmt.Errorf("create session failed: %w", err)
		}

		for _, p := range data.Players {
			player := &model.SessionPlayer{
				SessionID: sessionIDInt64,
				UserID:    parseInt64(p.UserID),
			}

			result := tx.Where(player).
				Assign(model.SessionPlayer{
					RoomID:   parseInt64(event.RoomID),
					Nickname: p.Nickname,
					Avatar:   p.Avatar,
					SeatNo:   p.SeatNo,
					JoinedAt: now,
				}).
				FirstOrCreate(player)

			if result.Error != nil {
				return fmt.Errorf("create or update session player failed: %w", result.Error)
			}
		}

		logger.Info("session created",
			"session_id", session.SessionID,
			"room_id", event.RoomID,
			"player_count", len(data.Players))
		return nil
	})
}

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

	// 幂等检查：该 round 的 packet 已创建则跳过（Kafka 重试时主键冲突会导致死循环）
	var packetCount int64
	if err := c.db.WithContext(ctx).Model(&model.Packet{}).
		Where("round_id = ?", parseInt64(event.RoundID)).Count(&packetCount).Error; err == nil && packetCount > 0 {
		logger.Info("packets already created, skip", "round_id", event.RoundID)
		return nil
	}

	now := time.Now()

	if err := c.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Round{}).
			Where("round_id = ?", parseInt64(event.RoundID)).
			Updates(map[string]interface{}{
				"status":     model.RoundStatusSending,
				"sender_id":  parseInt64(data.SenderID),
				"started_at": &now,
			}).Error; err != nil {
			return fmt.Errorf("update round failed: %w", err)
		}

		for _, p := range data.Packets {
			packet := &model.Packet{
				PacketID: parseInt64(p.PacketID),
				RoundID:  parseInt64(p.RoundID),
				RoomID:   parseInt64(p.RoomID),
				Amount:   p.Amount,
				Position: p.Position,
			}
			if err := tx.Create(packet).Error; err != nil {
				return fmt.Errorf("create packet failed: %w", err)
			}
		}

		logger.Info("round updated and packets created",
			"room_id", event.RoomID,
			"round_id", event.RoundID,
			"session_id", sessionIDInt64,
			"packet_count", len(data.Packets))
		return nil
	}); err != nil {
		return err
	}

	// Trigger robot grab behavior
	if c.robotBehaviorEngine != nil {
		c.robotBehaviorEngine.OnPacketCreated(ctx, event.RoomID, event.RoundID)
	}

	return nil
}

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

	// 幂等检查：round 已是 Ended 状态则跳过。
	// 避免 Kafka 重试时 session_player 的 grab_count/send_count 重复累加、special_reward 重复创建。
	// grab_record 已用 FirstOrCreate 幂等，但 +1 更新和 reward.Create 无法直接幂等化，需在入口拦截。
	var existingRound model.Round
	if err := c.db.WithContext(ctx).Where("round_id = ? AND status = ?", parseInt64(event.RoundID), model.RoundStatusEnded).First(&existingRound).Error; err == nil {
		logger.Info("round already settled, skip", "round_id", event.RoundID)
		return nil
	}

	now := time.Now()
	traceID := parseInt64(event.TraceID)
	if err != nil {
		return fmt.Errorf("invalid trace_id: %w", err)
	}

	if err := c.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Round{}).
			Where("round_id = ?", parseInt64(event.RoundID)).
			Updates(map[string]interface{}{
				"status":          model.RoundStatusEnded,
				"ended_at":        &now,
				"settle_trace_id": traceID,
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
				RoundID:   parseInt64(event.RoundID),
				PacketID:  parseInt64(r.PacketID),
				SessionID: sessionIDInt64,
				UserID:    parseInt64(r.UserID),
			}
			// 用 FirstOrCreate 防止 Kafka 重试时重复插入（按 round_id + user_id 查重）
			result := tx.Where(grabRecord).
				Assign(model.RoundGrabRecord{
					Amount:         r.Amount,
					IsMin:          isMin,
					IsAutoAssigned: isAutoAssigned,
					GrabbedAt:      now,
				}).
				FirstOrCreate(grabRecord)
			if result.Error != nil {
				return fmt.Errorf("create or update grab record failed: %w", result.Error)
			}
		}

		for _, r := range data.Results {
			updates := map[string]interface{}{
				"grab_count": gorm.Expr("grab_count + 1"),
				"total_grab": gorm.Expr("total_grab + ?", r.Amount),
			}
			if err := tx.Model(&model.SessionPlayer{}).
				Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(r.UserID)).
				Updates(updates).Error; err != nil {
				return fmt.Errorf("update session player grab failed: %w", err)
			}
		}

		if data.SenderType != domain.SenderTypeSystem && data.SenderType != domain.SenderTypeSystemForced {
			senderIDInt := parseInt64(data.SenderID)
			if senderIDInt != 0 {
				if err := tx.Model(&model.SessionPlayer{}).
					Where("session_id = ? AND user_id = ?", sessionIDInt64, senderIDInt).
					Updates(map[string]interface{}{
						"send_count": gorm.Expr("send_count + 1"),
						"total_send": gorm.Expr("total_send + ?", data.TotalAmount),
					}).Error; err != nil {
					return fmt.Errorf("update session player send failed: %w", err)
				}
			}
		}

		if err := tx.Model(&model.GameSession{}).
			Where("session_id = ?", sessionIDInt64).
			Update("current_round", data.RoundNo).Error; err != nil {
			return fmt.Errorf("update session current_round failed: %w", err)
		}

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

		logger.Info("round settled",
			"session_id", sessionIDInt64,
			"round_id", event.RoundID,
			"round_no", data.RoundNo)

		players := make([]*settlementDto.PlayerSettleInfo, 0, len(data.Results))
		for _, r := range data.Results {
			players = append(players, &settlementDto.PlayerSettleInfo{
				UserID: parseInt64(r.UserID),
				Amount: r.Amount,
				IsMin:  r.UserID == data.SenderID && data.SenderType != "system",
			})
		}

		settleReq := &settlementDto.RoundSettleRequest{
			RoomID:           parseInt64(event.RoomID),
			SessionID:        sessionIDInt64,
			RoundID:          parseInt64(event.RoundID),
			RoundNo:          data.RoundNo,
			SenderID:         parseInt64(data.SenderID),
			SenderType:       data.SenderType,
			TotalAmount:      data.TotalAmount,
			Commission:       data.Commission,
			RoomFeePerPlayer: data.RoomFeePerPlayer,
			MinPlayerID:      parseInt64(data.MinPlayerID),
			Players:          players,
			RewardType:       data.RewardType,
			RewardAmount:     data.RewardAmount,
		}

		if err := c.settlementService.SettleRound(ctx, settleReq); err != nil {
			// SettleRound 失败必须 return err 触发事务回滚，避免 round 标记 Ended 但平台账未结算。
			//
			// 幂等性分析（Kafka 重试场景）：
			//   - handleRoundSettle 入口检查 round.status == Ended → 已处理则跳过，避免重复进入事务 ✅
			//   - SettleRound 内部：RoundStatusCredited 早返回 + GetBillByRoundTypeAndUser 跳过已创建 bill → 幂等 ✅
			//   - grab_record：FirstOrCreate 按 round_id + user_id 查重 → 幂等 ✅
			//   - session_player grab_count/send_count 的 +1 更新和 special_reward 的 Create 非幂等，
			//     由入口幂等检查兜底；仅在 TOCTOU 窗口（检查与事务执行之间）并发触发时可能重复，
			//     此时由 tryAcquire（SetNX）兜底防止重复消费。
			//
			// 事务一致性：SettleRound 内部用 billMgr.db（非事务 tx），其创建的 bill records 不随主事务回滚。
			// 这是已知设计权衡：SettleRound 的幂等机制保证重试时不会重复创建 bill。
			// 如需严格事务一致性，需让 billMgr 支持 tx 参数（影响面较大，未在本轮修复）。
			return fmt.Errorf("settle round failed: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	// Trigger robot send behavior
	if c.robotBehaviorEngine != nil {
		c.robotBehaviorEngine.OnRoundSettle(ctx, event.RoomID, parseInt64(data.MinPlayerID), data.IsGameEnd)
	}

	return nil
}

func (c *GameEventConsumer) handleSessionEnd(ctx context.Context, event *domain.GameEvent) error {
	dataBytes, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal session end data failed: %w", err)
	}

	var data domain.SessionEndData
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return fmt.Errorf("unmarshal session end data failed: %w", err)
	}

	sessionIDInt64, err := strconv.ParseInt(event.SessionID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid session_id: %w", err)
	}

	// 幂等检查：如果 session 已经是 Completed 状态，跳过更新
	var existingSession model.GameSession
	if err := c.db.WithContext(ctx).Where("session_id = ?", sessionIDInt64).First(&existingSession).Error; err != nil {
		return fmt.Errorf("query session failed: %w", err)
	}
	if existingSession.Status == model.SessionStatusCompleted {
		logger.Info("session already completed, skip", "session_id", sessionIDInt64)
		return nil
	}

	now := time.Now()

	if err := c.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":        model.SessionStatusCompleted,
			"actual_rounds": data.ActualRounds,
			"ended_at":      &now,
			"end_reason":    data.EndReason,
		}
		if err := tx.Model(&model.GameSession{}).
			Where("session_id = ?", sessionIDInt64).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("update session failed: %w", err)
		}

		// Update each player's total_profit from final results
		for _, fr := range data.FinalResults {
			if err := tx.Model(&model.SessionPlayer{}).
				Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(fr.UserID)).
				Update("total_profit", fr.TotalProfit).Error; err != nil {
				return fmt.Errorf("update session player total_profit failed: user_id=%d: %w", parseInt64(fr.UserID), err)
			}
		}

		logger.Info("session ended",
			"session_id", sessionIDInt64,
			"room_id", event.RoomID,
			"actual_rounds", data.ActualRounds)

		// Trigger game-level settlement after session ends.
		// SettleGame 失败必须 return err 触发事务回滚，避免 session 标记 Completed 但结算未完成。
		// 重试时依赖 session 幂等检查（L390-397）和 SettleGame 内部幂等（allSettled 返回 nil）。
		if err := c.settlementService.SettleGame(ctx, sessionIDInt64); err != nil {
			return fmt.Errorf("settle game failed: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

// tryAcquire 用 SetNX 原子抢占事件处理权。
// 返回 (true, nil) 表示抢占成功（首次处理）。
// 返回 (false, nil) 表示已被其他 consumer 处理过（幂等跳过）。
// 返回 (false, err) 表示 Redis 不可用（fail-closed），调用方应返回 error 让 Kafka 重试。
// 业务侧幂等（DB 唯一索引/FirstOrCreate/状态机）仍作为兜底防线。
func (c *GameEventConsumer) tryAcquire(ctx context.Context, traceID string) (bool, error) {
	if c.redis == nil {
		return true, nil
	}
	key := redisKeys.GameEventProcessedKey(traceID)
	ok, err := c.redis.SetNX(ctx, key, 1, 7*24*time.Hour).Result()
	if err != nil {
		logger.Error("tryAcquire SetNX failed, fail-closed to prevent duplicate processing",
			"trace_id", traceID,
			"error", err)
		return false, fmt.Errorf("tryAcquire SetNX failed: %w", err)
	}
	return ok, nil
}

// releaseAcquire 处理失败时释放抢占，让 Kafka 重试能重新进入。
func (c *GameEventConsumer) releaseAcquire(ctx context.Context, traceID string) {
	if c.redis == nil {
		return
	}
	key := redisKeys.GameEventProcessedKey(traceID)
	if err := c.redis.Del(ctx, key).Err(); err != nil {
		logger.Warn("releaseAcquire Del failed",
			"trace_id", traceID,
			"error", err)
	}
}

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
