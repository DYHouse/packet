package config

import (
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

func setDefaults(cfg *Config) {
	if cfg.Server.Name == "" {
		cfg.Server.Name = "stats-service"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8082
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 60 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 60 * time.Second
	}

	commonconfig.SetMySQLDefaults(&cfg.MySQL)
	commonconfig.SetLogDefaults(&cfg.Log)

	// stats 使用较小的 Redis 连接池
	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = 50
	}

	if len(cfg.AmountRanges) == 0 {
		cfg.AmountRanges = []AmountRange{
			{Min: 0, Max: 1000, Label: "0-10元"},
			{Min: 1000, Max: 5000, Label: "10-50元"},
			{Min: 5000, Max: 10000, Label: "50-100元"},
			{Min: 10000, Max: 50000, Label: "100-500元"},
			{Min: 50000, Max: 0, Label: "500元以上"},
		}
	}
}
