package config

// NacosConfig 是 nacos 共用配置基础。各服务独有的 DataID 通过嵌入扩展。
// Used by: game (via GameNacosConfig), gateway (via GatewayNacosConfig).
// Not used by: stats.
type NacosConfig struct {
	// --- 连接 ---
	Enabled    bool   `mapstructure:"enabled" yaml:"enabled"`
	ServerAddr string `mapstructure:"server_addr" yaml:"server_addr"`
	Namespace  string `mapstructure:"namespace" yaml:"namespace"`
	Group      string `mapstructure:"group" yaml:"group"`
	Username   string `mapstructure:"username" yaml:"username"`
	Password   string `mapstructure:"password" yaml:"password"`

	// --- 服务注册 ---
	ServiceName string `mapstructure:"service_name" yaml:"service_name"`
	ServiceAddr string `mapstructure:"service_addr" yaml:"service_addr"`
	ServicePort uint64 `mapstructure:"service_port" yaml:"service_port"`

	// --- 主配置 DataID ---
	ConfigDataID string `mapstructure:"config_data_id" yaml:"config_data_id"`
	ConfigGroup  string `mapstructure:"config_group" yaml:"config_group"`

	// --- sdk 调优 ---
	TimeoutMs uint64 `mapstructure:"timeout_ms" yaml:"timeout_ms"`
	LogLevel  string `mapstructure:"log_level" yaml:"log_level"`
	LogDir    string `mapstructure:"log_dir" yaml:"log_dir"`
	CacheDir  string `mapstructure:"cache_dir" yaml:"cache_dir"`
}

// SetNacosDefaults 设置共用 NacosConfig 的默认值。
func SetNacosDefaults(cfg *NacosConfig) {
	if cfg.ServerAddr == "" {
		cfg.ServerAddr = "127.0.0.1:8848"
	}
	if cfg.Group == "" {
		cfg.Group = "DEFAULT_GROUP"
	}
	if cfg.Username == "" {
		cfg.Username = "nacos"
	}
	if cfg.Password == "" {
		cfg.Password = "nacos"
	}
	if cfg.ConfigGroup == "" {
		cfg.ConfigGroup = "DEFAULT_GROUP"
	}
	if cfg.TimeoutMs == 0 {
		cfg.TimeoutMs = 5000
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "warn"
	}
	if cfg.LogDir == "" {
		cfg.LogDir = "/tmp/nacos/log"
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = "/tmp/nacos/cache"
	}
}
