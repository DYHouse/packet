package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/infrastructure/persistence/redis"
	"github.com/cashparty/backend/settlement/model"
)

type GameSettleService struct {
	platform       platform.Client
	billMgr        *BillManager
	redis          *cRedis.Client
	traceIDGen     *TraceIDGenerator
	cfg            *config.PlatformConfig
	lockCfg        *config.LockConfig
	userIDConvert  *UserIDConvertService
	callMgr        *PlatformCallManager
	robotChecker   RobotChecker
	virtualBalance *VirtualBalanceService
}

func NewGameSettleService(
	platformClient platform.Client,
	billMgr *BillManager,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
	robotChecker RobotChecker,
	virtualBalance *VirtualBalanceService,
) *GameSettleService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	return &GameSettleService{
		platform:       platformClient,
		billMgr:        billMgr,
		redis:          redis,
		traceIDGen:     traceIDGen,
		cfg:            cfg,
		lockCfg:        lockCfg,
		userIDConvert:  userIDConvert,
		callMgr:        callMgr,
		robotChecker:   robotChecker,
		virtualBalance: virtualBalance,
	}
}

// SettleGame performs game-level settlement: aggregates bill records and calls platform.Settle(/settle) for each player.
// This only reports game results (bet_amount, payout, result) without moving funds (funds already moved via Debit+Credit).
func (s *GameSettleService) SettleGame(ctx context.Context, sessionID int64) error {
	// Cheap pre-check BEFORE acquiring lock — if all settled, short-circuit
	allSettled, err := s.checkAllPlayersSettled(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("check all players settled failed: %w", err)
	}
	if allSettled {
		logger.Info("all players already settled, skipping", "session_id", sessionID)
		return nil
	}

	lockKey := redis.GameSettleLockKey(sessionID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.GameSettleLockTTL.Seconds()), func() error {
		// Double-check inside lock (in case of race)
		allSettled, err := s.checkAllPlayersSettled(ctx, sessionID)
		if err != nil {
			return err
		}
		if allSettled {
			return nil
		}

		// Check if already settled
		settlements, err := s.billMgr.GetAllRoundSettlementsBySession(ctx, sessionID)
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
		if err := s.billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusNone, dto.GameSettleStatusSettling); err != nil {
			logger.Error("update game settle status to settling failed", "session_id", sessionID, "error", err)
		}

		// Aggregate bet and payout per player
		betMap, err := s.billMgr.AggregateBetBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate bet by session failed: %w", err)
		}

		payOutMap, err := s.billMgr.AggregatePayOutBySession(ctx, sessionID)
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
		if err := s.creditSessionPayouts(ctx, sessionID, payOutMap, settlements[0].RoomID); err != nil {
			logger.Error("credit session payouts failed", "session_id", sessionID, "error", err)
		}

		// Settle each player
		allSuccess := true
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
			settled, err := s.billMgr.IsPlayerGameSettled(ctx, sessionID, userID)
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
		if err := s.billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusSettling, finalStatus); err != nil {
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
func (s *GameSettleService) checkAllPlayersSettled(ctx context.Context, sessionID int64) (bool, error) {
	settlements, err := s.billMgr.GetAllRoundSettlementsBySession(ctx, sessionID)
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
func (s *GameSettleService) settlePlayer(ctx context.Context, sessionID int64, userID int64, betAmount int64, payOut int64, startTime, endTime time.Time) error {
	// 幂等检查：已结算的玩家直接跳过，避免重试时重复调用 platform.Settle
	settled, err := s.billMgr.IsPlayerGameSettled(ctx, sessionID, userID)
	if err != nil {
		return fmt.Errorf("check player game settle status failed: %w", err)
	}
	if settled {
		return nil
	}

	// Retry case: if game_settle_status is Processing, previous RPC might have succeeded but DB update failed
	// TODO: query platform status when available

	// 机器人虚拟通道：跳过 platform.Settle，仅更新状态
	if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, userID) {
		if err := s.billMgr.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleNone, dto.BillGameSettleSettled); err != nil {
			logger.Error("mark robot game settle status failed", "session_id", sessionID, "user_id", userID, "error", err)
		}
		return nil
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	gameResult := "lose"
	if payOut > betAmount {
		gameResult = "win"
	}

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

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeSettle,
		BizOrderNo: bizOrderNo,
		ReqBody:    settleReq,
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", bizOrderNo, "error", callLogErr)
	}

	// Set game_settle_status to Processing before RPC (intermediate state for idempotency)
	if err := s.billMgr.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleNone, dto.BillGameSettleProcessing); err != nil {
		return fmt.Errorf("update game settle status to processing failed: %w", err)
	}

	result, err := s.platform.Settle(ctx, settleReq)
	if err != nil {
		// RPC failed — revert to None so it can be retried
		_ = s.billMgr.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleProcessing, dto.BillGameSettleNone)
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("game settle failed: %w", err)
	}

	// RPC succeeded — update to Settled
	if err := s.billMgr.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleProcessing, dto.BillGameSettleSettled); err != nil {
		logger.Error("mark player game settle status failed", "session_id", sessionID, "user_id", userID, "error", err)
	}

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: result,
			Status:   model.CallLogStatusSuccess,
		})
	}

	return nil
}

