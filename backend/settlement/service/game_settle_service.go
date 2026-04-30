package service

import (
	"context"
	"fmt"
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
	platform      platform.Client
	billMgr       *BillManager
	redis         *cRedis.Client
	traceIDGen    *TraceIDGenerator
	cfg           *config.PlatformConfig
	userIDConvert *UserIDConvertService
	callMgr       *PlatformCallManager
}

func NewGameSettleService(
	platformClient platform.Client,
	billMgr *BillManager,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
) *GameSettleService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &GameSettleService{
		platform:      platformClient,
		billMgr:       billMgr,
		redis:         redis,
		traceIDGen:    traceIDGen,
		cfg:           cfg,
		userIDConvert: userIDConvert,
		callMgr:       callMgr,
	}
}

// SettleGame performs game-level settlement: aggregates bill records and calls platform.Settle(/settle) for each player.
// This only reports game results (bet_amount, payout, result) without moving funds (funds already moved via Debit+Credit).
func (s *GameSettleService) SettleGame(ctx context.Context, sessionID int64) error {
	lockKey := redis.GameSettleLockKey(sessionID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, 60, func() error {
		// Check if already settled
		settlements, err := s.billMgr.GetAllRoundSettlementsBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("get round settlements failed: %w", err)
		}
		if len(settlements) == 0 {
			return fmt.Errorf("no round settlements found for session: %d", sessionID)
		}

		// Check if game settle already done
		allSettled := true
		for _, rs := range settlements {
			if rs.GameSettleStatus != dto.GameSettleStatusSuccess {
				allSettled = false
				break
			}
		}
		if allSettled {
			return nil
		}

		// Check all rounds are credited
		for _, rs := range settlements {
			if rs.Status != dto.RoundStatusCredited {
				return fmt.Errorf("round %d not yet credited (status=%d)", rs.RoundID, rs.Status)
			}
		}

		// Mark game settle as in progress
		if err := s.billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, dto.GameSettleStatusSettling); err != nil {
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

		// Net settlement: credit players with positive net amount (payout - bet)
		// This is the actual fund movement, while round-level credits are just internal bookkeeping
		if err := s.netSettlePlayers(ctx, sessionID, betMap, payOutMap, settlements[0].RoomID); err != nil {
			logger.Error("net settle players failed", "session_id", sessionID, "error", err)
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
		if err := s.billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, finalStatus); err != nil {
			logger.Error("update game settle final status failed", "session_id", sessionID, "error", err)
		}

		if !allSuccess {
			return fmt.Errorf("game settle partially failed for session: %d", sessionID)
		}
		return nil
	})
}

// settlePlayer calls platform.Settle(/settle) for a single player's game result
func (s *GameSettleService) settlePlayer(ctx context.Context, sessionID int64, userID int64, betAmount int64, payOut int64, startTime, endTime time.Time) error {
	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	gameResult := "lose"
	if payOut > betAmount {
		gameResult = "win"
	}

	bizOrderNo := s.traceIDGen.GenerateBizOrderNo("GAME_SETTLE", userID)

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

	callLog, _ := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeSettle,
		BizOrderNo: bizOrderNo,
		ReqBody:    settleReq,
	})

	result, err := s.platform.Settle(ctx, settleReq)
	if err != nil {
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("game settle failed: %w", err)
	}

	// Mark all bills for this user in this game as settled
	if err := s.billMgr.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleSettled); err != nil {
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
	return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
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

// netSettlePlayers performs session-level net settlement for all players.
// For each player with netAmount = payout - bet > 0, it calls platform.Credit()
// to move the net positive funds.
func (s *GameSettleService) netSettlePlayers(ctx context.Context, sessionID int64, betMap map[int64]int64, payOutMap map[int64]int64, roomID int64) error {
	allUsers := make(map[int64]bool)
	for uid := range betMap {
		allUsers[uid] = true
	}
	for uid := range payOutMap {
		allUsers[uid] = true
	}

	for userID := range allUsers {
		if userID == dto.PlatformAccountID {
			continue
		}

		betAmount := betMap[userID]
		payOut := payOutMap[userID]
		netAmount := payOut - betAmount

		if netAmount <= 0 {
			continue
		}

		if err := s.netSettlePlayer(ctx, sessionID, userID, netAmount, roomID); err != nil {
			logger.Error("net settle player failed", "session_id", sessionID, "user_id", userID, "error", err)
		}
	}

	return nil
}

// netSettlePlayer performs net settlement for a single player with idempotency check.
func (s *GameSettleService) netSettlePlayer(ctx context.Context, sessionID int64, userID int64, netAmount int64, roomID int64) error {
	existingBills, err := s.billMgr.GetBillsBySessionTypeAndUser(ctx, sessionID, dto.BillTypeNetSettlement, userID)
	if err != nil {
		return fmt.Errorf("check existing net settlement bills failed: %w", err)
	}

	for _, bill := range existingBills {
		if bill.Status == dto.BillStatusSuccess {
			return nil
		}
		if bill.Status == dto.BillStatusProcessing || bill.Status == dto.BillStatusFailed {
			return s.executeNetCredit(ctx, bill)
		}
	}

	traceID := fmt.Sprintf("NET_SETTLE_%d_%d", sessionID, userID)
	bill := &model.BillRecord{
		RoundTraceID: traceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("NET_CREDIT", userID),
		BillType:     dto.BillTypeNetSettlement,
		RoomID:       roomID,
		SessionID:    sessionID,
		RoundID:      0,
		UserID:       userID,
		Amount:       netAmount,
		Status:       dto.BillStatusProcessing,
		Remark:       fmt.Sprintf("会话级净额入账,局ID:%d,净额:%d", sessionID, netAmount),
	}

	if err := s.billMgr.CreateBill(ctx, bill); err != nil {
		return fmt.Errorf("create net settlement bill failed: %w", err)
	}

	return s.executeNetCredit(ctx, bill)
}

// executeNetCredit calls platform.Credit() for a net settlement bill.
// On success, marks bill as Success. On failure, marks as Failed and sets next_retry_at.
func (s *GameSettleService) executeNetCredit(ctx context.Context, bill *model.BillRecord) error {
	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, bill.UserID)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
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

	callLog, _ := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeCredit,
		BizOrderNo: bill.BizOrderNo,
		ReqBody:    creditReq,
	})

	result, err := s.platform.Credit(ctx, creditReq)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
		nextRetryAt := time.Now().Add(5 * time.Second)
		if retryErr := s.billMgr.SetNextRetryTime(ctx, bill.ID, nextRetryAt); retryErr != nil {
			logger.Error("set next retry time failed", "bill_id", bill.ID, "error", retryErr)
		}
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("net credit failed: %w", err)
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
		return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, 0)
	}

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: result,
			Status:   model.CallLogStatusSuccess,
		})
	}

	return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter)
}
