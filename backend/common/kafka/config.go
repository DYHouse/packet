package kafka

import (
	"time"

	"github.com/segmentio/kafka-go"
)

// ProducerConfig 定义 Kafka 生产者的配置。
type ProducerConfig struct {
	Brokers      []string
	Balancer     kafka.Balancer     // 默认 &kafka.Hash{}
	BatchSize    int                // 默认 100
	BatchTimeout time.Duration      // 默认 10ms
	WriteTimeout time.Duration      // 默认 10s
	RequiredAcks kafka.RequiredAcks // 默认 RequireOne
	Async        bool               // 默认 false（同步）
}

// defaultProducerConfig 返回填充了默认值的 ProducerConfig。
func defaultProducerConfig() ProducerConfig {
	return ProducerConfig{
		Balancer:     &kafka.Hash{},
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		WriteTimeout: 10 * time.Second,
		RequiredAcks: kafka.RequireOne,
		Async:        false,
	}
}

// ConsumerConfig 定义 Kafka 消费者的配置。
type ConsumerConfig struct {
	Brokers        []string
	Topic          string
	GroupID        string
	MinBytes       int           // 默认 1
	MaxBytes       int           // 默认 10e6
	MaxWait        time.Duration // 默认 500ms
	CommitInterval time.Duration // 默认 0（禁用自动 commit）
	StartOffset    int64         // 默认 kafka.FirstOffset
	MaxRetries     int           // 默认 3
	RetryBackoff   time.Duration // 默认 1s
	DLQTopic       string        // 默认 ""（不投 DLQ）
}

// defaultConsumerConfig 返回填充了默认值的 ConsumerConfig。
func defaultConsumerConfig() ConsumerConfig {
	return ConsumerConfig{
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        500 * time.Millisecond,
		CommitInterval: 0,
		StartOffset:    kafka.FirstOffset,
		MaxRetries:     3,
		RetryBackoff:   1 * time.Second,
	}
}

// NewConsumerConfig 创建 ConsumerConfig 并填充默认值。
// 用户传入的字段优先于默认值（零值字段使用默认值填充）。
func NewConsumerConfig(brokers []string, topic, groupID string) ConsumerConfig {
	cfg := defaultConsumerConfig()
	cfg.Brokers = brokers
	cfg.Topic = topic
	cfg.GroupID = groupID
	return cfg
}
