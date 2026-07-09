package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
)

// RefundApplyService 负责退款申请。
// dbRepo 用于跨表事务方法（同时更新 refund_audit 与 bill_record）的事务编排。
type RefundApplyService struct {
	billRepo        repository.BillRepository
	dbRepo          repository.DBRepository
	refundAuditRepo repository.RefundAuditRepository
	traceIDGen      *TraceIDGenerator
	lockCfg         *config.LockConfig
}

// NewRefundApplyService 构造 RefundApplyService 实例。
func NewRefundApplyService(
	billRepo repository.BillRepository,
	dbRepo repository.DBRepository,
	refundAuditRepo repository.RefundAuditRepository,
	traceIDGen *TraceIDGenerator,
	lockCfg *config.LockConfig,
) *RefundApplyService {
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	return &RefundApplyService{
		billRepo:        billRepo,
		dbRepo:          dbRepo,
		refundAuditRepo: refundAuditRepo,
		traceIDGen:      traceIDGen,
		lockCfg:         lockCfg,
	}
}

// ApplyForRefund 申请退款，获取分布式锁后委托 applyForRefundLocked 执行。
func (s *RefundApplyService) ApplyForRefund(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	lockKey := rediskeys.RefundApplyLockKey(req.BillID)
	var refundOrderNo string
	err := lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.RefundApplyLockTTL.Seconds()), func() error {
		var applyErr error
		refundOrderNo, applyErr = s.applyForRefundLocked(ctx, req)
		return applyErr
	})
	return refundOrderNo, err
}

func (s *RefundApplyService) applyForRefundLocked(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	bill, err := s.billRepo.GetBillByID(ctx, req.BillID)
	if err != nil {
		return "", fmt.Errorf("bill not found: %w", err)
	}

	if bill.Status != domain.BillStatusSuccess {
		return "", fmt.Errorf("bill status is not success, cannot refund")
	}

	if bill.RefundStatus == domain.RefundStatusRefunded {
		return "", fmt.Errorf("bill already refunded")
	}

	if bill.RefundStatus == domain.RefundStatusPending {
		existingRefund, err := s.refundAuditRepo.GetRefundAuditByBillID(ctx, bill.ID)
		if err == nil && existingRefund != nil {
			return existingRefund.RefundOrderNo, nil
		}
	}

	refundOrderNo := s.traceIDGen.GenerateRefundOrderNo(bill.ID)
	refundAudit := &domain.RefundAudit{
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
		Status:        domain.RefundStatusPending,
		AppliedAt:     time.Now(),
		AppliedBy:     req.AppliedBy,
	}

	// 跨表事务（refund_audit + bill_record），通过 dbRepo.WithTransaction 编排。
	if err := s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return tx.RefundAuditRepo().CreateRefundAuditAndUpdateBillRefundStatus(ctx, refundAudit, bill.ID, domain.RefundStatusNone, domain.RefundStatusPending, refundOrderNo)
	}); err != nil {
		return "", err
	}

	return refundOrderNo, nil
}
