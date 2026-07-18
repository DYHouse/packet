package service

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	smodel "github.com/cashparty/backend/settlement/model"
)

// PenaltySettlementService 负责罚款相关结算操作：从用户扣款上交平台、将平台罚款分配给指定接收方。
// 从原 SettlementService 拆分而来（P0-10）。
// dbRepo 用于含 RPC 用例中对 DB 写入片段编排事务（短事务原则：禁止事务内 RPC）。
type PenaltySettlementService struct {
	platform       platform.Client
	dbRepo         repository.DBRepository
	billRepo       repository.BillRepository
	traceIDGen     *TraceIDGenerator
	cfg            *config.PlatformConfig
	lockCfg        *config.LockConfig
	userIDConvert  *UserIDConvertService
	callMgr        repository.PlatformCallLogRepository
	robotChecker   RobotChecker
	virtualBalance domain.VirtualBalanceService
	exceptionMgr   repository.ExceptionRepository
}

// NewPenaltySettlementService 构造 PenaltySettlementService 实例。
func NewPenaltySettlementService(
	platformClient platform.Client,
	dbRepo repository.DBRepository,
	billRepo repository.BillRepository,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	userIDConvert *UserIDConvertService,
	callMgr repository.PlatformCallLogRepository,
	robotChecker RobotChecker,
	virtualBalance domain.VirtualBalanceService,
	exceptionMgr repository.ExceptionRepository,
) *PenaltySettlementService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}

	return &PenaltySettlementService{
		platform:       platformClient,
		dbRepo:         dbRepo,
		billRepo:       billRepo,
		traceIDGen:     traceIDGen,
		cfg:            cfg,
		lockCfg:        lockCfg,
		userIDConvert:  userIDConvert,
		callMgr:        callMgr,
		robotChecker:   robotChecker,
		virtualBalance: virtualBalance,
		exceptionMgr:   exceptionMgr,
	}
}

