package config

import (
	"fmt"
	"regexp"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

type Config struct {
	Server       ServerConfig             `mapstructure:"server" yaml:"server"`
	MySQL        commonconfig.MySQLConfig `mapstructure:"mysql" yaml:"mysql"`
	Redis        commonconfig.RedisConfig `mapstructure:"redis" yaml:"redis"`
	Log          commonconfig.LogConfig   `mapstructure:"log" yaml:"log"`
	AmountRanges []AmountRange            `mapstructure:"amount_ranges" yaml:"amount_ranges"`
}

type AmountRange struct {
	Min   int64  `mapstructure:"min" yaml:"min"`
	Max   int64  `mapstructure:"max" yaml:"max"`
	Label string `mapstructure:"label" yaml:"label"`
}

// ServerConfig 是 stats 独有的服务配置（使用 Port 字段，与 commonconfig.ServerConfig 的 GRPCPort/HTTPPort/WSPort 不同）。
type ServerConfig struct {
	Name         string        `mapstructure:"name" yaml:"name"`
	Port         int           `mapstructure:"port" yaml:"port"`
	Mode         string        `mapstructure:"mode" yaml:"mode"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
}

// amountLabelPattern 限制 AmountRange.Label 仅允许字母、数字、中文、连字符，
// 防止 SQL 拼接注入（规约 CODING_STANDARD.md §8.6 / §16 SC-7）。
var amountLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9\u4e00-\u9fa5\-]+$`)

// validateAmountRanges 校验 AmountRange.Label 字符集白名单。
// buildAmountCaseWhen 将 Label 直接拼入 SQL CASE WHEN 表达式，
// 因此 MUST 在配置加载时校验，拒绝包含 ' " ; -- 等危险字符的 Label。
func validateAmountRanges(ranges []AmountRange) error {
	for i, r := range ranges {
		if r.Label == "" {
			return fmt.Errorf("amount_ranges[%d]: label must not be empty", i)
		}
		if !amountLabelPattern.MatchString(r.Label) {
			return fmt.Errorf("amount_ranges[%d]: label %q contains invalid characters, only letters, digits, CJK characters and hyphens are allowed", i, r.Label)
		}
	}
	return nil
}

func Load(configPath string) (*Config, error) {
	var cfg Config
	if err := commonconfig.LoadYAML(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load stats config: %w", err)
	}

	setDefaults(&cfg)

	if err := validateAmountRanges(cfg.AmountRanges); err != nil {
		return nil, fmt.Errorf("invalid stats config: %w", err)
	}

	return &cfg, nil
}
