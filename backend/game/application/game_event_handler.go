package application

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	settlementApplication "github.com/cashparty/backend/settlement/application"
	settlementDto "github.com/cashparty/backend/settlement/dto"
	"gorm.io/gorm"
)

// dbAccessor 暴露底层 *gorm.DB，供尚未抽象为 repo 方法的原始 DB 操作使用。
// 过渡期保留：后续 Phase 将逐步补齐 repo 方法并移除此接口。
// DBRepositoryImpl 与 GormTransactionImpl 均已实现 DB() 方法。
type dbAccessor interface {
	DB() *gorm.DB
}

// GameEventHandler 处理游戏事件，从 GameEventConsumer 迁移而来。
// 所有 DB 写操作通过 dbRepo.WithTransaction 执行，事务边界与原 consumer 一致。
// 非事务读操作（幂等检查）通过 dbRepo.DB() 执行。
// Phase 3.5：settlement 用例调用从直接依赖 RoundSettleService 改为通过
// settlement/application.SettleAppService facade，统一 Application 层入口。
type GameEventHandler struct {
	dbRepo              domain.DBRepository
	settleAppService    *settlementApplication.SettleAppService
	robotBehaviorEngine *RobotBehaviorEngine
}

// NewGameEventHandler 创建 GameEventHandler 实例。
func NewGameEventHandler(
	dbRepo domain.DBRepository,
	settleAppService *settlementApplication.SettleAppService,
	robotBehaviorEngine *RobotBehaviorEngine,
) *GameEventHandler {
	return &GameEventHandler{
		dbRepo:              dbRepo,
		settleAppService:    settleAppService,
		robotBehaviorEngine: robotBehaviorEngine,
	}
}

// HandleGameEvent 处理游戏事件，实现 messaging.GameEventHandlerInterface。
// 根据 event.EventType 分发到对应的处理方法，业务逻辑与原 consumer 一致。
func (h *GameEventHandler) HandleGameEvent(ctx context.Context, event *domain.GameEvent) error {
	switch event.EventType {
	case domain.GameEventSessionStart:
		return h.handleSessionStart(ctx, event)
	case domain.GameEventPacketCreated:
		return h.handlePacketCreated(ctx, event)
	case domain.GameEventRoundSettle:
		return h.handleRoundSettle(ctx, event)
	case domain.GameEventSessionEnd:
		return h.handleSessionEnd(ctx, event)
	default:
		return fmt.Errorf("unknown event type: %s", event.EventType)
	}
}

// db 返回底层 *gorm.DB，用于非事务读操作（如幂等检查）。
func (h *GameEventHandler) db() *gorm.DB {
	return h.dbRepo.(dbAccessor).DB()
}

func (h *GameEventHandler) handleSessionStart(ctx context.Context, event *domain.GameEvent) error {
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
	if err := h.db().WithContext(ctx).Where("session_id = ?", sessionIDInt64).First(&existingSession).Error; err == nil {
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

	return h.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
		txDB := tx.(dbAccessor).DB()
		if err := txDB.Create(session).Error; err != nil {
			return fmt.Errorf("create session failed: %w", err)
		}

		for _, p := range data.Players {
			player := &model.SessionPlayer{
				SessionID: sessionIDInt64,
				UserID:    parseInt64(p.UserID),
			}

			result := txDB.Where(player).
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

func (h *GameEventHandler) handlePacketCreated(ctx context.Context, event *domain.GameEvent) error {
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
	if err := h.db().WithContext(ctx).Model(&model.Packet{}).
		Where("round_id = ?", parseInt64(event.RoundID)).Count(&packetCount).Error; err == nil && packetCount > 0 {
		logger.Info("packets already created, skip", "round_id", event.RoundID)
		return nil
	}

	now := time.Now()

	if err := h.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
		txDB := tx.(dbAccessor).DB()
		if err := txDB.Model(&model.Round{}).
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
			if err := txDB.Create(packet).Error; err != nil {
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
	if h.robotBehaviorEngine != nil {
		h.robotBehaviorEngine.OnPacketCreated(ctx, event.RoomID, event.RoundID)
	}

	return nil
}

func (h *GameEventHandler) handleRoundSettle(ctx context.Context, event *domain.GameEvent) error {
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
	if err := h.db().WithContext(ctx).Where("round_id = ? AND status = ?", parseInt64(event.RoundID), model.RoundStatusEnded).First(&existingRound).Error; err == nil {
		logger.Info("round already settled, skip", "round_id", event.RoundID)
		return nil
	}

	now := time.Now()
	traceID := parseInt64(event.TraceID)
	if err != nil {
		return fmt.Errorf("invalid trace_id: %w", err)
	}

	if err := h.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
		txDB := tx.(dbAccessor).DB()
		if err := txDB.Model(&model.Round{}).
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
			result := txDB.Where(grabRecord).
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
			if err := txDB.Model(&model.SessionPlayer{}).
				Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(r.UserID)).
				Updates(updates).Error; err != nil {
				return fmt.Errorf("update session player grab failed: %w", err)
			}
		}

		if data.SenderType != domain.SenderTypeSystem && data.SenderType != domain.SenderTypeSystemForced {
			senderIDInt := parseInt64(data.SenderID)
			if senderIDInt != 0 {
				if err := txDB.Model(&model.SessionPlayer{}).
					Where("session_id = ? AND user_id = ?", sessionIDInt64, senderIDInt).
					Updates(map[string]interface{}{
						"send_count": gorm.Expr("send_count + 1"),
						"total_send": gorm.Expr("total_send + ?", data.TotalAmount),
					}).Error; err != nil {
					return fmt.Errorf("update session player send failed: %w", err)
				}
			}
		}

		if err := txDB.Model(&model.GameSession{}).
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
			if err := txDB.Create(reward).Error; err != nil {
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

	if err := h.settleAppService.SettleRound(ctx, settleReq); err != nil {
		return fmt.Errorf("settle round failed: %w", err)
	}

	// Trigger robot send behavior
	if h.robotBehaviorEngine != nil {
		h.robotBehaviorEngine.OnRoundSettle(ctx, event.RoomID, parseInt64(data.MinPlayerID), data.IsGameEnd)
	}

	return nil
}

func (h *GameEventHandler) handleSessionEnd(ctx context.Context, event *domain.GameEvent) error {
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
	if err := h.db().WithContext(ctx).Where("session_id = ?", sessionIDInt64).First(&existingSession).Error; err != nil {
		return fmt.Errorf("query session failed: %w", err)
	}
	if existingSession.Status == model.SessionStatusCompleted {
		logger.Info("session already completed, skip", "session_id", sessionIDInt64)
		return nil
	}

	now := time.Now()

	if err := h.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
		txDB := tx.(dbAccessor).DB()
		updates := map[string]interface{}{
			"status":        model.SessionStatusCompleted,
			"actual_rounds": data.ActualRounds,
			"ended_at":      &now,
			"end_reason":    data.EndReason,
		}
		if err := txDB.Model(&model.GameSession{}).
			Where("session_id = ?", sessionIDInt64).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("update session failed: %w", err)
		}

		// Update each player's total_profit from final results
		for _, fr := range data.FinalResults {
			if err := txDB.Model(&model.SessionPlayer{}).
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
	if err := h.settleAppService.SettleGame(ctx, sessionIDInt64); err != nil {
		return fmt.Errorf("settle game failed: %w", err)
	}

	return nil
}

// parseInt64 将 interface{} 转换为 int64，支持 int64/float64/int/string 类型。
// 迁移自 GameEventConsumer。
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
