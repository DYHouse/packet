package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writers map[string]*kafka.Writer
	brokers []string
}

func NewProducer(cfg *config.KafkaConfig) *Producer {
	return &Producer{
		writers: make(map[string]*kafka.Writer),
		brokers: cfg.Brokers,
	}
}

func NewProducerWithBrokers(brokers []string) *Producer {
	return &Producer{
		writers: make(map[string]*kafka.Writer),
		brokers: brokers,
	}
}

func (p *Producer) Brokers() []string {
	return p.brokers
}

func (p *Producer) GetWriter(topic string) *kafka.Writer {
	if w, ok := p.writers[topic]; ok {
		return w
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(p.brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: 10 * time.Second,
		RequiredAcks: kafka.RequireOne,
		Async:        false,
	}
	p.writers[topic] = w
	return w
}

func (p *Producer) Send(ctx context.Context, topic string, key, value []byte) error {
	w := p.GetWriter(topic)
	err := w.WriteMessages(ctx, kafka.Message{
		Key:   key,
		Value: value,
	})
	if err != nil {
		logger.Error("kafka send failed", "topic", topic, "error", err)
		return fmt.Errorf("kafka send to %s failed: %w", topic, err)
	}
	return nil
}

func (p *Producer) SendBatch(ctx context.Context, topic string, messages []Message) error {
	w := p.GetWriter(topic)
	err := w.WriteMessages(ctx, messages...)
	if err != nil {
		logger.Error("kafka batch send failed", "topic", topic, "count", len(messages), "error", err)
		return fmt.Errorf("kafka batch send to %s failed: %w", topic, err)
	}
	return nil
}

func (p *Producer) Close() error {
	for topic, w := range p.writers {
		if err := w.Close(); err != nil {
			logger.Error("failed to close kafka writer", "topic", topic, "error", err)
		}
	}
	return nil
}
