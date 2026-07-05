package config

import (
	commonconfig "github.com/cashparty/backend/common/config"
)

// GatewayNacosConfig 扩展共用 NacosConfig，添加 gateway 独有的 DataID。
type GatewayNacosConfig struct {
	commonconfig.NacosConfig `mapstructure:",squash" yaml:",inline"`
	RouterDataID             string `mapstructure:"router_data_id" yaml:"router_data_id"`
	RouterGroup              string `mapstructure:"router_group" yaml:"router_group"`
}
