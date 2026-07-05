package bootstrap

import (
	"runtime/debug"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	gameconfig "github.com/cashparty/backend/game/config"
)

// registerConfigListeners 注册所有 nacos 配置热更新回调。
// 在 Start() 中调用，失败时 Warn 但不阻塞启动。
//
// 注意：主配置（ConfigDataID）不在此注册——主配置仅在启动时通过
// reloadMainConfigFromNacos 拉取一次，不热更新。主配置变更需重启服务生效。
func registerConfigListeners(nacosClient *nacos.Client, cfg *gameconfig.Config, container *Container) {
	if nacosClient == nil {
		return
	}
	registerAlgorithmConfigListener(nacosClient, cfg, container)
	registerRateLimiterListener(nacosClient, cfg, container)
}

func registerAlgorithmConfigListener(nacosClient *nacos.Client, cfg *gameconfig.Config, container *Container) {
	if cfg.Nacos.AlgorithmDataID == "" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("algorithm config listener panic",
					"data_id", cfg.Nacos.AlgorithmDataID,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		if err := nacosClient.ListenConfig(
			cfg.Nacos.AlgorithmDataID,
			cfg.Nacos.AlgorithmGroup,
			func(content string) {
				algoCfg, err := gameconfig.LoadAlgorithmFromContent(content)
				if err != nil {
					logger.Warn("parse algorithm config from nacos failed, keep old config",
						"data_id", cfg.Nacos.AlgorithmDataID,
						"error", err)
					return
				}
				newConfig := convertAlgorithmConfig(algoCfg)
				container.PacketGenerator.UpdateConfig(newConfig)
				logger.Info("algorithm config reloaded from nacos",
					"data_id", cfg.Nacos.AlgorithmDataID)
			},
		); err != nil {
			logger.Warn("listen algorithm config failed",
				"data_id", cfg.Nacos.AlgorithmDataID,
				"error", err)
		}
	}()
}
