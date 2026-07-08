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
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

// RefundService 负责退款申请、审批与执行。
// dbRepo 用于跨表事务方法（同时更新 refund_audit 与 bill_record）的事务编排。
type RefundService struct {
	platform        platform.Client
	dbRepo          repository.DBRepository
	billRepo        repository.BillRepository
	refundAuditRepo repository.RefundAuditRepository
	redis           cRedis.RedisClient
	traceIDGen      *TraceIDGenerator
	cfg             *config.PlatformConfig
	lockCfg         *config.LockConfig
	userIDConvert   *UserIDConvertService
	callMgr         repository.PlatformCallLogRepository
}

func NewRefundService(
	platformClient platform.Client,
	dbRepo repository.DBRepository,
	billRepo repository.BillRepository,
	refundAuditRepo repository.RefundAuditRepository,
	redis cRedis.RedisClient,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	userIDConvert *UserIDConvertService,
	callMgr repository.PlatformCallLogRepository,
) *RefundService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	return &RefundService{
		platform:        platformClient,
		dbRepo:          dbRepo,
		billRepo:        billRepo,
		refundAuditRepo: refundAuditRepo,
		redis:           redis,
		traceIDGen:      traceIDGen,
		cfg:             cfg,
		lockCfg:         lockCfg,
		userIDConvert:   userIDConvert,
		callMgr:         callMgr,
	}
}

func (s *RefundService) ApplyForRefund(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	lockKey := rediskeys.RefundApplyLockKey(req.BillID)
	var refundOrderNo string
	err := lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.RefundApplyLockTTL.Seconds()), func() error {
		var applyErr error
		refundOrderNo, applyErr = s.applyForRefundLocked(ctx, req)
		return applyErr
	})
	return refundOrderNo, err
}

func (s *RefundService) applyForRefundLocked(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	bill, err := s.billRepo.GetBillByID(ctx, req.BillID)
	if err != nil {
		return "", fmt.Errorf("bill not found: %w", err)
	}

	if bill.Status != dto.BillStatusSuccess {
		return "", fmt.Errorf("bill status is not success, cannot refund")
	}

	if bill.RefundStatus == dto.RefundStatusRefunded {
		return "", fmt.Errorf("bill already refunded")
	}

	if bill.RefundStatus == dto.RefundStatusPending {
		existingRefund, err := s.refundAuditRepo.GetRefundAuditByBillID(ctx, bill.ID)
		if err == nil && existingRefund != nil {
			return existingRefund.RefundOrderNo, nil
		}
	}

	refundOrderNo := s.traceIDGen.GenerateRefundOrderNo(bill.ID)
	refundAudit := &model.RefundAudit{
		RefundOrderNo: refundOrderNo,
		RoundTraceID:  bill.RoundTraceID,
		BatchID:       bill.BatchID,
		RoomID:        bill.RoomID,
		SessionID:     bill.SessionID,
		RoundID:       bill.RoundID,
		UserID:        bill.UserID,
		BillID:        bill.ID,
		BillOrderNo:   bill.BizOrderNo,
		RefundAmount:  req.RefundAmount,
		RefundReason:  req.RefundReason,
		RefundType:    req.RefundType,
		Status:        dto.RefundStatusPending,
		AppliedAt:     time.Now(),
		AppliedBy:     req.AppliedBy,
	}

	// 跨表事务（refund_audit + bill_record），通过 dbRepo.WithTransaction 编排。
	if err := s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return tx.RefundAuditRepo().CreateRefundAuditAndUpdateBillRefundStatus(ctx, refundAudit, bill.ID, dto.RefundStatusNone, dto.RefundStatusPending, refundOrderNo)
	}); err != nil {
		return "", err
	}

	return refundOrderNo, nil
}

func (s *RefundService) ApproveRefund(ctx context.Context, req *dto.RefundApproveRequest) error {
	refund, err := s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
	if err != nil {
		return fmt.Errorf("refund audit not found: %w", err)
	}

	if refund.Status != dto.RefundStatusPending {
		return fmt.Errorf("refund status is not pending")
	}

	lockKey := rediskeys.RefundLockKey(req.RefundOrderNo)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.RefundApproveLockTTL.Seconds()), func() error {
		// 锁内二次检查状态，防止并发重复退款
		refund, err := s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
		if err != nil {
			return fmt.Errorf("refund audit not found: %w", err)
		}
		if refund.Status != dto.RefundStatusPending {
			return nil
		}

		now := time.Now()
		if err := s.refundAuditRepo.UpdateRefundAuditStatus(ctx, refund.ID, dto.RefundStatusApproved,
			now, req.ApprovedBy, req.Remark); err != nil {
			return err
		}

		return s.executeRefund(ctx, refund)
	})
}

