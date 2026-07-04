package nacos

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type Client struct {
	configClient config_client.IConfigClient
	namingClient naming_client.INamingClient
	cfg          *config.NacosConfig
	serviceName  string
	serviceAddr  string
	servicePort  uint64
	mu           sync.Mutex
	closed       bool
}

func NewClient(cfg *config.NacosConfig) (*Client, error) {
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

	return &Client{
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

func (c *Client) RegisterService() error {
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
		"address", fmt.Sprintf("%s:%d", c.serviceAddr, c.servicePort),
		"group", c.cfg.Group,
	)
	return nil
}

func (c *Client) DeregisterService() error {
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

func (c *Client) DiscoverService(serviceName string) ([]model.Instance, error) {
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

func (c *Client) GetConfig(dataID, group string) (string, error) {
	content, err := c.configClient.GetConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	return content, nil
}

func (c *Client) ListenConfig(dataID, group string, onChange func(content string)) error {
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

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.DeregisterService()
}
