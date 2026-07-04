package config

import "time"

type RedisConfig struct {
	Addr         string        `mapstructure:"addr" yaml:"addr"`
	Password     string        `mapstructure:"password" yaml:"password"`
	DB           int           `mapstructure:"db" yaml:"db"`
	PoolSize     int           `mapstructure:"pool_size" yaml:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns" yaml:"min_idle_conns"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout" yaml:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
	UseEvalSHA   *bool         `mapstructure:"use_evalsha" yaml:"use_evalsha"`

	Mode          string   `mapstructure:"mode" yaml:"mode"`
	MasterName    string   `mapstructure:"master_name" yaml:"master_name"`
	SentinelAddrs []string `mapstructure:"sentinel_addrs" yaml:"sentinel_addrs"`
}

func SetRedisDefaults(cfg *RedisConfig) {
	if cfg.PoolSize == 0 {
		cfg.PoolSize = 100
	}
	if cfg.MinIdleConns == 0 {
		cfg.MinIdleConns = 20
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 3 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 3 * time.Second
	}
}
