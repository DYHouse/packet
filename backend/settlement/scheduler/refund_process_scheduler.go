package scheduler

import (
	"context"

	commonconfig "github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	csched "github.com/cashparty/backend/common/scheduler"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

type RefundProcessScheduler struct {
	base      *csched.BaseScheduler
	refundSvc *service.RefundService
	billMgr   *service.BillManager
}

func NewRefundProcessScheduler(refundSvc *service.RefundService, billMgr *service.BillManager, redis *cRedis.Client, cfg commonconfig.SettlementSchedulerSubConfig) *RefundProcessScheduler {
	config := csched.BaseSchedulerConfig{
		Name:         "refund_process",
		Interval:     cfg.Interval,
		InitialDelay: cfg.InitialDelay,
		LockKey:      rediskeys.KeySchedulerRefundProcessLock,
		LockTTL:      cfg.LockTTL,
	}

	s := &RefundProcessScheduler{
		refundSvc: refundSvc,
		billMgr:   billMgr,
	}
	s.base = csched.NewBaseScheduler(config, s.execute, redis)
	return s
}

func (s *RefundProcessScheduler) Name() string { return s.base.Name() }

func (s *RefundProcessScheduler) Start(ctx context.Context) error {
	return s.base.Start(ctx)
}

func (s *RefundProcessScheduler) execute(ctx context.Context) error {
	refunds, err := s.billMgr.GetRefundsByStatus(ctx, dto.RefundStatusPending, 100, 0)
	if err != nil {
		logger.Error("get pending refunds failed", "error", err)
		return err
	}

	for _, refund := range refunds {
		if refund.RefundType == dto.RefundTypeFirstRoundFail {
			if err := s.refundSvc.ApproveRefund(ctx, &dto.RefundApproveRequest{
				RefundOrderNo: refund.RefundOrderNo,
				ApprovedBy:    0,
			}); err != nil {
				logger.Error("approve refund failed", "refund_order_no", refund.RefundOrderNo, "error", err)
			}
		}
	}

	return nil
}

func (s *RefundProcessScheduler) Stop() {
	s.base.Stop()
}
