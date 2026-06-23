package bootstrap

import (
	"context"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/kafka"
	cRedis "github.com/cashparty/backend/common/redis"
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
	PlatformCfg              *config.PlatformConfig
	TimeoutCfg               *config.TimeoutConfig
	AvatarCfg                *config.AvatarConfig
	DB                       *gorm.DB
	Redis                    *cRedis.Client
	KafkaProducer            *kafka.Producer
	DBRepo                   domain.DBRepository
	RoomRepo                 domain.RoomRepository
	Broadcaster              domain.Broadcaster
	EventPublisher           domain.EventPublisher
	GameEventPublisher       *messaging.GameEventPublisher
	EventConsumer            *messaging.RoomEventConsumer
	TimeoutScheduler         *scheduler.TimeoutScheduler
	GrabService              *application.GrabService
	PenaltyService           *application.PenaltyService
	RoomAppService           *application.RoomAppService
	SeatAppService           *application.SeatAppService
	GameAppService           *application.GameAppService
	UserService              *application.UserService
	SettlementSvc            *settlementService.SettlementService
	DeductSvc                *settlementService.DeductService
	RefundSvc                *settlementService.RefundService
	BalanceService           *settlementService.BalanceService
	PacketGenerator          *algorithm.PacketGenerator
	CreditRetryScheduler     *settlementScheduler.CreditRetryScheduler
	RefundProcessScheduler   *settlementScheduler.RefundProcessScheduler
	SettlementCheckScheduler *settlementScheduler.SettlementCheckScheduler
	GameSettleRetryScheduler *settlementScheduler.GameSettleRetryScheduler
	GameSettleTimeoutScheduler *settlementScheduler.GameSettleTimeoutScheduler

	// Robot system services
	RobotCfg                    *config.RobotConfig
	VirtualBalanceService       *redisRepo.VirtualBalanceService
	RobotPoolService            *redisRepo.RobotPoolService
	RobotSchedulerRedis         *redisRepo.RobotSchedulerRedis
	RobotAccountService         *application.RobotAccountService
	RobotPlayer                 *application.RobotPlayer
	RobotBehaviorEngine         *application.RobotBehaviorEngine
	RobotSchedulerService       *application.RobotSchedulerService
	VirtualBalanceSyncScheduler *scheduler.VirtualBalanceSyncScheduler

	// Shared settlement service instances (created in app.go, not recreated)
	platformClient  platform.Client
	billMgr         *settlementService.BillManager
	traceIDGen      *settlementService.TraceIDGenerator
	platformCfg     *settlementConfig.PlatformConfig
	userIDConvert   *settlementService.UserIDConvertService
	exceptionMgr    *settlementService.ExceptionManager
	creditRetrySvc  *settlementService.CreditRetryService
	rewardSettler   *settlementService.RewardSettler
	callMgr         *settlementService.PlatformCallManager
	gameSettleSvc   *settlementService.GameSettleService
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
) *Container {
	dbRepo := mysqlRepo.NewDBRepository(db)
	broadcaster := broadcast.NewGameBroadcaster(broadcastCfg, kafkaProducer, redis)
	eventPublisher := messaging.NewRoomEventPublisher(kafkaProducer, kafka.TopicRoomEvents)
	gameEventPublisher := messaging.NewGameEventPublisher(kafkaProducer)
	timeoutScheduler := scheduler.NewTimeoutScheduler(redis, &scheduler.Config{
		Seat:    timeoutCfg.Seat,
		Ready:   timeoutCfg.Ready,
		Grab:    timeoutCfg.Grab,
		Send:    timeoutCfg.Send,
		Replace: timeoutCfg.Replace,
	})

	grabService := application.NewGrabService(redis, timeoutCfg.Grab, timeoutCfg.Send)
	penaltyService := application.NewPenaltyService(redis, nil, settlementSvc)

	return &Container{
		PlatformCfg:     platformCfg,
		TimeoutCfg:      timeoutCfg,
		AvatarCfg:       avatarCfg,
		RobotCfg:        robotCfg,
		DB:              db,
		Redis:           redis,
		KafkaProducer:   kafkaProducer,
		DBRepo:          dbRepo,
		RoomRepo:        roomRepo,
		Broadcaster:     broadcaster,
		EventPublisher:  eventPublisher,
		GameEventPublisher: gameEventPublisher,
		TimeoutScheduler: timeoutScheduler,
		GrabService:     grabService,
		PenaltyService:  penaltyService,
		SettlementSvc:   settlementSvc,
		DeductSvc:       deductSvc,
		RefundSvc:       refundSvc,
		PacketGenerator: packetGenerator,

		platformClient:  platformClient,
		billMgr:         billMgr,
		traceIDGen:      traceIDGen,
		platformCfg:     settlementPlatformCfg,
		userIDConvert:   userIDConvert,
		exceptionMgr:    exceptionMgr,
		creditRetrySvc:  creditRetrySvc,
		rewardSettler:   rewardSettler,
		callMgr:         callMgr,
		gameSettleSvc:   gameSettleSvc,
	}
}

