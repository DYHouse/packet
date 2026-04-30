package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	cRedis "github.com/cashparty/backend/common/redis"
	gatewayConfig "github.com/cashparty/backend/gateway/config"
	"github.com/cashparty/backend/gateway/discovery"
	"github.com/cashparty/backend/gateway/router"
)

type Application struct {
	Container *Container
	cfg       *gatewayConfig.Config
	nacos     *nacos.Client
	cancel    context.CancelFunc
	nodeID    string
	errChan   chan error
}

func NewApplication(cfgPath, routerPath string) (*Application, error) {
	cfg, err := gatewayConfig.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return NewApplicationWithConfig(cfg, routerPath)
}

func NewApplicationWithConfig(cfg *gatewayConfig.Config, routerPath string) (*Application, error) {
	nacosClient, err := initNacos(cfg)
	if err != nil {
		return nil, err
	}

	if nacosClient != nil && cfg.Nacos.ConfigDataID != "" {
		cfg = reloadConfigFromNacos(nacosClient, cfg)
	}

	initLogger(cfg)

	nodeID := idgen.GetNodeIDString()

	logger.Info("gateway service starting",
		"name", cfg.Server.Name,
		"port", cfg.Server.Port,
		"max_connections", cfg.Gateway.MaxConnections,
		"go_version", runtime.Version(),
		"cpu_cores", runtime.NumCPU(),
		"node_id", nodeID,
	)

	redisClient, err := initRedis(cfg)
	if err != nil {
		return nil, err
	}

	var kafkaProducer *kafka.Producer
	if cfg.Kafka.Enabled {
		kafkaProducer = kafka.NewProducerWithBrokers(cfg.Kafka.Brokers)
	}

	container := NewContainer(cfg, redisClient, kafkaProducer, nodeID)

	routerConfig, err := loadRouterConfig(nacosClient, cfg, routerPath)
	if err != nil {
		return nil, err
	}

	var serviceDiscovery *discovery.ServiceDiscovery
	if nacosClient != nil {
		serviceDiscovery = discovery.NewServiceDiscovery(nacosClient)
	} else {
		logger.Warn("nacos is disabled, service discovery will not work")
	}

	container.InitServices(serviceDiscovery, routerConfig)

	return &Application{
		Container: container,
		cfg:       cfg,
		nacos:     nacosClient,
		nodeID:    nodeID,
	}, nil
}

func (a *Application) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.errChan = make(chan error, 1)

	a.Container.InitKafkaConsumer()
	a.Container.InitServer()

	if a.Container.BroadcastSvc != nil {
		go a.Container.BroadcastSvc.Start()
	}

	if a.nacos != nil {
		if err := a.nacos.RegisterService(); err != nil {
			logger.Error("failed to register service to nacos", "error", err)
		}
	}

	if a.nacos != nil && a.cfg.Nacos.RateLimiterDataID != "" {
		go func() {
			err := a.nacos.ListenConfig(
				a.cfg.Nacos.RateLimiterDataID,
				a.cfg.Nacos.RateLimiterGroup,
				func(content string) {
					logger.Info("rate limiter config changed, reloading...")
					rlCfg, err := gatewayConfig.LoadRateLimiterFromContent(content)
					if err != nil {
						logger.Error("failed to parse rate limiter config", "error", err)
						return
					}
					a.Container.RateLimiter.UpdateConfig(rlCfg)
					logger.Info("rate limiter config reloaded successfully")
				},
			)
			if err != nil {
				logger.Error("failed to listen rate limiter config", "error", err)
			}
		}()
	}

	go func() {
		if err := a.Container.Server.Start(); err != nil {
			a.errChan <- err
		}
	}()

	logger.Info("gateway application started", "port", a.cfg.Server.Port)
	return nil
}

func (a *Application) Wait() error {
	return <-a.errChan
}

func (a *Application) Stop() {
	logger.Info("shutting down gateway service...")

	if a.cancel != nil {
		a.cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	a.Container.Server.Stop(shutdownCtx)
	a.Container.Stop()

	if a.nacos != nil {
		a.nacos.Close()
	}

	if a.Container.Redis != nil {
		a.Container.Redis.Close()
	}

	logger.Info("gateway service stopped")
}

func Run() {
	cfgPath := "config/gateway.yaml"
	routerPath := "config/gateway-router.yaml"

	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	if len(os.Args) > 2 {
		routerPath = os.Args[2]
	}

	app, err := NewApplication(cfgPath, routerPath)
	if err != nil {
		logger.Fatal("failed to create application", "error", err)
	}

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		logger.Fatal("failed to start application", "error", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		logger.Info("received shutdown signal")
	case err := <-app.errChan:
		logger.Error("application error", "error", err)
	}

	app.Stop()
}

func initNacos(cfg *gatewayConfig.Config) (*nacos.Client, error) {
	if !cfg.Nacos.Enabled {
		return nil, nil
	}

	nacosClient, err := nacos.NewClient(&nacos.ClientConfig{
		ServerAddr:  cfg.Nacos.ServerAddr,
		Namespace:   cfg.Nacos.Namespace,
		Group:       cfg.Nacos.Group,
		Username:    cfg.Nacos.Username,
		Password:    cfg.Nacos.Password,
		ServiceName: cfg.Nacos.ServiceName,
		ServiceAddr: cfg.Nacos.ServiceAddr,
		ServicePort: uint64(cfg.Nacos.ServicePort),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create nacos client: %w", err)
	}

	return nacosClient, nil
}

func reloadConfigFromNacos(nacosClient *nacos.Client, cfg *gatewayConfig.Config) *gatewayConfig.Config {
	content, err := nacosClient.GetConfig(cfg.Nacos.ConfigDataID, cfg.Nacos.ConfigGroup)
	if err != nil {
		logger.Warn("failed to get config from nacos, using local config", "error", err)
		return cfg
	}

	newCfg, err := gatewayConfig.LoadFromContent(content)
	if err != nil {
		logger.Warn("failed to parse config from nacos, using local config", "error", err)
		return cfg
	}

	logger.Info("loaded config from nacos", "data_id", cfg.Nacos.ConfigDataID)
	return newCfg
}

func initLogger(cfg *gatewayConfig.Config) {
	logger.Init(&logger.LogConfig{
		Level:    cfg.Log.Level,
		Filename: cfg.Log.Filename,
	})
}

func initRedis(cfg *gatewayConfig.Config) (*cRedis.Client, error) {
	redisClient, err := cRedis.NewClient(&config.RedisConfig{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init redis: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := redisClient.Raw().Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return redisClient, nil
}

func loadRouterConfig(nacosClient *nacos.Client, cfg *gatewayConfig.Config, routerPath string) (*router.RouterConfig, error) {
	if nacosClient != nil && cfg.Nacos.RouterDataID != "" {
		routerContent, err := nacosClient.GetConfig(cfg.Nacos.RouterDataID, cfg.Nacos.RouterGroup)
		if err != nil {
			logger.Warn("failed to get router config from nacos, using local config", "error", err)
		} else {
			routerConfig, err := gatewayConfig.LoadRouterConfigFromContent(routerContent)
			if err != nil {
				return nil, fmt.Errorf("failed to parse router config from nacos: %w", err)
			}
			logger.Info("loaded router config from nacos", "data_id", cfg.Nacos.RouterDataID)
			return routerConfig, nil
		}
	}

	routerConfig, err := gatewayConfig.LoadRouterConfig(routerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load router config: %w", err)
	}

	return routerConfig, nil
}
