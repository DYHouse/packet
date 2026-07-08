package service

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

// PenaltySettlementService 负责罚款相关结算操作：从用户扣款上交平台、将平台罚款分配给指定接收方。
// 从原 SettlementService 拆分而来（P0-10）。
// dbRepo 用于含 RPC 用例中对 DB 写入片段编排事务（短事务原则：禁止事务内 RPC）。
type PenaltySettlementService struct {
	platform       platform.Client
	dbRepo         domain.DBRepository
	billRepo       domain.BillRepository
	traceIDGen     *TraceIDGenerator
	cfg            *config.PlatformConfig
	userIDConvert  *UserIDConvertService
	callMgr        *PlatformCallManager
	robotChecker   RobotChecker
	virtualBalance domain.VirtualBalanceService
	exceptionMgr   *ExceptionManager
}

// NewPenaltySettlementService 构造 PenaltySettlementService 实例。
func NewPenaltySettlementService(
	platformClient platform.Client,
	dbRepo domain.DBRepository,
	billRepo domain.BillRepository,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
	robotChecker RobotChecker,
	virtualBalance domain.VirtualBalanceService,
	exceptionMgr *ExceptionManager,
) *PenaltySettlementService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}

	return &PenaltySettlementService{
		platform:       platformClient,
		dbRepo:         dbRepo,
		billRepo:       billRepo,
		traceIDGen:     traceIDGen,
		cfg:            cfg,
		userIDConvert:  userIDConvert,
		callMgr:        callMgr,
		robotChecker:   robotChecker,
		virtualBalance: virtualBalance,
		exceptionMgr:   exceptionMgr,
	}
}

func (s *PenaltySettlementService) DeductPenaltyToPlatform(ctx context.Context, req *dto.PenaltyDeductRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDeductTraceID(req.RoomID, req.SessionID)

	// 幂等检查：若已存在同 traceID + BillType + userID 的非 Failed 状态 bill，直接返回 nil。
	//   - Success：已扣款成功，跳过
	//   - Processing：扣款进行中，由重试流程完成，跳过避免重复创建 bill
	//   - Pending：扣款已排队，将由后续流程处理，跳过
	//   - Failed：允许重新创建 bill 并重试扣款
	// 注意：仅 Failed 状态才放行；其他非 Failed 状态都视为「已完成或进行中」从而跳过，
	// 避免重试时重复创建 bill 导致重复扣款（依赖 DB 唯一索引兜底仍会留下冗余记录）。
	existingBill, err := s.billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, dto.BillTypePenaltyIncome, req.UserID)
	if err == nil && existingBill != nil && existingBill.Status != dto.BillStatusFailed {
		logger.Info("penalty bill already exists, skipping",
			"round_trace_id", roundTraceID,
			"bill_id", existingBill.ID,
			"status", existingBill.Status)
		return nil
	}

	playerBill := &model.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, dto.BillTypePenaltyIncome, req.UserID),
		BillType:     dto.BillTypePenaltyIncome,
		RoomID:       req.RoomID,
		SessionID:    req.SessionID,
		RoundNo:      req.RoundNo,
		UserID:       req.UserID,
		Amount:       -req.Amount,
		Status:       dto.BillStatusProcessing,
		Remark:       fmt.Sprintf("惩罚扣款,类型:%s,回合:%d", req.PenaltyType, req.RoundNo),
	}
	if s.robotChecker == nil {
		return fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, req.UserID)
	if err != nil {
		return fmt.Errorf("check robot failed: %w", err)
	}
	playerBill.IsRobot = isRobot

	platformBill := &model.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, dto.BillTypePenaltyIncome, dto.PlatformAccountID),
		BillType:     dto.BillTypePenaltyIncome,
		RoomID:       req.RoomID,
		SessionID:    req.SessionID,
		RoundNo:      req.RoundNo,
		UserID:       dto.PlatformAccountID,
		Amount:       req.Amount,
		Status:       dto.BillStatusSuccess,
		Remark:       fmt.Sprintf("惩罚收入,来自用户:%d,类型:%s", req.UserID, req.PenaltyType),
	}

	// 同一事务创建两个 Bill，保证账目配对（含 RPC，事务仅包裹 DB 写入片段）
	if err := s.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
		return tx.BillRepo().CreateBillsPair(ctx, playerBill, platformBill)
	}); err != nil {
		return fmt.Errorf("create penalty bills failed: %w", err)
	}

	// 机器人虚拟通道：跳过 platform.Debit，直接走虚拟钱包扣款
	if isRobot {
		if err := s.virtualBalance.Deduct(ctx, req.UserID, req.Amount); err != nil {
			s.billRepo.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
			return fmt.Errorf("robot virtual deduct penalty failed: %w", err)
		}
		balanceAfter, _ := s.virtualBalance.GetBalance(ctx, req.UserID)
		return s.billRepo.UpdateBillSuccess(ctx, playerBill.ID, dto.BillStatusProcessing, 0, balanceAfter)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, req.UserID)
	if err != nil {
		s.billRepo.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	debitReq := &platform.DebitRequest{
		BizID:    playerBill.BizOrderNo,
		RoundID:  fmt.Sprintf("%d", req.SessionID),
		GameID:   s.cfg.GameID,
		GameCode: s.cfg.GameCode,
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
		Amount:   platform.FormatAmount(req.Amount),
		Reason:   playerBill.Remark,
		GameName: s.cfg.GameName,
	}

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeDebit,
		BizOrderNo: playerBill.BizOrderNo,
		ReqBody:    debitReq,
	})
	if callLogErr != nil {
		logger.Warn("create call log failed", "biz_order_no", playerBill.BizOrderNo, "error", callLogErr)
	}

	result, err := s.platform.Debit(ctx, debitReq)
	if err != nil {
		s.billRepo.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("debit penalty failed: %w", err)
	}

	balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
	if err != nil {
		// ParseAmount 失败：平台可能已实际扣款但响应余额无法解析。
		// 资金安全要求 fail-closed：标记账单为 Failed 并创建异常记录供人工对账，不得标记为 Success。
		logger.Error("parse balance amount failed after successful debit, mark bill as failed",
			"bill_id", playerBill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
		if updateErr := s.billRepo.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error()); updateErr != nil {
			logger.Error("update bill to failed after parse amount error", "bill_id", playerBill.ID, "error", updateErr)
		}
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:       callLog.ID,
				RespBody: result,
				Status:   model.CallLogStatusSuccess,
			})
		}
		detail := fmt.Sprintf("罚款扣款 ParseAmount 解析失败,平台可能已扣款但余额无法解析,需人工对账, user_id: %d, raw_amount: %s, error: %s", req.UserID, result.Data.Balance.Amount, err.Error())
		if excErr := s.createExceptionRecord(ctx, playerBill, model.ExceptionTypeDebitFailed, detail); excErr != nil {
			logger.Error("create exception record for parse amount failure failed", "bill_id", playerBill.ID, "error", excErr)
		}
		return fmt.Errorf("parse amount failed for penalty debit, bill_id: %d, user_id: %d: %w", playerBill.ID, req.UserID, err)
	}
	if err := s.billRepo.UpdateBillSuccess(ctx, playerBill.ID, dto.BillStatusProcessing, 0, balanceAfter); err != nil {
		return err
	}

	if callLog != nil {
		s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
			ID:       callLog.ID,
			RespBody: result,
			Status:   model.CallLogStatusSuccess,
		})
	}

	return nil
}

