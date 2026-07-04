package config

type MySQLConfig struct {
	DSN             string `mapstructure:"dsn" yaml:"dsn"`
	MaxOpenConns    int    `mapstructure:"max_open_conns" yaml:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns" yaml:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime" yaml:"conn_max_lifetime"`
}

func SetMySQLDefaults(cfg *MySQLConfig) {
	if cfg.MaxOpenConns == 0 {
		cfg.MaxOpenConns = 100
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 20
	}
	if cfg.ConnMaxLifetime == 0 {
		cfg.ConnMaxLifetime = 3600
	}
}
