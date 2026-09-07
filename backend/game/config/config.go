package config

import (
	commonconfig "github.com/cashparty/backend/common/config"
)

type Config struct {
	Server              commonconfig.ServerConfig              `mapstructure:"server" yaml:"server"`
	Gateway             commonconfig.GatewayConfig             `mapstructure:"gateway" yaml:"gateway"`
	Timeout             commonconfig.TimeoutConfig             `mapstructure:"timeout" yaml:"timeout"`
	Redis               commonconfig.RedisConfig               `mapstructure:"redis" yaml:"redis"`
	MySQL               commonconfig.MySQLConfig               `mapstructure:"mysql" yaml:"mysql"`
	Kafka               commonconfig.KafkaConfig               `mapstructure:"kafka" yaml:"kafka"`
	Broadcast           commonconfig.BroadcastConfig           `mapstructure:"broadcast" yaml:"broadcast"`
	Platform            commonconfig.PlatformConfig            `mapstructure:"platform" yaml:"platform"`
	Log                 commonconfig.LogConfig                 `mapstructure:"log" yaml:"log"`
	GameService         commonconfig.GameServiceConfig         `mapstructure:"game_service" yaml:"game_service"`
	Nacos               GameNacosConfig                        `mapstructure:"nacos" yaml:"nacos"`
	Algorithm           AlgorithmConfig                        `mapstructure:"algorithm" yaml:"algorithm"`
	IDGenerator         commonconfig.IDGeneratorConfig         `mapstructure:"id_generator" yaml:"id_generator"`
	Avatar              commonconfig.AvatarConfig              `mapstructure:"avatar" yaml:"avatar"`
	Robot               commonconfig.RobotConfig               `mapstructure:"robot" yaml:"robot"`
	Room                commonconfig.RoomConfig                `mapstructure:"room" yaml:"room"`
	SettlementScheduler commonconfig.SettlementSchedulerConfig `mapstructure:"settlement_scheduler" yaml:"settlement_scheduler"`
	Lock                commonconfig.LockConfig                `mapstructure:"lock" yaml:"lock"`
	Lua                 commonconfig.LuaConfig                 `mapstructure:"lua" yaml:"lua"`
	RedisTTL            commonconfig.RedisTTLConfig            `mapstructure:"redis_ttl" yaml:"redis_ttl"`
	RateLimiter         RateLimiterConfig                      `mapstructure:"rate_limiter" yaml:"rate_limiter"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	if err := commonconfig.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	setDefaults(&cfg)
	return &cfg, nil
}

func LoadFromContent(content string) (*Config, error) {
	var cfg Config
	if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
		return nil, err
	}
	setDefaults(&cfg)
	return &cfg, nil
}
