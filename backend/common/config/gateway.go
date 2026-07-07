package config

import "time"

// GatewayConfig 网关服务配置
type GatewayConfig struct {
	MaxConnections    int           `mapstructure:"max_connections" yaml:"max_connections"`
	SendQueueSize     int           `mapstructure:"send_queue_size" yaml:"send_queue_size"`
	ReadBufferSize    int           `mapstructure:"read_buffer_size" yaml:"read_buffer_size"`
	WriteBufferSize   int           `mapstructure:"write_buffer_size" yaml:"write_buffer_size"`
	MaxMessageSize    int64         `mapstructure:"max_message_size" yaml:"max_message_size"`
	HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval" yaml:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `mapstructure:"heartbeat_timeout" yaml:"heartbeat_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	ReconnectTimeout  time.Duration `mapstructure:"reconnect_timeout" yaml:"reconnect_timeout"`
	KickOldConnection bool          `mapstructure:"kick_old_connection" yaml:"kick_old_connection"`
	AllowedOrigins    []string      `mapstructure:"allowed_origins" yaml:"allowed_origins"`
	AdminAPIKey       string        `mapstructure:"admin_api_key" yaml:"admin_api_key"`
}

func SetGatewayDefaults(cfg *GatewayConfig) {
	if cfg.MaxConnections == 0 {
		cfg.MaxConnections = 50000
	}
	if cfg.SendQueueSize == 0 {
		cfg.SendQueueSize = 256
	}
	if cfg.ReadBufferSize == 0 {
		cfg.ReadBufferSize = 4096
	}
	if cfg.WriteBufferSize == 0 {
		cfg.WriteBufferSize = 4096
	}
	if cfg.MaxMessageSize == 0 {
		cfg.MaxMessageSize = 65536
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 90 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 60 * time.Second
	}
	if cfg.ReconnectTimeout == 0 {
		cfg.ReconnectTimeout = 60 * time.Second
	}
}
