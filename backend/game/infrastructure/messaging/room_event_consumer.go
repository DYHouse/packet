package messaging

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/kafka"
	lockScripts "github.com/cashparty/backend/common/lock/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/common/trace"
	"github.com/cashparty/backend/game/domain/events"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"github.com/google/uuid"
)

type RoomEventConsumer struct {
	dbRepo   repository.DBRepository
	roomRepo repository.RoomRepository
	redis    cRedis.RedisClient
	consumer *kafka.Consumer
}

func NewRoomEventConsumer(
	dbRepo repository.DBRepository,
	roomRepo repository.RoomRepository,
	redis cRedis.RedisClient,
	cfg kafka.ConsumerConfig,
) (*RoomEventConsumer, error) {
	c := &RoomEventConsumer{
		dbRepo:   dbRepo,
		roomRepo: roomRepo,
		redis:    redis,
	}
	consumer, err := kafka.NewConsumer(cfg, c.handleMessage, nil)
	if err != nil {
		return nil, fmt.Errorf("create room event kafka consumer failed: %w", err)
	}
	c.consumer = consumer
	return c, nil
}

func (c *RoomEventConsumer) handleMessage(ctx context.Context, msg kafka.Message) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("room event consumer panic",
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()

	event, err := events.ParseRoomEvent(msg.Value)
	if err != nil {
		// fail-closed: return error to trigger common/kafka.Consumer retry + DLQ.
		logger.Error("failed to parse room event", "error", err)
		return fmt.Errorf("parse room event failed: %w", err)
	}

	// 从 event 恢复 TraceID 到 context，使下游日志/DB 操作可关联
	if event.TraceID != "" {
		ctx = trace.WithTraceID(ctx, event.TraceID)
	}

	acquired, token, acquireErr := c.tryAcquire(ctx, event.RoomID, event.EventID)
	if acquireErr != nil {
		// Redis 不可用（fail-closed），返回 error 让 Kafka 重试
		return acquireErr
	}
	if !acquired {
		logger.Debug("event already processed, skipping",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"user_id", event.UserID,
			"trace_id", event.TraceID)
		return nil
	}

	// 仅在处理失败时释放抢占锁，让 Kafka 重试能重新进入；
	// 处理成功时保留锁作为 7 天幂等标记，防止重复消费。
	shouldRelease := false
	defer func() {
		if !shouldRelease {
			return
		}
		if releaseErr := c.releaseAcquire(ctx, event.RoomID, event.EventID, token); releaseErr != nil {
			logger.Warn("failed to release acquire lock",
				"event_id", event.EventID,
				"error", releaseErr)
		}
	}()

	var handleErr error
	switch event.EventType {
	case events.RoomEventSubstitute:
		handleErr = c.handleSubstitute(ctx, event)
	case events.RoomEventSpectatorKick:
		handleErr = c.handleSpectatorKick(ctx, event)
	case events.RoomEventSeatCancel:
		handleErr = c.handleSeatCancel(ctx, event)
	case events.RoomEventSpectatorJoin, events.RoomEventSpectatorLeave,
		events.RoomEventPlayerReady,
		events.RoomEventQueueJoin, events.RoomEventQueueLeave:
		// 这些事件仅影响计数，无需 snapshot 写入
		handleErr = c.syncRoomCounts(ctx, event)
	default:
		// fail-closed: unknown event type returns error to trigger retry + DLQ.
		logger.Warn("unknown event type",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"trace_id", event.TraceID)
		return fmt.Errorf("unknown event type: %s", event.EventType)
	}

	if handleErr != nil {
		shouldRelease = true
		logger.Error("handle room event failed",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"user_id", event.UserID,
			"error", handleErr,
			"trace_id", event.TraceID)
		return handleErr
	}

	logger.Info("room event processed",
		"event_type", event.EventType,
		"room_id", event.RoomID,
		"user_id", event.UserID,
		"trace_id", event.TraceID)

	return nil
}