func (s *PenaltySettlementService) DeductPenaltyToPlatform(ctx context.Context, req *dto.PenaltyDeductRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDeductTraceID(req.RoomID, req.SessionID, req.UserID, int64(req.RoundNo))
	// 分布式锁：按 userID + roundTraceID 粒度加锁，防止并发/重试导致重复扣款。
	// WithRedisLock 底层基于 redsync（随机 token + Lua 脚本释放），避免 TTL 过期后误删他人锁。
	lockKey := rediskeys.PenaltyDeductLockKey(req.UserID, roundTraceID)

	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.PenaltyDeductLockTTL.Seconds()), func() error {
		// 幂等检查：若已存在同 traceID + BillType + userID 的非 Failed 状态 bill，直接返回 nil。
		//   - Success：已扣款成功，跳过
		//   - Processing：扣款进行中，由重试流程完成，跳过避免重复创建 bill
		//   - Pending：扣款已排队，将由后续流程处理，跳过
		//   - Failed：允许重新创建 bill 并重试扣款
		// 注意：仅 Failed 状态才放行；其他非 Failed 状态都视为「已完成或进行中」从而跳过，
		// 避免重试时重复创建 bill 导致重复扣款（依赖 DB 唯一索引兜底仍会留下冗余记录）。
		existingBill, err := s.billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, domain.BillTypePenaltyIncome, req.UserID)
		if err == nil && existingBill != nil && existingBill.Status != domain.BillStatusFailed {
			logger.Info("penalty bill already exists, skipping",
				"round_trace_id", roundTraceID,
				"bill_id", existingBill.ID,
				"status", existingBill.Status)
			return nil
		}

		playerBill := &domain.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, domain.BillTypePenaltyIncome, req.UserID),
			BillType:     domain.BillTypePenaltyIncome,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      req.RoundID,
			RoundNo:      req.RoundNo,
			UserID:       req.UserID,
			Amount:       -req.Amount,
			Status:       domain.BillStatusProcessing,
			Remark:       fmt.Sprintf("惩罚扣款,类型:%s,回合:%d", req.PenaltyType, req.RoundNo),
			PenaltyType:  req.PenaltyType,
		}
		if s.robotChecker == nil {
			return fmt.Errorf("robot checker is nil")
		}
		isRobot, err := s.robotChecker.IsRobot(ctx, req.UserID)
		if err != nil {
			return fmt.Errorf("check robot failed: %w", err)
		}
		playerBill.IsRobot = isRobot

		platformBill := &domain.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, domain.BillTypePenaltyIncome, dto.PlatformAccountID),
			BillType:     domain.BillTypePenaltyIncome,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      req.RoundID,
			RoundNo:      req.RoundNo,
			UserID:       dto.PlatformAccountID,
			Amount:       req.Amount,
			Status:       domain.BillStatusSuccess,
			Remark:       fmt.Sprintf("惩罚收入,来自用户:%d,类型:%s", req.UserID, req.PenaltyType),
			PenaltyType:  req.PenaltyType,
		}

		// 同一事务创建两个 Bill，保证账目配对（含 RPC，事务仅包裹 DB 写入片段）
		if err := s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
			return tx.BillRepo().CreateBillsPair(ctx, playerBill, platformBill)
		}); err != nil {
			return fmt.Errorf("create penalty bills failed: %w", err)
		}

		// 机器人虚拟通道：跳过 platform.Debit，直接走虚拟钱包扣款
		if isRobot {
			if err := s.virtualBalance.Deduct(ctx, req.UserID, req.Amount); err != nil {
				s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
				return fmt.Errorf("robot virtual deduct penalty failed: %w", err)
			}
			balanceAfter, _ := s.virtualBalance.GetBalance(ctx, req.UserID)
			if err := playerBill.TransitionTo(domain.BillStatusSuccess); err != nil {
				logger.Error("invalid bill status transition", "bill_id", playerBill.ID, "error", err)
				return err
			}
			return s.billRepo.UpdateBillSuccess(ctx, playerBill.ID, domain.BillStatusProcessing, 0, balanceAfter)
		}

		platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, req.UserID)
		if err != nil {
			s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
			return fmt.Errorf("get platform user id failed: %w", err)
		}

		debitReq := &platform.DebitRequest{
		BizID:    playerBill.BizOrderNo,
		RoundID:  fmt.Sprintf("%d", req.RoundID),
		GameID:   s.cfg.GameID,
		GameCode: s.cfg.GameCode,
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
		Amount:   platform.FormatAmount(req.Amount),
		Reason:   playerBill.Remark,
		GameName: s.cfg.GameName,
	}

	callLog, callLogErr := s.callMgr.CreateLog(ctx, &dto.CallLogCreateParams{
		TraceID:        resolveTraceID(ctx),
		CallType:       domain.CallTypeDebit,
		BizOrderNo:     playerBill.BizOrderNo,
		UserID:         playerBill.UserID,
		PlatformUserID: platformUserID,
		SessionID:      playerBill.SessionID,
		RoundID:        req.RoundID,
		Amount:         req.Amount,
		Currency:       s.cfg.Currency,
		ReqBody:        debitReq,
		NodeID:         resolveNodeID(),
	})
		if callLogErr != nil {
			logger.Warn("create call log failed", "biz_order_no", playerBill.BizOrderNo, "error", callLogErr)
		}

		result, err := s.platform.Debit(ctx, debitReq)
		if err != nil {
			s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
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
			return fmt.Errorf("debit penalty failed: %w", err)
		}

		balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
		if err != nil {
			// ParseAmount 失败：平台可能已实际扣款但响应余额无法解析。
			// 资金安全要求 fail-closed：标记账单为 Failed 并创建异常记录供人工对账，不得标记为 Success。
			logger.Error("parse balance amount failed after successful debit, mark bill as failed",
				"bill_id", playerBill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
			if updateErr := s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error()); updateErr != nil {
				logger.Error("update bill to failed after parse amount error", "bill_id", playerBill.ID, "error", updateErr)
			}
			if callLog != nil {
				s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
					ID:          callLog.ID,
					RespBody:    result,
					Status:      domain.CallLogStatusSuccess,
					RequestTime: callLog.RequestTime,
				})
			}
			detail := fmt.Sprintf("罚款扣款 ParseAmount 解析失败,平台可能已扣款但余额无法解析,需人工对账, user_id: %d, raw_amount: %s, error: %s", req.UserID, result.Data.Balance.Amount, err.Error())
			if excErr := s.createExceptionRecord(ctx, playerBill, domain.ExceptionTypeDebitFailed, detail); excErr != nil {
				logger.Error("create exception record for parse amount failure failed", "bill_id", playerBill.ID, "error", excErr)
			}
			return fmt.Errorf("parse amount failed for penalty debit, bill_id: %d, user_id: %d: %w", playerBill.ID, req.UserID, err)
		}
		if err := playerBill.TransitionTo(domain.BillStatusSuccess); err != nil {
			logger.Error("invalid bill status transition", "bill_id", playerBill.ID, "error", err)
			return err
		}
		if err := s.billRepo.UpdateBillSuccess(ctx, playerBill.ID, domain.BillStatusProcessing, 0, balanceAfter); err != nil {
			return err
		}

		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
				ID:          callLog.ID,
				RespBody:    result,
				Status:      domain.CallLogStatusSuccess,
				RequestTime: callLog.RequestTime,
			})
		}

		return nil
	})
}