// RetryPlayerSettle retries game-level settle for a single player
func (s *GameSettleService) RetryPlayerSettle(ctx context.Context, sessionID int64, userID int64) error {
	lockKey := redis.GameSettleRetryLockKey(sessionID, userID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.GameSettleRetryLockTTL.Seconds()), func() error {
		betMap, err := s.billMgr.AggregateBetBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate bet by session failed: %w", err)
		}

		payOutMap, err := s.billMgr.AggregatePayOutBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("aggregate payout by session failed: %w", err)
		}

		settlements, err := s.billMgr.GetAllRoundSettlementsBySession(ctx, sessionID)
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

// creditSessionPayouts performs session-level credit for all players.
// For each player with payout > 0, it calls platform.Credit() to credit the full payout amount.
// The bet amount was already debited from the player's wallet during the deduction phase,
// so only the payout needs to be credited here.
func (s *GameSettleService) creditSessionPayouts(ctx context.Context, sessionID int64, payOutMap map[int64]int64, roomID int64) error {
	for userID, payOut := range payOutMap {
		if userID == dto.PlatformAccountID {
			continue
		}

		if payOut <= 0 {
			continue
		}

		if err := s.creditSessionPayout(ctx, sessionID, userID, payOut, roomID); err != nil {
			logger.Error("credit session payout failed", "session_id", sessionID, "user_id", userID, "error", err)
		}
	}

	return nil
}

// creditSessionPayout performs session-level payout credit for a single player with idempotency check.
func (s *GameSettleService) creditSessionPayout(ctx context.Context, sessionID int64, userID int64, payOut int64, roomID int64) error {
	existingBills, err := s.billMgr.GetBillsBySessionTypeAndUser(ctx, sessionID, dto.BillTypeSessionCredit, userID)
	if err != nil {
		return fmt.Errorf("check existing session credit bills failed: %w", err)
	}

	for _, bill := range existingBills {
		if bill.Status == dto.BillStatusSuccess {
			return nil
		}
		if bill.Status == dto.BillStatusProcessing || bill.Status == dto.BillStatusFailed {
			return s.executeSessionCredit(ctx, bill)
		}
	}

	traceID := s.traceIDGen.GenerateSessionCreditTraceID(sessionID, userID)
	bill := &model.BillRecord{
		RoundTraceID: traceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(traceID, dto.BillTypeSessionCredit, userID),
		BillType:     dto.BillTypeSessionCredit,
		RoomID:       roomID,
		SessionID:    sessionID,
		RoundID:      0,
		UserID:       userID,
		Amount:       payOut,
		Status:       dto.BillStatusProcessing,
		Remark:       fmt.Sprintf("会话级抢红包/奖励入账,局ID:%d,入账:%d", sessionID, payOut),
	}
	bill.IsRobot = s.robotChecker != nil && s.robotChecker.IsRobot(ctx, userID)

	if err := s.billMgr.CreateBill(ctx, bill); err != nil {
		return fmt.Errorf("create session credit bill failed: %w", err)
	}

	return s.executeSessionCredit(ctx, bill)
}

