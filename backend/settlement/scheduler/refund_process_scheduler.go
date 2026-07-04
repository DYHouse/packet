package scheduler

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

type RefundProcessScheduler struct {
	base      *BaseScheduler
	refundSvc *service.RefundService
	billMgr   *service.BillManager
}

func NewRefundProcessScheduler(ctx context.Context, refundSvc *service.RefundService, billMgr *service.BillManager, redis *cRedis.Client) *RefundProcessScheduler {
	config := SchedulerConfig{
		Name:         "refund_process",
		Interval:     time.Minute,
		InitialDelay: 30 * time.Second,
		LockKey:      rediskeys.KeySchedulerRefundProcessLock,
		LockTTL:      120,
	}

	return &RefundProcessScheduler{
		base:      NewBaseScheduler(ctx, config, nil, redis),
		refundSvc: refundSvc,
		billMgr:   billMgr,
	}
}

func (s *RefundProcessScheduler) Start() {
	s.base.task = s.execute
	s.base.Start()
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
