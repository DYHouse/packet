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

type GameEventPublisher struct {
	producer *kafka.Producer
}

func NewGameEventPublisher(producer *kafka.Producer) *GameEventPublisher {
	return &GameEventPublisher{
		producer: producer,
	}
}

func (p *GameEventPublisher) PublishSessionStart(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventSessionStart
	return p.publish(ctx, event)
}

func (p *GameEventPublisher) PublishPacketCreated(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventPacketCreated
	return p.publish(ctx, event)
}

func (p *GameEventPublisher) PublishRoundSettle(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventRoundSettle
	return p.publish(ctx, event)
}

func (p *GameEventPublisher) PublishSessionEnd(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventSessionEnd
	return p.publish(ctx, event)
}

func (p *GameEventPublisher) publish(ctx context.Context, event *domain.GameEvent) error {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().Unix()
	}
	if event.TraceID == "" {
		return fmt.Errorf("event TraceID must be set by caller")
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}

	key := fmt.Sprintf("%s_%s", event.RoomID, event.SessionID)

	if err := p.producer.Send(ctx, kafka.TopicGameEvents, []byte(key), data); err != nil {
		logger.Error("publish game event failed",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"session_id", event.SessionID,
			"error", err)
		return err
	}

	logger.Info("game event published",
		"event_type", event.EventType,
		"room_id", event.RoomID,
		"session_id", event.SessionID,
		"trace_id", event.TraceID)

	return nil
}
