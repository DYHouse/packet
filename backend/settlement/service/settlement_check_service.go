package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

type SettlementCheckService struct {
	billMgr      *BillManager
	exceptionMgr *ExceptionManager
	refundSvc    *RefundService
	traceIDGen   *TraceIDGenerator
}

func NewSettlementCheckService(
	billMgr *BillManager,
	exceptionMgr *ExceptionManager,
	refundSvc *RefundService,
	traceIDGen *TraceIDGenerator,
) *SettlementCheckService {
	return &SettlementCheckService{
		billMgr:      billMgr,
		exceptionMgr: exceptionMgr,
		refundSvc:    refundSvc,
		traceIDGen:   traceIDGen,
	}
}

func (s *SettlementCheckService) CheckFirstRoundDeductFailure(ctx context.Context) error {
	settlements, err := s.billMgr.GetFailedFirstRoundSettlements(ctx, time.Now().Add(-10*time.Minute), 100)
	if err != nil {
		return err
	}

	for _, settlement := range settlements {
		if err := s.ensureRefundCreated(ctx, settlement); err != nil {
			logger.Error("ensure refund created failed", "round_trace_id", settlement.RoundTraceID, "error", err)
		}
	}

	return nil
}

// CheckDeductedButNotSettled finds rounds that were deducted but never settled (SettleRound never called).
// In the new model where creditRound() always succeeds (internal bookkeeping), this scenario indicates
// the game result event was likely lost. Instead of auto-refunding (which could incorrectly refund
// when the game was actually played), we create exception records for manual investigation.
func (s *SettlementCheckService) CheckDeductedButNotSettled(ctx context.Context, since time.Time) error {
	settlements, err := s.billMgr.GetDeductedButNotSettled(ctx, since, 100)
	if err != nil {
		return err
	}

	for _, settlement := range settlements {
		if err := s.handleDeductedNotSettled(ctx, settlement); err != nil {
			logger.Error("handle deducted but not settled failed", "round_trace_id", settlement.RoundTraceID, "error", err)
		}
	}

	return nil
}

func (s *SettlementCheckService) handleDeductedNotSettled(ctx context.Context, settlement *model.RoundSettlement) error {
	exception := &model.ExceptionRecord{
		ExceptionNo:   s.traceIDGen.GenerateExceptionNo(),
		ExceptionType: model.ExceptionTypeDeductedNotSettled,
		RoundTraceID:  settlement.RoundTraceID,
		RoundID:       settlement.RoundID,
		BillType:      0,
		UserID:        0,
		Amount:        0,
		Status:        model.ExceptionStatusPending,
		ExceptionDetail: fmt.Sprintf("扣款成功但未结算，可能游戏结果事件丢失，round_trace_id: %s, round_id: %d", settlement.RoundTraceID, settlement.RoundID),
	}

	if err := s.exceptionMgr.Create(ctx, exception); err != nil {
		return fmt.Errorf("create deducted-not-settled exception failed: %w", err)
	}

	logger.Warn("deducted but not settled detected, exception created for manual review",
		"round_trace_id", settlement.RoundTraceID,
		"round_id", settlement.RoundID,
		"exception_id", exception.ID,
	)

	return nil
}

func (s *SettlementCheckService) ensureRefundCreated(ctx context.Context, settlement *model.RoundSettlement) error {
	bills, err := s.billMgr.GetBillsByTraceID(ctx, settlement.RoundTraceID)
	if err != nil {
		return err
	}

	for _, bill := range bills {
		if bill.Status == dto.BillStatusSuccess && bill.RefundStatus == dto.RefundStatusNone {
			refundAmount := bill.Amount
			if refundAmount < 0 {
				refundAmount = -refundAmount
			}

			refundReq := &dto.RefundApplyRequest{
				BillID:       bill.ID,
				RefundAmount: refundAmount,
				RefundReason: "首回合扣款失败，自动退款",
				RefundType:   dto.RefundTypeFirstRoundFail,
			}
			if _, err := s.refundSvc.ApplyForRefund(ctx, refundReq); err != nil {
				logger.Error("apply refund failed", "bill_id", bill.ID, "error", err)
			}
		}
	}

	return nil
}
