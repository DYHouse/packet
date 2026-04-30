package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
)

type KafkaConsumer struct {
	consumer *kafka.Consumer
}

func NewKafkaConsumer(consumer *kafka.Consumer) *KafkaConsumer {
	return &KafkaConsumer{
		consumer: consumer,
	}
}

func (c *KafkaConsumer) Start(ctx context.Context) error {
	if c.consumer == nil {
		logger.Warn("kafka consumer is nil")
		return nil
	}

	return c.consumer.Start(ctx)
}

func (c *KafkaConsumer) Close() error {
	if c.consumer != nil {
		return c.consumer.Close()
	}
	return nil
}