func (s *PenaltySettlementService) DistributePenaltyFromPlatform(ctx context.Context, tx domain.Transaction, req *dto.PenaltyDistributeRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDistTraceID(req.RoomID, req.SessionID)
	billRepo := tx.BillRepo()

	// 幂等检查：若已存在同 traceID + BillType + PlatformAccountID 的 Success 状态 bill，直接返回 nil。
	// DistributePenaltyFromPlatform 的所有 bill 都是 Success 状态（不涉及平台调用），所以一次成功即可跳过。
	existingBill, err := billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, dto.BillTypePenaltyDistribute, dto.PlatformAccountID)
	if err == nil && existingBill != nil && existingBill.Status == dto.BillStatusSuccess {
		return nil
	}

	platformBill := &model.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, dto.BillTypePenaltyDistribute, dto.PlatformAccountID),
		BillType:     dto.BillTypePenaltyDistribute,
		RoomID:       req.RoomID,
		SessionID:    req.SessionID,
		RoundID:      req.RoundID,
		UserID:       dto.PlatformAccountID,
		Amount:       -req.Amount,
		Status:       dto.BillStatusSuccess,
		Remark:       fmt.Sprintf("罚款分配,%s", req.Reason),
	}

	allBills := []*model.BillRecord{platformBill}

	if len(req.Recipients) > 0 && req.Amount > 0 {
		recipientCount := int64(len(req.Recipients))
		shareAmount := req.Amount / recipientCount
		remainder := req.Amount % recipientCount

		for i, recipientID := range req.Recipients {
			amount := shareAmount
			if int64(i) < remainder {
				amount++
			}

			shareBill := &model.BillRecord{
				RoundTraceID: roundTraceID,
				BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, dto.BillTypePenaltyDistribute, recipientID),
				BillType:     dto.BillTypePenaltyDistribute,
				RoomID:       req.RoomID,
				SessionID:    req.SessionID,
				RoundID:      req.RoundID,
				UserID:       recipientID,
				Amount:       amount,
				Status:       dto.BillStatusSuccess,
				Remark:       fmt.Sprintf("罚款分红,总额:%d,原因:%s", req.Amount, req.Reason),
			}
			if s.robotChecker == nil {
				return fmt.Errorf("robot checker is nil")
			}
			isRobot, err := s.robotChecker.IsRobot(ctx, recipientID)
			if err != nil {
				return fmt.Errorf("check robot failed: %w", err)
			}
			shareBill.IsRobot = isRobot
			allBills = append(allBills, shareBill)
		}
	}

	return billRepo.CreateBills(ctx, allBills)
}

// createExceptionRecord 创建异常记录并关联到账单，供人工对账。
func (s *PenaltySettlementService) createExceptionRecord(ctx context.Context, bill *model.BillRecord, exceptionType model.ExceptionType, detail string) error {
	exception := &model.ExceptionRecord{
		ExceptionNo:     s.traceIDGen.GenerateExceptionNo(bill.ID, strconv.Itoa(int(exceptionType))),
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

	return s.billRepo.UpdateBillExceptionID(ctx, bill.ID, exception.ID)
}
