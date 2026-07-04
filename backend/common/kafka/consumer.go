package kafka

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/segmentio/kafka-go"
)

type Message = kafka.Message

type MessageHandler func(ctx context.Context, msg Message) error

type ConsumerConfig struct {
	Brokers        []string
	Topic          string
	GroupID        string
	MinBytes       int
	MaxBytes       int
	MaxWait        time.Duration
	CommitInterval time.Duration
	StartOffset    int64
}

func NewConsumerConfig(brokers []string, topic, groupID string) *ConsumerConfig {
	return &ConsumerConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        500 * time.Millisecond,
		CommitInterval: time.Second,
		StartOffset:    kafka.LastOffset,
	}
}

type Consumer struct {
	reader  *kafka.Reader
	handler MessageHandler
	topic   string
}

func NewConsumer(brokers []string, topic string, groupID string, handler MessageHandler) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        500 * time.Millisecond,
		CommitInterval: time.Second,
		StartOffset:    kafka.LastOffset,
	})

	return &Consumer{
		reader:  reader,
		handler: handler,
		topic:   topic,
	}
}

func NewConsumerWithConfig(cfg *ConsumerConfig, handler MessageHandler) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        cfg.Brokers,
		Topic:          cfg.Topic,
		GroupID:        cfg.GroupID,
		MinBytes:       cfg.MinBytes,
		MaxBytes:       cfg.MaxBytes,
		MaxWait:        cfg.MaxWait,
		CommitInterval: cfg.CommitInterval,
		StartOffset:    cfg.StartOffset,
	})

	return &Consumer{
		reader:  reader,
		handler: handler,
		topic:   cfg.Topic,
	}
}

func (c *Consumer) Start(ctx context.Context) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("kafka consumer start panic",
				"topic", c.topic, "panic", r, "stack", string(debug.Stack()))
		}
	}()

	logger.Info("kafka consumer started", "topic", c.topic)

	for {
		select {
		case <-ctx.Done():
			logger.Info("kafka consumer stopping", "topic", c.topic)
			return c.reader.Close()
		default:
		}

		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Error("kafka fetch message failed", "topic", c.topic, "error", err)
			time.Sleep(time.Second)
			continue
		}

		if err := c.processMessage(ctx, msg); err != nil {
			logger.Error("kafka process message failed",
				"topic", c.topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
				"error", err,
			)
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			logger.Error("kafka commit failed", "topic", c.topic, "error", err)
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, msg Message) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("kafka handler panic recovered",
				"topic", c.topic,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()

	return c.handler(ctx, msg)
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}

func Timestamp() int64 {
	return time.Now().UnixMilli()
}
