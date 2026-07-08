package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/reward"
	"github.com/cashparty/backend/settlement/config"
	settlementRepository "github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

// GameSettleReportingService 负责游戏结果上报：调用 platform.Settle(/settle) 上报每个玩家本局游戏结果。
// 本 Service 仅上报游戏结果（bet_amount、payout、result），不直接移动资金；
// 资金移动由 SessionPayoutService 通过 platform.Credit 完成。
type GameSettleReportingService struct {
	platform            platform.Client
	billRepo            settlementRepository.BillRepository
	roundSettlementRepo settlementRepository.RoundSettlementRepository
	settlementQueryRepo settlementRepository.SettlementQueryRepository
	redis               cRedis.RedisClient
	traceIDGen          *TraceIDGenerator
	cfg                 *config.PlatformConfig
	lockCfg             *config.LockConfig
	userIDConvert       *UserIDConvertService
	callMgr             settlementRepository.PlatformCallLogRepository
	robotChecker        RobotChecker
	sessionPayoutSvc    *SessionPayoutService
}

func NewGameSettleReportingService(
	platformClient platform.Client,
	billRepo settlementRepository.BillRepository,
	roundSettlementRepo settlementRepository.RoundSettlementRepository,
	settlementQueryRepo settlementRepository.SettlementQueryRepository,
	redis cRedis.RedisClient,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	userIDConvert *UserIDConvertService,
	callMgr settlementRepository.PlatformCallLogRepository,
	robotChecker RobotChecker,
	sessionPayoutSvc *SessionPayoutService,
) *GameSettleReportingService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	return &GameSettleReportingService{
		platform:            platformClient,
		billRepo:            billRepo,
		roundSettlementRepo: roundSettlementRepo,
		settlementQueryRepo: settlementQueryRepo,
		redis:               redis,
		traceIDGen:          traceIDGen,
		cfg:                 cfg,
		lockCfg:             lockCfg,
		userIDConvert:       userIDConvert,
		callMgr:             callMgr,
		robotChecker:        robotChecker,
		sessionPayoutSvc:    sessionPayoutSvc,
	}
}

// SettleGame performs game-level settlement: aggregates bill records and calls platform.Settle(/settle) for each player.
// This only reports game results (bet_amount, payout, result) without moving funds (funds already moved via Debit+Credit).
func (s *GameSettleReportingService) SettleGame(ctx context.Context, sessionID int64) error {
	// Cheap pre-check BEFORE acquiring lock — if all settled, short-circuit
	allSettled, err := s.checkAllPlayersSettled(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("check all players settled failed: %w", err)
	}
	if allSettled {
		logger.Info("all players already settled, skipping", "session_id", sessionID)
		return nil
	}

	lockKey := rediskeys.GameSettleLockKey(sessionID)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.GameSettleLockTTL.Seconds()), func() error {
		// Double-check inside lock (in case of race)
		allSettled, err := s.checkAllPlayersSettled(ctx, sessionID)
		if err != nil {
			return err
		}
		if allSettled {
			return nil
		}

		// Check if already settled
		settlements, err := s.roundSettlementRepo.GetAllRoundSettlementsBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("get round settlements failed: %w", err)
		}
		if len(settlements) == 0 {
			return fmt.Errorf("no round settlements found for session: %d", sessionID)
		}

		// Check all rounds are credited
		for _, rs := range settlements {
			if rs.Status != dto.RoundStatusCredited {
				return fmt.Errorf("round %d not yet credited (status=%d)", rs.RoundID, rs.Status)
			}
		}

		// Mark game settle as in progress
		if err := s.roundSettlementRepo.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusNone, dto.GameSettleStatusSettling); err != nil {
			logger.Error("update game settle status to settling failed", "session_id", sessionID, "error", err)
		}

		// Aggregate bet and payout per player
		betMap, err := s.settlementQueryRepo.AggregateBetBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate bet by session failed: %w", err)
		}

		payOutMap, err := s.settlementQueryRepo.AggregatePayOutBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate payout by session failed: %w", err)
		}

		// Merge all user IDs
		allUsers := make(map[int64]bool)
		for uid := range betMap {
			allUsers[uid] = true
		}
		for uid := range payOutMap {
			allUsers[uid] = true
		}

		// Determine game time range
		startTime := settlements[0].CreatedAt
		endTime := settlements[len(settlements)-1].UpdatedAt

		// Session-level credit: credit players with their payout amount (grab packet + reward income).
		// This is the actual fund movement, while round-level credits are just internal bookkeeping.
		allSuccess := true
		if err := s.sessionPayoutSvc.CreditSessionPayouts(ctx, sessionID, payOutMap, settlements[0].RoomID); err != nil {
			logger.Error("credit session payouts failed", "session_id", sessionID, "error", err)
			allSuccess = false
		}

		// Settle each player
		for userID := range allUsers {
			betAmount := betMap[userID]
			payOut := payOutMap[userID]

			// Skip platform account
			if userID == dto.PlatformAccountID {
				continue
			}

			// Skip players with no activity
			if betAmount == 0 && payOut == 0 {
				continue
			}

			// 幂等检查：跳过已成功结算的玩家，避免重试时重复调用 platform.Settle
			settled, err := s.settlementQueryRepo.IsPlayerGameSettled(ctx, sessionID, userID)
			if err != nil {
				logger.Error("check player settled failed", "session_id", sessionID, "user_id", userID, "error", err)
				allSuccess = false
				continue
			}
			if settled {
				continue
			}

			if err := s.settlePlayer(ctx, sessionID, userID, betAmount, payOut, startTime, endTime); err != nil {
				logger.Error("settle player failed", "session_id", sessionID, "user_id", userID, "error", err)
				allSuccess = false
			}
		}

		// Update game settle status
		finalStatus := dto.GameSettleStatusSuccess
		if !allSuccess {
			finalStatus = dto.GameSettleStatusFailed
		}
		if err := s.roundSettlementRepo.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusSettling, finalStatus); err != nil {
			logger.Error("update game settle final status failed", "session_id", sessionID, "error", err)
		}

		if !allSuccess {
			return fmt.Errorf("game settle partially failed for session: %d", sessionID)
		}
		return nil
	})
}