func (c *RoomEventConsumer) syncRoomCounts(ctx context.Context, event *events.RoomEvent) error {
	pipe := c.redis.Pipeline()
	playersCmd := pipe.HLen(ctx, rediskeys.RoomPlayersKey(event.RoomID))
	spectatorsCmd := pipe.HLen(ctx, rediskeys.RoomSpectatorsKey(event.RoomID))
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	playerCount := int(playersCmd.Val())
	spectatorCount := int(spectatorsCmd.Val())

	return c.dbRepo.RoomDBRepo().UpdateRoom(ctx, event.RoomID, map[string]interface{}{
		"player_count":    playerCount,
		"spectator_count": spectatorCount,
	})
}

// tryAcquire 用 SetNX 原子抢占事件处理权。
// 返回 (true, token, nil) 表示抢占成功（首次处理），token 为本次持有的随机值，释放锁时需传入。
// 返回 (false, "", nil) 表示已被其他 consumer 处理过（幂等跳过）。
// 返回 (false, "", err) 表示 Redis 不可用（fail-closed），调用方应返回 error 让 Kafka 重试。
// token 用于 releaseAcquire 校验持有者，防止 TTL 过期后误删其他实例的锁（§7.4/§15.5）。
// 业务侧幂等（DB 唯一索引/FirstOrCreate/状态机）仍作为兜底防线。
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, roomID, eventID string) (bool, string, error) {
	if c.redis == nil {
		return true, "", nil
	}
	token := uuid.New().String()
	key := rediskeys.RoomEventProcessedKey(roomID, eventID)
	ok, err := c.redis.SetNX(ctx, key, token, 7*24*time.Hour).Result()
	if err != nil {
		logger.Error("tryAcquire SetNX failed, fail-closed to prevent duplicate processing",
			"event_id", eventID,
			"error", err)
		return false, "", fmt.Errorf("tryAcquire SetNX failed: %w", err)
	}
	return ok, token, nil
}

// releaseAcquire 处理失败时释放抢占，让 Kafka 重试能重新进入。
// 通过 Lua 脚本原子校验 token 后才 DEL，防止 TTL 过期后被其他实例抢占，原持有者误删新持有者的锁（§7.4/§15.5）。
func (c *RoomEventConsumer) releaseAcquire(ctx context.Context, roomID, eventID string, token string) error {
	if c.redis == nil {
		return nil
	}
	key := rediskeys.RoomEventProcessedKey(roomID, eventID)
	return lockScripts.ReleaseLockScript.Run(ctx, c.redis, []string{key}, token).Err()
}

func (c *RoomEventConsumer) Start(ctx context.Context) error {
	logger.Info("room event consumer started")
	return c.consumer.Start(ctx)
}

// Close 委托给内部 kafka.Consumer，由 bootstrap 统一管理生命周期。
func (c *RoomEventConsumer) Close() error {
	return c.consumer.Close()
}

