package bootstrap

import (
	"context"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/limiter"
	cRedis "github.com/cashparty/backend/common/redis"
	csched "github.com/cashparty/backend/common/scheduler"
	"github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/application"
	"github.com/cashparty/backend/game/application/robot"
	gameconfig "github.com/cashparty/backend/game/config"
	"github.com/cashparty/backend/game/domain/events"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/infrastructure/adapter"
	"github.com/cashparty/backend/game/infrastructure/broadcast"
	"github.com/cashparty/backend/game/infrastructure/messaging"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	redisRepo "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
	settlementConfig "github.com/cashparty/backend/settlement/config"
	settlementDomain "github.com/cashparty/backend/settlement/domain"
	settlementRepository "github.com/cashparty/backend/settlement/domain/repository"
	settlementMysqlRepo "github.com/cashparty/backend/settlement/infrastructure/persistence/mysql"
	settlementScheduler "github.com/cashparty/backend/settlement/scheduler"
	settlementService "github.com/cashparty/backend/settlement/service"
	"gorm.io/gorm"
)

type Container struct {
	PlatformCfg            *config.PlatformConfig
	TimeoutCfg             *config.TimeoutConfig
	AvatarCfg              *config.AvatarConfig
	SettlementSchedulerCfg *config.SettlementSchedulerConfig
	LockCfg                *config.LockConfig
	RedisTTL               *config.RedisTTLConfig
	DB                     *gorm.DB
	Redis                  cRedis.RedisClient
	KafkaProducer          kafka.KafkaProducer
	DBRepo                 repository.DBRepository
	RoomRepo               repository.RoomRepository
	Broadcaster            events.Broadcaster
	EventPublisher         events.RoomEventPublisher
	GameEventPublisher     events.GameEventPublisher
	// RoomEventConsumer / GameEventConsumer 的 Close 生命周期由 Application.kafkaConsumers
	// 统一管理（PLAN §6 LF-2），Container 不持有 io.Closer 列表，避免双重关闭。
	RoomEventConsumer *messaging.RoomEventConsumer
	GameEventConsumer *messaging.GameEventConsumer
	TimeoutScheduler  *scheduler.TimeoutScheduler
	GrabService       *application.GrabService
	PenaltyService    *application.PenaltyService
	RoomAppService    *application.RoomAppService
	SeatAppService    *application.SeatAppService
	GameAppService    *application.GameAppService
	// Phase 2.1 拆分：GameAppService 现作为 facade，转发到以下 3 个子 Service。
	PacketOrchestrator   *application.PacketOrchestrator
	RoundSettlementSvc   *application.RoundSettlementService
	GameLifecycleSvc     *application.GameLifecycleService
	UserService          *application.UserService
	RoundSettleSvc       *settlementService.RoundSettleService
	PenaltySettlementSvc *settlementService.PenaltySettlementService
	DeductSvc            *settlementService.DeductService
	RefundApplySvc       *settlementService.RefundApplyService
	RefundExecuteSvc     *settlementService.RefundExecuteService
	BalanceService       *settlementService.BalanceService
	// SettleAppSvc 是 settlement 模块的 Application 层入口（Phase 3.5），
	// 供外部调用方（如 GameEventConsumer）通过 facade 调用 settlement 用例，
	// 不再直接依赖 settlement/service 下的具体 Service。
	SettleAppSvc      *settlementApplication.SettleAppService
	HistoryService    *application.HistoryService
	PacketGenerator   *algorithm.PacketGenerator
	SchedulerRegistry *csched.Registry

	// TaskRunner manages fire-and-forget async tasks for the application layer.
	TaskRunner async.TaskRunner

	// Rate limiter (grab command)
	UserLimiter    *limiter.UserLimiter
	RateLimiterCfg *gameconfig.RateLimiterConfig

	// Robot system services
	RobotCfg              *config.RobotConfig
	VirtualBalanceService settlementDomain.VirtualBalanceService
	RobotPoolService      *redisRepo.RobotPoolService
	RobotSchedulerRedis   *redisRepo.RobotSchedulerRedis
	RobotAccountService   *robot.RobotAccountService
	RobotPlayer           *robot.RobotPlayer
	RobotBehaviorEngine   *robot.RobotBehaviorEngine
	RobotSchedulerService *robot.RobotSchedulerService

	// Shared settlement service instances (created in app.go, not recreated)
	platformClient           platform.Client
	billRepo                 settlementRepository.BillRepository
	roundSettlementRepo      settlementRepository.RoundSettlementRepository
	refundAuditRepo          settlementRepository.RefundAuditRepository
	settlementQueryRepo      settlementRepository.SettlementQueryRepository
	traceIDGen               *settlementService.TraceIDGenerator
	platformCfg              *settlementConfig.PlatformConfig
	userIDConvert            *settlementService.UserIDConvertService
	exceptionMgr             settlementRepository.ExceptionRepository
	creditRetrySvc           *settlementService.CreditRetryService
	rewardSettler            *settlementService.RewardSettler
	callMgr                  settlementRepository.PlatformCallLogRepository
	gameSettleSvc            *settlementService.GameSettleReportingService
	robotChecker             settlementService.RobotChecker
	settlementVirtualBalance settlementDomain.VirtualBalanceService
	idGen                    idgen.IDGenerator
}

