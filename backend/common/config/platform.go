package config

import "time"

// PlatformConfig 外部平台 API 配置
type PlatformConfig struct {
	Provider       string        `mapstructure:"provider" yaml:"provider"`
	BaseURL        string        `mapstructure:"base_url" yaml:"base_url"`
	MerchantID     string        `mapstructure:"merchant_id" yaml:"merchant_id"`
	MerchantSecret string        `mapstructure:"merchant_secret" yaml:"merchant_secret"`
	GameID         int           `mapstructure:"game_id" yaml:"game_id"`
	GameCode       string        `mapstructure:"game_code" yaml:"game_code"`
	GameName       string        `mapstructure:"game_name" yaml:"game_name"`
	Currency       string        `mapstructure:"currency" yaml:"currency"`
	Timeout        time.Duration `mapstructure:"timeout" yaml:"timeout"`
	MaxRetries     int           `mapstructure:"max_retries" yaml:"max_retries"`
}

func SetPlatformDefaults(cfg *PlatformConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
}

// GameServiceConfig gRPC 游戏服务连接配置
type GameServiceConfig struct {
	Address        string        `mapstructure:"address" yaml:"address"`
	Timeout        time.Duration `mapstructure:"timeout" yaml:"timeout"`
	MaxRecvMsgSize int           `mapstructure:"max_recv_msg_size" yaml:"max_recv_msg_size"`
	MaxSendMsgSize int           `mapstructure:"max_send_msg_size" yaml:"max_send_msg_size"`
}
