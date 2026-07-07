package scheduler

import (
	"context"
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/service"
)

type GameSettleTimeoutScheduler struct {
	base                *csched.BaseScheduler
	roundSettlementRepo domain.RoundSettlementRepository
	gameSettleSvc       *service.GameSettleReportingService
	timeoutDuration     time.Duration
	limit               int
}

func NewGameSettleTimeoutScheduler(roundSettlementRepo domain.RoundSettlementRepository, gameSettleSvc *service.GameSettleReportingService, redis *cRedis.Client, cfg commonconfig.SettlementSchedulerSubConfig) *GameSettleTimeoutScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "game_settle_timeout",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerGameSettleTimeoutLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &GameSettleTimeoutScheduler{
		roundSettlementRepo: roundSettlementRepo,
		gameSettleSvc:       gameSettleSvc,
		timeoutDuration:     cfg.TimeoutDuration,
		limit:               cfg.Limit,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *GameSettleTimeoutScheduler) Name() string { return s.base.Name() }

func (s *GameSettleTimeoutScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *GameSettleTimeoutScheduler) execute(ctx context.Context) error {
	// 查找所有回合已入账但游戏结算超过 timeoutDuration 未完成的会话
	sessionIDs, err := s.roundSettlementRepo.GetTimedOutGameSettlements(ctx, s.timeoutDuration, s.limit)
	if err != nil {
		logger.Error("get timed out game settlements failed", "error", err)
		return err
	}

	for _, sessionID := range sessionIDs {
		if err := s.gameSettleSvc.SettleGame(ctx, sessionID); err != nil {
			logger.Error("force game settle failed", "session_id", sessionID, "error", err)
		} else {
			logger.Info("force game settle success", "session_id", sessionID)
		}
	}

	return nil
}

func (s *GameSettleTimeoutScheduler) Stop() {
	s.base.Stop()
}
