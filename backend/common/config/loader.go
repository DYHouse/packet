package config

import (
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// LoadYAML 用 viper 从文件加载 yaml 到 v。
func LoadYAML(path string, v interface{}) error {
	f := viper.New()
	f.SetConfigFile(path)
	f.SetConfigType("yaml")
	f.AutomaticEnv()
	if err := f.ReadInConfig(); err != nil {
		return fmt.Errorf("read config file failed: %w", err)
	}
	if err := f.Unmarshal(v, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
		return fmt.Errorf("parse config failed: %w", err)
	}
	return nil
}

// LoadYAMLFromContent 用 viper 从内容加载 yaml 到 v。
func LoadYAMLFromContent(content string, v interface{}) error {
	f := viper.New()
	f.SetConfigType("yaml")
	f.AutomaticEnv()
	if err := f.ReadConfig(strings.NewReader(content)); err != nil {
		return fmt.Errorf("read config content failed: %w", err)
	}
	if err := f.Unmarshal(v, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
		return fmt.Errorf("parse config failed: %w", err)
	}
	return nil
}
