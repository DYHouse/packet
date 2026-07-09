package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
)

// SessionPayoutService 负责会话级派奖：调用 platform.Credit 派发玩家在整局游戏中应得的奖金。
// 抢红包/奖励在 round 级别仅做内部记账（BillRecord），实际资金移动延迟到会话级通过本 Service 完成。
type SessionPayoutService struct {
	platform       platform.Client
	billRepo       repository.BillRepository
	traceIDGen     *TraceIDGenerator
	cfg            *config.PlatformConfig
	userIDConvert  *UserIDConvertService
	callMgr        repository.PlatformCallLogRepository
	robotChecker   RobotChecker
	virtualBalance domain.VirtualBalanceService
	exceptionMgr   repository.ExceptionRepository
}

func NewSessionPayoutService(
	platformClient platform.Client,
	billRepo repository.BillRepository,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	userIDConvert *UserIDConvertService,
	callMgr repository.PlatformCallLogRepository,
	robotChecker RobotChecker,
	virtualBalance domain.VirtualBalanceService,
	exceptionMgr repository.ExceptionRepository,
) *SessionPayoutService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &SessionPayoutService{
		platform:       platformClient,
		billRepo:       billRepo,
		traceIDGen:     traceIDGen,
		cfg:            cfg,
		userIDConvert:  userIDConvert,
		callMgr:        callMgr,
		robotChecker:   robotChecker,
		virtualBalance: virtualBalance,
		exceptionMgr:   exceptionMgr,
	}
}

// CreditSessionPayouts performs session-level credit for all players.
// For each player with payout > 0, it calls platform.Credit() to credit the full payout amount.
// The bet amount was already debited from the player's wallet during the deduction phase,
// so only the payout needs to be credited here.
func (s *SessionPayoutService) CreditSessionPayouts(ctx context.Context, sessionID int64, payOutMap map[int64]int64, roomID int64) error {
	for userID, payOut := range payOutMap {
		if userID == dto.PlatformAccountID {
			continue
		}

		if payOut <= 0 {
			continue
		}

		if err := s.creditSessionPayout(ctx, sessionID, userID, payOut, roomID); err != nil {
			logger.Error("credit session payout failed", "session_id", sessionID, "user_id", userID, "error", err)
			return fmt.Errorf("creditSessionPayout failed: %w", err)
		}
	}

	return nil
}

// creditSessionPayout performs session-level payout credit for a single player with idempotency check.
func (s *SessionPayoutService) creditSessionPayout(ctx context.Context, sessionID int64, userID int64, payOut int64, roomID int64) error {
	existingBills, err := s.billRepo.GetBillsBySessionTypeAndUser(ctx, sessionID, domain.BillTypeSessionCredit, userID)
	if err != nil {
		return fmt.Errorf("check existing session credit bills failed: %w", err)
	}

	for _, bill := range existingBills {
		if bill.Status == domain.BillStatusSuccess {
			return nil
		}
		if bill.Status == domain.BillStatusProcessing || bill.Status == domain.BillStatusFailed {
			return s.executeSessionCredit(ctx, bill)
		}
	}

	traceID := s.traceIDGen.GenerateSessionCreditTraceID(sessionID, userID)
	bill := &domain.BillRecord{
		RoundTraceID: traceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(traceID, domain.BillTypeSessionCredit, userID),
		BillType:     domain.BillTypeSessionCredit,
		RoomID:       roomID,
		SessionID:    sessionID,
		RoundID:      0,
		UserID:       userID,
		Amount:       payOut,
		Status:       domain.BillStatusProcessing,
		Remark:       fmt.Sprintf("会话级抢红包/奖励入账,局ID:%d,入账:%d", sessionID, payOut),
	}
	if s.robotChecker == nil {
		return fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, userID)
	if err != nil {
		return fmt.Errorf("check robot failed: %w", err)
	}
	bill.IsRobot = isRobot

	if err := s.billRepo.CreateBill(ctx, bill); err != nil {
		return fmt.Errorf("create session credit bill failed: %w", err)
	}

	return s.executeSessionCredit(ctx, bill)
}

