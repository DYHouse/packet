package bootstrap

import (
	"fmt"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	gameconfig "github.com/cashparty/backend/game/config"
)

// initNacos 构造 nacos 客户端。
// 未启用或创建失败时 Warn 后返回 nil（降级：服务发现不可用，但本地功能正常）。
func initNacos(cfg *gameconfig.Config) nacos.NacosClient {
	if !cfg.Nacos.Enabled {
		return nil
	}
	client, err := nacos.NewClient(&cfg.Nacos.NacosConfig)
	if err != nil {
		logger.Warn("failed to create nacos client, nacos disabled", "error", err)
		return nil
	}
	return client
}

// reloadMainConfigFromNacos 从 nacos 拉取主配置并解析覆盖。
// 失败时 Warn 后返回 oldCfg（保持现有配置继续运行）。
// 保留 nacos 运行时配置（不能被远程覆盖）。
func reloadMainConfigFromNacos(client nacos.NacosClient, oldCfg *gameconfig.Config) *gameconfig.Config {
	content, err := client.GetConfig(oldCfg.Nacos.ConfigDataID, oldCfg.Nacos.ConfigGroup)
	if err != nil {
		logger.Warn("get main config from nacos failed, keep local config",
			"data_id", oldCfg.Nacos.ConfigDataID, "error", err)
		return oldCfg
	}
	newCfg, err := gameconfig.LoadFromContent(content)
	if err != nil {
		logger.Warn("parse main config from nacos failed, keep local config",
			"data_id", oldCfg.Nacos.ConfigDataID, "error", err)
		return oldCfg
	}
	// 保留 nacos 运行时配置（不能被远程覆盖）
	newCfg.Nacos.NacosConfig = oldCfg.Nacos.NacosConfig
	logger.Info("main config loaded from nacos",
		"data_id", oldCfg.Nacos.ConfigDataID)
	return newCfg
}

// loadAlgorithmConfigFromNacos 从 nacos 拉取 algorithm 配置。
// 返回 (nil, nil) 表示未配置或 nacos 未启用。
func loadAlgorithmConfigFromNacos(client nacos.NacosClient, cfg *gameconfig.Config) (*gameconfig.AlgorithmConfig, error) {
	if client == nil || cfg.Nacos.AlgorithmDataID == "" {
		return nil, nil
	}
	content, err := client.GetConfig(cfg.Nacos.AlgorithmDataID, cfg.Nacos.AlgorithmGroup)
	if err != nil {
		return nil, fmt.Errorf("get algorithm config from nacos failed: %w", err)
	}
	algoCfg, err := gameconfig.LoadAlgorithmFromContent(content)
	if err != nil {
		return nil, fmt.Errorf("parse algorithm config from nacos failed: %w", err)
	}
	logger.Info("algorithm config loaded from nacos",
		"data_id", cfg.Nacos.AlgorithmDataID)
	return algoCfg, nil
}
