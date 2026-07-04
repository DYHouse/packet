package config

import "fmt"

type ServerConfig struct {
	Name     string `mapstructure:"name" yaml:"name"`
	GRPCPort int    `mapstructure:"grpc_port" yaml:"grpc_port"`
	HTTPPort int    `mapstructure:"http_port" yaml:"http_port"`
	WSPort   int    `mapstructure:"ws_port" yaml:"ws_port"`
	Mode     string `mapstructure:"mode" yaml:"mode"` // debug / release
}

func (c *ServerConfig) HTTPAddr() string {
	return fmt.Sprintf(":%d", c.HTTPPort)
}

func SetServerDefaults(cfg *ServerConfig) {
	// Name/Mode/Port 无全局默认值（由各服务 setDefaults 设置）
}
