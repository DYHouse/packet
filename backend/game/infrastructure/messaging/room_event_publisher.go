package messaging

import (
	"context"
	"encoding/json"

	"github.com/cashparty/backend/common/kafka"
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

func (p *RoomEventPublisher) Publish(ctx context.Context, event *domain.RoomEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return p.producer.Send(ctx, p.topic, nil, data)
}