func NewContainer(
	platformCfg *config.PlatformConfig,
	timeoutCfg *config.TimeoutConfig,
	avatarCfg *config.AvatarConfig,
	robotCfg *config.RobotConfig,
	broadcastCfg *config.BroadcastConfig,
	db *gorm.DB,
	redis cRedis.RedisClient,
	kafkaProducer kafka.KafkaProducer,
	roundSettleSvc *settlementService.RoundSettleService,
	penaltySettlementSvc *settlementService.PenaltySettlementService,
	packetGenerator *algorithm.PacketGenerator,
	roomRepo repository.RoomRepository,
	platformClient platform.Client,
	billRepo settlementRepository.BillRepository,
	roundSettlementRepo settlementRepository.RoundSettlementRepository,
	refundAuditRepo settlementRepository.RefundAuditRepository,
	settlementQueryRepo settlementRepository.SettlementQueryRepository,
	traceIDGen *settlementService.TraceIDGenerator,
	settlementPlatformCfg *settlementConfig.PlatformConfig,
	userIDConvert *settlementService.UserIDConvertService,
	exceptionMgr settlementRepository.ExceptionRepository,
	creditRetrySvc *settlementService.CreditRetryService,
	deductSvc *settlementService.DeductService,
	refundApplySvc *settlementService.RefundApplyService,
	refundExecuteSvc *settlementService.RefundExecuteService,
	rewardSettler *settlementService.RewardSettler,
	callMgr settlementRepository.PlatformCallLogRepository,
	gameSettleSvc *settlementService.GameSettleReportingService,
	robotChecker settlementService.RobotChecker,
	settlementVirtualBalance settlementDomain.VirtualBalanceService,
	taskRunner async.TaskRunner,
	settlementSchedulerCfg *config.SettlementSchedulerConfig,
	redisTTL *config.RedisTTLConfig,
	rateLimiterCfg *gameconfig.RateLimiterConfig,
	idGen idgen.IDGenerator,
) *Container {
	dbRepo := mysqlRepo.NewDBRepository(db)
	broadcaster := broadcast.NewGameBroadcaster(broadcastCfg, kafkaProducer, redis)
	eventPublisher := messaging.NewRoomEventPublisher(kafkaProducer, kafka.TopicRoomEvents)
	gameEventPublisher := messaging.NewGameEventPublisher(kafkaProducer)
	timeoutScheduler := scheduler.NewTimeoutScheduler(redis, &scheduler.Config{
		Seat:           timeoutCfg.Seat,
		Ready:          timeoutCfg.Ready,
		Grab:           timeoutCfg.Grab,
		Send:           timeoutCfg.Send,
		Replace:        timeoutCfg.Replace,
		Robot:          timeoutCfg.Robot,
		CheckInterval:  timeoutCfg.CheckInterval,
		HandlerTimeout: timeoutCfg.HandlerTimeout,
	})

	registry := csched.NewRegistry()
	registry.Register(timeoutScheduler)

	packetCacheRepo := redisRepo.NewPacketCacheRepository(redis)
	grabService := application.NewGrabService(redis, packetCacheRepo, timeoutCfg.Grab, timeoutCfg.Send, *redisTTL)

	// 从 RateLimiterConfig.Commands 构建 UserLimiter 配置
	userLimiterConfigs := buildUserLimiterConfigs(rateLimiterCfg)
	userLimiter := limiter.NewUserLimiter(redis, userLimiterConfigs)

	return &Container{
		PlatformCfg:            platformCfg,
		TimeoutCfg:             timeoutCfg,
		AvatarCfg:              avatarCfg,
		SettlementSchedulerCfg: settlementSchedulerCfg,
		RobotCfg:               robotCfg,
		RedisTTL:               redisTTL,
		DB:                     db,
		Redis:                  redis,
		KafkaProducer:          kafkaProducer,
		DBRepo:                 dbRepo,
		RoomRepo:               roomRepo,
		Broadcaster:            broadcaster,
		EventPublisher:         eventPublisher,
		GameEventPublisher:     gameEventPublisher,
		TimeoutScheduler:       timeoutScheduler,
		SchedulerRegistry:      registry,
		GrabService:            grabService,
		RoundSettleSvc:         roundSettleSvc,
		PenaltySettlementSvc:   penaltySettlementSvc,
		DeductSvc:              deductSvc,
		RefundApplySvc:         refundApplySvc,
		RefundExecuteSvc:       refundExecuteSvc,
		PacketGenerator:        packetGenerator,
		UserLimiter:            userLimiter,
		RateLimiterCfg:         rateLimiterCfg,
		TaskRunner:             taskRunner,

		platformClient:           platformClient,
		billRepo:                 billRepo,
		roundSettlementRepo:      roundSettlementRepo,
		refundAuditRepo:          refundAuditRepo,
		settlementQueryRepo:      settlementQueryRepo,
		traceIDGen:               traceIDGen,
		platformCfg:              settlementPlatformCfg,
		userIDConvert:            userIDConvert,
		exceptionMgr:             exceptionMgr,
		creditRetrySvc:           creditRetrySvc,
		rewardSettler:            rewardSettler,
		callMgr:                  callMgr,
		gameSettleSvc:            gameSettleSvc,
		robotChecker:             robotChecker,
		settlementVirtualBalance: settlementVirtualBalance,
		idGen:                    idGen,
	}
}

