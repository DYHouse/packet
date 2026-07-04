package config

import (
	"fmt"

	commonconfig "github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/gateway/router"
)

func LoadRouterConfig(configPath string) (*router.RouterConfig, error) {
	var cfg router.RouterConfig
	if err := commonconfig.LoadYAML(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load router config: %w", err)
	}

	return &cfg, nil
}

func LoadRouterConfigFromContent(content string) (*router.RouterConfig, error) {
	var cfg router.RouterConfig
	if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse router config content: %w", err)
	}

	return &cfg, nil
}
