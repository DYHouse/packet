package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/game/domain"
)

type RoomEventPublisher struct {
	producer *kafka.Producer
	topic    string
}

func NewRoomEventPublisher(producer *kafka.Producer, topic string) *RoomEventPublisher {
	return &RoomEventPublisher{
		producer: producer,
		topic:    topic,
	}
}

// PublishRoomEvent 实现 domain.EventPublisher 接口。
func (p *RoomEventPublisher) PublishRoomEvent(ctx context.Context, event *domain.RoomEvent) error {
	return p.publish(ctx, event)
}

// PublishGameEvent 实现 domain.EventPublisher 接口，但 RoomEventPublisher 不支持发布 GameEvent。
func (p *RoomEventPublisher) PublishGameEvent(ctx context.Context, event *domain.GameEvent) error {
	return fmt.Errorf("RoomEventPublisher does not support PublishGameEvent")
}

// Publish 是 PublishRoomEvent 的别名，保留向后兼容。
func (p *RoomEventPublisher) Publish(ctx context.Context, event *domain.RoomEvent) error {
	return p.PublishRoomEvent(ctx, event)
}

func (p *RoomEventPublisher) publish(ctx context.Context, event *domain.RoomEvent) error {
	if event.TraceID == "" {
		return fmt.Errorf("event TraceID must be set by caller")
	}
	if event.EventID == "" {
		event.EventID = generateEventID()
	}
	if event.Version == 0 {
		event.Version = domain.RoomEventVersion
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal room event failed: %w", err)
	}

	// Key 用 roomID 保证同房间事件落同分区（修复 P0-3）
	key := event.RoomID

	if err := p.producer.Send(ctx, p.topic, []byte(key), data); err != nil {
		logger.Error("publish room event failed",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"user_id", event.UserID,
			"error", err)
		return err
	}

	logger.Info("room event published",
		"event_type", event.EventType,
		"room_id", event.RoomID,
		"user_id", event.UserID,
		"event_id", event.EventID)

	return nil
}
