package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/mysql"
	"github.com/cashparty/backend/common/nacos"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/application"
	gameconfig "github.com/cashparty/backend/game/config"
	"github.com/cashparty/backend/game/infrastructure/messaging"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	redisRepo "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/server"
	settlementConfig "github.com/cashparty/backend/settlement/config"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type Application struct {
	Container  *Container
	config     *gameconfig.Config
	grpcServer *server.GRPCServer
	cancel     context.CancelFunc
	nacos      *nacos.Client
	grpcPort   int
}

func NewApplication(cfgPath string) (*Application, error) {
	cfg, err := gameconfig.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return NewApplicationWithConfig(cfg)
}

func NewApplicationWithConfig(cfg *gameconfig.Config) (*Application, error) {
	nacosClient := initNacos(cfg)

	if nacosClient != nil && cfg.Nacos.ConfigDataID != "" {
		cfg = reloadMainConfigFromNacos(nacosClient, cfg)
	}

	if nacosClient != nil && cfg.Nacos.AlgorithmDataID != "" {
		algoCfg, err := loadAlgorithmConfigFromNacos(nacosClient, cfg)
		if err != nil {
			logger.Warn("failed to load algorithm config from nacos, using local config", "error", err)
		} else if algoCfg != nil {
			cfg.Algorithm = *algoCfg
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
	rewardSettler := settlementService.NewRewardSettler(settlementService.DefaultRewardSettlementConfig(), settlementRecorder, traceIDGen, robotChecker)
	gameSettleSvc := settlementService.NewGameSettleService(platformClient, settlementRecorder, redisClient, traceIDGen, platformCfg, userIDConvert, callMgr, robotChecker, settlementVirtualBalance)

	settlementSvc := settlementService.NewSettlementService(platformClient, settlementRecorder, redisClient, traceIDGen, platformCfg, deductSvc, rewardSettler, gameSettleSvc, userIDConvert, callMgr, robotChecker, settlementVirtualBalance)

	algorithmConfig := convertAlgorithmConfig(&cfg.Algorithm)
	logger.Info("algorithm config loaded", "room_configs_count", len(algorithmConfig.RewardControl.RoomConfigs))
	roomRepo := redisRepo.NewRoomRepository(redisClient)
	packetGenerator := algorithm.NewPacketGenerator(algorithmConfig, redisClient, db, roomRepo)

	// Validate robot configuration before assembling robot services.
	application.ValidateRobotConfig(&cfg.Robot)

	container := NewContainer(&cfg.Platform, &cfg.Timeout, &cfg.Avatar, &cfg.Robot, &cfg.Broadcast, db, redisClient, kafkaProducer, settlementSvc, packetGenerator, roomRepo,
		platformClient, settlementRecorder, traceIDGen, platformCfg, userIDConvert, exceptionMgr, creditRetrySvc, deductSvc, refundSvc, rewardSettler, callMgr, gameSettleSvc, robotChecker, settlementVirtualBalance)
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
		a.Container.HistoryService,
		a.Container.Redis,
		a.Container.UserLimiter,
		a.Container.Broadcaster.Broadcast,
	)

	if a.nacos != nil {
		if err := a.nacos.RegisterService(); err != nil {
			logger.Warn("failed to register service to nacos", "error", err)
		}
	}

	registerConfigListeners(a.nacos, a.config, a.Container)

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
		if err := a.nacos.Close(); err != nil {
			logger.Warn("failed to close nacos client", "error", err)
		}
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

func convertAlgorithmConfig(cfg *gameconfig.AlgorithmConfig) *algorithm.Config {
	algoCfg := algorithm.DefaultConfig()

	// 用 nil 判断"未设置"，区分"显式 0"与"未配置"。
	// 显式 0 的语义：MinPacketAmount=0 允许 0 金额红包；
	//               StraightProbability/LeopardProbability=0 关闭该奖励类型；
	//               ProfitRatioThreshold=0 表示无阈值（reward_controller.go isProbabilityAllowed 内 `> 0` 才检查）。
	if cfg.MinPacketAmount != nil {
		algoCfg.MinPacketAmount = *cfg.MinPacketAmount
	}
	if cfg.StraightProbability != nil {
		algoCfg.StraightProbability = *cfg.StraightProbability
	}
	if cfg.LeopardProbability != nil {
		algoCfg.LeopardProbability = *cfg.LeopardProbability
	}

	if cfg.RewardControl != nil && len(cfg.RewardControl.RoomConfigs) > 0 {
		algoCfg.RewardControl = &algorithm.RewardControlConfig{
			GlobalSwitchEnabled:  cfg.RewardControl.GlobalSwitchEnabled,
			ProfitRatioThreshold: 0.05, // default；若下方显式设置则覆盖
			RoomConfigs:          make(map[int64]*algorithm.RoomRewardConfig),
		}
		if cfg.RewardControl.ProfitRatioThreshold != nil {
			algoCfg.RewardControl.ProfitRatioThreshold = *cfg.RewardControl.ProfitRatioThreshold
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
