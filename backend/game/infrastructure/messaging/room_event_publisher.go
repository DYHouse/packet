package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/trace"
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

// PublishRoomEvent 实现 domain.RoomEventPublisher 接口。
func (p *RoomEventPublisher) PublishRoomEvent(ctx context.Context, event *domain.RoomEvent) error {
	return p.publish(ctx, event)
}

// Publish 是 PublishRoomEvent 的别名，保留向后兼容。
func (p *RoomEventPublisher) Publish(ctx context.Context, event *domain.RoomEvent) error {
	return p.PublishRoomEvent(ctx, event)
}

func (p *RoomEventPublisher) publish(ctx context.Context, event *domain.RoomEvent) error {
	// 优先使用 event 已有的 TraceID（向后兼容调用方显式设置）；
	// 其次从 context 提取 TraceID（实现端到端追踪）；
	// 两者都为空时自动生成（兜底，保证消息一定能发出）。
	if event.TraceID == "" {
		event.TraceID = trace.FromContext(ctx)
	}
	if event.TraceID == "" {
		event.TraceID = trace.Generate()
		logger.Warn("event TraceID not in ctx, auto-generated",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"trace_id", event.TraceID)
	}
	event.EventHeader.FillIfEmpty()

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
		"event_id", event.EventID,
		"trace_id", event.TraceID)

	return nil
}
