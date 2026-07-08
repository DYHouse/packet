package bootstrap

import (
	"github.com/cashparty/backend/common/nacos"
	gatewayConfig "github.com/cashparty/backend/gateway/config"
)

// registerConfigListeners 注册所有 nacos 配置热更新回调。
// 在 Start() 中调用，失败时 Warn 但不阻塞启动。
//
// 注意：主配置（ConfigDataID）不在此注册——主配置仅在启动时通过
// reloadMainConfigFromNacos 拉取一次，不热更新。主配置变更需重启服务生效。
func registerConfigListeners(nacosClient nacos.NacosClient, cfg *gatewayConfig.Config, container *Container) {
	if nacosClient == nil {
		return
	}
}
