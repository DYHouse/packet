package messaging

import (
	"context"
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

	if c.isProcessed(ctx, event) {
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
		logger.Error("handle room event failed",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"user_id", event.UserID,
			"error", handleErr)
		return handleErr
	}

	c.markProcessed(ctx, event)
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

func (c *RoomEventConsumer) isProcessed(ctx context.Context, event *domain.RoomEvent) bool {
	if c.redis == nil {
		return false
	}
	key := redisKeys.RoomEventProcessedKey(event.EventID)
	exists, _ := c.redis.Exists(ctx, key).Result()
	return exists > 0
}

func (c *RoomEventConsumer) markProcessed(ctx context.Context, event *domain.RoomEvent) {
	if c.redis == nil {
		return
	}
	key := redisKeys.RoomEventProcessedKey(event.EventID)
	c.redis.Set(ctx, key, "1", 24*time.Hour)
}

func (c *RoomEventConsumer) Start(ctx context.Context) error {
	logger.Info("room event consumer started")
	return c.consumer.Start(ctx)
}
