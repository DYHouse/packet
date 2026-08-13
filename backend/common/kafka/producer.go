package kafka

import (
	"context"
	"fmt"
	"sync"

	"github.com/cashparty/backend/common/logger"
	"github.com/segmentio/kafka-go"
)

// KafkaProducer Kafka 生产者接口，支持 mock 测试。
type KafkaProducer interface {
	Brokers() []string
	GetWriter(topic string) *kafka.Writer
	Send(ctx context.Context, topic string, key, value []byte) error
	SendBatch(ctx context.Context, topic string, messages []Message) error
	Close() error
}

// producer Kafka 生产者实现
type producer struct {
	mu      sync.RWMutex
	writers map[string]*kafka.Writer
	cfg     ProducerConfig
}

// NewProducer 创建 Kafka 生产者。brokers 为空时返回 error。
// 用户传入的字段优先于默认值（零值字段使用默认值填充）。
func NewProducer(cfg ProducerConfig) (KafkaProducer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka producer config: brokers must not be empty")
	}

	def := defaultProducerConfig()
	if cfg.Balancer == nil {
		cfg.Balancer = def.Balancer
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = def.BatchSize
	}
	if cfg.BatchTimeout == 0 {
		cfg.BatchTimeout = def.BatchTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = def.WriteTimeout
	}
	if cfg.RequiredAcks == 0 {
		cfg.RequiredAcks = def.RequiredAcks
	}

	return &producer{
		writers: make(map[string]*kafka.Writer),
		cfg:     cfg,
	}, nil
}

func (p *producer) Brokers() []string {
	return p.cfg.Brokers
}

func (p *producer) GetWriter(topic string) *kafka.Writer {
	p.mu.RLock()
	if w, ok := p.writers[topic]; ok {
		p.mu.RUnlock()
		return w
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	// 双检：可能在升级锁期间已被其他 goroutine 创建
	if w, ok := p.writers[topic]; ok {
		return w
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(p.cfg.Brokers...),
		Topic:        topic,
		Balancer:     p.cfg.Balancer,
		BatchSize:    p.cfg.BatchSize,
		BatchTimeout: p.cfg.BatchTimeout,
		WriteTimeout: p.cfg.WriteTimeout,
		RequiredAcks: p.cfg.RequiredAcks,
		Async:        p.cfg.Async,
		Transport:    buildTransport(p.cfg.SASL, p.cfg.TLS),
	}
	p.writers[topic] = w
	return w
}

func (p *producer) Send(ctx context.Context, topic string, key, value []byte) error {
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

func (p *producer) SendBatch(ctx context.Context, topic string, messages []Message) error {
	w := p.GetWriter(topic)
	err := w.WriteMessages(ctx, messages...)
	if err != nil {
		logger.Error("kafka batch send failed", "topic", topic, "count", len(messages), "error", err)
		return fmt.Errorf("kafka batch send to %s failed: %w", topic, err)
	}
	return nil
}

func (p *producer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var firstErr error
	for topic, w := range p.writers {
		if err := w.Close(); err != nil {
			logger.Error("failed to close kafka writer", "topic", topic, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
