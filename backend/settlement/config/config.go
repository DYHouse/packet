package config

import (
	"github.com/cashparty/backend/common/config"
)

type PlatformConfig = config.PlatformConfig

func DefaultPlatformConfig() *PlatformConfig {
	return &PlatformConfig{
		Provider: "mock",
		GameCode: "redpacket",
		GameName: "redpacket",
		Currency: "MXN",
	}
}

func FromCommonConfig(cfg *config.PlatformConfig) *PlatformConfig {
	if cfg == nil {
		return DefaultPlatformConfig()
	}
	return cfg
}
