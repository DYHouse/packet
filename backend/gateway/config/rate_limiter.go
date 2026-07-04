package config

import (
	"fmt"

	commonconfig "github.com/cashparty/backend/common/config"
)

type RateLimiterConfig struct {
	IPRequestsPerSecond   int `mapstructure:"ip_requests_per_second" yaml:"ip_requests_per_second"`
	IPBurstSize           int `mapstructure:"ip_burst_size" yaml:"ip_burst_size"`
	UserRequestsPerSecond int `mapstructure:"user_requests_per_second" yaml:"user_requests_per_second"`
	UserBurstSize         int `mapstructure:"user_burst_size" yaml:"user_burst_size"`
	GlobalRequestsPerSec  int `mapstructure:"global_requests_per_sec" yaml:"global_requests_per_sec"`
}

type RateLimiterYAML struct {
	RateLimiter RateLimiterConfig `mapstructure:"rate_limiter" yaml:"rate_limiter"`
}

func LoadRateLimiterFromContent(content string) (*RateLimiterConfig, error) {
	var raw RateLimiterYAML
	if err := commonconfig.LoadYAMLFromContent(content, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse rate limiter config content: %w", err)
	}

	cfg := &raw.RateLimiter
	setRateLimiterDefaults(cfg)
	return cfg, nil
}

func setRateLimiterDefaults(cfg *RateLimiterConfig) {
	if cfg.IPRequestsPerSecond == 0 {
		cfg.IPRequestsPerSecond = 100
	}
	if cfg.IPBurstSize == 0 {
		cfg.IPBurstSize = 200
	}
	if cfg.UserRequestsPerSecond == 0 {
		cfg.UserRequestsPerSecond = 50
	}
	if cfg.UserBurstSize == 0 {
		cfg.UserBurstSize = 100
	}
	if cfg.GlobalRequestsPerSec == 0 {
		cfg.GlobalRequestsPerSec = 10000
	}
}
