package config

import (
	commonconfig "github.com/cashparty/backend/common/config"
)

func setDefaults(cfg *Config) {
	commonconfig.SetServerDefaults(&cfg.Server)
	commonconfig.SetGatewayDefaults(&cfg.Gateway)
	commonconfig.SetTimeoutDefaults(&cfg.Timeout)
	commonconfig.SetRedisDefaults(&cfg.Redis)
	commonconfig.SetMySQLDefaults(&cfg.MySQL)
	commonconfig.SetLogDefaults(&cfg.Log)
	commonconfig.SetPlatformDefaults(&cfg.Platform)
	commonconfig.SetRobotDefaults(&cfg.Robot)
	commonconfig.SetSettlementSchedulerDefaults(&cfg.SettlementScheduler)
	commonconfig.SetLockDefaults(&cfg.Lock)
	commonconfig.SetRedisTTLDefaults(&cfg.RedisTTL)
	commonconfig.SetNacosDefaults(&cfg.Nacos.NacosConfig)
	commonconfig.SetIDGeneratorDefaults(&cfg.IDGenerator)

	// game 独有
	if cfg.Nacos.AlgorithmGroup == "" {
		cfg.Nacos.AlgorithmGroup = "DEFAULT_GROUP"
	}
}