func (c *Container) InitAppServices() {
	userCacheRepo := redisRepo.NewUserCacheRepository(c.Redis)
	c.UserService = application.NewUserService(c.DBRepo, userCacheRepo, c.AvatarCfg, c.idGen)

	// 通过 FeeCalculatorAdapter 将 game 层 room.CalculateRequiredFee 适配为
	// settlement/domain.FeeCalculator 接口，解除 settlement 对 game/domain/room 的直接依赖（Phase 4）。
	feeCalculatorAdapter := adapter.NewFeeCalculatorAdapter()
	c.BalanceService = settlementService.NewBalanceService(c.platformClient, c.platformCfg, c.userIDConvert, c.settlementVirtualBalance, c.robotChecker, feeCalculatorAdapter)

	// Phase 3.5：创建 settlement Application 层 facade，作为外部调用方访问 settlement 用例的统一入口。
	// P0-1：注入 settlement DBRepository，由 AppService 通过 dbRepo.WithTransaction 编排事务。
	// 事务超时由 SettlementConfig.TransactionTimeout 注入（默认 30s，与原硬编码一致）。
	settlementDbRepo := settlementMysqlRepo.NewDBRepository(c.DB, settlementConfig.DefaultSettlementConfig().TransactionTimeout)
	c.SettleAppSvc = settlementApplication.NewSettleAppService(
		settlementDbRepo,
		c.RoundSettleSvc,
		c.PenaltySettlementSvc,
		c.DeductSvc,
		c.BalanceService,
	)

	// PenaltyService 依赖 SettleAppSvc，故在 SettleAppSvc 创建之后构造。
	c.PenaltyService = application.NewPenaltyService(c.Redis, nil, c.SettleAppSvc, *c.RedisTTL)

	c.HistoryService = application.NewHistoryService(c.DBRepo)

	c.RoomAppService = application.NewRoomAppService(
		c.RoomRepo,
		c.DBRepo,
		c.UserService,
		c.Broadcaster,
		c.EventPublisher,
		c.TimeoutScheduler,
		c.SettleAppSvc,
		c.TaskRunner,
	)

	// Phase 2.1 拆分：创建 3 个职责单一的子 Service。
	// 跨 Service 依赖通过接口注入（DeductFailureHandler/GameEnder/PacketInitiator/RoundSettler）避免循环依赖。
	packetCacheRepo := redisRepo.NewPacketCacheRepository(c.Redis)
	c.PacketOrchestrator = application.NewPacketOrchestrator(
		c.RoomRepo,
		c.DBRepo,
		c.Broadcaster,
		c.GameEventPublisher,
		c.GrabService,
		c.PacketGenerator,
		packetCacheRepo,
		c.SettleAppSvc,
		c.TaskRunner,
		c.idGen,
		c.LockCfg,
		c.TimeoutCfg,
		c.TimeoutScheduler,
	)

	c.RoundSettlementSvc = application.NewRoundSettlementService(
		c.RoomRepo,
		c.Broadcaster,
		c.GameEventPublisher,
		c.Redis,
		c.TimeoutCfg,
		c.LockCfg,
		c.TimeoutScheduler,
		c.TaskRunner,
		c.idGen,
	)

	c.GameLifecycleSvc = application.NewGameLifecycleService(
		c.RoomRepo,
		c.Broadcaster,
		c.GameEventPublisher,
		c.TimeoutScheduler,
		c.Redis,
		c.LockCfg,
		c.TimeoutCfg,
		*c.RedisTTL,
		c.PenaltyService,
		c.SettleAppSvc,
		c.GrabService,
		c.TaskRunner,
		c.idGen,
	)

	// 装配跨 Service 依赖（接口注入避免循环依赖）：
	//   PacketOrchestrator  --(DeductFailureHandler)--> GameLifecycleService
	//   RoundSettlementSvc  --(GameEnder)------------> GameLifecycleService
	//   GameLifecycleSvc    --(PacketInitiator)-----> PacketOrchestrator
	//   GameLifecycleSvc    --(RoundSettler)---------> RoundSettlementSvc
	c.PacketOrchestrator.SetDeductFailureHandler(c.GameLifecycleSvc)
	c.RoundSettlementSvc.SetGameEnder(c.GameLifecycleSvc)
	c.GameLifecycleSvc.SetPacketInitiator(c.PacketOrchestrator)
	c.GameLifecycleSvc.SetRoundSettler(c.RoundSettlementSvc)

	// 创建 GameAppService facade（注入 3 个子 Service）。
	// 已移除原构造函数中的 3 个死字段参数：publisher、refundSvc、rewardController。
	c.GameAppService = application.NewGameAppService(
		c.RoomRepo,
		c.GrabService,
		c.Broadcaster,
		c.TimeoutScheduler,
		c.TaskRunner,
		c.PacketOrchestrator,
		c.RoundSettlementSvc,
		c.GameLifecycleSvc,
	)

	c.SeatAppService = application.NewSeatAppService(
		c.RoomRepo,
		c.DBRepo,
		c.Broadcaster,
		c.EventPublisher,
		c.TimeoutScheduler,
		c.SettleAppSvc,
		c.GameAppService,
		c.Redis,
		c.TimeoutCfg.Ready,
		c.TaskRunner,
	)

	// 注入 RoomAppService 引用（用于 CancelSeat/Kick 后触发自动替补）
	c.SeatAppService.SetRoomAppService(c.RoomAppService)
	c.GameAppService.SetRoomAppService(c.RoomAppService)

	if c.TimeoutScheduler != nil {
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeSeat, c.SeatAppService.HandleSeatTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeReady, c.SeatAppService.HandleReadyTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeGrab, c.GameAppService.OnGrabTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeSend, c.GameAppService.OnSendTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeReplace, c.GameAppService.OnReplaceTimeout)
	}

	// 注入 ResumeGame 回调（避免 RoomAppService 与 GameAppService 循环依赖）
	c.RoomAppService.SetResumeGameCallback(func(ctx context.Context, roomID string, currentRound int) {
		c.GameAppService.ResumeGame(ctx, &application.ResumeGameRequest{
			RoomID:       roomID,
			CurrentRound: currentRound,
		})
	})

	c.initSettlementSchedulers()
	c.initRobotServices()
}