// checkAllPlayersSettled checks whether all round settlements for a session have GameSettleStatusSuccess.
// Used as a cheap pre-check before acquiring the Redis lock in SettleGame.
func (s *GameSettleReportingService) checkAllPlayersSettled(ctx context.Context, sessionID int64) (bool, error) {
	settlements, err := s.roundSettlementRepo.GetAllRoundSettlementsBySession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("get round settlements failed: %w", err)
	}
	if len(settlements) == 0 {
		return false, fmt.Errorf("no round settlements found for session: %d", sessionID)
	}
	for _, rs := range settlements {
		if rs.GameSettleStatus != dto.GameSettleStatusSuccess {
			return false, nil
		}
	}
	return true, nil
}

// settlePlayer calls platform.Settle(/settle) for a single player's game result
func (s *GameSettleReportingService) settlePlayer(ctx context.Context, sessionID int64, userID int64, betAmount int64, payOut int64, startTime, endTime time.Time) error {
	// 幂等检查：已结算的玩家直接跳过，避免重试时重复调用 platform.Settle
	settled, err := s.settlementQueryRepo.IsPlayerGameSettled(ctx, sessionID, userID)
	if err != nil {
		return fmt.Errorf("check player game settle status failed: %w", err)
	}
	if settled {
		return nil
	}

	// 重试场景：game_settle_status 为 Processing 时，先前 RPC 可能已成功但 DB 更新失败。
	// 平台暂未提供查询接口，当前依靠 platform.Settle 的 BizOrderNo 幂等兜底。

	// 机器人虚拟通道：跳过 platform.Settle，仅更新状态
	if s.robotChecker == nil {
		return fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, userID)
	if err != nil {
		return fmt.Errorf("check robot failed: %w", err)
	}
	if isRobot {
		if err := s.billRepo.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleNone, dto.BillGameSettleSettled); err != nil {
			logger.Error("mark robot game settle status failed", "session_id", sessionID, "user_id", userID, "error", err)
		}
		return nil
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	gameResult := reward.DetermineGameResult(payOut, betAmount)

	bizOrderNo := s.traceIDGen.GenerateBizOrderNo(s.traceIDGen.GenerateGameSettleTraceID(sessionID), dto.BillTypeGameSettle, userID)

	settleReq := &platform.SettleRequest{
		BizID:           bizOrderNo,
		RoundID:         fmt.Sprintf("%d", sessionID),
		GameID:          s.cfg.GameID,
		GameCode:        s.cfg.GameCode,
		GameName:        s.cfg.GameName,
		UserID:          platformUserID,
		Currency:        s.cfg.Currency,
		BetAmount:       platform.FormatAmount(betAmount),
		PayOut:          platform.FormatAmount(payOut),
		Multiplier:      "1",
		StartTime:       startTime.UnixMilli(),
		EndTime:         endTime.UnixMilli(),
		Result:          gameResult,
		ActualBetAmount: platform.FormatAmount(betAmount),
	}

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &dto.CallLogCreateParams{
		CallType:   model.CallTypeSettle,
		BizOrderNo: bizOrderNo,
		ReqBody:    settleReq,
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", bizOrderNo, "error", callLogErr)
	}

	// Set game_settle_status to Processing before RPC (intermediate state for idempotency)
	if err := s.billRepo.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleNone, dto.BillGameSettleProcessing); err != nil {
		return fmt.Errorf("update game settle status to processing failed: %w", err)
	}

	result, err := s.platform.Settle(ctx, settleReq)
	if err != nil {
		// RPC failed — revert to None so it can be retried
		_ = s.billRepo.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleProcessing, dto.BillGameSettleNone)
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("game settle failed: %w", err)
	}

	// RPC succeeded — update to Settled
	if err := s.billRepo.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleProcessing, dto.BillGameSettleSettled); err != nil {
		logger.Error("mark player game settle status failed", "session_id", sessionID, "user_id", userID, "error", err)
	}

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: result,
			Status:   model.CallLogStatusSuccess,
		})
	}

	return nil
}

// RetryPlayerSettle retries game-level settle for a single player
func (s *GameSettleReportingService) RetryPlayerSettle(ctx context.Context, sessionID int64, userID int64) error {
	lockKey := rediskeys.GameSettleRetryLockKey(sessionID, userID)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.GameSettleRetryLockTTL.Seconds()), func() error {
		betMap, err := s.settlementQueryRepo.AggregateBetBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate bet by session failed: %w", err)
		}

		payOutMap, err := s.settlementQueryRepo.AggregatePayOutBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate payout by session failed: %w", err)
		}

		settlements, err := s.roundSettlementRepo.GetAllRoundSettlementsBySession(ctx, sessionID)
		if err != nil || len(settlements) == 0 {
			return fmt.Errorf("get round settlements failed: %w", err)
		}

		startTime := settlements[0].CreatedAt
		endTime := settlements[len(settlements)-1].UpdatedAt

		betAmount := betMap[userID]
		payOut := payOutMap[userID]

		return s.settlePlayer(ctx, sessionID, userID, betAmount, payOut, startTime, endTime)
	})
}