// DeductSubstituteFee 扣除真人替补玩家的替补费。
// 每次补位事件独立扣款（以 roundNo 区分同一玩家多次补位），金额由 game 层通过
// commissionCfg.Calculate(meta.RoomFee) 计算后传入，作为平台佣金收入。
// 机器人替补由调用方在 game 层通过 robotChecker.IsRobot 短路判断后不调用本方法。
// 扣款流程参照 DeductPenaltyToPlatform（15 步标准扣款模式）。
func (s *PenaltySettlementService) DeductSubstituteFee(ctx context.Context, req *dto.SubstituteFeeDeductRequest) error {
	// 步骤 1：生成确定性 traceID（含 roundNo 维度，区分同一玩家多次补位）。
	roundTraceID := s.traceIDGen.GenerateSubstituteFeeTraceID(req.SessionID, req.UserID, req.RoundNo)
	// 步骤 2：分布式锁（按 userID + traceID 粒度，防止并发/重试导致重复扣款）。
	// WithRedisLock 底层基于 redsync（随机 token + Lua 脚本释放），避免 TTL 过期后误删他人锁。
	lockKey := rediskeys.SubstituteFeeDeductLockKey(req.UserID, roundTraceID)

	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.SubstituteFeeDeductLockTTL.Seconds()), func() error {
		// 步骤 3：幂等检查。若已存在同 traceID + BillType + userID 的非 Failed 状态 bill，直接返回 nil。
		//   - Success：已扣款成功，跳过
		//   - Processing：扣款进行中，跳过
		//   - Failed：允许重新创建 bill 并重试扣款
		existingBill, err := s.billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, domain.BillTypeSubstituteFee, req.UserID)
		if err == nil && existingBill != nil && existingBill.Status != domain.BillStatusFailed {
			logger.Info("substitute fee bill already exists, skipping",
				"round_trace_id", roundTraceID,
				"bill_id", existingBill.ID,
				"status", existingBill.Status)
			return nil
		}

		// 步骤 4：构建 playerBill（玩家侧扣款，amount 为负，status=Processing）。
		playerBill := &domain.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, domain.BillTypeSubstituteFee, req.UserID),
			BillType:     domain.BillTypeSubstituteFee,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      0,    // 替补费无特定 round 关联，但 RoundNo 记录补位发生时的轮次
			RoundNo:      req.RoundNo,
			UserID:       req.UserID,
			Amount:       -req.Amount,
			Status:       domain.BillStatusProcessing,
			Remark:       fmt.Sprintf("替补费扣款,会话:%d,用户:%d,轮次:%d", req.SessionID, req.UserID, req.RoundNo),
		}
		// 步骤 5：robotChecker 守卫（fail-closed：nil 或查询失败必须中止，不得 fallback）。
		if s.robotChecker == nil {
			return fmt.Errorf("robot checker is nil")
		}
		isRobot, err := s.robotChecker.IsRobot(ctx, req.UserID)
		if err != nil {
			return fmt.Errorf("check robot failed: %w", err)
		}
		playerBill.IsRobot = isRobot

		// 步骤 6：构建 platformBill（平台侧收入，amount 为正，status=Success，无 RPC）。
		platformBill := &domain.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, domain.BillTypeSubstituteFee, dto.PlatformAccountID),
			BillType:     domain.BillTypeSubstituteFee,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      0,
			RoundNo:      req.RoundNo,
			UserID:       dto.PlatformAccountID,
			Amount:       req.Amount,
			Status:       domain.BillStatusSuccess,
			Remark:       fmt.Sprintf("替补费收入,来自用户:%d,会话:%d,轮次:%d", req.UserID, req.SessionID, req.RoundNo),
		}

		// 步骤 7：短事务创建成对 bill（仅包裹 DB 写入片段，遵循 §5.4 短事务原则）。
		if err := s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
			return tx.BillRepo().CreateBillsPair(ctx, playerBill, platformBill)
		}); err != nil {
			return fmt.Errorf("create substitute fee bills failed: %w", err)
		}

		// 步骤 8：机器人虚拟通道（跳过 platform.Debit，直接走虚拟钱包扣款）。
		// 正常情况下 game 层已通过 robotChecker 短路，不会对机器人调用本方法；
		// 此处保留机器人分支作为防御性兜底，确保 service 层独立可用。
		if isRobot {
			if err := s.virtualBalance.Deduct(ctx, req.UserID, req.Amount); err != nil {
				s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
				return fmt.Errorf("robot virtual deduct substitute fee failed: %w", err)
			}
			balanceAfter, _ := s.virtualBalance.GetBalance(ctx, req.UserID)
			if err := playerBill.TransitionTo(domain.BillStatusSuccess); err != nil {
				logger.Error("invalid bill status transition", "bill_id", playerBill.ID, "error", err)
				return err
			}
			return s.billRepo.UpdateBillSuccess(ctx, playerBill.ID, domain.BillStatusProcessing, 0, balanceAfter)
		}

		// 步骤 9：真人分支 - 获取平台用户 ID。
		platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, req.UserID)
		if err != nil {
			s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
			return fmt.Errorf("get platform user id failed: %w", err)
		}

		// 步骤 10：构造 platform.DebitRequest（BizID 作为平台幂等键，RoundID 用 sessionID 占位）。
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

		// 步骤 11：创建平台调用日志（失败仅 Warn，不阻断主流程）。
		callLog, callLogErr := s.callMgr.CreateLog(ctx, &dto.CallLogCreateParams{
			TraceID:        resolveTraceID(ctx),
			CallType:       domain.CallTypeDebit,
			BizOrderNo:     playerBill.BizOrderNo,
			UserID:         playerBill.UserID,
			PlatformUserID: platformUserID,
			SessionID:      playerBill.SessionID,
			RoundID:        0,
			Amount:         req.Amount,
			Currency:       s.cfg.Currency,
			ReqBody:        debitReq,
			NodeID:         resolveNodeID(),
		})
		if callLogErr != nil {
			logger.Warn("create call log failed", "biz_order_no", playerBill.BizOrderNo, "error", callLogErr)
		}

		// 步骤 12：调用 platform.Debit RPC。
		result, err := s.platform.Debit(ctx, debitReq)
		if err != nil {
			s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error())
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
			return fmt.Errorf("debit substitute fee failed: %w", err)
		}

		// 步骤 13：解析余额（fail-closed：平台可能已扣款但响应无法解析，标记 Failed + 异常记录供人工对账）。
		balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
		if err != nil {
			logger.Error("parse balance amount failed after successful debit, mark bill as failed",
				"bill_id", playerBill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
			if updateErr := s.billRepo.UpdateBillStatus(ctx, playerBill.ID, domain.BillStatusProcessing, domain.BillStatusFailed, err.Error()); updateErr != nil {
				logger.Error("update bill to failed after parse amount error", "bill_id", playerBill.ID, "error", updateErr)
			}
			if callLog != nil {
				s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
					ID:          callLog.ID,
					RespBody:    result,
					Status:      domain.CallLogStatusSuccess,
					RequestTime: callLog.RequestTime,
				})
			}
			detail := fmt.Sprintf("替补费扣款 ParseAmount 解析失败,平台可能已扣款但余额无法解析,需人工对账, user_id: %d, raw_amount: %s, error: %s", req.UserID, result.Data.Balance.Amount, err.Error())
			if excErr := s.createExceptionRecord(ctx, playerBill, domain.ExceptionTypeDebitFailed, detail); excErr != nil {
				logger.Error("create exception record for parse amount failure failed", "bill_id", playerBill.ID, "error", excErr)
			}
			return fmt.Errorf("parse amount failed for substitute fee debit, bill_id: %d, user_id: %d: %w", playerBill.ID, req.UserID, err)
		}

		// 步骤 14：状态机转换 + 更新 bill 为成功。
		if err := playerBill.TransitionTo(domain.BillStatusSuccess); err != nil {
			logger.Error("invalid bill status transition", "bill_id", playerBill.ID, "error", err)
			return err
		}
		if err := s.billRepo.UpdateBillSuccess(ctx, playerBill.ID, domain.BillStatusProcessing, 0, balanceAfter); err != nil {
			return err
		}

		// 步骤 15：更新平台调用日志为成功。
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &dto.CallLogUpdateParams{
				ID:          callLog.ID,
				RespBody:    result,
				Status:      domain.CallLogStatusSuccess,
				RequestTime: callLog.RequestTime,
			})
		}

		return nil
	})
}

