package bootstrap

import (
	"runtime/debug"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	gatewayConfig "github.com/cashparty/backend/gateway/config"
)

// registerConfigListeners 注册所有 nacos 配置热更新回调。
// 在 Start() 中调用，失败时 Warn 但不阻塞启动。
//
// 注意：主配置（ConfigDataID）不在此注册——主配置仅在启动时通过
// reloadMainConfigFromNacos 拉取一次，不热更新。主配置变更需重启服务生效。
func registerConfigListeners(nacosClient *nacos.Client, cfg *gatewayConfig.Config, container *Container) {
	if nacosClient == nil {
		return
	}
	registerRateLimiterConfigListener(nacosClient, cfg, container)
}

func registerRateLimiterConfigListener(nacosClient *nacos.Client, cfg *gatewayConfig.Config, container *Container) {
	if cfg.Nacos.RateLimiterDataID == "" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("rate limiter config listener panic",
					"data_id", cfg.Nacos.RateLimiterDataID,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		if err := nacosClient.ListenConfig(
			cfg.Nacos.RateLimiterDataID,
			cfg.Nacos.RateLimiterGroup,
			func(content string) {
				rlCfg, err := gatewayConfig.LoadRateLimiterFromContent(content)
				if err != nil {
					logger.Warn("parse rate limiter config from nacos failed, keep old config",
						"data_id", cfg.Nacos.RateLimiterDataID,
						"error", err)
					return
				}
				container.RateLimiter.UpdateConfig(rlCfg)
				logger.Info("rate limiter config reloaded from nacos",
					"data_id", cfg.Nacos.RateLimiterDataID)
			},
		); err != nil {
			logger.Warn("listen rate limiter config failed",
				"data_id", cfg.Nacos.RateLimiterDataID,
				"error", err)
		}
	}()
}
