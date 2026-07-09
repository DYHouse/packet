package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
)

// RefundExecuteService 负责退款审批与执行。
// dbRepo 用于跨表事务方法（同时更新 refund_audit 与 bill_record）的事务编排。
// 审批含 platform.Credit RPC，采用 Processing 中间状态 + BizOrderNo 幂等兜底。
type RefundExecuteService struct {
	platform        platform.Client
	dbRepo          repository.DBRepository
	refundAuditRepo repository.RefundAuditRepository
	cfg             *config.PlatformConfig
	lockCfg         *config.LockConfig
	userIDConvert   *UserIDConvertService
	callMgr         repository.PlatformCallLogRepository
}

// NewRefundExecuteService 构造 RefundExecuteService 实例。
func NewRefundExecuteService(
	platformClient platform.Client,
	dbRepo repository.DBRepository,
	refundAuditRepo repository.RefundAuditRepository,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	userIDConvert *UserIDConvertService,
	callMgr repository.PlatformCallLogRepository,
) *RefundExecuteService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}
	return &RefundExecuteService{
		platform:        platformClient,
		dbRepo:          dbRepo,
		refundAuditRepo: refundAuditRepo,
		cfg:             cfg,
		lockCfg:         lockCfg,
		userIDConvert:   userIDConvert,
		callMgr:         callMgr,
	}
}

// ApproveRefund 审批退款单，状态置为 Approved 后立即执行退款。
func (s *RefundExecuteService) ApproveRefund(ctx context.Context, req *dto.RefundApproveRequest) error {
	refund, err := s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
	if err != nil {
		return fmt.Errorf("refund audit not found: %w", err)
	}

	if refund.Status != domain.RefundStatusPending {
		return fmt.Errorf("refund status is not pending")
	}

	lockKey := rediskeys.RefundLockKey(req.RefundOrderNo)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.RefundApproveLockTTL.Seconds()), func() error {
		// 锁内二次检查状态，防止并发重复退款
		refund, err := s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
		if err != nil {
			return fmt.Errorf("refund audit not found: %w", err)
		}
		if refund.Status != domain.RefundStatusPending {
			return nil
		}

		now := time.Now()
		// 守卫：校验 Pending → Approved 状态转换合法性
		if err := refund.TransitionTo(domain.RefundStatusApproved); err != nil {
			return fmt.Errorf("invalid refund audit status transition: %w", err)
		}
		if err := s.refundAuditRepo.UpdateRefundAuditStatus(ctx, refund.ID, domain.RefundStatusApproved,
			now, req.ApprovedBy, req.Remark); err != nil {
			return err
		}

		return s.executeRefund(ctx, refund)
	})
}

func (s *RefundExecuteService) executeRefund(ctx context.Context, refund *domain.RefundAudit) error {
	// 1. 幂等跳过：已退款的不再重复处理
	if refund.Status == domain.RefundStatusRefunded {
		return nil
	}

	// 2. 重试场景：退款单已处于 Processing，先前 RPC 可能已成功但 DB 更新失败。
	//    平台暂未提供查询接口，当前依靠 BizOrderNo 幂等兜底，fall through 重试 RPC。

	// 3. 将退款单置为 Processing（乐观锁 WHERE status = Approved）
	if err := s.refundAuditRepo.UpdateRefundAuditToProcessing(ctx, refund.ID, domain.RefundStatusApproved); err != nil {
		return fmt.Errorf("update refund to processing failed: %w", err)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, refund.UserID)
	if err != nil {
		if retryErr := s.refundAuditRepo.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, domain.RefundStatusProcessing, err.Error()); retryErr != nil {
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
		TraceID:        resolveTraceID(ctx),
		CallType:       domain.CallTypeCredit,
		BizOrderNo:     refund.RefundOrderNo,
		UserID:         refund.UserID,
		PlatformUserID: platformUserID,
		SessionID:      refund.SessionID,
		RoundID:        0,
		Amount:         refund.RefundAmount,
		Currency:       s.cfg.Currency,
		ReqBody:        creditReq,
		NodeID:         resolveNodeID(),
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", refund.RefundOrderNo, "error", callLogErr)
	}

	creditResult, err := s.platform.Credit(ctx, creditReq)
	if err != nil {
		if retryErr := s.refundAuditRepo.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, domain.RefundStatusProcessing, err.Error()); retryErr != nil {
			logger.Warn("update refund audit to pending for retry failed", "refund_id", refund.ID, "error", retryErr)
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
		return fmt.Errorf("platform refund failed: %w", err)
	}

	platformTransID := refund.RefundOrderNo

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
			ID:          callLog.ID,
			RespBody:    creditResult,
			Status:      domain.CallLogStatusSuccess,
			RequestTime: callLog.RequestTime,
		})
	}

	// 跨表事务（refund_audit + bill_record），通过 dbRepo.WithTransaction 编排。
	return s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return tx.RefundAuditRepo().UpdateRefundSuccess(ctx, refund.ID, domain.RefundStatusProcessing, domain.BillStatusSuccess, platformTransID, time.Now())
	})
}
