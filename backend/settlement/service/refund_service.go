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

type RefundService struct {
	platform      platform.Client
	billMgr       *BillManager
	redis         *cRedis.Client
	traceIDGen    *TraceIDGenerator
	cfg           *config.PlatformConfig
	lockCfg       *config.LockConfig
	userIDConvert *UserIDConvertService
	callMgr       *PlatformCallManager
}

func NewRefundService(
	platformClient platform.Client,
	billMgr *BillManager,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
) *RefundService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	return &RefundService{
		platform:      platformClient,
		billMgr:       billMgr,
		redis:         redis,
		traceIDGen:    traceIDGen,
		cfg:           cfg,
		lockCfg:       lockCfg,
		userIDConvert: userIDConvert,
		callMgr:       callMgr,
	}
}

func (s *RefundService) ApplyForRefund(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	lockKey := redis.RefundApplyLockKey(req.BillID)
	var refundOrderNo string
	err := lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.RefundApplyLockTTL.Seconds()), func() error {
		var applyErr error
		refundOrderNo, applyErr = s.applyForRefundLocked(ctx, req)
		return applyErr
	})
	return refundOrderNo, err
}

func (s *RefundService) applyForRefundLocked(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	bill, err := s.billMgr.GetBillByID(ctx, req.BillID)
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
		existingRefund, err := s.billMgr.GetRefundAuditByBillID(ctx, bill.ID)
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

	if err := s.billMgr.CreateRefundAuditAndUpdateBillRefundStatus(ctx, refundAudit, bill.ID, dto.RefundStatusNone, dto.RefundStatusPending, refundOrderNo); err != nil {
		return "", err
	}

	return refundOrderNo, nil
}

func (s *RefundService) ApproveRefund(ctx context.Context, req *dto.RefundApproveRequest) error {
	refund, err := s.billMgr.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
	if err != nil {
		return fmt.Errorf("refund audit not found: %w", err)
	}

	if refund.Status != dto.RefundStatusPending {
		return fmt.Errorf("refund status is not pending")
	}

	lockKey := redis.RefundLockKey(req.RefundOrderNo)
	return lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.RefundApproveLockTTL.Seconds()), func() error {
		// 锁内二次检查状态，防止并发重复退款
		refund, err := s.billMgr.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
		if err != nil {
			return fmt.Errorf("refund audit not found: %w", err)
		}
		if refund.Status != dto.RefundStatusPending {
			return nil
		}

		now := time.Now()
		if err := s.billMgr.UpdateRefundAuditStatus(ctx, refund.ID, dto.RefundStatusApproved,
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

	// 2. 重试场景：退款单已处于 Processing，应先查询平台状态
	if refund.Status == dto.RefundStatusProcessing {
		// TODO: 待平台提供 QueryStatus 接口后，先查询平台退款状态以避免重复退款
		// 当前先 fall through 重试 RPC（平台应按 BizOrderNo 保证幂等）
	}

	// 3. 将退款单置为 Processing（乐观锁 WHERE status = Approved）
	if err := s.billMgr.UpdateRefundAuditToProcessing(ctx, refund.ID, dto.RefundStatusApproved); err != nil {
		return fmt.Errorf("update refund to processing failed: %w", err)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, refund.UserID)
	if err != nil {
		if retryErr := s.billMgr.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, dto.RefundStatusProcessing, err.Error()); retryErr != nil {
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

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeCredit,
		BizOrderNo: refund.RefundOrderNo,
		ReqBody:    creditReq,
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", refund.RefundOrderNo, "error", callLogErr)
	}

	creditResult, err := s.platform.Credit(ctx, creditReq)
	if err != nil {
		if retryErr := s.billMgr.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, dto.RefundStatusProcessing, err.Error()); retryErr != nil {
			logger.Warn("update refund audit to pending for retry failed", "refund_id", refund.ID, "error", retryErr)
		}
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("platform refund failed: %w", err)
	}

	platformTransID := refund.RefundOrderNo

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: creditResult,
			Status:   model.CallLogStatusSuccess,
		})
	}

	return s.billMgr.UpdateRefundSuccessInTransaction(ctx, refund.ID, dto.RefundStatusProcessing, dto.BillStatusSuccess, platformTransID, time.Now())
}

func (s *RefundService) RejectRefund(ctx context.Context, req *dto.RefundRejectRequest) error {
	refund, err := s.billMgr.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
	if err != nil {
		return fmt.Errorf("refund audit not found: %w", err)
	}

	if refund.Status != dto.RefundStatusPending {
		return fmt.Errorf("refund status is not pending")
	}

	lockKey := redis.RefundLockKey(req.RefundOrderNo)
	return lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.RefundRejectLockTTL.Seconds()), func() error {
		// Re-check status after acquiring lock
		refund, err := s.billMgr.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
		if err != nil {
			return fmt.Errorf("refund audit not found: %w", err)
		}
		if refund.Status != dto.RefundStatusPending {
			return fmt.Errorf("refund status is not pending")
		}

		return s.billMgr.RejectRefund(ctx, refund.ID, dto.RefundStatusPending, refund.BillID, dto.RefundStatusPending, dto.RefundStatusRejected, req.Remark)
	})
}

func (s *RefundService) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error) {
	return s.billMgr.GetRefundAuditByOrderNo(ctx, refundOrderNo)
}

func (s *RefundService) GetRefundsByStatus(ctx context.Context, status int, limit, offset int) ([]*model.RefundAudit, error) {
	return s.billMgr.GetRefundsByStatus(ctx, status, limit, offset)
}
