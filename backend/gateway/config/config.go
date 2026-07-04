package config

import (
	"fmt"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

type Config struct {
	Server      ServerConfig       `mapstructure:"server" yaml:"server"`
	Gateway     GatewayConfig      `mapstructure:"gateway" yaml:"gateway"`
	Redis       RedisConfig        `mapstructure:"redis" yaml:"redis"`
	Nacos       GatewayNacosConfig `mapstructure:"nacos" yaml:"nacos"`
	Kafka       KafkaConfig        `mapstructure:"kafka" yaml:"kafka"`
	Broadcast   BroadcastConfig    `mapstructure:"broadcast" yaml:"broadcast"`
	Merchant    MerchantConfig     `mapstructure:"merchant" yaml:"merchant"`
	Token       TokenConfig        `mapstructure:"token" yaml:"token"`
	Log         LogConfig          `mapstructure:"log" yaml:"log"`
	RateLimiter RateLimiterConfig  `mapstructure:"rate_limiter" yaml:"rate_limiter"`
}

type ServerConfig struct {
	Name         string        `mapstructure:"name" yaml:"name"`
	Port         int           `mapstructure:"port" yaml:"port"`
	Mode         string        `mapstructure:"mode" yaml:"mode"`
	TestEnabled  bool          `mapstructure:"test_enabled" yaml:"test_enabled"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
}

type GatewayConfig struct {
	ReadBufferSize  int      `mapstructure:"read_buffer_size" yaml:"read_buffer_size"`
	WriteBufferSize int      `mapstructure:"write_buffer_size" yaml:"write_buffer_size"`
	SendQueueSize   int      `mapstructure:"send_queue_size" yaml:"send_queue_size"`
	MaxConnections  int      `mapstructure:"max_connections" yaml:"max_connections"`
	AllowedOrigins  []string `mapstructure:"allowed_origins" yaml:"allowed_origins"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr" yaml:"addr"`
	Password string `mapstructure:"password" yaml:"password"`
	DB       int    `mapstructure:"db" yaml:"db"`
	PoolSize int    `mapstructure:"pool_size" yaml:"pool_size"`
}

type KafkaConfig struct {
	Enabled bool     `mapstructure:"enabled" yaml:"enabled"`
	Brokers []string `mapstructure:"brokers" yaml:"brokers"`
}

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

type MerchantConfig struct {
	ID           string `mapstructure:"id" yaml:"id"`
	Secret       string `mapstructure:"secret" yaml:"secret"`
	GameEntryURL string `mapstructure:"game_entry_url" yaml:"game_entry_url"`
	WsURL        string `mapstructure:"ws_url" yaml:"ws_url"`
}

type TokenConfig struct {
	SecretKey string        `mapstructure:"secret_key" yaml:"secret_key"`
	TokenTTL  time.Duration `mapstructure:"token_ttl" yaml:"token_ttl"`
	Issuer    string        `mapstructure:"issuer" yaml:"issuer"`
}

type LogConfig struct {
	Level      string `mapstructure:"level" yaml:"level"`
	Filename   string `mapstructure:"filename" yaml:"filename"`
	MaxSize    int    `mapstructure:"max_size" yaml:"max_size"`
	MaxBackups int    `mapstructure:"max_backups" yaml:"max_backups"`
	MaxAge     int    `mapstructure:"max_age" yaml:"max_age"`
	Compress   bool   `mapstructure:"compress" yaml:"compress"`
}

func Load(configPath string) (*Config, error) {
	var cfg Config
	if err := commonconfig.LoadYAML(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load gateway config: %w", err)
	}

	setDefaults(&cfg)

	return &cfg, nil
}

func LoadFromContent(content string) (*Config, error) {
	var cfg Config
	if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse gateway config content: %w", err)
	}

	setDefaults(&cfg)

	return &cfg, nil
}
