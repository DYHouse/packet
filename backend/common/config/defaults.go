package config

import "time"

func setDefaults(cfg *Config) {
	// Gateway defaults
	if cfg.Gateway.MaxConnections == 0 {
		cfg.Gateway.MaxConnections = 50000
	}
	if cfg.Gateway.SendQueueSize == 0 {
		cfg.Gateway.SendQueueSize = 256
	}
	if cfg.Gateway.ReadBufferSize == 0 {
		cfg.Gateway.ReadBufferSize = 4096
	}
	if cfg.Gateway.WriteBufferSize == 0 {
		cfg.Gateway.WriteBufferSize = 4096
	}
	if cfg.Gateway.MaxMessageSize == 0 {
		cfg.Gateway.MaxMessageSize = 65536
	}
	if cfg.Gateway.HeartbeatInterval == 0 {
		cfg.Gateway.HeartbeatInterval = 30 * time.Second
	}
	if cfg.Gateway.HeartbeatTimeout == 0 {
		cfg.Gateway.HeartbeatTimeout = 90 * time.Second
	}
	if cfg.Gateway.WriteTimeout == 0 {
		cfg.Gateway.WriteTimeout = 10 * time.Second
	}
	if cfg.Gateway.ReadTimeout == 0 {
		cfg.Gateway.ReadTimeout = 60 * time.Second
	}
	if cfg.Gateway.ReconnectTimeout == 0 {
		cfg.Gateway.ReconnectTimeout = 60 * time.Second
	}

	// Timeout scheduler defaults
	if cfg.Timeout.Seat == 0 {
		cfg.Timeout.Seat = 30 * time.Second
	}
	if cfg.Timeout.Ready == 0 {
		cfg.Timeout.Ready = 3 * time.Second
	}
	if cfg.Timeout.Grab == 0 {
		cfg.Timeout.Grab = 20 * time.Second
	}
	if cfg.Timeout.Send == 0 {
		cfg.Timeout.Send = 30 * time.Second
	}
	if cfg.Timeout.Replace == 0 {
		cfg.Timeout.Replace = 30 * time.Second
	}

	// Redis defaults
	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = 100
	}
	if cfg.Redis.MinIdleConns == 0 {
		cfg.Redis.MinIdleConns = 20
	}
	if cfg.Redis.DialTimeout == 0 {
		cfg.Redis.DialTimeout = 5 * time.Second
	}
	if cfg.Redis.ReadTimeout == 0 {
		cfg.Redis.ReadTimeout = 3 * time.Second
	}
	if cfg.Redis.WriteTimeout == 0 {
		cfg.Redis.WriteTimeout = 3 * time.Second
	}

	// MySQL defaults
	if cfg.MySQL.MaxOpenConns == 0 {
		cfg.MySQL.MaxOpenConns = 100
	}
	if cfg.MySQL.MaxIdleConns == 0 {
		cfg.MySQL.MaxIdleConns = 20
	}
	if cfg.MySQL.ConnMaxLifetime == 0 {
		cfg.MySQL.ConnMaxLifetime = 3600
	}

	// Platform defaults
	if cfg.Platform.Timeout == 0 {
		cfg.Platform.Timeout = 10 * time.Second
	}
	if cfg.Platform.MaxRetries == 0 {
		cfg.Platform.MaxRetries = 3
	}

	// Log defaults
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

	// Nacos defaults
	if cfg.Nacos.ServerAddr == "" {
		cfg.Nacos.ServerAddr = "127.0.0.1:8848"
	}
	if cfg.Nacos.Group == "" {
		cfg.Nacos.Group = "DEFAULT_GROUP"
	}
	if cfg.Nacos.Username == "" {
		cfg.Nacos.Username = "nacos"
	}
	if cfg.Nacos.Password == "" {
		cfg.Nacos.Password = "nacos"
	}
	if cfg.Nacos.ConfigGroup == "" {
		cfg.Nacos.ConfigGroup = "DEFAULT_GROUP"
	}
}