func (c *Container) initRobotServices() {
	if c.RobotCfg == nil {
		return
	}

	// Create robot account repository
	robotAccountRepo := mysqlRepo.NewRobotAccountRepository(c.DB)

	// P0-4：复用 app.go 创建的统一 VirtualBalanceRepository 实例（经 domain.VirtualBalanceService 接口），
	// 消除 game 层与 settlement 层的双实现。RobotAccountStore 适配器在 app.go 中创建。
	c.VirtualBalanceService = c.settlementVirtualBalance
	c.RobotPoolService = redisRepo.NewRobotPoolService(c.Redis)
	c.RobotSchedulerRedis = redisRepo.NewRobotSchedulerRedis(c.Redis)

	// Create account service
	c.RobotAccountService = robot.NewRobotAccountService(
		robotAccountRepo, c.UserService, c.VirtualBalanceService, c.RobotPoolService, c.AvatarCfg,
	)

	// Create robot player
	c.RobotPlayer = robot.NewRobotPlayer(
		c.SeatAppService, c.GameAppService, c.RoomAppService,
		c.RobotAccountService, c.GrabService, c.RoomRepo, c.RobotCfg,
	)

	// Create behavior engine
	c.RobotBehaviorEngine = robot.NewRobotBehaviorEngine(
		c.RobotCfg, c.RobotPlayer, c.TimeoutScheduler,
		c.RobotAccountService, c.GrabService, c.RobotSchedulerRedis,
	)

	// Wire behavior engine to robot player (breaks circular dependency)
	c.RobotPlayer.SetBehaviorEngine(c.RobotBehaviorEngine)

	// Register robot timeout handler
	c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeRobot, c.RobotBehaviorEngine.HandleRobotTimeout)

	// Create scheduler service
	c.RobotSchedulerService = robot.NewRobotSchedulerService(
		c.RobotAccountService, c.RobotPlayer, c.RoomRepo, c.DBRepo,
		c.RobotSchedulerRedis, c.RobotPoolService, c.RobotCfg,
	)
	c.SchedulerRegistry.Register(c.RobotSchedulerService)

	// Wire behavior engine to scheduler service (breaks circular dependency)
	c.RobotSchedulerService.SetBehaviorEngine(c.RobotBehaviorEngine)

	// Set game end callback so the robot scheduler can recycle robots on game end
	c.GameAppService.SetGameEndCallback(func(ctx context.Context, roomID string) {
		c.RobotSchedulerService.OnGameEnd(ctx, roomID)
	})

	// Create virtual balance sync scheduler
	c.SchedulerRegistry.Register(scheduler.NewVirtualBalanceSyncScheduler(
		c.VirtualBalanceService, c.RobotCfg.Account.SyncInterval, c.Redis,
	))
}

