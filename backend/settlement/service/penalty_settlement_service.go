package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

// PenaltySettlementService 负责罚款相关结算操作：从用户扣款上交平台、将平台罚款分配给指定接收方。
// 从原 SettlementService 拆分而来（P0-10）。
type PenaltySettlementService struct {
	platform       platform.Client
	billRepo       domain.BillRepository
	traceIDGen     *TraceIDGenerator
	cfg            *config.PlatformConfig
	userIDConvert  *UserIDConvertService
	callMgr        *PlatformCallManager
	robotChecker   RobotChecker
	virtualBalance *VirtualBalanceService
}

// NewPenaltySettlementService 构造 PenaltySettlementService 实例。
func NewPenaltySettlementService(
	platformClient platform.Client,
	billRepo domain.BillRepository,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
	robotChecker RobotChecker,
	virtualBalance *VirtualBalanceService,
) *PenaltySettlementService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}

	return &PenaltySettlementService{
		platform:       platformClient,
		billRepo:       billRepo,
		traceIDGen:     traceIDGen,
		cfg:            cfg,
		userIDConvert:  userIDConvert,
		callMgr:        callMgr,
		robotChecker:   robotChecker,
		virtualBalance: virtualBalance,
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
	isRobot := s.robotChecker != nil && s.robotChecker.IsRobot(ctx, req.UserID)
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

	// 同一事务创建两个 Bill，保证账目配对
	if err := s.billRepo.CreateBillsPairInTransaction(ctx, playerBill, platformBill); err != nil {
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
		logger.Error("parse balance amount failed after successful debit, mark bill as success with balance=0",
			"bill_id", playerBill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
		balanceAfter = 0
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

func (s *PenaltySettlementService) DistributePenaltyFromPlatform(ctx context.Context, req *dto.PenaltyDistributeRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDistTraceID(req.RoomID, req.SessionID)

	// 幂等检查：若已存在同 traceID + BillType + PlatformAccountID 的 Success 状态 bill，直接返回 nil。
	// DistributePenaltyFromPlatform 的所有 bill 都是 Success 状态（不涉及平台调用），所以一次成功即可跳过。
	existingBill, err := s.billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, dto.BillTypePenaltyDistribute, dto.PlatformAccountID)
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
			shareBill.IsRobot = s.robotChecker != nil && s.robotChecker.IsRobot(ctx, recipientID)
			allBills = append(allBills, shareBill)
		}
	}

	return s.billRepo.CreateBillsInTransaction(ctx, allBills)
}
