package bootstrap

import (
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/discovery"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway/broadcast"
	gatewayConfig "github.com/cashparty/backend/gateway/config"
	"github.com/cashparty/backend/gateway/connection"
	"github.com/cashparty/backend/gateway/handler"
	"github.com/cashparty/backend/gateway/health"
	"github.com/cashparty/backend/gateway/middleware"
	"github.com/cashparty/backend/gateway/router"
	"github.com/cashparty/backend/gateway/server"
	"github.com/cashparty/backend/gateway/service"
	"github.com/cashparty/backend/gateway/store"
)

type Container struct {
	Config              *gatewayConfig.Config
	Redis               *cRedis.Client
	KafkaConsumer       *kafka.Consumer
	ConnMgr             *connection.Manager
	BroadcastSvc        *broadcast.BroadcastService
	ServiceDiscovery    *discovery.ServiceDiscovery
	RouterConfig        *router.RouterConfig
	MessageRouter       *router.MessageRouter
	TokenService        *service.TokenService
	TestService         *service.TestService
	AuthMiddleware      *middleware.AuthMiddleware
	RateLimiter         *middleware.RateLimiter
	HealthChecker       *health.HealthChecker
	SignatureMiddleware *middleware.SignatureMiddleware
	GameService         *service.GameService
	GameStore           *store.MemoryGameStore
	GameHandler         *handler.GameHandler
	Server              *server.Server
	TaskRunner          *async.TaskRunner
	nodeID              string
}

func NewContainer(cfg *gatewayConfig.Config, redis *cRedis.Client, nodeID string, taskRunner *async.TaskRunner) *Container {
	connMgr := connection.NewManager(&connection.ManagerConfig{
		MaxConnections:       cfg.Gateway.MaxConnections,
		DisconnectTimeout:    30 * time.Second,
		ConnRedisTTL:         24 * time.Hour,
		HeartbeatRenewalTick: 30 * time.Second,
	}, redis, nodeID)

	return &Container{
		Config:     cfg,
		Redis:      redis,
		ConnMgr:    connMgr,
		TaskRunner: taskRunner,
		nodeID:     nodeID,
	}
}

func (c *Container) InitServices(serviceDiscovery *discovery.ServiceDiscovery, routerConfig *router.RouterConfig) {
	c.ServiceDiscovery = serviceDiscovery
	c.RouterConfig = routerConfig
	c.MessageRouter = router.NewMessageRouter(c.ServiceDiscovery, c.RouterConfig)

	c.RateLimiter = middleware.NewRateLimiter(c.Redis, &c.Config.RateLimiter)
	c.HealthChecker = health.NewHealthChecker(c.Redis, c.ConnMgr)

	c.TokenService = service.NewTokenService(&service.TokenConfig{
		SecretKey: c.Config.Token.SecretKey,
		TokenTTL:  c.Config.Token.TokenTTL,
		Issuer:    c.Config.Token.Issuer,
	})

	c.AuthMiddleware = middleware.NewAuthMiddleware(c.TokenService, c.Redis)

	c.SignatureMiddleware = middleware.NewSignatureMiddleware(
		c.Config.Merchant.ID,
		c.Config.Merchant.Secret,
	)

	c.GameStore = store.NewMemoryGameStore()

	var userSaver service.UserSaver
	if serviceDiscovery != nil {
		userSaver = discovery.NewUserSaverAdapter(serviceDiscovery)
	}
	c.GameService = service.NewGameService(c.Config, c.TokenService, userSaver)
	c.TestService = service.NewTestService(c.TokenService, userSaver)
	c.GameHandler = handler.NewGameHandler(c.GameService, c.GameStore)

	c.ConnMgr.SetEventCallback(func(conn *connection.Connection, event string, roomID string) {
		switch event {
		case "kicked":
			logger.Info("connection kicked", "user_id", conn.UserID)
		case "disconnected":
			logger.Info("connection disconnected", "user_id", conn.UserID, "room_id", roomID)
		}
	})
}

func (c *Container) InitServer() {
	c.Server = server.NewServer(
		&server.Config{
			Port:            c.Config.Server.Port,
			ReadTimeout:     c.Config.Server.ReadTimeout,
			WriteTimeout:    c.Config.Server.WriteTimeout,
			ReadBufferSize:  c.Config.Gateway.ReadBufferSize,
			WriteBufferSize: c.Config.Gateway.WriteBufferSize,
			SendQueueSize:   c.Config.Gateway.SendQueueSize,
			AllowedOrigins:  c.Config.Gateway.AllowedOrigins,
			TestEnabled:     c.Config.Server.TestEnabled,
		},
		c.ConnMgr,
		c.AuthMiddleware,
		c.MessageRouter,
		c.BroadcastSvc,
		c.RateLimiter,
		c.HealthChecker,
		c.SignatureMiddleware,
		c.GameHandler,
		c.TestService,
	)
}

func (c *Container) InitKafkaConsumer() {
	svc, err := createBroadcastService(c.Config, c.Redis, c.ConnMgr, c.nodeID)
	if err != nil {
		logger.Warn("failed to create broadcast service", "error", err)
		return
	}
	c.BroadcastSvc = svc
}

func (c *Container) Stop() {
	if c.RateLimiter != nil {
		c.RateLimiter.Stop()
	}
	if c.AuthMiddleware != nil {
		c.AuthMiddleware.Stop()
	}
	if c.SignatureMiddleware != nil {
		c.SignatureMiddleware.Stop()
	}
	if c.BroadcastSvc != nil {
		c.BroadcastSvc.Stop()
	}
	if c.ConnMgr != nil {
		c.ConnMgr.Stop()
	}
}
