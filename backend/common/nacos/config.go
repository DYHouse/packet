package nacos

type ClientConfig struct {
	ServerAddr    string `mapstructure:"server_addr" yaml:"server_addr"`
	Namespace     string `mapstructure:"namespace" yaml:"namespace"`
	Group         string `mapstructure:"group" yaml:"group"`
	Username      string `mapstructure:"username" yaml:"username"`
	Password      string `mapstructure:"password" yaml:"password"`
	ServiceName   string `mapstructure:"service_name" yaml:"service_name"`
	ServiceAddr   string `mapstructure:"service_addr" yaml:"service_addr"`
	ServicePort   uint64 `mapstructure:"service_port" yaml:"service_port"`
	ConfigDataID  string `mapstructure:"config_data_id" yaml:"config_data_id"`
	ConfigGroup   string `mapstructure:"config_group" yaml:"config_group"`
}

func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerAddr:   "127.0.0.1:8848",
		Namespace:    "",
		Group:        "DEFAULT_GROUP",
		Username:     "nacos",
		Password:     "nacos",
		ConfigGroup:  "DEFAULT_GROUP",
	}
}