func (c *Container) initSettlementSchedulers() {
	// 创建 SchedulerAppService，作为 5 个 scheduler 的统一 Application 层入口。
	// 各 scheduler 不再直接持有 service/repository，仅通过本 facade 调用用例。
	schedulerApp := settlementApplication.NewSchedulerAppService(
		c.creditRetrySvc,
		c.gameSettleSvc,
		c.RefundExecuteSvc,
		settlementService.NewSettlementCheckService(
			c.billRepo,
			c.roundSettlementRepo,
			c.exceptionMgr,
			c.RefundApplySvc,
			c.traceIDGen,
			settlementConfig.DefaultSettlementConfig().SettlementCheckLimit,
		),
		c.roundSettlementRepo,
		c.settlementQueryRepo,
		c.refundAuditRepo,
	)

	c.SchedulerRegistry.Register(settlementScheduler.NewCreditRetryScheduler(schedulerApp, c.Redis, c.SettlementSchedulerCfg.CreditRetry))
	c.SchedulerRegistry.Register(settlementScheduler.NewRefundProcessScheduler(schedulerApp, c.Redis, c.SettlementSchedulerCfg.RefundProcess))
	c.SchedulerRegistry.Register(settlementScheduler.NewSettlementCheckScheduler(schedulerApp, c.Redis, c.SettlementSchedulerCfg.SettlementCheck))
	c.SchedulerRegistry.Register(settlementScheduler.NewGameSettleRetryScheduler(schedulerApp, c.Redis, c.SettlementSchedulerCfg.GameSettleRetry))
	c.SchedulerRegistry.Register(settlementScheduler.NewGameSettleTimeoutScheduler(schedulerApp, c.Redis, c.SettlementSchedulerCfg.GameSettleTimeout))
}