// handleSubstitute 处理替补事件：
// 1. 插入替补者 snapshot（source 来自 payload.Source：queue/spectator，向前兼容 "substitute"）
// 2. 替补者首次入会话则插入 session_player（聚合表一人一行）
// 3. 同步 rooms 表计数
// 扣款已在 game 层同步完成（tryAutoSubstitute / SetReady），消费者侧不再扣款。
func (c *RoomEventConsumer) handleSubstitute(ctx context.Context, event *events.RoomEvent) error {
	var payload events.SubstitutePayload
	if err := event.GetPayload(&payload); err != nil {
		return fmt.Errorf("parse substitute payload failed: %w", err)
	}

	meta, err := c.roomRepo.GetRoomMeta(ctx, event.RoomID)
	if err != nil {
		return fmt.Errorf("get room meta failed: %w", err)
	}
	if meta == nil || meta.CurrentSessionID == "" {
		logger.Warn("substitute event: room meta missing session_id",
			"room_id", event.RoomID,
			"trace_id", event.TraceID)
		return c.syncRoomCounts(ctx, event)
	}

	// 所有 ID 统一从 RoomMeta 拿（Redis 中已有，无需查 DB）
	sessionID := converter.ParseID(meta.CurrentSessionID)
	roundID := converter.ParseID(meta.CurrentRoundID)
	roundNo := meta.CurrentRound
	roomIDInt := converter.ParseID(event.RoomID)
	substituteUserIDInt := converter.ParseID(event.UserID)
	now := time.Now()

	// snapshot 来源：优先使用 payload.Source（区分排队替补/观众补位），
	// 空值向前兼容旧消息（视为 "substitute"）。
	source := payload.Source
	if source == "" {
		source = "substitute"
	}

	// 事务内更新 snapshot + session_players（多表一致性）
	if err := c.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		// 1. 插入替补者 snapshot
		if roundID > 0 {
			seatNo := payload.SeatNo
			if err := tx.SnapshotRepo().AddPlayerMidRound(ctx, &model.RoundPlayerSnapshot{
				SessionID:   sessionID,
				RoundID:     roundID,
				RoundNo:     roundNo,
				UserID:      substituteUserIDInt,
				Role:        "player",
				SeatNo:      &seatNo,
				JoinedAt:    now,
				ActiveStart: now,
				Source:      source,
			}); err != nil {
				return fmt.Errorf("add substitute snapshot failed: %w", err)
			}
		}

		// 2. 替补者首次入会话则插入 session_player（聚合表一人一行）
		// 注：session_players 表仅做聚合统计，不加 status/left_at 字段
		// 玩家流转状态由 round_player_snapshot 事实表记录
		if err := tx.SessionDBRepo().UpsertPlayer(ctx, &model.SessionPlayer{
			SessionID: sessionID,
			RoomID:    roomIDInt,
			UserID:    substituteUserIDInt,
			Nickname:  payload.Nickname,
			Avatar:    payload.Avatar,
			SeatNo:    payload.SeatNo,
			JoinedAt:  now,
		}); err != nil {
			return fmt.Errorf("upsert substitute session player failed: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	// 3. 同步 rooms 表计数（移出事务，遵循 §15 短事务原则）
	return c.syncRoomCounts(ctx, event)
}

// handleSpectatorKick 处理旁观者被踢：仅标记 snapshot 离开
func (c *RoomEventConsumer) handleSpectatorKick(ctx context.Context, event *events.RoomEvent) error {
	// 旁观者被踢不影响 session_players（旁观者不入 session_players 表）
	if err := c.markSnapshotLeft(ctx, event, "kicked"); err != nil {
		return err
	}
	return c.syncRoomCounts(ctx, event)
}

// handleSeatCancel 处理玩家离座：标记 snapshot 离开
func (c *RoomEventConsumer) handleSeatCancel(ctx context.Context, event *events.RoomEvent) error {
	if err := c.markSnapshotLeft(ctx, event, "user_request"); err != nil {
		return err
	}
	return c.syncRoomCounts(ctx, event)
}

// markSnapshotLeft 通用辅助：从 RoomMeta 拿 sessionID/roundID，标记玩家在某轮离开
func (c *RoomEventConsumer) markSnapshotLeft(ctx context.Context, event *events.RoomEvent, reason string) error {
	meta, err := c.roomRepo.GetRoomMeta(ctx, event.RoomID)
	if err != nil {
		return err
	}
	if meta == nil || meta.CurrentSessionID == "" || meta.CurrentRoundID == "" {
		return nil
	}
	sessionID := converter.ParseID(meta.CurrentSessionID)
	roundID := converter.ParseID(meta.CurrentRoundID)
	userIDInt := converter.ParseID(event.UserID)
	if sessionID == 0 || roundID == 0 || userIDInt == 0 {
		return nil
	}
	return c.dbRepo.SnapshotDBRepo().MarkPlayerLeft(ctx, sessionID, roundID, userIDInt,
		time.Now(), reason)
}
