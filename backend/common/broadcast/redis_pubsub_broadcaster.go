package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
)

type RedisPubSubBroadcaster struct {
	redis   *cRedis.Client
	channel string
}

func NewRedisPubSubBroadcaster(redis *cRedis.Client, channel string) *RedisPubSubBroadcaster {
	if channel == "" {
		channel = BroadcastChannelGateway
	}
	return &RedisPubSubBroadcaster{
		redis:   redis,
		channel: channel,
	}
}

func (b *RedisPubSubBroadcaster) Broadcast(ctx context.Context, roomID string, event string, data interface{}, excludeUserID string) error {
	msg, err := message.NewRoomBroadcastMessage(roomID, event, data)
	if err != nil {
		logger.Error("failed to create broadcast message", "error", err)
		return err
	}
	msg.WithExcludeID(excludeUserID)

	msgData, err := msg.Marshal()
	if err != nil {
		logger.Error("failed to marshal broadcast message", "error", err)
		return err
	}

	if err := b.redis.Publish(ctx, b.channel, msgData).Err(); err != nil {
		logger.Error("failed to publish broadcast message via redis pubsub", "error", err)
		return err
	}

	logger.Debug("broadcast message via redis pubsub",
		"room_id", roomID,
		"event", event,
		"exclude_user_id", excludeUserID)

	return nil
}

func (b *RedisPubSubBroadcaster) BroadcastToUser(ctx context.Context, userID string, event string, data interface{}) error {
	msg, err := message.NewUserBroadcastMessage([]string{userID}, event, data)
	if err != nil {
		logger.Error("failed to create broadcast message", "error", err)
		return err
	}

	msgData, err := msg.Marshal()
	if err != nil {
		logger.Error("failed to marshal broadcast message", "error", err)
		return err
	}

	if err := b.redis.Publish(ctx, b.channel, msgData).Err(); err != nil {
		logger.Error("failed to publish broadcast message via redis pubsub", "error", err)
		return err
	}

	logger.Debug("broadcast message to user via redis pubsub",
		"user_id", userID,
		"event", event)

	return nil
}
