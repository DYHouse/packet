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

type CreditRetryConfig struct {
	MaxRetryCount   int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	RetryMultiplier float64
}

func DefaultCreditRetryConfig() *CreditRetryConfig {
	return &CreditRetryConfig{
		MaxRetryCount:   dto.MaxRetryCount,
		BaseDelay:       5 * time.Second,
		MaxDelay:        5 * time.Minute,
		RetryMultiplier: 2.0,
	}
}

type CreditRetryService struct {
	billMgr       *BillManager
	platform      platform.Client
	redis         *cRedis.Client
	traceIDGen    *TraceIDGenerator
	cfg           *config.PlatformConfig
	retryCfg      *CreditRetryConfig
	exceptionMgr  *ExceptionManager
	userIDConvert *UserIDConvertService
	callMgr       *PlatformCallManager
}

func NewCreditRetryService(
	billMgr *BillManager,
	platform platform.Client,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	exceptionMgr *ExceptionManager,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
) *CreditRetryService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &CreditRetryService{
		billMgr:       billMgr,
		platform:      platform,
		redis:         redis,
		traceIDGen:    traceIDGen,
		cfg:           cfg,
		retryCfg:      DefaultCreditRetryConfig(),
		exceptionMgr:  exceptionMgr,
		userIDConvert: userIDConvert,
		callMgr:       callMgr,
	}
}

func (s *CreditRetryService) GetRetryableCredits(ctx context.Context, limit int) ([]*model.BillRecord, error) {
	return s.billMgr.GetRetryableCredits(ctx, limit)
}

func (s *CreditRetryService) RetryCredit(ctx context.Context, billID int64) error {
	lockKey := redis.BillRetryLockKey(billID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
		return s.doRetryCredit(ctx, billID)
	})
}

func (s *CreditRetryService) doRetryCredit(ctx context.Context, billID int64) error {
	bill, err := s.billMgr.GetBillByID(ctx, billID)
	if err != nil {
		return err
	}

	if bill.Status == dto.BillStatusSuccess {
		return nil
	}

	if bill.RetryCount >= s.retryCfg.MaxRetryCount {
		return s.createException(ctx, bill)
	}

	if err := s.executeCredit(ctx, bill); err != nil {
		nextRetryAt := s.calculateNextRetryTime(bill.RetryCount)
		if incErr := s.billMgr.IncrementRetryCountWithNextRetryTime(ctx, billID, nextRetryAt); incErr != nil {
			logger.Error("increment retry count failed", "bill_id", billID, "error", incErr)
		}
		return err
	}

	return nil
}

func (s *CreditRetryService) executeCredit(ctx context.Context, bill *model.BillRecord) error {
	if bill.UserID == dto.PlatformAccountID {
		logger.Info("skip platform account bill, mark as success directly",
			"bill_id", bill.ID,
			"bill_type", bill.BillType,
			"amount", bill.Amount,
		)
		return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, 0)
	}

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
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("credit failed: %w", err)
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

func (s *CreditRetryService) calculateNextRetryTime(retryCount int) time.Time {
	delay := time.Duration(float64(s.retryCfg.BaseDelay) *
		math.Pow(s.retryCfg.RetryMultiplier, float64(retryCount)))
	if delay > s.retryCfg.MaxDelay {
		delay = s.retryCfg.MaxDelay
	}
	return time.Now().Add(delay)
}

func (s *CreditRetryService) createExceptionRecord(ctx context.Context, bill *model.BillRecord, exceptionType model.ExceptionType, detail string) error {
	exception := &model.ExceptionRecord{
		ExceptionNo:     s.traceIDGen.GenerateExceptionNo(),
		ExceptionType:   exceptionType,
		BillID:          bill.ID,
		RoundTraceID:    bill.RoundTraceID,
		RoundID:         bill.RoundID,
		BillType:        bill.BillType,
		UserID:          bill.UserID,
		Amount:          bill.Amount,
		Status:          model.ExceptionStatusPending,
		ExceptionDetail: detail,
	}

	if err := s.exceptionMgr.Create(ctx, exception); err != nil {
		return err
	}

	return s.billMgr.UpdateBillExceptionID(ctx, bill.ID, exception.ID)
}

func (s *CreditRetryService) createException(ctx context.Context, bill *model.BillRecord) error {
	detail := fmt.Sprintf("入账重试超限，重试次数: %d, 最后错误: %s", bill.RetryCount, bill.ErrorMessage)
	return s.createExceptionRecord(ctx, bill, model.ExceptionTypeCreditRetryExceed, detail)
}

func (s *CreditRetryService) CreateDebitFailedException(ctx context.Context, bill *model.BillRecord) error {
	detail := fmt.Sprintf("扣款失败: %s", bill.ErrorMessage)
	if err := s.createExceptionRecord(ctx, bill, model.ExceptionTypeDebitFailed, detail); err != nil {
		logger.Error("create debit failed exception failed", "bill_id", bill.ID, "error", err)
		return err
	}
	return nil
}
