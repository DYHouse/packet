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
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/broadcast"
	"github.com/cashparty/backend/game/infrastructure/messaging"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	redisRepo "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/scheduler"
	settlementConfig "github.com/cashparty/backend/settlement/config"
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
	Redis                  *cRedis.Client
	KafkaProducer          *kafka.Producer
	DBRepo                 domain.DBRepository
	RoomRepo               domain.RoomRepository
	Broadcaster            domain.Broadcaster
	EventPublisher         domain.EventPublisher
	GameEventPublisher     *messaging.GameEventPublisher
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
	UserService       *application.UserService
	SettlementSvc     *settlementService.SettlementService
	DeductSvc         *settlementService.DeductService
	RefundSvc         *settlementService.RefundService
	BalanceService    *settlementService.BalanceService
	HistoryService    *application.HistoryService
	PacketGenerator   *algorithm.PacketGenerator
	SchedulerRegistry *csched.Registry

	// TaskRunner manages fire-and-forget async tasks for the application layer.
	TaskRunner *async.TaskRunner

	// Rate limiter (grab command)
	UserLimiter *limiter.UserLimiter

	// Robot system services
	RobotCfg              *config.RobotConfig
	VirtualBalanceService *redisRepo.VirtualBalanceService
	RobotPoolService      *redisRepo.RobotPoolService
	RobotSchedulerRedis   *redisRepo.RobotSchedulerRedis
	RobotAccountService   *application.RobotAccountService
	RobotPlayer           *application.RobotPlayer
	RobotBehaviorEngine   *application.RobotBehaviorEngine
	RobotSchedulerService *application.RobotSchedulerService

	// Shared settlement service instances (created in app.go, not recreated)
	platformClient           platform.Client
	billMgr                  *settlementService.BillManager
	traceIDGen               *settlementService.TraceIDGenerator
	platformCfg              *settlementConfig.PlatformConfig
	userIDConvert            *settlementService.UserIDConvertService
	exceptionMgr             *settlementService.ExceptionManager
	creditRetrySvc           *settlementService.CreditRetryService
	rewardSettler            *settlementService.RewardSettler
	callMgr                  *settlementService.PlatformCallManager
	gameSettleSvc            *settlementService.GameSettleService
	robotChecker             settlementService.RobotChecker
	settlementVirtualBalance *settlementService.VirtualBalanceService
	idGen                    idgen.IDGenerator
}

