package config

import (
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

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

	commonconfig.SetNacosDefaults(&cfg.Nacos.NacosConfig)

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

	if cfg.Token.SecretKey == "" {
		cfg.Token.SecretKey = "default-secret-key-please-change-in-production"
	}
	if cfg.Token.TokenTTL == 0 {
		cfg.Token.TokenTTL = 2 * time.Hour
	}
	if cfg.Token.Issuer == "" {
		cfg.Token.Issuer = "gateway-service"
	}

	// 雪花 ID 生成器默认值与范围校验（规约 SID-CFG1、SID-CFG2）
	commonconfig.SetIDGeneratorDefaults(&cfg.IDGenerator)

	// Auth 锁定默认值
	if cfg.AuthLock.MaxAttempts == 0 {
		cfg.AuthLock.MaxAttempts = 5
	}
	if cfg.AuthLock.LockDuration == 0 {
		cfg.AuthLock.LockDuration = 15 * time.Minute
	}
	if cfg.AuthLock.CounterWindow == 0 {
		cfg.AuthLock.CounterWindow = 15 * time.Minute
	}
}
