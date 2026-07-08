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

type Consumer struct {
	reader  *kafka.Reader
	handler MessageHandler
	cfg     ConsumerConfig
	topic   string
	dlq     KafkaProducer
}

// NewConsumer 创建 Kafka 消费者。
// 校验 Brokers/Topic/GroupID 非空，否则返回 error。
// 若 cfg.DLQTopic != "" 则 dlq 必须非 nil。
func NewConsumer(cfg ConsumerConfig, handler MessageHandler, dlq KafkaProducer) (*Consumer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka consumer config: brokers must not be empty")
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("kafka consumer config: topic must not be empty")
	}
	if cfg.GroupID == "" {
		return nil, fmt.Errorf("kafka consumer config: group_id must not be empty")
	}
	if cfg.DLQTopic != "" && dlq == nil {
		return nil, fmt.Errorf("kafka consumer config: DLQTopic set but dlq producer is nil")
	}

	def := defaultConsumerConfig()
	if cfg.MinBytes == 0 {
		cfg.MinBytes = def.MinBytes
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = def.MaxBytes
	}
	if cfg.MaxWait == 0 {
		cfg.MaxWait = def.MaxWait
	}
	if cfg.StartOffset == 0 {
		cfg.StartOffset = def.StartOffset
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = def.MaxRetries
	}
	if cfg.RetryBackoff == 0 {
		cfg.RetryBackoff = def.RetryBackoff
	}
	// CommitInterval=0 即为禁用自动 commit（默认）

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
		cfg:     cfg,
		topic:   cfg.Topic,
		dlq:     dlq,
	}, nil
}

// Start 启动消费循环：FetchMessage → processWithRetry → 成功才 commit；失败投 DLQ 后再 commit。
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
			// 使用 select 监听 ctx.Done()，避免在优雅关闭期间阻塞 goroutine
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}

		// processWithRetry 内部对 handler 做 panic recovery
		if err := processWithRetry(ctx, c.handler, msg, c.cfg); err != nil {
			logger.Error("kafka process message failed after retries",
				"topic", c.topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
				"error", err)

			// 投递 DLQ（若配置）；DLQ 投递失败仅记日志，仍 commit 避免毒消息永久阻塞
			if c.dlq != nil && c.cfg.DLQTopic != "" {
				if dlqErr := sendToDLQ(ctx, c.dlq, c.cfg.DLQTopic, msg, err); dlqErr != nil {
					logger.Error("send to DLQ failed",
						"topic", c.topic,
						"dlq_topic", c.cfg.DLQTopic,
						"partition", msg.Partition,
						"offset", msg.Offset,
						"error", dlqErr)
				} else {
					logger.Info("message moved to DLQ",
						"topic", c.topic,
						"dlq_topic", c.cfg.DLQTopic,
						"partition", msg.Partition,
						"offset", msg.Offset)
				}
			}
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			logger.Error("kafka commit failed", "topic", c.topic, "error", err)
		}
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}

func Timestamp() int64 {
	return time.Now().UnixMilli()
}
