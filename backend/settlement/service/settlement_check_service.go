package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
)

type SettlementCheckService struct {
	billRepo            repository.BillRepository
	roundSettlementRepo repository.RoundSettlementRepository
	exceptionMgr        repository.ExceptionRepository
	refundApplySvc      *RefundApplyService
	traceIDGen          *TraceIDGenerator
	limit               int
}

func NewSettlementCheckService(
	billRepo repository.BillRepository,
	roundSettlementRepo repository.RoundSettlementRepository,
	exceptionMgr repository.ExceptionRepository,
	refundApplySvc *RefundApplyService,
	traceIDGen *TraceIDGenerator,
	limit int,
) *SettlementCheckService {
	if limit <= 0 {
		limit = 100
	}
	return &SettlementCheckService{
		billRepo:            billRepo,
		roundSettlementRepo: roundSettlementRepo,
		exceptionMgr:        exceptionMgr,
		refundApplySvc:      refundApplySvc,
		traceIDGen:          traceIDGen,
		limit:               limit,
	}
}

// CheckFirstRoundDeductFailure 检查首回合扣款失败的回合并触发退款。
// limit 覆盖 s.limit，传入 <= 0 时回退到构造时注入的 s.limit（默认 100，与原硬编码一致）。
func (s *SettlementCheckService) CheckFirstRoundDeductFailure(ctx context.Context, since time.Time, limit int) error {
	if limit <= 0 {
		limit = s.limit
	}
	settlements, err := s.roundSettlementRepo.GetFailedFirstRoundSettlements(ctx, since, limit)
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
// limit 覆盖 s.limit，传入 <= 0 时回退到构造时注入的 s.limit（默认 100，与原硬编码一致）。
func (s *SettlementCheckService) CheckDeductedButNotSettled(ctx context.Context, since time.Time, limit int) error {
	if limit <= 0 {
		limit = s.limit
	}
	settlements, err := s.roundSettlementRepo.GetDeductedButNotSettled(ctx, since, limit)
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

func (s *SettlementCheckService) handleDeductedNotSettled(ctx context.Context, settlement *domain.RoundSettlement) error {
	exception := &domain.ExceptionRecord{
		ExceptionNo:     s.traceIDGen.GenerateExceptionNo(settlement.RoundID, strconv.Itoa(int(domain.ExceptionTypeDeductedNotSettled))),
		ExceptionType:   domain.ExceptionTypeDeductedNotSettled,
		RoundTraceID:    settlement.RoundTraceID,
		RoundID:         settlement.RoundID,
		BillType:        0,
		UserID:          0,
		Amount:          0,
		Status:          domain.ExceptionStatusPending,
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

func (s *SettlementCheckService) ensureRefundCreated(ctx context.Context, settlement *domain.RoundSettlement) error {
	bills, err := s.billRepo.GetBillsByTraceID(ctx, settlement.RoundTraceID)
	if err != nil {
		return err
	}

	for _, bill := range bills {
		if bill.Status == domain.BillStatusSuccess && bill.RefundStatus == domain.RefundStatusNone {
			refundAmount := bill.Amount
			if refundAmount < 0 {
				refundAmount = -refundAmount
			}

			refundReq := &dto.RefundApplyRequest{
				BillID:       bill.ID,
				RefundAmount: refundAmount,
				RefundReason: "首回合扣款失败，自动退款",
				RefundType:   domain.RefundTypeFirstRoundFail,
			}
			if _, err := s.refundApplySvc.ApplyForRefund(ctx, refundReq); err != nil {
				logger.Error("apply refund failed", "bill_id", bill.ID, "error", err)
			}
		}
	}

	return nil
}