func NewContainer(
	platformCfg *config.PlatformConfig,
	timeoutCfg *config.TimeoutConfig,
	avatarCfg *config.AvatarConfig,
	robotCfg *config.RobotConfig,
	broadcastCfg *config.BroadcastConfig,
	db *gorm.DB,
	redis *cRedis.Client,
	kafkaProducer *kafka.Producer,
	settlementSvc *settlementService.SettlementService,
	packetGenerator *algorithm.PacketGenerator,
	roomRepo domain.RoomRepository,
	platformClient platform.Client,
	billMgr *settlementService.BillManager,
	traceIDGen *settlementService.TraceIDGenerator,
	settlementPlatformCfg *settlementConfig.PlatformConfig,
	userIDConvert *settlementService.UserIDConvertService,
	exceptionMgr *settlementService.ExceptionManager,
	creditRetrySvc *settlementService.CreditRetryService,
	deductSvc *settlementService.DeductService,
	refundSvc *settlementService.RefundService,
	rewardSettler *settlementService.RewardSettler,
	callMgr *settlementService.PlatformCallManager,
	gameSettleSvc *settlementService.GameSettleService,
	robotChecker settlementService.RobotChecker,
	settlementVirtualBalance *settlementService.VirtualBalanceService,
	taskRunner *async.TaskRunner,
	settlementSchedulerCfg *config.SettlementSchedulerConfig,
	redisTTL *config.RedisTTLConfig,
	idGen idgen.IDGenerator,
) *Container {
	dbRepo := mysqlRepo.NewDBRepository(db)
	broadcaster := broadcast.NewGameBroadcaster(broadcastCfg, kafkaProducer, redis)
	eventPublisher := messaging.NewRoomEventPublisher(kafkaProducer, kafka.TopicRoomEvents)
	gameEventPublisher := messaging.NewGameEventPublisher(kafkaProducer)
	timeoutScheduler := scheduler.NewTimeoutScheduler(redis, &scheduler.Config{
		Seat:          timeoutCfg.Seat,
		Ready:         timeoutCfg.Ready,
		Grab:          timeoutCfg.Grab,
		Send:          timeoutCfg.Send,
		Replace:       timeoutCfg.Replace,
		Robot:         timeoutCfg.Robot,
		CheckInterval: timeoutCfg.CheckInterval,
	})

	registry := csched.NewRegistry()
	registry.Register(timeoutScheduler)

	grabService := application.NewGrabService(redis, timeoutCfg.Grab, timeoutCfg.Send, *redisTTL)
	penaltyService := application.NewPenaltyService(redis, nil, settlementSvc, *redisTTL)

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
		PenaltyService:         penaltyService,
		SettlementSvc:          settlementSvc,
		DeductSvc:              deductSvc,
		RefundSvc:              refundSvc,
		PacketGenerator:        packetGenerator,
		UserLimiter:            limiter.NewUserLimiter(redis),
		TaskRunner:             taskRunner,

		platformClient:           platformClient,
		billMgr:                  billMgr,
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
	c.UserService = application.NewUserService(c.DBRepo, c.Redis, c.AvatarCfg, c.idGen)

	c.BalanceService = settlementService.NewBalanceService(c.platformClient, c.platformCfg, c.userIDConvert, c.settlementVirtualBalance, c.robotChecker)

	c.HistoryService = application.NewHistoryService(c.DBRepo, c.billMgr)

	c.RoomAppService = application.NewRoomAppService(
		c.RoomRepo,
		c.DBRepo,
		c.UserService,
		c.Broadcaster,
		c.EventPublisher,
		c.TimeoutScheduler,
		c.SettlementSvc,
		c.BalanceService,
		c.TaskRunner,
	)

	c.GameAppService = application.NewGameAppService(
		c.RoomRepo,
		c.DBRepo,
		c.Broadcaster,
		c.EventPublisher,
		c.GameEventPublisher,
		c.GrabService,
		c.PenaltyService,
		c.TimeoutScheduler,
		c.PacketGenerator,
		c.SettlementSvc,
		c.DeductSvc,
		c.RefundSvc,
		c.PacketGenerator.GetRewardController(),
		c.rewardSettler,
		c.Redis,
		c.TimeoutCfg,
		c.LockCfg,
		*c.RedisTTL,
		c.TaskRunner,
		c.idGen,
	)

	c.SeatAppService = application.NewSeatAppService(
		c.RoomRepo,
		c.DBRepo,
		c.Broadcaster,
		c.EventPublisher,
		c.TimeoutScheduler,
		c.SettlementSvc,
		c.GameAppService,
		c.Redis,
		c.BalanceService,
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

	// Create Redis services (game layer)
	c.VirtualBalanceService = redisRepo.NewVirtualBalanceService(c.Redis, robotAccountRepo)
	c.RobotPoolService = redisRepo.NewRobotPoolService(c.Redis)
	c.RobotSchedulerRedis = redisRepo.NewRobotSchedulerRedis(c.Redis)

	// Create account service
	c.RobotAccountService = application.NewRobotAccountService(
		robotAccountRepo, c.UserService, c.VirtualBalanceService, c.RobotPoolService, c.AvatarCfg,
	)

	// Create robot player
	c.RobotPlayer = application.NewRobotPlayer(
		c.SeatAppService, c.GameAppService, c.RoomAppService,
		c.RobotAccountService, c.GrabService, c.RoomRepo, c.RobotCfg,
	)

	// Create behavior engine
	c.RobotBehaviorEngine = application.NewRobotBehaviorEngine(
		c.RobotCfg, c.RobotPlayer, c.TimeoutScheduler,
		c.RobotAccountService, c.GrabService, c.Redis, c.RobotSchedulerRedis,
	)

	// Wire behavior engine to robot player (breaks circular dependency)
	c.RobotPlayer.SetBehaviorEngine(c.RobotBehaviorEngine)

	// Register robot timeout handler
	c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeRobot, c.RobotBehaviorEngine.HandleRobotTimeout)

	// Create scheduler service
	c.RobotSchedulerService = application.NewRobotSchedulerService(
		c.RobotAccountService, c.RobotPlayer, c.RoomRepo, c.DBRepo,
		c.Redis, c.RobotSchedulerRedis, c.RobotPoolService, c.RobotCfg,
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
	settlementCheckSvc := settlementService.NewSettlementCheckService(
		c.billMgr,
		c.exceptionMgr,
		c.RefundSvc,
		c.traceIDGen,
	)

	c.SchedulerRegistry.Register(settlementScheduler.NewCreditRetryScheduler(c.creditRetrySvc, c.Redis, c.SettlementSchedulerCfg.CreditRetry))
	c.SchedulerRegistry.Register(settlementScheduler.NewRefundProcessScheduler(c.RefundSvc, c.billMgr, c.Redis, c.SettlementSchedulerCfg.RefundProcess))
	c.SchedulerRegistry.Register(settlementScheduler.NewSettlementCheckScheduler(settlementCheckSvc, c.Redis, c.SettlementSchedulerCfg.SettlementCheck))
	c.SchedulerRegistry.Register(settlementScheduler.NewGameSettleRetryScheduler(c.billMgr, c.gameSettleSvc, c.Redis, c.SettlementSchedulerCfg.GameSettleRetry))
	c.SchedulerRegistry.Register(settlementScheduler.NewGameSettleTimeoutScheduler(c.billMgr, c.gameSettleSvc, c.Redis, c.SettlementSchedulerCfg.GameSettleTimeout))
}

func (c *Container) NewRoomEventConsumer(cfg kafka.ConsumerConfig) (*messaging.RoomEventConsumer, error) {
	return messaging.NewRoomEventConsumer(c.DBRepo, c.Redis, cfg)
}

// GetRobotBehaviorEngine returns the robot behavior engine as the messaging
// interface, or nil when the robot system is not initialized. This avoids
// passing a non-nil interface wrapping a nil pointer to the game event
// consumer.
func (c *Container) GetRobotBehaviorEngine() messaging.RobotBehaviorEngineInterface {
	if c.RobotBehaviorEngine == nil {
		return nil
	}
	return c.RobotBehaviorEngine
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
