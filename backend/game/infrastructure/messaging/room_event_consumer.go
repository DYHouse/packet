package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	redisKeys "github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

type RoomEventConsumer struct {
	dbRepo   domain.DBRepository
	redis    *cRedis.Client
	consumer *kafka.Consumer
}

func NewRoomEventConsumer(
	dbRepo domain.DBRepository,
	redis *cRedis.Client,
	cfg *kafka.ConsumerConfig,
) *RoomEventConsumer {
	c := &RoomEventConsumer{
		dbRepo: dbRepo,
		redis:  redis,
	}
	c.consumer = kafka.NewConsumerWithConfig(cfg, c.handleMessage)
	return c
}

func (c *RoomEventConsumer) handleMessage(ctx context.Context, msg kafka.Message) error {
	event, err := domain.ParseRoomEvent(msg.Value)
	if err != nil {
		logger.Error("failed to parse room event", "error", err)
		return nil
	}

	acquired, acquireErr := c.tryAcquire(ctx, event)
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
		logger.Warn("unknown event type",
			"event_type", event.EventType,
			"room_id", event.RoomID)
		return nil
	}

	if handleErr != nil {
		c.releaseAcquire(ctx, event)
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
// 返回 (true, nil) 表示抢占成功（首次处理）。
// 返回 (false, nil) 表示已被其他 consumer 处理过（幂等跳过）。
// 返回 (false, err) 表示 Redis 不可用（fail-closed），调用方应返回 error 让 Kafka 重试。
// 业务侧幂等（DB 唯一索引/FirstOrCreate/状态机）仍作为兜底防线。
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, event *domain.RoomEvent) (bool, error) {
	if c.redis == nil {
		return true, nil
	}
	key := redisKeys.RoomEventProcessedKey(event.EventID)
	ok, err := c.redis.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil {
		logger.Error("tryAcquire SetNX failed, fail-closed to prevent duplicate processing",
			"event_id", event.EventID,
			"error", err)
		return false, fmt.Errorf("tryAcquire SetNX failed: %w", err)
	}
	return ok, nil
}

// releaseAcquire 处理失败时释放抢占，让 Kafka 重试能重新进入。
func (c *RoomEventConsumer) releaseAcquire(ctx context.Context, event *domain.RoomEvent) {
	if c.redis == nil {
		return
	}
	key := redisKeys.RoomEventProcessedKey(event.EventID)
	if err := c.redis.Del(ctx, key).Err(); err != nil {
		logger.Warn("releaseAcquire Del failed",
			"event_id", event.EventID,
			"error", err)
	}
}

func (c *RoomEventConsumer) Start(ctx context.Context) error {
	logger.Info("room event consumer started")
	return c.consumer.Start(ctx)
}