func (c *Container) NewRoomEventConsumer(cfg kafka.ConsumerConfig) (*messaging.RoomEventConsumer, error) {
	return messaging.NewRoomEventConsumer(c.DBRepo, c.Redis, cfg)
}

// StartSchedulers starts all registered schedulers.
func (c *Container) StartSchedulers(appCtx context.Context) error {
	if err := c.SchedulerRegistry.StartAll(appCtx); err != nil {
		return err
	}
	// Validate robot reserve ratio after schedulers start
	if c.RobotSchedulerService != nil {
		c.RobotSchedulerService.ValidateReserveRatio(appCtx)
	}
	return nil
}

func (c *Container) Stop() {
	c.SchedulerRegistry.StopAll(30 * time.Second)
}

// buildUserLimiterConfigs 将 RateLimiterConfig.Commands 转换为 limiter.LimitConfig map。
// cfg 为 nil 时返回空 map（保持与默认行为兼容）。
func buildUserLimiterConfigs(cfg *gameconfig.RateLimiterConfig) map[string]limiter.LimitConfig {
	configs := make(map[string]limiter.LimitConfig)
	if cfg == nil {
		return configs
	}
	for cmd, c := range cfg.Commands {
		configs[cmd] = limiter.LimitConfig{
			Limit:  int64(c.Limit),
			Window: c.Window,
		}
	}
	return configs
}

// robotAccountStoreAdapter 将 game 层的 repository.RobotAccountRepository 适配为
// settlement/domain.RobotAccountStore 接口。
// 用于在 settlement 层 VirtualBalanceRepository 与 game 层 RobotAccountRepository 之间解耦，
// 避免 settlement → game 反向依赖。
type robotAccountStoreAdapter struct {
	repo repository.RobotAccountRepository
}

// GetVirtualBalance 根据 userID 查询机器人账户的虚拟余额。
func (a *robotAccountStoreAdapter) GetVirtualBalance(ctx context.Context, userID int64) (int64, error) {
	account, err := a.repo.GetByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	return account.VirtualBalance, nil
}

// UpdateBalance 更新机器人账户的虚拟余额。
func (a *robotAccountStoreAdapter) UpdateBalance(ctx context.Context, userID int64, balance int64) error {
	return a.repo.UpdateBalance(ctx, userID, balance)
}
