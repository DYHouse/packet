package nacos

import (
	"fmt"
	"sync"

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
	cfg          *ClientConfig
	serviceName  string
	serviceAddr  string
	servicePort  uint64
	mu           sync.RWMutex
	listeners    []func(content string)
}

func NewClient(cfg *ClientConfig) (*Client, error) {
	serverConfigs := []constant.ServerConfig{
		{
			IpAddr: cfg.ServerAddr,
			Port:   8848,
		},
	}

	if len(cfg.ServerAddr) > 0 && cfg.ServerAddr[len(cfg.ServerAddr)-1] >= '0' && cfg.ServerAddr[len(cfg.ServerAddr)-1] <= '9' {
		for i := len(cfg.ServerAddr) - 1; i >= 0; i-- {
			if cfg.ServerAddr[i] == ':' {
				var port uint64
				fmt.Sscanf(cfg.ServerAddr[i+1:], "%d", &port)
				serverConfigs[0].IpAddr = cfg.ServerAddr[:i]
				serverConfigs[0].Port = port
				break
			}
		}
	}

	clientConfig := constant.ClientConfig{
		NamespaceId:         cfg.Namespace,
		Username:            cfg.Username,
		Password:            cfg.Password,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              "/tmp/nacos/log",
		CacheDir:            "/tmp/nacos/cache",
		LogLevel:            "warn",
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
		listeners:    make([]func(content string), 0),
	}, nil
}

func (c *Client) RegisterService() error {
	if c.serviceName == "" {
		return fmt.Errorf("service name is empty")
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
		logger.Error("failed to deregister service", "error", err)
		return err
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

func (c *Client) GetOneInstance(serviceName string) (*model.Instance, error) {
	instance, err := c.namingClient.SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
		ServiceName: serviceName,
		GroupName:   c.cfg.Group,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get one instance for service %s: %w", serviceName, err)
	}

	return instance, nil
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

func (c *Client) PublishConfig(dataID, group, content string) (bool, error) {
	return c.configClient.PublishConfig(vo.ConfigParam{
		DataId:  dataID,
		Group:   group,
		Content: content,
	})
}

func (c *Client) SubscribeService(serviceName string, callback func(services []model.Instance)) error {
	err := c.namingClient.Subscribe(&vo.SubscribeParam{
		ServiceName: serviceName,
		GroupName:   c.cfg.Group,
		SubscribeCallback: func(services []model.Instance, err error) {
			if err != nil {
				logger.Error("service subscribe callback error", "error", err)
				return
			}
			callback(services)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe service: %w", err)
	}
	return nil
}

func (c *Client) Close() {
	c.DeregisterService()
}
