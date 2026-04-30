package scheduler

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

type GameSettleRetryScheduler struct {
	base          *BaseScheduler
	billMgr       *service.BillManager
	gameSettleSvc *service.GameSettleService
}

func NewGameSettleRetryScheduler(billMgr *service.BillManager, gameSettleSvc *service.GameSettleService, redis *cRedis.Client) *GameSettleRetryScheduler {
	config := SchedulerConfig{
		Name:         "game_settle_retry",
		Interval:     30 * time.Second,
		InitialDelay: 15 * time.Second,
		LockKey:      "scheduler:game_settle_retry:lock",
		LockTTL:      60,
	}

	return &GameSettleRetryScheduler{
		base:          NewBaseScheduler(config, nil, redis),
		billMgr:       billMgr,
		gameSettleSvc: gameSettleSvc,
	}
}

func (s *GameSettleRetryScheduler) Start() {
	s.base.task = s.execute
	s.base.Start()
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
			s.billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusSuccess)
		}
	}

	return nil
}

func (s *GameSettleRetryScheduler) Stop() {
	s.base.Stop()
}
