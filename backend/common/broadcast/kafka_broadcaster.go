package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
)

type KafkaBroadcaster struct {
	producer *kafka.Producer
	topic    string
}

func NewKafkaBroadcaster(producer *kafka.Producer, topic string) *KafkaBroadcaster {
	if topic == "" {
		topic = kafka.TopicGatewayBroadcast
	}
	return &KafkaBroadcaster{
		producer: producer,
		topic:    topic,
	}
}

func (b *KafkaBroadcaster) Broadcast(ctx context.Context, roomID string, event string, data interface{}, excludeUserID string) error {
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

	if err := b.producer.Send(ctx, b.topic, []byte(roomID), msgData); err != nil {
		logger.Error("failed to send broadcast message via kafka", "error", err)
		return err
	}

	logger.Debug("broadcast message via kafka",
		"room_id", roomID,
		"event", event,
		"exclude_user_id", excludeUserID)

	return nil
}

func (b *KafkaBroadcaster) BroadcastToUser(ctx context.Context, userID string, event string, data interface{}) error {
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

	if err := b.producer.Send(ctx, b.topic, []byte(userID), msgData); err != nil {
		logger.Error("failed to send broadcast message via kafka", "error", err)
		return err
	}

	logger.Debug("broadcast message to user via kafka",
		"user_id", userID,
		"event", event)

	return nil
}