// executeSessionCredit calls platform.Credit() for a session credit bill.
// On success, marks bill as Success. On failure, marks as Failed and sets next_retry_at.
func (s *GameSettleService) executeSessionCredit(ctx context.Context, bill *model.BillRecord) error {
	// Idempotency: already success
	if bill.Status == dto.BillStatusSuccess {
		return nil
	}

	// Retry case: bill is already Processing — previous RPC might have succeeded but DB update failed
	// TODO: query platform status when available

	// Transition bill to Processing (from current status) before calling RPC
	if bill.Status != dto.BillStatusProcessing {
		if err := s.billMgr.UpdateBillStatus(ctx, bill.ID, bill.Status, dto.BillStatusProcessing, ""); err != nil {
			return fmt.Errorf("update bill to processing failed: %w", err)
		}
		bill.Status = dto.BillStatusProcessing
	}

	// 机器人虚拟通道
	if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, bill.UserID) {
		if err := s.virtualBalance.Credit(ctx, bill.UserID, bill.Amount); err != nil {
			s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
			return fmt.Errorf("robot virtual credit failed: %w", err)
		}
		balanceAfter, _ := s.virtualBalance.GetBalance(ctx, bill.UserID)
		return s.billMgr.UpdateBillSuccess(ctx, bill.ID, dto.BillStatusProcessing, 0, balanceAfter)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, bill.UserID)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	creditReq := &platform.CreditRequest{
		BizID:    bill.BizOrderNo,
		RoundID:  fmt.Sprintf("%d", bill.SessionID),
		GameID:   s.cfg.GameID,
		GameCode: s.cfg.GameCode,
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
		Amount:   platform.FormatAmount(bill.Amount),
		Reason:   bill.Remark,
		GameName: s.cfg.GameName,
	}

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeCredit,
		BizOrderNo: bill.BizOrderNo,
		ReqBody:    creditReq,
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", bill.BizOrderNo, "error", callLogErr)
	}

	result, err := s.platform.Credit(ctx, creditReq)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
		// Exponential backoff: delay = base * 2^retryCount, capped at max
		delay := time.Duration(float64(dto.CreditRetryBaseDelay) * math.Pow(2, float64(bill.RetryCount)))
		if delay > dto.CreditRetryMaxDelay {
			delay = dto.CreditRetryMaxDelay
		}
		nextRetryAt := time.Now().Add(delay)
		if retryErr := s.billMgr.IncrementRetryCountWithNextRetryTime(ctx, bill.ID, nextRetryAt); retryErr != nil {
			logger.Error("increment retry count with next retry time failed", "bill_id", bill.ID, "error", retryErr)
		}
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("session credit failed: %w", err)
	}

	balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
	if err != nil {
		logger.Error("parse balance amount failed after successful credit, mark bill as success with balance=0",
			"bill_id", bill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:       callLog.ID,
				RespBody: result,
				Status:   model.CallLogStatusSuccess,
			})
		}
		return s.billMgr.UpdateBillSuccess(ctx, bill.ID, dto.BillStatusProcessing, 0, 0)
	}

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: result,
			Status:   model.CallLogStatusSuccess,
		})
	}

	return s.billMgr.UpdateBillSuccess(ctx, bill.ID, dto.BillStatusProcessing, 0, balanceAfter)
}
