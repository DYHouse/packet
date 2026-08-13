package config

import "time"

// TLSConfig Redis TLS 传输加密配置。用于 AWS ElastiCache Valkey TLS 或任何需要加密连接的场景。
type TLSConfig struct {
	Enable             bool   `mapstructure:"enable" yaml:"enable"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify" yaml:"insecure_skip_verify"`
	CACertPath         string `mapstructure:"ca_cert_path" yaml:"ca_cert_path"`
	CertPath           string `mapstructure:"cert_path" yaml:"cert_path"`
	KeyPath            string `mapstructure:"key_path" yaml:"key_path"`
	ServerName         string `mapstructure:"server_name" yaml:"server_name"`
}

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

	TLS          TLSConfig `mapstructure:"tls" yaml:"tls"`           // TLS 传输加密配置
	ClusterAddrs []string  `mapstructure:"cluster_addrs" yaml:"cluster_addrs"` // Cluster 模式节点地址列表（mode=cluster 时使用，本次预留不实现）
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
