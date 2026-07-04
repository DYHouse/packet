package scheduler

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/service"
)

type GameSettleTimeoutScheduler struct {
	base          *BaseScheduler
	billMgr       *service.BillManager
	gameSettleSvc *service.GameSettleService
}

func NewGameSettleTimeoutScheduler(ctx context.Context, billMgr *service.BillManager, gameSettleSvc *service.GameSettleService, redis *cRedis.Client) *GameSettleTimeoutScheduler {
	config := SchedulerConfig{
		Name:         "game_settle_timeout",
		Interval:     5 * time.Minute,
		InitialDelay: 1 * time.Minute,
		LockKey:      rediskeys.KeySchedulerGameSettleTimeoutLock,
		LockTTL:      300,
	}

	return &GameSettleTimeoutScheduler{
		base:          NewBaseScheduler(ctx, config, nil, redis),
		billMgr:       billMgr,
		gameSettleSvc: gameSettleSvc,
	}
}

func (s *GameSettleTimeoutScheduler) Start() {
	s.base.task = s.execute
	s.base.Start()
}

func (s *GameSettleTimeoutScheduler) execute(ctx context.Context) error {
	// Find games where all rounds are credited but game settle not done for over 1 hour
	sessionIDs, err := s.billMgr.GetTimedOutGameSettlements(ctx, 1*time.Hour, 100)
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
