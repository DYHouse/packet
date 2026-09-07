package bootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	neturl "net/url"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/mysql"
	"github.com/cashparty/backend/common/nacos"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/application"
	"github.com/cashparty/backend/game/application/robot"
	gameconfig "github.com/cashparty/backend/game/config"
	"github.com/cashparty/backend/game/infrastructure/adapter"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	redisRepo "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/server"
	settlementConfig "github.com/cashparty/backend/settlement/config"
	settlementMysqlRepo "github.com/cashparty/backend/settlement/infrastructure/persistence/mysql"
	settlementRedis "github.com/cashparty/backend/settlement/infrastructure/persistence/redis"
	settlementService "github.com/cashparty/backend/settlement/service"
)

type Application struct {
	Container      *Container
	config         *gameconfig.Config
	grpcServer     *server.GRPCServer
	cancel         context.CancelFunc
	nacos          nacos.NacosClient
	grpcPort       int
	appCtx         context.Context
	taskRunner     async.TaskRunner
	wg             sync.WaitGroup
	kafkaConsumers []io.Closer
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

	// 从 nacos 加载限流配置（可选,未配置 RateLimiterDataID 时使用本地配置）
	loadRateLimiterConfigFromNacos(nacosClient, cfg)

	logger.Init(&logger.LogConfig{
		Level:    cfg.Log.Level,
		Filename: cfg.Log.Filename,
	})

	logger.Info("game service starting", "port", cfg.Server.GRPCPort)

	appCtx, cancel := context.WithCancel(context.Background())
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()

	redisClient, err := cRedis.NewClient(&cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis client: %w", err)
	}

	// 初始化雪花 ID 生成器（规约 SID-7：bootstrap 层显式初始化）。
	// node_id > 0：显式指定（单实例/测试环境）
	// node_id = 0：Redis 自动分配（多实例生产环境）
	if cfg.IDGenerator.Enabled {
		if cfg.IDGenerator.NodeID > 0 {
			if err := idgen.Init(cfg.IDGenerator.NodeID); err != nil {
				redisClient.Close()
				return nil, fmt.Errorf("failed to init id generator: %w", err)
			}
			logger.Info("id generator initialized with explicit node_id", "node_id", cfg.IDGenerator.NodeID)
		} else {
			// 使用 appCtx 派生的 context（规约 §6：禁止 context.Background()）
			allocator, err := idgen.InitWithAutoAlloc(appCtx, redisClient)
			if err != nil {
				redisClient.Close()
				return nil, fmt.Errorf("failed to init id generator with auto alloc: %w", err)
			}
			logger.Info("id generator initialized with auto-allocated node_id", "node_id", allocator.GetNodeID())
		}
	}

	lock.InitLocker(redisClient)

	db, err := mysql.NewDB(&cfg.MySQL)
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("failed to create mysql client: %w", err)
	}

	kafkaProducer, err := createKafkaProducer(cfg)
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("failed to create kafka producer: %w", err)
	}

	platformClient, err := platform.NewClient(&cfg.Platform)
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("failed to create platform client: %w", err)
	}
	// 创建 settlement 层 4 个 Repository 实例（Phase 2.3：拆分 BillManager）
	billRepo := settlementMysqlRepo.NewBillRepository(db)
	roundSettlementRepo := settlementMysqlRepo.NewRoundSettlementRepository(db)
	refundAuditRepo := settlementMysqlRepo.NewRefundAuditRepository(db)
	settlementQueryRepo := settlementMysqlRepo.NewSettlementQueryRepository(db)
	// SettlementConfig 汇总 settlement 模块的历史硬编码参数（事务超时/对账limit/扣款并发/退避参数），
	// 统一从配置注入，默认值与原硬编码一致。后续可由外部配置覆盖。
	settlementCfg := settlementConfig.DefaultSettlementConfig()
	// P0-1：创建 settlement DBRepository 聚合，用于 AppService 编排事务。
	// Repository 不再自治开事务，由 AppService/Service 通过 dbRepo.WithTransaction 编排。
	// 事务超时由 SettlementConfig.TransactionTimeout 注入（默认 30s，与原硬编码一致）。
	settlementDbRepo := settlementMysqlRepo.NewDBRepository(db, settlementCfg.TransactionTimeout)

	// 获取已初始化的 IDGenerator（规约 SID-7：禁止懒加载，GetGenerator 返回 error 时 fail-fast）
	idGen, err := idgen.GetGenerator()
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("id generator not initialized: %w", err)
	}

	traceIDGen := settlementService.NewTraceIDGenerator(idGen)
	platformCfg := settlementConfig.FromCommonConfig(&cfg.Platform)

	dbRepo := mysqlRepo.NewDBRepository(db, cfg.Room.MinRoomFee)
	userCacheRepo := redisRepo.NewUserCacheRepository(redisClient)
	userSvc := application.NewUserService(dbRepo, userCacheRepo, &cfg.Avatar, idGen, nil)
	// 通过 UserSaverAdapter 将 game 层 *application.UserService 适配为 settlement/domain.UserService，
	// 解除 settlement 对 game/model 的反向依赖（Phase 1.1）。
	userSaverAdapter := adapter.NewUserSaverAdapter(userSvc)
	userIDConvert := settlementService.NewUserIDConvertService(userSaverAdapter)
	exceptionMgr := settlementMysqlRepo.NewExceptionRepository(db)

	callMgr := settlementMysqlRepo.NewPlatformCallLogRepository(db)
	creditRetrySvc := settlementService.NewCreditRetryService(billRepo, platformClient, redisClient, traceIDGen, platformCfg, &cfg.Lock, exceptionMgr, userIDConvert, callMgr, settlementCfg.CreditRetryBaseDelay, settlementCfg.CreditRetryMaxDelay, settlementCfg.MaxRetryCount)

	// Robot checker and settlement-layer virtual balance service (shared with
	// game-layer robot services via the same Redis keys).
	robotChecker := settlementService.NewRobotChecker(redisClient)
	// 创建 RobotAccountStore 适配器，将 game 层 RobotAccountRepository 适配为
	// settlement/domain.RobotAccountStore 接口，避免 settlement → game 反向依赖。
	// P0-4：统一使用 VirtualBalanceRepository（经 domain.VirtualBalanceService 接口注入），
	// 消除旧的 settlement/service.VirtualBalanceService 双实现。
	robotAccountRepo := mysqlRepo.NewRobotAccountRepository(db)
	robotAccountStore := &robotAccountStoreAdapter{repo: robotAccountRepo}
	settlementVirtualBalance := settlementRedis.NewVirtualBalanceRepository(redisClient, robotAccountStore)

	deductSvc := settlementService.NewDeductService(platformClient, settlementDbRepo, billRepo, roundSettlementRepo, refundAuditRepo, redisClient, traceIDGen, platformCfg, &cfg.Lock, creditRetrySvc, userIDConvert, callMgr, robotChecker, settlementVirtualBalance, settlementCfg.MaxConcurrentDeduct)
	// Phase 7 Task 7.1：拆分 RefundService 为 Apply / Execute / Query 三个职责单一的 Service。
	refundApplySvc := settlementService.NewRefundApplyService(billRepo, settlementDbRepo, refundAuditRepo, traceIDGen, &cfg.Lock)
	refundExecuteSvc := settlementService.NewRefundExecuteService(platformClient, settlementDbRepo, refundAuditRepo, platformCfg, &cfg.Lock, userIDConvert, callMgr)
	rewardSettler := settlementService.NewRewardSettler(billRepo, traceIDGen, robotChecker)
	// Phase 2.4：拆分 GameSettleService 为 SessionPayoutService（调 platform.Credit 派奖）
	// 与 GameSettleReportingService（调 platform.Settle 上报游戏结果）。
	// SessionPayoutService 先创建，作为 GameSettleReportingService 的依赖注入。
	sessionPayoutSvc := settlementService.NewSessionPayoutService(platformClient, billRepo, traceIDGen, platformCfg, userIDConvert, callMgr, robotChecker, settlementVirtualBalance, exceptionMgr)
	gameSettleSvc := settlementService.NewGameSettleReportingService(platformClient, billRepo, roundSettlementRepo, settlementQueryRepo, redisClient, traceIDGen, platformCfg, &cfg.Lock, userIDConvert, callMgr, robotChecker, sessionPayoutSvc)

	// 拆分原 SettlementService 为 2 个职责单一的 Service（P0-10，余额查询已合并入 BalanceService，不再单独构造）：
	// - RoundSettleService：单局结算（SettleRound/creditRound/settleCommission/SettleGame）
	// - PenaltySettlementService：罚款扣款与分配（DeductPenaltyToPlatform/DistributePenaltyFromPlatform）
	roundSettleSvc := settlementService.NewRoundSettleService(billRepo, roundSettlementRepo, redisClient, traceIDGen, rewardSettler, gameSettleSvc, &cfg.Lock, robotChecker)
	penaltySettlementSvc := settlementService.NewPenaltySettlementService(platformClient, settlementDbRepo, billRepo, traceIDGen, platformCfg, &cfg.Lock, userIDConvert, callMgr, robotChecker, settlementVirtualBalance, exceptionMgr)

	algorithmConfig := convertAlgorithmConfig(&cfg.Algorithm)
	logger.Info("algorithm config loaded", "room_configs_count", len(algorithmConfig.RewardControl.RoomConfigs))
	roomRepo := redisRepo.NewRoomRepository(redisClient, cfg.RedisTTL)
	packetCacheRepo := redisRepo.NewPacketCacheRepository(redisClient)
	rewardCacheRepo := redisRepo.NewRewardCacheRepository(redisClient)
	packetGenerator := algorithm.NewPacketGenerator(algorithmConfig, packetCacheRepo, rewardCacheRepo, roomRepo)

	// Validate robot configuration before assembling robot services.
	robot.ValidateRobotConfig(&cfg.Robot)

	taskRunner := async.NewTaskRunner(appCtx, 10*time.Second)
	if err := taskRunner.Start(); err != nil {
		return nil, fmt.Errorf("start task runner failed: %w", err)
	}

	container := NewContainer(&cfg.Platform, &cfg.Timeout, &cfg.Avatar, &cfg.Robot, &cfg.Broadcast, db, redisClient, kafkaProducer, roundSettleSvc, penaltySettlementSvc, packetGenerator, roomRepo,
		platformClient, billRepo, roundSettlementRepo, refundAuditRepo, settlementQueryRepo, traceIDGen, platformCfg, userIDConvert, exceptionMgr, creditRetrySvc, deductSvc, refundApplySvc, refundExecuteSvc, rewardSettler, callMgr, gameSettleSvc, robotChecker, settlementVirtualBalance, taskRunner, &cfg.SettlementScheduler, &cfg.RedisTTL, &cfg.RateLimiter, idGen, &cfg.Room)
	container.LockCfg = &cfg.Lock
	container.InitAppServices()

	success = true
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

	roomEventConsumer, err := createRoomEventConsumer(a.config, a.Container.DBRepo, a.Container.RoomRepo, a.Container.Redis, nodeID)
	if err != nil {
		return fmt.Errorf("failed to create room event consumer: %w", err)
	}
	a.Container.RoomEventConsumer = roomEventConsumer
	a.kafkaConsumers = append(a.kafkaConsumers, roomEventConsumer)

	// Phase 3.1：创建 GameEventHandler（application 层），承载从 consumer 迁移的业务编排逻辑。
	// consumer 仅做消息解析与转发，所有 DB 操作走 dbRepo.WithTransaction。
	// Phase 3.5：settlement 用例通过 SettleAppService facade 调用，不再直接依赖 RoundSettleService。
	gameEventHandler := application.NewGameEventHandler(
		a.Container.DBRepo, a.Container.SettleAppSvc, a.Container.RobotBehaviorEngine,
	)
	gameEventConsumer, err := createGameEventConsumer(
		a.config, a.Container.Redis, gameEventHandler, nodeID,
	)
	if err != nil {
		return fmt.Errorf("failed to create game event consumer: %w", err)
	}
	a.Container.GameEventConsumer = gameEventConsumer
	a.kafkaConsumers = append(a.kafkaConsumers, gameEventConsumer)

	if err := a.Container.StartSchedulers(a.appCtx); err != nil {
		logger.Error("some schedulers failed to start", "error", err)
		// Don't return error - some schedulers may have started successfully
		// and we want the application to continue starting (consistent with previous behavior)
	}

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
				logger.Error("game event consumer panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := a.Container.GameEventConsumer.Start(a.appCtx); err != nil {
			logger.Error("game event consumer failed", "error", err)
		}
	}()

	// 从限流配置读取 fail-open 策略
	// grab: 命令级别覆盖 > defaults.fail_open
	grabFailOpen := a.config.RateLimiter.Defaults.FailOpen
	if grabCfg, ok := a.config.RateLimiter.Commands["grab"]; ok && grabCfg.FailOpen != nil {
		grabFailOpen = *grabCfg.FailOpen
	}
	// send_packet: 命令级别覆盖 > defaults.financial_fail_open
	financialFailOpen := a.config.RateLimiter.Defaults.FinancialFailOpen
	if sendCfg, ok := a.config.RateLimiter.Commands["send_packet"]; ok && sendCfg.FailOpen != nil {
		financialFailOpen = *sendCfg.FailOpen
	}
	// allowedAvatarHosts 从 AvatarCfg.BaseURL 解析 host，用于 update_avatar 命令 URL 白名单校验。
	// 默认头像与上传头像共享同一域名（如 opc.narrytech.cn），故从 base_url 推导即可。
	var allowedAvatarHosts []string
	if u, err := neturl.Parse(a.config.Avatar.BaseURL); err == nil && u.Host != "" {
		allowedAvatarHosts = []string{u.Host}
	}

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
		allowedAvatarHosts,
		grabFailOpen,
		financialFailOpen,
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

	// 5. 关闭 Kafka consumer（每个加 5s 超时兜底，LF-2）
	for _, c := range a.kafkaConsumers {
		done := make(chan struct{})
		go func(cl io.Closer) {
			if err := cl.Close(); err != nil {
				logger.Warn("failed to close kafka consumer", "error", err)
			}
			close(done)
		}(c)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			logger.Warn("kafka consumer close timeout, force shutdown")
		}
	}

	// 6. 关闭底层资源
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
	if cfg.PacketCacheTTL != nil {
		algoCfg.PacketCacheTTL = *cfg.PacketCacheTTL
	}

	if cfg.RewardControl != nil && len(cfg.RewardControl.RoomConfigs) > 0 {
		algoCfg.RewardControl = &algorithm.RewardControlConfig{
			GlobalSwitchEnabled:  cfg.RewardControl.GlobalSwitchEnabled,
			ProfitRatioThreshold: 0.05, // default；若下方显式设置则覆盖
			LeopardMultiplier:    10,   // default；若下方显式设置则覆盖
			RoomConfigs:          make(map[int64]*algorithm.RoomRewardConfig),
		}
		if cfg.RewardControl.ProfitRatioThreshold != nil {
			algoCfg.RewardControl.ProfitRatioThreshold = *cfg.RewardControl.ProfitRatioThreshold
		}
		if cfg.RewardControl.LeopardMultiplier != nil {
			algoCfg.RewardControl.LeopardMultiplier = *cfg.RewardControl.LeopardMultiplier
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

// loadRateLimiterConfigFromNacos 从 nacos 拉取限流配置并覆盖 cfg.RateLimiter。
// 未配置 RateLimiterDataID 或拉取/解析失败时保持本地配置,仅 Warn 不阻塞启动。
func loadRateLimiterConfigFromNacos(nacosClient nacos.NacosClient, cfg *gameconfig.Config) {
	if nacosClient == nil || cfg.Nacos.RateLimiterDataID == "" {
		return
	}
	content, err := nacosClient.GetConfig(cfg.Nacos.RateLimiterDataID, cfg.Nacos.RateLimiterGroup)
	if err != nil {
		logger.Warn("failed to load rate limiter config from nacos, using local config", "error", err)
		return
	}
	if content == "" {
		return
	}
	rateLimiterCfg, err := gameconfig.LoadRateLimiterFromContent(content)
	if err != nil {
		logger.Warn("failed to parse rate limiter config from nacos, using local config", "error", err)
		return
	}
	cfg.RateLimiter = *rateLimiterCfg
	logger.Info("rate limiter config loaded from nacos", "data_id", cfg.Nacos.RateLimiterDataID)
}
