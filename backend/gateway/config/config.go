package config

import (
	"fmt"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

type Config struct {
	Server      ServerConfig                   `mapstructure:"server" yaml:"server"`
	Gateway     GatewayConfig                  `mapstructure:"gateway" yaml:"gateway"`
	Redis       commonconfig.RedisConfig       `mapstructure:"redis" yaml:"redis"`
	Nacos       GatewayNacosConfig             `mapstructure:"nacos" yaml:"nacos"`
	Kafka       commonconfig.KafkaConfig       `mapstructure:"kafka" yaml:"kafka"`
	Broadcast   commonconfig.BroadcastConfig   `mapstructure:"broadcast" yaml:"broadcast"`
	Merchant    MerchantConfig                 `mapstructure:"merchant" yaml:"merchant"`
	Token       TokenConfig                    `mapstructure:"token" yaml:"token"`
	Log         LogConfig                      `mapstructure:"log" yaml:"log"`
	IDGenerator commonconfig.IDGeneratorConfig `mapstructure:"id_generator" yaml:"id_generator"`
	AuthLock    AuthLockConfig                 `mapstructure:"auth_lock" yaml:"auth_lock"`
	Avatar      AvatarConfig                   `mapstructure:"avatar" yaml:"avatar"`
}

// AvatarConfig 头像上传配置（仅 gateway 服务使用）。
// 默认头像配置（base_url / default_count）仍归 game 服务管理，见 config/game.yaml。
type AvatarConfig struct {
	Upload AvatarUploadConfig `mapstructure:"upload" yaml:"upload"`
}

// AvatarUploadConfig 头像上传子配置。
type AvatarUploadConfig struct {
	UploadDir     string   `mapstructure:"upload_dir" yaml:"upload_dir"`         // 落盘根目录
	PublicBaseURL string   `mapstructure:"public_base_url" yaml:"public_base_url"` // 对外访问基 URL
	MaxSizeBytes  int64    `mapstructure:"max_size_bytes" yaml:"max_size_bytes"` // 默认 2MB
	AllowedTypes  []string `mapstructure:"allowed_types" yaml:"allowed_types"`   // 默认 ["image/png","image/jpeg","image/webp"]
}

// AuthLockConfig Auth 锁定配置（config 层 DTO，与 middleware.AuthLockConfig 字段一致）
type AuthLockConfig struct {
	MaxAttempts   int           `mapstructure:"max_attempts" yaml:"max_attempts"`
	LockDuration  time.Duration `mapstructure:"lock_duration" yaml:"lock_duration"`
	CounterWindow time.Duration `mapstructure:"counter_window" yaml:"counter_window"`
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