func (c *Container) InitAppServices() {
	c.UserService = application.NewUserService(c.DBRepo, c.Redis, c.AvatarCfg)

	c.RoomAppService = application.NewRoomAppService(
		c.RoomRepo,
		c.DBRepo,
		c.UserService,
		c.Broadcaster,
		c.EventPublisher,
		c.TimeoutScheduler,
		c.SettlementSvc,
	)

	c.BalanceService = settlementService.NewBalanceService(c.platformClient, c.platformCfg, c.userIDConvert)

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
	)

	if c.TimeoutScheduler != nil {
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeSeat, c.SeatAppService.HandleSeatTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeReady, c.SeatAppService.HandleReadyTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeGrab, c.GameAppService.OnGrabTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeSend, c.GameAppService.OnSendTimeout)
		c.TimeoutScheduler.RegisterHandler(scheduler.TimeoutTypeReplace, c.GameAppService.OnReplaceTimeout)
	}

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

	// Wire behavior engine to scheduler service (breaks circular dependency)
	c.RobotSchedulerService.SetBehaviorEngine(c.RobotBehaviorEngine)

	// Set game end callback so the robot scheduler can recycle robots on game end
	c.GameAppService.SetGameEndCallback(func(ctx context.Context, roomID string) {
		c.RobotSchedulerService.OnGameEnd(ctx, roomID)
	})

	// Create virtual balance sync scheduler
	c.VirtualBalanceSyncScheduler = scheduler.NewVirtualBalanceSyncScheduler(
		c.VirtualBalanceService, c.RobotCfg.Account.SyncInterval,
	)
}

func (c *Container) initSettlementSchedulers() {
	settlementCheckSvc := settlementService.NewSettlementCheckService(
		c.billMgr,
		c.exceptionMgr,
		c.RefundSvc,
		c.traceIDGen,
	)

	c.CreditRetryScheduler = settlementScheduler.NewCreditRetryScheduler(c.creditRetrySvc, c.Redis)
	c.RefundProcessScheduler = settlementScheduler.NewRefundProcessScheduler(c.RefundSvc, c.billMgr, c.Redis)
	c.SettlementCheckScheduler = settlementScheduler.NewSettlementCheckScheduler(settlementCheckSvc, c.Redis)
	c.GameSettleRetryScheduler = settlementScheduler.NewGameSettleRetryScheduler(c.billMgr, c.gameSettleSvc, c.Redis)
	c.GameSettleTimeoutScheduler = settlementScheduler.NewGameSettleTimeoutScheduler(c.billMgr, c.gameSettleSvc, c.Redis)
}

func (c *Container) NewRoomEventConsumer(cfg *kafka.ConsumerConfig) *messaging.RoomEventConsumer {
	return messaging.NewRoomEventConsumer(c.DBRepo, c.Redis, cfg)
}

func (c *Container) NewGameEventConsumer(cfg *kafka.ConsumerConfig) *messaging.GameEventConsumer {
	return messaging.NewGameEventConsumer(c.DB, c.Redis, c.SettlementSvc, c.GetRobotBehaviorEngine())
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

func (c *Container) StartSchedulers() {
	if c.TimeoutScheduler != nil {
		c.TimeoutScheduler.Start()
	}
	if c.CreditRetryScheduler != nil {
		c.CreditRetryScheduler.Start()
	}
	if c.RefundProcessScheduler != nil {
		c.RefundProcessScheduler.Start()
	}
	if c.SettlementCheckScheduler != nil {
		c.SettlementCheckScheduler.Start()
	}
	if c.GameSettleRetryScheduler != nil {
		c.GameSettleRetryScheduler.Start()
	}
	if c.GameSettleTimeoutScheduler != nil {
		c.GameSettleTimeoutScheduler.Start()
	}

	// Start robot schedulers only when robot is enabled
	if c.RobotCfg != nil && c.RobotCfg.Enabled {
		if c.RobotSchedulerService != nil {
			c.RobotSchedulerService.Start()
		}
		if c.VirtualBalanceSyncScheduler != nil {
			c.VirtualBalanceSyncScheduler.Start()
		}
		if c.RobotSchedulerService != nil {
			c.RobotSchedulerService.ValidateReserveRatio(context.Background())
		}
	}
}

func (c *Container) Stop() {
	if c.VirtualBalanceSyncScheduler != nil {
		c.VirtualBalanceSyncScheduler.Stop()
	}
	if c.RobotSchedulerService != nil {
		c.RobotSchedulerService.Stop()
	}
	if c.TimeoutScheduler != nil {
		c.TimeoutScheduler.Stop()
	}
	if c.CreditRetryScheduler != nil {
		c.CreditRetryScheduler.Stop()
	}
	if c.RefundProcessScheduler != nil {
		c.RefundProcessScheduler.Stop()
	}
	if c.SettlementCheckScheduler != nil {
		c.SettlementCheckScheduler.Stop()
	}
	if c.GameSettleRetryScheduler != nil {
		c.GameSettleRetryScheduler.Stop()
	}
	if c.GameSettleTimeoutScheduler != nil {
		c.GameSettleTimeoutScheduler.Stop()
	}
}
