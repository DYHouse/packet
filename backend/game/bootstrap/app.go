package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/async"
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
	appCtx     context.Context
	taskRunner *async.TaskRunner
	wg         sync.WaitGroup
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

	appCtx, cancel := context.WithCancel(context.Background())
	taskRunner := async.NewTaskRunner(appCtx, 10*time.Second)
	if err := taskRunner.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start task runner failed: %w", err)
	}

	container := NewContainer(&cfg.Platform, &cfg.Timeout, &cfg.Avatar, &cfg.Robot, &cfg.Broadcast, db, redisClient, kafkaProducer, settlementSvc, packetGenerator, roomRepo,
		platformClient, settlementRecorder, traceIDGen, platformCfg, userIDConvert, exceptionMgr, creditRetrySvc, deductSvc, refundSvc, rewardSettler, callMgr, gameSettleSvc, robotChecker, settlementVirtualBalance, taskRunner)
	container.InitAppServices()

	return &Application{
		Container:  container,
		config:     cfg,
		nacos:      nacosClient,
		grpcPort:   cfg.Server.GRPCPort,
		appCtx:     appCtx,
		cancel:     cancel,
		taskRunner: taskRunner,
	}, nil
}

func (a *Application) Start(ctx context.Context) error {
	nodeID := idgen.GetNodeIDString()

	roomEventConsumerCfg := kafka.NewConsumerConfig(
		a.config.Kafka.Brokers,
		kafka.TopicRoomEvents,
		fmt.Sprintf("game-room-events-%s", nodeID),
	)
	a.Container.RoomEventConsumer = a.Container.NewRoomEventConsumer(roomEventConsumerCfg)

	gameEventConsumer := messaging.NewGameEventConsumer(a.Container.DB, a.Container.Redis, a.Container.SettlementSvc, a.Container.GetRobotBehaviorEngine())
	a.Container.GameEventKafkaConsumer = kafka.NewConsumer(
		a.config.Kafka.Brokers,
		kafka.TopicGameEvents,
		fmt.Sprintf("game-events-%s", nodeID),
		gameEventConsumer.HandleEvent,
	)

	a.Container.StartSchedulers(a.appCtx)

	a.wg.Add(2)
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("room event consumer panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := a.Container.RoomEventConsumer.Start(a.appCtx); err != nil {
			logger.Error("room event consumer failed", "error", err)
		}
	}()
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("game event kafka consumer panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := a.Container.GameEventKafkaConsumer.Start(a.appCtx); err != nil {
			logger.Error("game event kafka consumer failed", "error", err)
		}
	}()

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

func (a *Application) Stop() error {
	logger.Info("shutting down game service...")

	// 1. 停止接受新异步任务 + cancel appCtx
	if a.taskRunner != nil {
		a.taskRunner.Stop()
	}
	if a.cancel != nil {
		a.cancel()
	}

	// 2. 停止 container（含 schedulers, connMgr 等）
	a.Container.Stop()

	// 3. 等待 consumer goroutine 退出（带 10s 兜底超时）
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warn("consumer goroutines wait timeout, force shutdown")
	}

	// 4. 等待在飞异步任务退出（带 30s 兜底超时）
	if a.taskRunner != nil {
		waitRunner := make(chan struct{})
		go func() { a.taskRunner.Wait(); close(waitRunner) }()
		select {
		case <-waitRunner:
		case <-time.After(30 * time.Second):
			logger.Warn("task runner wait timeout, force shutdown")
		}
	}

	// 5. 关闭底层资源
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
	return nil
}

// Wait blocks until the application context is cancelled (i.e. Stop is called
// or the appCtx is otherwise done).
func (a *Application) Wait() error {
	<-a.appCtx.Done()
	return nil
}

func Run() {
	cfgPath := "config/game.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	app, err := NewApplication(cfgPath)
	if err != nil {
		logger.Fatal("failed to create application", "error", err)
	}

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		logger.Fatal("failed to start application", "error", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	if err := app.Stop(); err != nil {
		logger.Error("failed to stop application", "error", err)
	}
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
