package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/kafka"
	lockScripts "github.com/cashparty/backend/common/lock/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	redisKeys "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/model"
	settlementDto "github.com/cashparty/backend/settlement/dto"
	settlementService "github.com/cashparty/backend/settlement/service"
	"github.com/google/uuid"
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
	consumer            *kafka.Consumer
}

func NewGameEventConsumer(
	db *gorm.DB,
	redis *cRedis.Client,
	settlementService *settlementService.SettlementService,
	robotBehaviorEngine RobotBehaviorEngineInterface,
	cfg kafka.ConsumerConfig,
) (*GameEventConsumer, error) {
	c := &GameEventConsumer{
		db:                  db,
		redis:               redis,
		settlementService:   settlementService,
		robotBehaviorEngine: robotBehaviorEngine,
	}
	consumer, err := kafka.NewConsumer(cfg, c.HandleEvent, nil)
	if err != nil {
		return nil, fmt.Errorf("create game event kafka consumer failed: %w", err)
	}
	c.consumer = consumer
	return c, nil
}

func (c *GameEventConsumer) Start(ctx context.Context) error {
	logger.Info("game event consumer started")
	return c.consumer.Start(ctx)
}

// Close 委托给内部 kafka.Consumer，由 bootstrap 统一管理生命周期。
func (c *GameEventConsumer) Close() error {
	return c.consumer.Close()
}

func (c *GameEventConsumer) HandleEvent(ctx context.Context, msg kafka.Message) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("game event consumer panic",
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()

	var event domain.GameEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("unmarshal game event failed", "error", err)
		return fmt.Errorf("unmarshal event failed: %w", err)
	}

	acquired, token, acquireErr := c.tryAcquire(ctx, event.TraceID)
	if acquireErr != nil {
		// Redis 不可用（fail-closed），返回 error 让 Kafka 重试
		return acquireErr
	}
	if !acquired {
		logger.Warn("event already processed", "trace_id", event.TraceID)
		return nil
	}

	// 仅在处理失败时释放抢占锁，让 Kafka 重试能重新进入；
	// 处理成功时保留锁作为 7 天幂等标记，防止重复消费。
	shouldRelease := false
	defer func() {
		if !shouldRelease {
			return
		}
		if releaseErr := c.releaseAcquire(ctx, event.TraceID, token); releaseErr != nil {
			logger.Warn("failed to release acquire lock",
				"trace_id", event.TraceID,
				"error", releaseErr)
		}
	}()

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
		shouldRelease = true
		logger.Error("handle game event failed",
			"event_type", event.EventType,
			"trace_id", event.TraceID,
			"error", err)
		return err
	}

	return nil
}

func (c *GameEventConsumer) handleSessionStart(ctx context.Context, event *domain.GameEvent) error {
	var data domain.SessionStartData
	if err := event.GetPayload(&data); err != nil {
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
	var data domain.PacketCreatedData
	if err := event.GetPayload(&data); err != nil {
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
	var data domain.RoundSettleData
	if err := event.GetPayload(&data); err != nil {
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

		return nil
	}); err != nil {
		return err
	}

	logger.Info("round settled",
		"session_id", sessionIDInt64,
		"round_id", event.RoundID,
		"round_no", data.RoundNo)

	// SettleRound 在主事务之外调用：主事务已提交 round 数据，SettleRound 创建平台账 bill。
	// 若 SettleRound 失败，Kafka 重试重新投递事件；SettleRound 内部幂等（RoundStatusCredited
	// 早返回 + GetBillByRoundTypeAndUser 跳过已创建 bill）保证不重复创建 bill。
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
		return fmt.Errorf("settle round failed: %w", err)
	}

	// Trigger robot send behavior
	if c.robotBehaviorEngine != nil {
		c.robotBehaviorEngine.OnRoundSettle(ctx, event.RoomID, parseInt64(data.MinPlayerID), data.IsGameEnd)
	}

	return nil
}

func (c *GameEventConsumer) handleSessionEnd(ctx context.Context, event *domain.GameEvent) error {
	var data domain.SessionEndData
	if err := event.GetPayload(&data); err != nil {
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

		return nil
	}); err != nil {
		return err
	}

	logger.Info("session ended",
		"session_id", sessionIDInt64,
		"room_id", event.RoomID,
		"actual_rounds", data.ActualRounds)

	// SettleGame 在主事务之外调用：主事务已提交 session 数据，SettleGame 完成平台账结算。
	// 若 SettleGame 失败，Kafka 重试重新投递事件；SettleGame 内部幂等（allSettled 返回 nil）保证不重复结算。
	if err := c.settlementService.SettleGame(ctx, sessionIDInt64); err != nil {
		return fmt.Errorf("settle game failed: %w", err)
	}

	return nil
}

// tryAcquire 用 SetNX 原子抢占事件处理权。
// 返回 (true, token, nil) 表示抢占成功（首次处理），token 为本次持有的随机值，释放锁时需传入。
// 返回 (false, "", nil) 表示已被其他 consumer 处理过（幂等跳过）。
// 返回 (false, "", err) 表示 Redis 不可用（fail-closed），调用方应返回 error 让 Kafka 重试。
// token 用于 releaseAcquire 校验持有者，防止 TTL 过期后误删其他实例的锁（§7.4/§15.5）。
// 业务侧幂等（DB 唯一索引/FirstOrCreate/状态机）仍作为兜底防线。
func (c *GameEventConsumer) tryAcquire(ctx context.Context, traceID string) (bool, string, error) {
	if c.redis == nil {
		return true, "", nil
	}
	token := uuid.New().String()
	key := redisKeys.GameEventProcessedKey(traceID)
	ok, err := c.redis.SetNX(ctx, key, token, 7*24*time.Hour).Result()
	if err != nil {
		logger.Error("tryAcquire SetNX failed, fail-closed to prevent duplicate processing",
			"trace_id", traceID,
			"error", err)
		return false, "", fmt.Errorf("tryAcquire SetNX failed: %w", err)
	}
	return ok, token, nil
}

// releaseAcquire 处理失败时释放抢占，让 Kafka 重试能重新进入。
// 通过 Lua 脚本原子校验 token 后才 DEL，防止 TTL 过期后被其他实例抢占，原持有者误删新持有者的锁（§7.4/§15.5）。
func (c *GameEventConsumer) releaseAcquire(ctx context.Context, traceID string, token string) error {
	if c.redis == nil {
		return nil
	}
	key := redisKeys.GameEventProcessedKey(traceID)
	return lockScripts.ReleaseLockScript.Run(ctx, c.redis, []string{key}, token).Err()
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
