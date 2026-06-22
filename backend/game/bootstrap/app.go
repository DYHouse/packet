package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/mysql"
	"github.com/cashparty/backend/common/nacos"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/application"
	"github.com/cashparty/backend/game/infrastructure/messaging"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	redisRepo "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/server"
	settlementConfig "github.com/cashparty/backend/settlement/config"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type Application struct {
	Container  *Container
	config     *config.Config
	grpcServer *server.GRPCServer
	cancel     context.CancelFunc
	nacos      *nacos.Client
	grpcPort   int
}

func NewApplication(cfgPath string) (*Application, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return NewApplicationWithConfig(cfg)
}

func NewApplicationWithConfig(cfg *config.Config) (*Application, error) {
	var nacosClient *nacos.Client
	var err error

	if cfg.Nacos.Enabled {
		nacosClient, err = nacos.NewClient(&nacos.ClientConfig{
			ServerAddr:  cfg.Nacos.ServerAddr,
			Namespace:   cfg.Nacos.Namespace,
			Group:       cfg.Nacos.Group,
			Username:    cfg.Nacos.Username,
			Password:    cfg.Nacos.Password,
			ServiceName: cfg.Nacos.ServiceName,
			ServiceAddr: cfg.Nacos.ServiceAddr,
			ServicePort: cfg.Nacos.ServicePort,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create nacos client: %w", err)
		}

		if cfg.Nacos.ConfigDataID != "" {
			content, err := nacosClient.GetConfig(cfg.Nacos.ConfigDataID, cfg.Nacos.ConfigGroup)
			if err != nil {
				logger.Warn("failed to get config from nacos, using local config", "error", err)
			} else {
				cfg, err = config.LoadFromContent(content)
				if err != nil {
					return nil, fmt.Errorf("failed to parse config from nacos: %w", err)
				}
				logger.Info("loaded config from nacos", "data_id", cfg.Nacos.ConfigDataID)
			}
		}

		if cfg.Nacos.AlgorithmDataID != "" {
			content, err := nacosClient.GetConfig(cfg.Nacos.AlgorithmDataID, cfg.Nacos.AlgorithmGroup)
			if err != nil {
				logger.Warn("failed to get algorithm config from nacos, using local config", "error", err)
			} else {
				algoCfg, err := config.LoadAlgorithmFromContent(content)
				if err != nil {
					return nil, fmt.Errorf("failed to parse algorithm config from nacos: %w", err)
				}
				cfg.Algorithm = *algoCfg
				logger.Info("loaded algorithm config from nacos", "data_id", cfg.Nacos.AlgorithmDataID)
			}
		}
	}

	logger.Init(&logger.LogConfig{
		Level:    cfg.Log.Level,
		Filename: cfg.Log.Filename,
	})

	logger.Info("game service starting", "port", cfg.Server.GRPCPort)

	if cfg.IDGenerator.Enabled {
		idgen.InitFromEnv()
		logger.Info("id generator initialized from environment")
	}

	redisClient, err := cRedis.NewClient(&cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis client: %w", err)
	}

	lock.InitLocker(redisClient)

	db, err := mysql.NewDB(&cfg.MySQL)
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("failed to create mysql client: %w", err)
	}

	kafkaProducer := kafka.NewProducerWithBrokers(cfg.Kafka.Brokers)

	platformClient, err := platform.NewClient(&cfg.Platform)
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("failed to create platform client: %w", err)
	}
	settlementRecorder := settlementService.NewBillManager(db)

	traceIDGen := settlementService.NewTraceIDGenerator(idgen.GetDefaultGenerator())
	platformCfg := settlementConfig.FromCommonConfig(&cfg.Platform)

	dbRepo := mysqlRepo.NewDBRepository(db)
	userSvc := application.NewUserService(dbRepo, redisClient, &cfg.Avatar)
	userIDConvert := settlementService.NewUserIDConvertService(userSvc)
	exceptionMgr := settlementService.NewExceptionManager(db)

	callMgr := settlementService.NewPlatformCallManager(db)
	creditRetrySvc := settlementService.NewCreditRetryService(settlementRecorder, platformClient, redisClient, traceIDGen, platformCfg, exceptionMgr, userIDConvert, callMgr)

	// Robot checker and settlement-layer virtual balance service (shared with
	// game-layer robot services via the same Redis keys).
	robotChecker := settlementService.NewRobotChecker(redisClient)
	settlementVirtualBalance := settlementService.NewVirtualBalanceService(redisClient)

	deductSvc := settlementService.NewDeductService(platformClient, settlementRecorder, redisClient, traceIDGen, platformCfg, creditRetrySvc, userIDConvert, callMgr, robotChecker, settlementVirtualBalance)
	refundSvc := settlementService.NewRefundService(platformClient, settlementRecorder, redisClient, traceIDGen, db, platformCfg, userIDConvert, callMgr)
	rewardSettler := settlementService.NewRewardSettler(settlementService.DefaultRewardSettlementConfig(), settlementRecorder, traceIDGen)
	gameSettleSvc := settlementService.NewGameSettleService(platformClient, settlementRecorder, redisClient, traceIDGen, platformCfg, userIDConvert, callMgr, robotChecker, settlementVirtualBalance)

	settlementSvc := settlementService.NewSettlementService(platformClient, settlementRecorder, redisClient, traceIDGen, platformCfg, deductSvc, rewardSettler, gameSettleSvc, userIDConvert, callMgr, robotChecker, settlementVirtualBalance)

	algorithmConfig := convertAlgorithmConfig(&cfg.Algorithm)
	logger.Info("algorithm config loaded", "room_configs_count", len(algorithmConfig.RewardControl.RoomConfigs))
	roomRepo := redisRepo.NewRoomRepository(redisClient)
	packetGenerator := algorithm.NewPacketGenerator(algorithmConfig, redisClient, db, roomRepo)

	// Validate robot configuration before assembling robot services.
	application.ValidateRobotConfig(&cfg.Robot)

	container := NewContainer(&cfg.Platform, &cfg.Timeout, &cfg.Avatar, &cfg.Robot, &cfg.Broadcast, db, redisClient, kafkaProducer, settlementSvc, packetGenerator, roomRepo,
		platformClient, settlementRecorder, traceIDGen, platformCfg, userIDConvert, exceptionMgr, creditRetrySvc, deductSvc, refundSvc, rewardSettler, callMgr, gameSettleSvc)
	container.InitAppServices()

	return &Application{
		Container: container,
		config:    cfg,
		nacos:     nacosClient,
		grpcPort:  cfg.Server.GRPCPort,
	}, nil
}

