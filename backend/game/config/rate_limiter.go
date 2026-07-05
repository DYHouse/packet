package config

import (
	"fmt"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

// RateLimiterConfig 业务限流配置
type RateLimiterConfig struct {
	Defaults DefaultsConfig          `mapstructure:"defaults" yaml:"defaults"`
	Commands map[string]CommandLimit `mapstructure:"commands" yaml:"commands"`
}

// DefaultsConfig 默认 fail-open 策略
type DefaultsConfig struct {
	FailOpen          bool `mapstructure:"fail_open" yaml:"fail_open"`
	FinancialFailOpen bool `mapstructure:"financial_fail_open" yaml:"financial_fail_open"`
}

// CommandLimit 单个命令的限流配置
type CommandLimit struct {
	Limit    int           `mapstructure:"limit" yaml:"limit"`
	Window   time.Duration `mapstructure:"window" yaml:"window"`
	FailOpen *bool         `mapstructure:"fail_open" yaml:"fail_open"` // 可选,覆盖默认值
}

// LoadRateLimiterFromContent 从 YAML 内容解析限流配置
func LoadRateLimiterFromContent(content string) (*RateLimiterConfig, error) {
	var raw RateLimiterConfig
	if err := commonconfig.LoadYAMLFromContent(content, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse rate limiter config: %w", err)
	}
	setRateLimiterDefaults(&raw)
	return &raw, nil
}

// setRateLimiterDefaults 设置限流配置默认值
func setRateLimiterDefaults(cfg *RateLimiterConfig) {
	if cfg.Commands == nil {
		cfg.Commands = make(map[string]CommandLimit)
	}
	// 若各命令未配置,补充默认值
	defaults := map[string]struct {
		Limit  int
		Window time.Duration
	}{
		"grab":         {10, time.Second},
		"send_packet":  {5, time.Second},
		"join_room":    {5, time.Minute},
		"auto_match":   {3, 10 * time.Second},
		"select_seat":  {10, time.Second},
		"player_ready": {5, time.Second},
	}
	for cmd, d := range defaults {
		if _, ok := cfg.Commands[cmd]; !ok {
			cfg.Commands[cmd] = CommandLimit{
				Limit:  d.Limit,
				Window: d.Window,
			}
		}
	}
}
