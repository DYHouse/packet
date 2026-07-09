package config

import (
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/settlement/dto"
)

type PlatformConfig = config.PlatformConfig
type LockConfig = config.LockConfig

// SettlementConfig 汇总 settlement 模块的历史硬编码参数，统一通过配置注入。
// 所有字段的默认值与原硬编码值保持一致（30s / 100 / 20 / 5s / 5min / 3）。
type SettlementConfig struct {
	// TransactionTimeout 事务超时（原 db_repository.go WithTransaction 中的 30s）。
	TransactionTimeout time.Duration
	// SettlementCheckLimit 对账查询上限（原 settlement_check_service.go 中的 100）。
	SettlementCheckLimit int
	// MaxConcurrentDeduct 批量扣款并发上限（原 deduct_service.go defaultMaxConcurrentDeduct = 20）。
	MaxConcurrentDeduct int
	// CreditRetryBaseDelay 入账重试基础退避（原 dto.CreditRetryBaseDelay = 5s）。
	CreditRetryBaseDelay time.Duration
	// CreditRetryMaxDelay 入账重试最大退避（原 dto.CreditRetryMaxDelay = 5min）。
	CreditRetryMaxDelay time.Duration
	// MaxRetryCount 最大重试次数（原 dto.MaxRetryCount = 3）。
	MaxRetryCount int
}

// DefaultSettlementConfig 返回与历史硬编码值一致的默认 SettlementConfig。
// 用于未注入配置时的兜底（例如测试），确保行为与重构前完全等价。
func DefaultSettlementConfig() *SettlementConfig {
	return &SettlementConfig{
		TransactionTimeout:   30 * time.Second,
		SettlementCheckLimit: 100,
		MaxConcurrentDeduct:  20,
		CreditRetryBaseDelay: dto.CreditRetryBaseDelay,
		CreditRetryMaxDelay:  dto.CreditRetryMaxDelay,
		MaxRetryCount:        dto.MaxRetryCount,
	}
}

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