func (a *Application) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	nodeID := idgen.GetNodeIDString()

	roomEventConsumerCfg := kafka.NewConsumerConfig(
		a.config.Kafka.Brokers,
		kafka.TopicRoomEvents,
		fmt.Sprintf("game-room-events-%s", nodeID),
	)
	roomEventConsumer := a.Container.NewRoomEventConsumer(roomEventConsumerCfg)

	gameEventConsumer := messaging.NewGameEventConsumer(a.Container.DB, a.Container.Redis, a.Container.SettlementSvc, a.Container.GetRobotBehaviorEngine())
	gameEventKafkaConsumer := kafka.NewConsumer(
		a.config.Kafka.Brokers,
		kafka.TopicGameEvents,
		fmt.Sprintf("game-events-%s", nodeID),
		gameEventConsumer.HandleEvent,
	)

	a.Container.StartSchedulers()

	go roomEventConsumer.Start(ctx)
	go gameEventKafkaConsumer.Start(ctx)

	a.grpcServer = server.NewGRPCServer(
		a.grpcPort,
		a.Container.RoomAppService,
		a.Container.SeatAppService,
		a.Container.GameAppService,
		a.Container.GrabService,
		a.Container.UserService,
		a.Container.BalanceService,
		a.Container.Redis,
		nil,
		a.Container.Broadcaster.Broadcast,
	)

	if a.nacos != nil {
		if err := a.nacos.RegisterService(); err != nil {
			logger.Error("failed to register service to nacos", "error", err)
		}

		if a.config.Nacos.AlgorithmDataID != "" {
			go func() {
				err := a.nacos.ListenConfig(
					a.config.Nacos.AlgorithmDataID,
					a.config.Nacos.AlgorithmGroup,
					func(content string) {
						logger.Info("algorithm config changed, reloading...")
						algoCfg, err := config.LoadAlgorithmFromContent(content)
						if err != nil {
							logger.Error("failed to parse algorithm config", "error", err)
							return
						}
						newConfig := convertAlgorithmConfig(algoCfg)
						a.Container.PacketGenerator.UpdateConfig(newConfig)
						logger.Info("algorithm config reloaded successfully")
					},
				)
				if err != nil {
					logger.Error("failed to listen algorithm config", "error", err)
				}
			}()
		}
	}

	if err := a.grpcServer.Start(); err != nil {
		return err
	}

	logger.Info("game application started")
	return nil
}

func (a *Application) Stop() {
	logger.Info("shutting down game service...")

	if a.cancel != nil {
		a.cancel()
	}

	a.Container.Stop()

	if a.grpcServer != nil {
		a.grpcServer.Stop()
	}

	if a.nacos != nil {
		a.nacos.Close()
	}

	if a.Container.KafkaProducer != nil {
		a.Container.KafkaProducer.Close()
	}

	if a.Container.Redis != nil {
		a.Container.Redis.Close()
	}

	logger.Info("game service stopped")
}

func Run() {
	cfgPath := "config/game.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	app, err := NewApplication(cfgPath)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		panic(err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	app.Stop()
}

func convertAlgorithmConfig(cfg *config.AlgorithmConfig) *algorithm.Config {
	algoCfg := algorithm.DefaultConfig()

	if cfg.MinPacketAmount > 0 {
		algoCfg.MinPacketAmount = cfg.MinPacketAmount
	}
	if cfg.StraightProbability > 0 {
		algoCfg.StraightProbability = cfg.StraightProbability
	}
	if cfg.LeopardProbability > 0 {
		algoCfg.LeopardProbability = cfg.LeopardProbability
	}

	if cfg.RewardControl != nil && len(cfg.RewardControl.RoomConfigs) > 0 {
		algoCfg.RewardControl = &algorithm.RewardControlConfig{
			GlobalSwitchEnabled:  cfg.RewardControl.GlobalSwitchEnabled,
			ProfitRatioThreshold: cfg.RewardControl.ProfitRatioThreshold,
			RoomConfigs:          make(map[int64]*algorithm.RoomRewardConfig),
		}
		for roomID, roomCfg := range cfg.RewardControl.RoomConfigs {
			algoCfg.RewardControl.RoomConfigs[roomID] = &algorithm.RoomRewardConfig{
				GuaranteeEnabled:    roomCfg.GuaranteeEnabled,
				GuaranteeStraight:   roomCfg.GuaranteeStraight,
				GuaranteeLeopard:    roomCfg.GuaranteeLeopard,
				ProbabilityEnabled:  roomCfg.ProbabilityEnabled,
				StraightProbability: roomCfg.StraightProbability,
				LeopardProbability:  roomCfg.LeopardProbability,
			}
		}
	}

	return algoCfg
}
