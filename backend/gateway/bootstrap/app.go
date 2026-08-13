package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/discovery"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	cRedis "github.com/cashparty/backend/common/redis"
	gatewayConfig "github.com/cashparty/backend/gateway/config"
	"github.com/cashparty/backend/gateway/router"
)

type Application struct {
	Container  *Container
	cfg        *gatewayConfig.Config
	nacos      nacos.NacosClient
	cancel     context.CancelFunc
	nodeID     string
	errChan    chan error
	appCtx     context.Context  // 新增
	taskRunner async.TaskRunner // 新增
	wg         sync.WaitGroup   // 新增：跟踪 BroadcastSvc/Server goroutine
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
		cfg = reloadMainConfigFromNacos(nacosClient, cfg)
	}

	initLogger(cfg)

	appCtx, cancel := context.WithCancel(context.Background())
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()
	taskRunner := async.NewTaskRunner(appCtx, 10*time.Second)
	if err := taskRunner.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start task runner failed: %w", err)
	}

	redisClient, err := initRedis(cfg)
	if err != nil {
		return nil, err
	}

	// 初始化雪花 ID 生成器（规约 SID-7：bootstrap 层显式初始化）。
	// gateway 仅使用 nodeID 作为 Kafka 消费者 group 后缀和节点标识，
	// node_id > 0：显式指定（单实例/测试环境）
	// node_id = 0：Redis 自动分配（多实例生产环境）
	if cfg.IDGenerator.Enabled {
		if cfg.IDGenerator.NodeID > 0 {
			if err := idgen.Init(cfg.IDGenerator.NodeID); err != nil {
				cancel()
				redisClient.Close()
				return nil, fmt.Errorf("failed to init id generator: %w", err)
			}
			logger.Info("id generator initialized with explicit node_id", "node_id", cfg.IDGenerator.NodeID)
		} else {
			allocator, err := idgen.InitWithAutoAlloc(appCtx, redisClient)
			if err != nil {
				cancel()
				redisClient.Close()
				return nil, fmt.Errorf("failed to init id generator with auto alloc: %w", err)
			}
			logger.Info("id generator initialized with auto-allocated node_id", "node_id", allocator.GetNodeID())
		}
	}

	nodeID := idgen.GetNodeIDString()

	logger.Info("gateway service starting",
		"name", cfg.Server.Name,
		"port", cfg.Server.Port,
		"max_connections", cfg.Gateway.MaxConnections,
		"go_version", runtime.Version(),
		"cpu_cores", runtime.NumCPU(),
		"node_id", nodeID,
	)

	container := NewContainer(cfg, redisClient, nodeID, taskRunner)

	routerConfig, err := loadRouterConfig(nacosClient, cfg, routerPath)
	if err != nil {
		return nil, err
	}

	var serviceDiscovery *discovery.ServiceDiscovery
	if nacosClient != nil {
		serviceDiscovery = discovery.NewServiceDiscovery(nacosClient, appCtx)
	} else {
		logger.Warn("nacos is disabled, service discovery will not work")
	}

	container.InitServices(serviceDiscovery, routerConfig)

	success = true
	return &Application{
		Container:  container,
		cfg:        cfg,
		nacos:      nacosClient,
		cancel:     cancel,
		nodeID:     nodeID,
		appCtx:     appCtx,
		taskRunner: taskRunner,
	}, nil
}

func (a *Application) Start(ctx context.Context) error {
	_ = ctx
	a.errChan = make(chan error, 1)

	a.Container.InitKafkaConsumer()
	a.Container.InitServer()

	if a.Container.BroadcastSvc != nil {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("broadcast service panic",
						"panic", r, "stack", string(debug.Stack()))
				}
			}()
			if err := a.Container.BroadcastSvc.Start(); err != nil {
				a.errChan <- err
			}
		}()
	}

	if a.nacos != nil {
		if err := a.nacos.RegisterService(); err != nil {
			logger.Warn("failed to register service to nacos", "error", err)
		}
	}

	registerConfigListeners(a.nacos, a.cfg, a.Container)

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gateway server panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
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

func (a *Application) Stop() error {
	logger.Info("shutting down gateway service...")

	// 1. 停止接受新异步任务 + cancel appCtx
	a.taskRunner.Stop()
	if a.cancel != nil {
		a.cancel()
	}

	// 2. 停止 container（含 connMgr.Stop / BroadcastSvc.Stop / Server.Stop / AuthMiddleware.Stop）
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if a.Container.Server != nil {
		a.Container.Server.Stop(shutdownCtx)
	}
	a.Container.Stop()

	// 3. 等待 BroadcastSvc/Server goroutine 退出
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warn("gateway goroutines wait timeout, force shutdown")
	}

	// 4. 等待 task runner
	waitRunner := make(chan struct{})
	go func() { a.taskRunner.Wait(); close(waitRunner) }()
	select {
	case <-waitRunner:
	case <-time.After(30 * time.Second):
		logger.Warn("task runner wait timeout, force shutdown")
	}

	// 5. 关闭底层资源（保留现有逻辑：nacos.Close, Redis.Close 等）
	if a.nacos != nil {
		if err := a.nacos.Close(); err != nil {
			logger.Warn("failed to close nacos client", "error", err)
		}
	}

	if a.Container.Redis != nil {
		a.Container.Redis.Close()
	}

	logger.Info("gateway service stopped")
	return nil
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

	if err := app.Stop(); err != nil {
		logger.Error("failed to stop application", "error", err)
	}
}

func initNacos(cfg *gatewayConfig.Config) (nacos.NacosClient, error) {
	if !cfg.Nacos.Enabled {
		return nil, nil
	}

	nacosClient, err := nacos.NewClient(&cfg.Nacos.NacosConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create nacos client: %w", err)
	}

	return nacosClient, nil
}

func reloadMainConfigFromNacos(nacosClient nacos.NacosClient, cfg *gatewayConfig.Config) *gatewayConfig.Config {
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

func initRedis(cfg *gatewayConfig.Config) (cRedis.RedisClient, error) {
	return cRedis.NewClient(&cfg.Redis)
}

func loadRouterConfig(nacosClient nacos.NacosClient, cfg *gatewayConfig.Config, routerPath string) (*router.RouterConfig, error) {
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
