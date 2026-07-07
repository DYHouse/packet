package config

// KafkaConfig Kafka 基础配置
type KafkaConfig struct {
	Enabled bool     `mapstructure:"enabled" yaml:"enabled"`
	Brokers []string `mapstructure:"brokers" yaml:"brokers"`
}

// BroadcastConfig 广播配置（支持 Kafka 和 Redis Pub/Sub 两种模式）
type BroadcastConfig struct {
	Mode     string               `mapstructure:"mode" yaml:"mode"`
	Kafka    BroadcastKafkaConfig `mapstructure:"kafka" yaml:"kafka"`
	RedisPub BroadcastRedisConfig `mapstructure:"redis_pubsub" yaml:"redis_pubsub"`
}

type BroadcastKafkaConfig struct {
	Topic string `mapstructure:"topic" yaml:"topic"`
}

type BroadcastRedisConfig struct {
	Channel string `mapstructure:"channel" yaml:"channel"`
}