// executeSessionCredit calls platform.Credit() for a session credit bill.
// On success, marks bill as Success. On failure, marks as Failed and sets next_retry_at.
func (s *SessionPayoutService) executeSessionCredit(ctx context.Context, bill *domain.BillRecord) error {
	// Idempotency: already success
	if bill.Status == domain.BillStatusSuccess {
		return nil
	}

	// 重试场景：账单已处于 Processing 时，先前 RPC 可能已成功但 DB 更新失败。
	// 平台暂未提供查询接口，当前依靠 platform.Credit 的 BizOrderNo 幂等兜底。

	// Transition bill to Processing (from current status) before calling RPC
	if bill.Status != domain.BillStatusProcessing {
		if err := s.billRepo.UpdateBillStatus(ctx, bill.ID, bill.Status, domain.BillStatusProcessing, ""); err != nil {
			return fmt.Errorf("update bill to processing failed: %w", err)
		}
		bill.Status = domain.BillStatusProcessing
	}

	// 机器人虚拟通道
	if s.robotChecker == nil {
		return fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, bill.UserID)
	if err != nil {
		return fmt.Errorf("check robot failed: %w", err)
	}
	if isRobot {
		if err := s.virtualBalance.Credit(ctx, bill.UserID, bill.Amount); err != nil {
			s.billRepo.UpdateBillStatus(ctx, bill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
			return fmt.Errorf("robot virtual credit failed: %w", err)
		}
		balanceAfter, _ := s.virtualBalance.GetBalance(ctx, bill.UserID)
		if err := bill.TransitionTo(domain.BillStatusSuccess); err != nil {
			logger.Error("invalid bill status transition", "bill_id", bill.ID, "error", err)
			return err
		}
		return s.billRepo.UpdateBillSuccess(ctx, bill.ID, domain.BillStatusProcessing, 0, balanceAfter)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, bill.UserID)
	if err != nil {
		s.billRepo.UpdateBillStatus(ctx, bill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
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

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &dto.CallLogCreateParams{
		TraceID:        resolveTraceID(ctx),
		CallType:       domain.CallTypeCredit,
		BizOrderNo:     bill.BizOrderNo,
		UserID:         bill.UserID,
		PlatformUserID: platformUserID,
		SessionID:      bill.SessionID,
		RoundID:        0,
		Amount:         bill.Amount,
		Currency:       s.cfg.Currency,
		ReqBody:        creditReq,
		NodeID:         resolveNodeID(),
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", bill.BizOrderNo, "error", callLogErr)
	}

	result, err := s.platform.Credit(ctx, creditReq)
	if err != nil {
		s.billRepo.UpdateBillStatus(ctx, bill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
		// Exponential backoff: delay = base * 2^retryCount, capped at max
		delay := time.Duration(float64(dto.CreditRetryBaseDelay) * math.Pow(2, float64(bill.RetryCount)))
		if delay > dto.CreditRetryMaxDelay {
			delay = dto.CreditRetryMaxDelay
		}
		nextRetryAt := time.Now().Add(delay)
		if retryErr := s.billRepo.IncrementRetryCountWithNextRetryTime(ctx, bill.ID, bill.RetryCount, nextRetryAt); retryErr != nil {
			logger.Error("increment retry count with next retry time failed", "bill_id", bill.ID, "error", retryErr)
		}
		if callLog != nil {
			logStatus := domain.CallLogStatusFailed
			if isTimeoutError(err) {
				logStatus = domain.CallLogStatusTimeout
			}
			s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       logStatus,
				ErrorMessage: err.Error(),
				RequestTime:  callLog.RequestTime,
			})
		}
		return fmt.Errorf("session credit failed: %w", err)
	}

	balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
	if err != nil {
		// ParseAmount 失败：平台可能已实际入账但响应余额无法解析。
		// 资金安全要求 fail-closed：标记账单为 Failed 并创建异常记录供人工对账，不得标记为 Success。
		logger.Error("parse balance amount failed after successful credit, mark bill as failed",
			"bill_id", bill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
		if updateErr := s.billRepo.UpdateBillStatus(ctx, bill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error()); updateErr != nil {
			logger.Error("update bill to failed after parse amount error", "bill_id", bill.ID, "error", updateErr)
		}
		detail := fmt.Sprintf("会话派奖 ParseAmount 解析失败,平台可能已入账但余额无法解析,需人工对账, bill_id: %d, raw_amount: %s, error: %s", bill.ID, result.Data.Balance.Amount, err.Error())
		if excErr := s.createExceptionRecord(ctx, bill, domain.ExceptionTypeCreditRetryExceed, detail); excErr != nil {
			logger.Error("create exception record for parse amount failure failed", "bill_id", bill.ID, "error", excErr)
		}
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
				ID:          callLog.ID,
				RespBody:    result,
				Status:      domain.CallLogStatusSuccess,
				RequestTime: callLog.RequestTime,
			})
		}
		return fmt.Errorf("parse balance amount failed for bill %d: %w", bill.ID, err)
	}

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
			ID:          callLog.ID,
			RespBody:    result,
			Status:      domain.CallLogStatusSuccess,
			RequestTime: callLog.RequestTime,
		})
	}

	if err := bill.TransitionTo(domain.BillStatusSuccess); err != nil {
		logger.Error("invalid bill status transition", "bill_id", bill.ID, "error", err)
		return err
	}
	return s.billRepo.UpdateBillSuccess(ctx, bill.ID, domain.BillStatusProcessing, 0, balanceAfter)
}

// createExceptionRecord 创建异常记录并关联到账单，供人工对账。
func (s *SessionPayoutService) createExceptionRecord(ctx context.Context, bill *domain.BillRecord, exceptionType domain.ExceptionType, detail string) error {
	exception := &domain.ExceptionRecord{
		ExceptionNo:     s.traceIDGen.GenerateExceptionNo(bill.ID, strconv.Itoa(int(exceptionType))),
		ExceptionType:   exceptionType,
		BillID:          bill.ID,
		RoundTraceID:    bill.RoundTraceID,
		RoundID:         bill.RoundID,
		BillType:        bill.BillType,
		UserID:          bill.UserID,
		Amount:          bill.Amount,
		Status:          domain.ExceptionStatusPending,
		ExceptionDetail: detail,
	}

	if err := s.exceptionMgr.Create(ctx, exception); err != nil {
		return err
	}

	return s.billRepo.UpdateBillExceptionID(ctx, bill.ID, exception.ID)
}
