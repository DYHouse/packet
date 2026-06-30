package config

import (
	"fmt"
	"os"
	"time"

	"github.com/cashparty/backend/gateway/router"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Gateway     GatewayConfig     `yaml:"gateway"`
	Redis       RedisConfig       `yaml:"redis"`
	Nacos       NacosConfig       `yaml:"nacos"`
	Kafka       KafkaConfig       `yaml:"kafka"`
	Broadcast   BroadcastConfig   `yaml:"broadcast"`
	Merchant    MerchantConfig    `yaml:"merchant"`
	Token       TokenConfig       `yaml:"token"`
	Log         LogConfig         `yaml:"log"`
	RateLimiter RateLimiterConfig `yaml:"rate_limiter"`
}

type ServerConfig struct {
	Name         string        `yaml:"name"`
	Port         int           `yaml:"port"`
	Mode         string        `yaml:"mode"`
	TestEnabled  bool          `yaml:"test_enabled"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type GatewayConfig struct {
	ReadBufferSize  int      `yaml:"read_buffer_size"`
	WriteBufferSize int      `yaml:"write_buffer_size"`
	SendQueueSize   int      `yaml:"send_queue_size"`
	MaxConnections  int      `yaml:"max_connections"`
	AllowedOrigins  []string `yaml:"allowed_origins"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

type NacosConfig struct {
	Enabled            bool   `yaml:"enabled"`
	ServerAddr         string `yaml:"server_addr"`
	Namespace          string `yaml:"namespace"`
	Group              string `yaml:"group"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	ServiceName        string `yaml:"service_name"`
	ServiceAddr        string `yaml:"service_addr"`
	ServicePort        int    `yaml:"service_port"`
	ConfigDataID       string `yaml:"config_data_id"`
	ConfigGroup        string `yaml:"config_group"`
	RouterDataID       string `yaml:"router_data_id"`
	RouterGroup        string `yaml:"router_group"`
	RateLimiterDataID  string `yaml:"rate_limiter_data_id"`
	RateLimiterGroup   string `yaml:"rate_limiter_group"`
}

type KafkaConfig struct {
	Enabled bool     `yaml:"enabled"`
	Brokers []string `yaml:"brokers"`
}

type BroadcastConfig struct {
	Mode     string               `yaml:"mode"`
	Kafka    BroadcastKafkaConfig `yaml:"kafka"`
	RedisPub BroadcastRedisConfig `yaml:"redis_pubsub"`
}

type BroadcastKafkaConfig struct {
	Topic string `yaml:"topic"`
}

type BroadcastRedisConfig struct {
	Channel string `yaml:"channel"`
}

type MerchantConfig struct {
	ID           string `yaml:"id"`
	Secret       string `yaml:"secret"`
	GameEntryURL string `yaml:"game_entry_url"`
	WsURL        string `yaml:"ws_url"`
}

type TokenConfig struct {
	SecretKey string        `yaml:"secret_key"`
	TokenTTL  time.Duration `yaml:"token_ttl"`
	Issuer    string        `yaml:"issuer"`
}

type LogConfig struct {
	Level      string `yaml:"level"`
	Filename   string `yaml:"filename"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
	Compress   bool   `yaml:"compress"`
}

type RateLimiterConfig struct {
	IPRequestsPerSecond   int           `yaml:"ip_requests_per_second"`
	IPBurstSize           int           `yaml:"ip_burst_size"`
	UserRequestsPerSecond int           `yaml:"user_requests_per_second"`
	UserBurstSize         int           `yaml:"user_burst_size"`
	GlobalRequestsPerSec  int           `yaml:"global_requests_per_sec"`
	CleanupInterval       time.Duration `yaml:"cleanup_interval"`
}

func Load(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	setDefaults(&cfg)

	return &cfg, nil
}

func LoadFromContent(content string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config content: %w", err)
	}

	setDefaults(&cfg)

	return &cfg, nil
}

func LoadRouterConfig(configPath string) (*router.RouterConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read router config file: %w", err)
	}

	var cfg router.RouterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse router config file: %w", err)
	}

	return &cfg, nil
}

func LoadRouterConfigFromContent(content string) (*router.RouterConfig, error) {
	var cfg router.RouterConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse router config content: %w", err)
	}

	return &cfg, nil
}

type RateLimiterYAML struct {
	RateLimiter RateLimiterConfig `yaml:"rate_limiter"`
}

func LoadRateLimiterFromContent(content string) (*RateLimiterConfig, error) {
	var raw RateLimiterYAML
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse rate limiter config content: %w", err)
	}

	cfg := &raw.RateLimiter
	setRateLimiterDefaults(cfg)
	return cfg, nil
}

func setRateLimiterDefaults(cfg *RateLimiterConfig) {
	if cfg.IPRequestsPerSecond == 0 {
		cfg.IPRequestsPerSecond = 100
	}
	if cfg.IPBurstSize == 0 {
		cfg.IPBurstSize = 200
	}
	if cfg.UserRequestsPerSecond == 0 {
		cfg.UserRequestsPerSecond = 50
	}
	if cfg.UserBurstSize == 0 {
		cfg.UserBurstSize = 100
	}
	if cfg.GlobalRequestsPerSec == 0 {
		cfg.GlobalRequestsPerSec = 10000
	}
	if cfg.CleanupInterval == 0 {
		cfg.CleanupInterval = 1 * time.Minute
	}
}

func setDefaults(cfg *Config) {
	if cfg.Server.Name == "" {
		cfg.Server.Name = "gateway-service"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8081
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	// TestEnabled 默认 false（生产安全）；dev 环境需在配置中显式设为 true
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 60 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 60 * time.Second
	}

	if cfg.Gateway.ReadBufferSize == 0 {
		cfg.Gateway.ReadBufferSize = 4096
	}
	if cfg.Gateway.WriteBufferSize == 0 {
		cfg.Gateway.WriteBufferSize = 4096
	}
	if cfg.Gateway.SendQueueSize == 0 {
		cfg.Gateway.SendQueueSize = 256
	}
	if cfg.Gateway.MaxConnections == 0 {
		cfg.Gateway.MaxConnections = 10000
	}

	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = 100
	}

	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.MaxSize == 0 {
		cfg.Log.MaxSize = 100
	}
	if cfg.Log.MaxBackups == 0 {
		cfg.Log.MaxBackups = 10
	}
	if cfg.Log.MaxAge == 0 {
		cfg.Log.MaxAge = 30
	}

	setRateLimiterDefaults(&cfg.RateLimiter)

	if cfg.Token.SecretKey == "" {
		cfg.Token.SecretKey = "default-secret-key-please-change-in-production"
	}
	if cfg.Token.TokenTTL == 0 {
		cfg.Token.TokenTTL = 2 * time.Hour
	}
	if cfg.Token.Issuer == "" {
		cfg.Token.Issuer = "gateway-service"
	}
}
