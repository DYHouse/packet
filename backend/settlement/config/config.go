package config

import (
	"github.com/cashparty/backend/common/config"
)

type PlatformConfig = config.PlatformConfig
type LockConfig = config.LockConfig

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

// DefaultLockConfig returns a LockConfig with all TTL fields set to the
// historically hardcoded defaults. Used as fallback when no config is
// injected (e.g., in tests).
func DefaultLockConfig() *LockConfig {
	cfg := &LockConfig{}
	config.SetLockDefaults(cfg)
	return cfg
}