func (s *PenaltySettlementService) DistributePenaltyFromPlatform(ctx context.Context, tx repository.Transaction, req *dto.PenaltyDistributeRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDistTraceID(req.RoomID, req.SessionID, req.RoundNo)
	billRepo := tx.BillRepo()

	// 幂等检查：若已存在同 traceID + BillType + PlatformAccountID 的 Success 状态 bill，直接返回 nil。
	// DistributePenaltyFromPlatform 的所有 bill 都是 Success 状态（不涉及平台调用），所以一次成功即可跳过。
	existingBill, err := billRepo.GetBillByTraceTypeAndUser(ctx, roundTraceID, domain.BillTypePenaltyDistribute, dto.PlatformAccountID)
	if err == nil && existingBill != nil && existingBill.Status == domain.BillStatusSuccess {
		return nil
	}

	platformBill := &domain.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, domain.BillTypePenaltyDistribute, dto.PlatformAccountID),
		BillType:     domain.BillTypePenaltyDistribute,
		RoomID:       req.RoomID,
		SessionID:    req.SessionID,
		RoundID:      req.RoundID,
		RoundNo:      req.RoundNo,
		UserID:       dto.PlatformAccountID,
		Amount:       -req.Amount,
		Status:       domain.BillStatusSuccess,
		Remark:       fmt.Sprintf("罚款分配,%s", req.Reason),
	}

	allBills := []*domain.BillRecord{platformBill}

	if len(req.Recipients) > 0 && req.Amount > 0 {
		recipientCount := int64(len(req.Recipients))
		shareAmount := req.Amount / recipientCount
		remainder := req.Amount % recipientCount

		for i, recipientID := range req.Recipients {
			amount := shareAmount
			if int64(i) < remainder {
				amount++
			}

			shareBill := &domain.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(roundTraceID, domain.BillTypePenaltyDistribute, recipientID),
			BillType:     domain.BillTypePenaltyDistribute,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      req.RoundID,
			RoundNo:      req.RoundNo,
			UserID:       recipientID,
			Amount:       amount,
			Status:       domain.BillStatusSuccess,
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

	if err := billRepo.CreateBills(ctx, allBills); err != nil {
		return err
	}

	// 持久化罚款分发记录
	shareAmount := int64(0)
	if len(req.Recipients) > 0 && req.Amount > 0 {
		shareAmount = req.Amount / int64(len(req.Recipients))
	}
	distribution := &smodel.PenaltyDistribution{
		RoomID:         req.RoomID,
		SessionID:      req.SessionID,
		RoundID:        req.RoundID,
		RoundNo:        req.RoundNo,
		TriggerType:    req.Reason,
		TotalAmount:    req.Amount,
		ShareAmount:    shareAmount,
		RecipientCount: len(req.Recipients),
		PlatformBillID: platformBill.ID,
	}
	if err := tx.PenaltyDistributionRepo().Create(ctx, distribution); err != nil {
		logger.Warn("persist penalty distribution failed",
			"room_id", req.RoomID,
			"session_id", req.SessionID,
			"round_id", req.RoundID,
			"error", err)
		// 不阻塞主流程，bills 已创建
	} else if len(allBills) > 1 {
		// 创建接收方明细
		recipients := make([]*smodel.PenaltyDistributionRecipient, 0, len(allBills)-1)
		for i := 1; i < len(allBills); i++ {
			recipients = append(recipients, &smodel.PenaltyDistributionRecipient{
				DistributionID: distribution.ID,
				UserID:         allBills[i].UserID,
				Amount:         allBills[i].Amount,
				BillID:         allBills[i].ID,
			})
		}
		if err := tx.PenaltyDistributionRepo().CreateRecipients(ctx, recipients); err != nil {
			logger.Warn("persist penalty distribution recipients failed",
				"room_id", req.RoomID,
				"session_id", req.SessionID,
				"round_id", req.RoundID,
				"error", err)
		}
	}

	return nil
}

// createExceptionRecord 创建异常记录并关联到账单，供人工对账。
func (s *PenaltySettlementService) createExceptionRecord(ctx context.Context, bill *domain.BillRecord, exceptionType domain.ExceptionType, detail string) error {
	exception := &domain.ExceptionRecord{
		ExceptionNo:     s.traceIDGen.GenerateExceptionNo(bill.ID, strconv.Itoa(int(exceptionType))),
		ExceptionType:   exceptionType,
		BillID:          bill.ID,
		RoundTraceID:    bill.RoundTraceID,
		RoundID:         bill.RoundID,
		BillType:        bill.BillType,
		UserID:          bill.UserID,
		Amount:          bill.Amount,
		Status:          domain.ExceptionStatusPending,
		ExceptionDetail: detail,
	}

	if err := s.exceptionMgr.Create(ctx, exception); err != nil {
		return err
	}

	return s.billRepo.UpdateBillExceptionID(ctx, bill.ID, exception.ID)
}
