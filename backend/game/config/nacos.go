package config

import (
	commonconfig "github.com/cashparty/backend/common/config"
)

// GameNacosConfig 扩展共用 NacosConfig，添加 game 独有的 DataID。
type GameNacosConfig struct {
	commonconfig.NacosConfig `mapstructure:",squash" yaml:",inline"`
	AlgorithmDataID          string `mapstructure:"algorithm_data_id" yaml:"algorithm_data_id"`
	AlgorithmGroup           string `mapstructure:"algorithm_group" yaml:"algorithm_group"`
}
