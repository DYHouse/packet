package messaging

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/cashparty/backend/common/kafka"
	lockScripts "github.com/cashparty/backend/common/lock/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	redisKeys "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/google/uuid"
)

type RoomEventConsumer struct {
	dbRepo   domain.DBRepository
	redis    *cRedis.Client
	consumer *kafka.Consumer
}

func NewRoomEventConsumer(
	dbRepo domain.DBRepository,
	redis *cRedis.Client,
	cfg kafka.ConsumerConfig,
) (*RoomEventConsumer, error) {
	c := &RoomEventConsumer{
		dbRepo: dbRepo,
		redis:  redis,
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

	event, err := domain.ParseRoomEvent(msg.Value)
	if err != nil {
		// fail-closed: return error to trigger common/kafka.Consumer retry + DLQ.
		logger.Error("failed to parse room event", "error", err)
		return fmt.Errorf("parse room event failed: %w", err)
	}

	acquired, token, acquireErr := c.tryAcquire(ctx, event.EventID)
	if acquireErr != nil {
		// Redis 不可用（fail-closed），返回 error 让 Kafka 重试
		return acquireErr
	}
	if !acquired {
		logger.Debug("event already processed, skipping",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"user_id", event.UserID)
		return nil
	}

	// 仅在处理失败时释放抢占锁，让 Kafka 重试能重新进入；
	// 处理成功时保留锁作为 7 天幂等标记，防止重复消费。
	shouldRelease := false
	defer func() {
		if !shouldRelease {
			return
		}
		if releaseErr := c.releaseAcquire(ctx, event.EventID, token); releaseErr != nil {
			logger.Warn("failed to release acquire lock",
				"event_id", event.EventID,
				"error", releaseErr)
		}
	}()

	var handleErr error
	switch event.EventType {
	case domain.RoomEventSpectatorJoin:
		handleErr = c.handleSpectatorJoin(ctx, event)
	case domain.RoomEventSpectatorLeave:
		handleErr = c.handleSpectatorLeave(ctx, event)
	case domain.RoomEventPlayerReady:
		handleErr = c.handlePlayerReady(ctx, event)
	case domain.RoomEventSeatCancel:
		handleErr = c.handleSeatCancel(ctx, event)
	case domain.RoomEventSpectatorKick:
		handleErr = c.handleSpectatorKick(ctx, event)
	default:
		// fail-closed: unknown event type returns error to trigger retry + DLQ.
		logger.Warn("unknown event type",
			"event_type", event.EventType,
			"room_id", event.RoomID)
		return fmt.Errorf("unknown event type: %s", event.EventType)
	}

	if handleErr != nil {
		shouldRelease = true
		logger.Error("handle room event failed",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"user_id", event.UserID,
			"error", handleErr)
		return handleErr
	}

	logger.Info("room event processed",
		"event_type", event.EventType,
		"room_id", event.RoomID,
		"user_id", event.UserID)

	return nil
}

func (c *RoomEventConsumer) syncRoomCounts(ctx context.Context, event *domain.RoomEvent) error {
	pipe := c.redis.Pipeline()
	playersCmd := pipe.HLen(ctx, redisKeys.RoomPlayersKey(event.RoomID))
	spectatorsCmd := pipe.HLen(ctx, redisKeys.RoomSpectatorsKey(event.RoomID))
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

func (c *RoomEventConsumer) handleSpectatorJoin(ctx context.Context, event *domain.RoomEvent) error {
	return c.syncRoomCounts(ctx, event)
}

func (c *RoomEventConsumer) handleSpectatorLeave(ctx context.Context, event *domain.RoomEvent) error {
	return c.syncRoomCounts(ctx, event)
}

func (c *RoomEventConsumer) handlePlayerReady(ctx context.Context, event *domain.RoomEvent) error {
	return c.syncRoomCounts(ctx, event)
}

func (c *RoomEventConsumer) handleSeatCancel(ctx context.Context, event *domain.RoomEvent) error {
	return c.syncRoomCounts(ctx, event)
}

func (c *RoomEventConsumer) handleSpectatorKick(ctx context.Context, event *domain.RoomEvent) error {
	return c.syncRoomCounts(ctx, event)
}

// tryAcquire 用 SetNX 原子抢占事件处理权。
// 返回 (true, token, nil) 表示抢占成功（首次处理），token 为本次持有的随机值，释放锁时需传入。
// 返回 (false, "", nil) 表示已被其他 consumer 处理过（幂等跳过）。
// 返回 (false, "", err) 表示 Redis 不可用（fail-closed），调用方应返回 error 让 Kafka 重试。
// token 用于 releaseAcquire 校验持有者，防止 TTL 过期后误删其他实例的锁（§7.4/§15.5）。
// 业务侧幂等（DB 唯一索引/FirstOrCreate/状态机）仍作为兜底防线。
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, eventID string) (bool, string, error) {
	if c.redis == nil {
		return true, "", nil
	}
	token := uuid.New().String()
	key := redisKeys.RoomEventProcessedKey(eventID)
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
func (c *RoomEventConsumer) releaseAcquire(ctx context.Context, eventID string, token string) error {
	if c.redis == nil {
		return nil
	}
	key := redisKeys.RoomEventProcessedKey(eventID)
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