func (s *RefundService) executeRefund(ctx context.Context, refund *model.RefundAudit) error {
	// 1. 幂等跳过：已退款的不再重复处理
	if refund.Status == dto.RefundStatusRefunded {
		return nil
	}

	// 2. 重试场景：退款单已处于 Processing，先前 RPC 可能已成功但 DB 更新失败。
	//    平台暂未提供查询接口，当前依靠 BizOrderNo 幂等兜底，fall through 重试 RPC。

	// 3. 将退款单置为 Processing（乐观锁 WHERE status = Approved）
	if err := s.refundAuditRepo.UpdateRefundAuditToProcessing(ctx, refund.ID, dto.RefundStatusApproved); err != nil {
		return fmt.Errorf("update refund to processing failed: %w", err)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, refund.UserID)
	if err != nil {
		if retryErr := s.refundAuditRepo.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, dto.RefundStatusProcessing, err.Error()); retryErr != nil {
			logger.Warn("update refund audit to pending for retry failed", "refund_id", refund.ID, "error", retryErr)
		}
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	creditReq := &platform.CreditRequest{
		BizID:    refund.RefundOrderNo,
		RoundID:  fmt.Sprintf("%d", refund.SessionID),
		GameID:   s.cfg.GameID,
		GameCode: s.cfg.GameCode,
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
		Amount:   platform.FormatAmount(refund.RefundAmount),
		Reason:   refund.RefundReason,
		GameName: s.cfg.GameName,
	}

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &dto.CallLogCreateParams{
		CallType:   model.CallTypeCredit,
		BizOrderNo: refund.RefundOrderNo,
		ReqBody:    creditReq,
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", refund.RefundOrderNo, "error", callLogErr)
	}

	creditResult, err := s.platform.Credit(ctx, creditReq)
	if err != nil {
		if retryErr := s.refundAuditRepo.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, dto.RefundStatusProcessing, err.Error()); retryErr != nil {
			logger.Warn("update refund audit to pending for retry failed", "refund_id", refund.ID, "error", retryErr)
		}
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("platform refund failed: %w", err)
	}

	platformTransID := refund.RefundOrderNo

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: creditResult,
			Status:   model.CallLogStatusSuccess,
		})
	}

	// 跨表事务（refund_audit + bill_record），通过 dbRepo.WithTransaction 编排。
	return s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return tx.RefundAuditRepo().UpdateRefundSuccess(ctx, refund.ID, dto.RefundStatusProcessing, dto.BillStatusSuccess, platformTransID, time.Now())
	})
}

func (s *RefundService) RejectRefund(ctx context.Context, req *dto.RefundRejectRequest) error {
	refund, err := s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
	if err != nil {
		return fmt.Errorf("refund audit not found: %w", err)
	}

	if refund.Status != dto.RefundStatusPending {
		return fmt.Errorf("refund status is not pending")
	}

	lockKey := rediskeys.RefundLockKey(req.RefundOrderNo)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.RefundRejectLockTTL.Seconds()), func() error {
		// Re-check status after acquiring lock
		refund, err := s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
		if err != nil {
			return fmt.Errorf("refund audit not found: %w", err)
		}
		if refund.Status != dto.RefundStatusPending {
			return fmt.Errorf("refund status is not pending")
		}

		// 跨表事务（refund_audit + bill_record），通过 dbRepo.WithTransaction 编排。
		return s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
			return tx.RefundAuditRepo().RejectRefund(ctx, refund.ID, dto.RefundStatusPending, refund.BillID, dto.RefundStatusPending, dto.RefundStatusRejected, req.Remark)
		})
	})
}

func (s *RefundService) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error) {
	return s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, refundOrderNo)
}

func (s *RefundService) GetRefundsByStatus(ctx context.Context, status int, limit, offset int) ([]*model.RefundAudit, error) {
	return s.refundAuditRepo.GetRefundsByStatus(ctx, status, limit, offset)
}
