package bootstrap

import (
	"runtime/debug"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	gameconfig "github.com/cashparty/backend/game/config"
)

// registerRateLimiterListener 注册限流配置的 nacos 热更新监听。
// 失败时 Warn 但不阻塞启动；运行期 panic 会被 recover 兜底。
func registerRateLimiterListener(nacosClient nacos.NacosClient, cfg *gameconfig.Config, container *Container) {
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
				rateLimiterCfg, err := gameconfig.LoadRateLimiterFromContent(content)
				if err != nil {
					logger.Warn("parse rate limiter config from nacos failed, keep old config",
						"data_id", cfg.Nacos.RateLimiterDataID,
						"error", err)
					return
				}
				newConfigs := buildUserLimiterConfigs(rateLimiterCfg)
				container.UserLimiter.UpdateConfigs(newConfigs)
				// 更新 Container 中持有的配置指针,供后续访问
				container.RateLimiterCfg = rateLimiterCfg
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
