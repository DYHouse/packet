package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
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
		BaseDelay:       dto.CreditRetryBaseDelay,
		MaxDelay:        dto.CreditRetryMaxDelay,
		RetryMultiplier: 2.0,
	}
}

type CreditRetryService struct {
	billRepo      repository.BillRepository
	platform      platform.Client
	redis         cRedis.RedisClient
	traceIDGen    *TraceIDGenerator
	cfg           *config.PlatformConfig
	lockCfg       *config.LockConfig
	retryCfg      *CreditRetryConfig
	exceptionMgr  repository.ExceptionRepository
	userIDConvert *UserIDConvertService
	callMgr       repository.PlatformCallLogRepository
}

func NewCreditRetryService(
	billRepo repository.BillRepository,
	platform platform.Client,
	redis cRedis.RedisClient,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	exceptionMgr repository.ExceptionRepository,
	userIDConvert *UserIDConvertService,
	callMgr repository.PlatformCallLogRepository,
	baseDelay time.Duration,
	maxDelay time.Duration,
	maxRetryCount int,
) *CreditRetryService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	// 退避参数以 dto 常量为兜底默认值（与原硬编码 5s/5min/3 一致），传入非零值则覆盖。
	retryCfg := DefaultCreditRetryConfig()
	if baseDelay > 0 {
		retryCfg.BaseDelay = baseDelay
	}
	if maxDelay > 0 {
		retryCfg.MaxDelay = maxDelay
	}
	if maxRetryCount > 0 {
		retryCfg.MaxRetryCount = maxRetryCount
	}
	return &CreditRetryService{
		billRepo:      billRepo,
		platform:      platform,
		redis:         redis,
		traceIDGen:    traceIDGen,
		cfg:           cfg,
		lockCfg:       lockCfg,
		retryCfg:      retryCfg,
		exceptionMgr:  exceptionMgr,
		userIDConvert: userIDConvert,
		callMgr:       callMgr,
	}
}

func (s *CreditRetryService) GetRetryableCredits(ctx context.Context, limit int) ([]*domain.BillRecord, error) {
	return s.billRepo.GetRetryableCredits(ctx, limit)
}

func (s *CreditRetryService) RetryCredit(ctx context.Context, billID int64) error {
	lockKey := rediskeys.BillRetryLockKey(billID)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.CreditRetryLockTTL.Seconds()), func() error {
		return s.doRetryCredit(ctx, billID)
	})
}

func (s *CreditRetryService) doRetryCredit(ctx context.Context, billID int64) error {
	bill, err := s.billRepo.GetBillByID(ctx, billID)
	if err != nil {
		return err
	}

	if bill.Status == domain.BillStatusSuccess {
		return nil
	}

	if bill.RetryCount >= s.retryCfg.MaxRetryCount {
		return s.createException(ctx, bill)
	}

	if err := s.executeCredit(ctx, bill); err != nil {
		nextRetryAt := s.calculateNextRetryTime(bill.RetryCount)
		if incErr := s.billRepo.IncrementRetryCountWithNextRetryTime(ctx, billID, bill.RetryCount, nextRetryAt); incErr != nil {
			logger.Error("increment retry count failed", "bill_id", billID, "error", incErr)
		}
		return err
	}

	return nil
}

func (s *CreditRetryService) executeCredit(ctx context.Context, bill *domain.BillRecord) error {
	if bill.UserID == dto.PlatformAccountID {
		logger.Info("skip platform account bill, mark as success directly",
			"bill_id", bill.ID,
			"bill_type", bill.BillType,
			"amount", bill.Amount,
		)
		return s.billRepo.UpdateBillSuccess(ctx, bill.ID, domain.BillStatusProcessing, 0, 0)
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
		RoundID:        bill.RoundID,
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
		return fmt.Errorf("credit failed: %w", err)
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
		detail := fmt.Sprintf("入账 ParseAmount 解析失败,平台可能已入账但余额无法解析,需人工对账, bill_id: %d, raw_amount: %s, error: %s", bill.ID, result.Data.Balance.Amount, err.Error())
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

func (s *CreditRetryService) calculateNextRetryTime(retryCount int) time.Time {
	delay := time.Duration(float64(s.retryCfg.BaseDelay) *
		math.Pow(s.retryCfg.RetryMultiplier, float64(retryCount)))
	if delay > s.retryCfg.MaxDelay {
		delay = s.retryCfg.MaxDelay
	}
	return time.Now().Add(delay)
}

func (s *CreditRetryService) createExceptionRecord(ctx context.Context, bill *domain.BillRecord, exceptionType domain.ExceptionType, detail string) error {
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

func (s *CreditRetryService) createException(ctx context.Context, bill *domain.BillRecord) error {
	detail := fmt.Sprintf("入账重试超限，重试次数: %d, 最后错误: %s", bill.RetryCount, bill.ErrorMessage)
	return s.createExceptionRecord(ctx, bill, domain.ExceptionTypeCreditRetryExceed, detail)
}

func (s *CreditRetryService) CreateDebitFailedException(ctx context.Context, bill *domain.BillRecord) error {
	detail := fmt.Sprintf("扣款失败: %s", bill.ErrorMessage)
	if err := s.createExceptionRecord(ctx, bill, domain.ExceptionTypeDebitFailed, detail); err != nil {
		logger.Error("create debit failed exception failed", "bill_id", bill.ID, "error", err)
		return err
	}
	return nil
}
