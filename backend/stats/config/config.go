package config

import (
	"fmt"
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

func Load(configPath string) (*Config, error) {
	var cfg Config
	if err := commonconfig.LoadYAML(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load stats config: %w", err)
	}

	setDefaults(&cfg)

	return &cfg, nil
}
