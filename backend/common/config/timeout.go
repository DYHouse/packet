package config

import "time"

// TimeoutConfig 游戏各阶段超时配置
type TimeoutConfig struct {
	Seat          time.Duration `mapstructure:"seat" yaml:"seat"`
	Ready         time.Duration `mapstructure:"ready" yaml:"ready"`
	Grab          time.Duration `mapstructure:"grab" yaml:"grab"`
	Send          time.Duration `mapstructure:"send" yaml:"send"`
	Replace       time.Duration `mapstructure:"replace" yaml:"replace"`
	Robot         time.Duration `mapstructure:"robot" yaml:"robot"`
	CheckInterval time.Duration `mapstructure:"check_interval" yaml:"check_interval"`
	// HandlerTimeout 单个 timeout handler 的执行超时（默认 30s）。
	HandlerTimeout time.Duration `mapstructure:"handler_timeout" yaml:"handler_timeout"`
}

func SetTimeoutDefaults(cfg *TimeoutConfig) {
	if cfg.Seat == 0 {
		cfg.Seat = 30 * time.Second
	}
	if cfg.Ready == 0 {
		cfg.Ready = 3 * time.Second
	}
	if cfg.Grab == 0 {
		cfg.Grab = 20 * time.Second
	}
	if cfg.Send == 0 {
		cfg.Send = 30 * time.Second
	}
	if cfg.Replace == 0 {
		cfg.Replace = 30 * time.Second
	}
	if cfg.Robot == 0 {
		cfg.Robot = 5 * time.Second
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 1 * time.Second
	}
	if cfg.HandlerTimeout == 0 {
		cfg.HandlerTimeout = 30 * time.Second
	}
}
