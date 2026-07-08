package nacos

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/strutil"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// NacosClient Nacos 客户端接口，支持 mock 测试。
// 封装 nacos sdk：服务注册、反注册、发现、配置拉取、配置监听。
type NacosClient interface {
	RegisterService() error
	DeregisterService() error
	DiscoverService(serviceName string) ([]model.Instance, error)
	GetConfig(dataID, group string) (string, error)
	ListenConfig(dataID, group string, onChange func(content string)) error
	Close() error
}

// client Nacos 客户端实现
type client struct {
	configClient config_client.IConfigClient
	namingClient naming_client.INamingClient
	cfg          *config.NacosConfig
	serviceName  string
	serviceAddr  string
	servicePort  uint64
	mu           sync.Mutex
	closed       bool
}

// NewClient 创建 Nacos 客户端。
func NewClient(cfg *config.NacosConfig) (NacosClient, error) {
	host, port := parseServerAddr(cfg.ServerAddr)
	serverConfigs := []constant.ServerConfig{
		{
			IpAddr: host,
			Port:   port,
		},
	}

	clientConfig := constant.ClientConfig{
		NamespaceId:         cfg.Namespace,
		Username:            cfg.Username,
		Password:            cfg.Password,
		TimeoutMs:           cfg.TimeoutMs,
		NotLoadCacheAtStart: true,
		LogDir:              cfg.LogDir,
		CacheDir:            cfg.CacheDir,
		LogLevel:            cfg.LogLevel,
	}

	configClient, err := clients.NewConfigClient(
		vo.NacosClientParam{
			ClientConfig:  &clientConfig,
			ServerConfigs: serverConfigs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create nacos config client: %w", err)
	}

	namingClient, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ClientConfig:  &clientConfig,
			ServerConfigs: serverConfigs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create nacos naming client: %w", err)
	}

	return &client{
		configClient: configClient,
		namingClient: namingClient,
		cfg:          cfg,
		serviceName:  cfg.ServiceName,
		serviceAddr:  cfg.ServiceAddr,
		servicePort:  cfg.ServicePort,
	}, nil
}

// parseServerAddr 解析 ServerAddr（host 或 host:port）。
// 使用 net.SplitHostPort 解析，更健壮。
func parseServerAddr(addr string) (string, uint64) {
	if !strings.Contains(addr, ":") {
		return addr, 8848
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 8848
	}
	port, err := strconv.ParseUint(portStr, 10, 64)
	if err != nil {
		return host, 8848
	}
	return host, port
}

func (c *client) RegisterService() error {
	if c.serviceName == "" {
		return ErrServiceNameEmpty
	}

	_, err := c.namingClient.RegisterInstance(vo.RegisterInstanceParam{
		ServiceName: c.serviceName,
		Ip:          c.serviceAddr,
		Port:        c.servicePort,
		Weight:      1,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   true,
		GroupName:   c.cfg.Group,
	})
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	logger.Info("service registered to nacos",
		"service", c.serviceName,
		"address", strutil.JoinHostPort(c.serviceAddr, int(c.servicePort)),
		"group", c.cfg.Group,
	)
	return nil
}

func (c *client) DeregisterService() error {
	if c.serviceName == "" {
		return nil
	}

	_, err := c.namingClient.DeregisterInstance(vo.DeregisterInstanceParam{
		ServiceName: c.serviceName,
		Ip:          c.serviceAddr,
		Port:        c.servicePort,
		GroupName:   c.cfg.Group,
		Ephemeral:   true,
	})
	if err != nil {
		return fmt.Errorf("deregister service %s failed: %w", c.serviceName, err)
	}

	logger.Info("service deregistered from nacos", "service", c.serviceName)
	return nil
}

func (c *client) DiscoverService(serviceName string) ([]model.Instance, error) {
	instances, err := c.namingClient.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		GroupName:   c.cfg.Group,
		HealthyOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to discover service %s: %w", serviceName, err)
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("no healthy instances found for service %s", serviceName)
	}

	return instances, nil
}

func (c *client) GetConfig(dataID, group string) (string, error) {
	content, err := c.configClient.GetConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	return content, nil
}

func (c *client) ListenConfig(dataID, group string, onChange func(content string)) error {
	err := c.configClient.ListenConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
		OnChange: func(namespace, group, dataID, data string) {
			logger.Info("config changed",
				"data_id", dataID,
				"group", group,
			)
			onChange(data)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to listen config: %w", err)
	}
	return nil
}

func (c *client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.DeregisterService()
}
