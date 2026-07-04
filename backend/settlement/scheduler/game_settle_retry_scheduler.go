package scheduler

import (
	"context"

	commonconfig "github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

type GameSettleRetryScheduler struct {
	base          *csched.BaseScheduler
	billMgr       *service.BillManager
	gameSettleSvc *service.GameSettleService
}

func NewGameSettleRetryScheduler(billMgr *service.BillManager, gameSettleSvc *service.GameSettleService, redis *cRedis.Client, cfg commonconfig.SettlementSchedulerSubConfig) *GameSettleRetryScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "game_settle_retry",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerGameSettleRetryLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &GameSettleRetryScheduler{
		billMgr:       billMgr,
		gameSettleSvc: gameSettleSvc,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *GameSettleRetryScheduler) Name() string { return s.base.Name() }

func (s *GameSettleRetryScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *GameSettleRetryScheduler) execute(ctx context.Context) error {
	// Find games where game settle failed
	sessionIDs, err := s.billMgr.GetFailedGameSettlements(ctx, 100)
	if err != nil {
		logger.Error("get failed game settlements failed", "error", err)
		return err
	}

	for _, sessionID := range sessionIDs {
		// Get unsettled users in this game
		userIDs, err := s.billMgr.GetUnsettledUsersBySession(ctx, sessionID)
		if err != nil {
			logger.Error("get unsettled users failed", "session_id", sessionID, "error", err)
			continue
		}

		allSuccess := true
		for _, userID := range userIDs {
			if err := s.gameSettleSvc.RetryPlayerSettle(ctx, sessionID, userID); err != nil {
				logger.Error("retry player game settle failed", "session_id", sessionID, "user_id", userID, "error", err)
				allSuccess = false
			}
		}

		if allSuccess && len(userIDs) > 0 {
			if err := s.billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusFailed, dto.GameSettleStatusSuccess); err != nil {
				logger.Error("update game settle status by session failed", "session_id", sessionID, "error", err)
			}
		}
	}

	return nil
}

func (s *GameSettleRetryScheduler) Stop() {
	s.base.Stop()
}
