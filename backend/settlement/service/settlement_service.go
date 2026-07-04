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

type SettlementService struct {
	platform       platform.Client
	billMgr        *BillManager
	redis          *cRedis.Client
	traceIDGen     *TraceIDGenerator
	deductSvc      *DeductService
	rewardSettler  *RewardSettler
	cfg            *config.PlatformConfig
	lockCfg        *config.LockConfig
	userIDConvert  *UserIDConvertService
	gameSettleSvc  *GameSettleService
	callMgr        *PlatformCallManager
	robotChecker   RobotChecker
	virtualBalance *VirtualBalanceService
}

func NewSettlementService(
	platformClient platform.Client,
	billMgr *BillManager,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	lockCfg *config.LockConfig,
	deductSvc *DeductService,
	rewardSettler *RewardSettler,
	gameSettleSvc *GameSettleService,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
	robotChecker RobotChecker,
	virtualBalance *VirtualBalanceService,
) *SettlementService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}

	return &SettlementService{
		platform:       platformClient,
		billMgr:        billMgr,
		redis:          redis,
		traceIDGen:     traceIDGen,
		deductSvc:      deductSvc,
		rewardSettler:  rewardSettler,
		cfg:            cfg,
		lockCfg:        lockCfg,
		userIDConvert:  userIDConvert,
		gameSettleSvc:  gameSettleSvc,
		callMgr:        callMgr,
		robotChecker:   robotChecker,
		virtualBalance: virtualBalance,
	}
}

func (s *SettlementService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) error {
	settlement, err := s.billMgr.GetRoundSettlementByRoundID(ctx, req.RoundID)
	if err == nil && settlement != nil && settlement.Status == dto.RoundStatusCredited {
		return nil
	}

	lockKey := redis.SettleRoundLockKey(req.RoundID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.SettleRoundLockTTL.Seconds()), func() error {
		existingSettlement, err := s.billMgr.GetRoundSettlementByRoundID(ctx, req.RoundID)
		if err != nil {
			return fmt.Errorf("get round settlement failed: %w", err)
		}
		if existingSettlement == nil {
			return fmt.Errorf("round settlement not found for roundID: %d", req.RoundID)
		}

		if existingSettlement.Status == dto.RoundStatusCredited {
			return nil
		}

		if err := s.billMgr.UpdateRoundSettlementSettleInfo(ctx, existingSettlement.RoundTraceID,
			req.SenderID, req.SenderType, req.TotalAmount, req.Commission, len(req.Players), req.MinPlayerID); err != nil {
			return fmt.Errorf("update round settlement settle info failed: %w", err)
		}

		existingSettlement.TotalAmount = req.TotalAmount
		existingSettlement.Commission = req.Commission
		existingSettlement.SenderID = req.SenderID
		existingSettlement.SenderType = req.SenderType
		existingSettlement.MinPlayerID = req.MinPlayerID
		existingSettlement.RewardType = req.RewardType
		existingSettlement.RewardAmount = req.RewardAmount

		// creditRound 仅写入 grab/commission BillRecord，不在此处标记 round_settlement.status = Credited。
		// status 由 SettleRound 在 credit + reward 全部成功后统一标记，避免 reward 失败但 round 被标 Credited
		// 导致重试时进入 L83-L84 早返回分支、reward 永远无法补偿。
		totalSettleAmount, settleUserCount, err := s.creditRound(ctx, existingSettlement, req.Players)
		if err != nil {
			return err
		}

		if req.RewardType > 0 && req.RewardAmount > 0 {
			if err := s.rewardSettler.SettleReward(ctx, existingSettlement, req.Players); err != nil {
				// 上抛 err 触发 caller (game_event_consumer) 事务回滚，并保证 Kafka 重试时
				// round_settlement.status 仍非 Credited，SettleReward 能被重新调用。
				return fmt.Errorf("settle system reward failed: %w", err)
			}
		}

		// 所有子结算成功后才标记 round_settlement.status = Credited
		now := time.Now()
		if err := s.billMgr.UpdateRoundSettlementCredited(ctx, existingSettlement.RoundTraceID, totalSettleAmount, settleUserCount, &now); err != nil {
			return fmt.Errorf("update round settlement credited failed: %w", err)
		}

		return nil
	})
}

// creditRound handles round-level internal bookkeeping: creates BillRecord entries with Success status
// without calling platform.Credit. Actual fund movement happens at session level via net settlement.
//
// 返回 totalSettleAmount/settleUserCount 供 SettleRound 在所有子结算成功后统一标记 round_settlement.status。
// 不再在此处调用 UpdateRoundSettlementCredited，避免 reward 失败后 status 被提前置位导致重试无法补偿。
func (s *SettlementService) creditRound(ctx context.Context, settlement *model.RoundSettlement, players []*dto.PlayerSettleInfo) (int64, int, error) {
	if settlement.Commission > 0 {
		if err := s.settleCommission(ctx, settlement); err != nil {
			logger.Error("settle commission failed", "round_id", settlement.RoundID, "error", err)
		}
	}

	bills := make([]*model.BillRecord, 0)
	var totalSettleAmount int64
	var settleUserCount int

	for _, player := range players {
		if player.Amount <= 0 {
			continue
		}

		existingBill, err := s.billMgr.GetBillByRoundTypeAndUser(ctx, settlement.RoundID, dto.BillTypeGrabPacket, player.UserID)
		if err == nil && existingBill != nil {
			if existingBill.Status == dto.BillStatusSuccess {
				totalSettleAmount += player.Amount
				settleUserCount++
			}
			continue
		}

		bill := &model.BillRecord{
			RoundTraceID: settlement.RoundTraceID,
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(settlement.RoundTraceID, dto.BillTypeGrabPacket, player.UserID),
			BillType:     dto.BillTypeGrabPacket,
			RoomID:       settlement.RoomID,
			SessionID:    settlement.SessionID,
			RoundID:      settlement.RoundID,
			RoundNo:      settlement.RoundNo,
			UserID:       player.UserID,
			Amount:       player.Amount,
			Status:       dto.BillStatusSuccess,
			Remark:       fmt.Sprintf("抢红包收入(待会话级入账),局ID:%d", settlement.RoundID),
			IsRobot:      s.robotChecker != nil && s.robotChecker.IsRobot(ctx, player.UserID),
		}
		bills = append(bills, bill)
		totalSettleAmount += player.Amount
		settleUserCount++
	}

	if len(bills) > 0 {
		if err := s.billMgr.CreateBillsInTransaction(ctx, bills); err != nil {
			return 0, 0, fmt.Errorf("create grab bills failed: %w", err)
		}
	}

	return totalSettleAmount, settleUserCount, nil
}

func (s *SettlementService) settleCommission(ctx context.Context, settlement *model.RoundSettlement) error {
	existingBill, err := s.billMgr.GetBillByRoundTypeAndUser(ctx, settlement.RoundID, dto.BillTypeCommission, dto.PlatformAccountID)
	if err == nil && existingBill != nil {
		if existingBill.Status == dto.BillStatusSuccess {
			return nil
		}
	}

	commissionBill := &model.BillRecord{
		RoundTraceID: settlement.RoundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo(settlement.RoundTraceID, dto.BillTypeCommission, dto.PlatformAccountID),
		BillType:     dto.BillTypeCommission,
		RoomID:       settlement.RoomID,
		SessionID:    settlement.SessionID,
		RoundID:      settlement.RoundID,
		RoundNo:      settlement.RoundNo,
		UserID:       dto.PlatformAccountID,
		Amount:       settlement.Commission,
		Status:       dto.BillStatusSuccess,
		Remark:       fmt.Sprintf("佣金收入,局ID:%d", settlement.RoundID),
	}

	if err := s.billMgr.CreateBill(ctx, commissionBill); err != nil {
		return fmt.Errorf("create commission bill failed: %w", err)
	}

	return nil
}

func (s *SettlementService) DeductPenaltyToPlatform(ctx context.Context, req *dto.PenaltyDeductRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDeductTraceID(req.RoomID, req.SessionID)

	// 幂等检查：若已存在同 traceID + BillType + userID 的非 Failed 状态 bill，直接返回 nil。
	//   - Success：已扣款成功，跳过
	//   - Processing：扣款进行中，由重试流程完成，跳过避免重复创建 bill
	//   - Pending：扣款已排队，将由后续流程处理，跳过
	//   - Failed：允许重新创建 bill 并重试扣款
	// 注意：仅 Failed 状态才放行；其他非 Failed 状态都视为「已完成或进行中」从而跳过，
	// 避免重试时重复创建 bill 导致重复扣款（依赖 DB 唯一索引兜底仍会留下冗余记录）。
	existingBill, err := s.billMgr.GetBillByTraceTypeAndUser(ctx, roundTraceID, dto.BillTypePenaltyIncome, req.UserID)
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
	if err := s.billMgr.CreateBillsPairInTransaction(ctx, playerBill, platformBill); err != nil {
		return fmt.Errorf("create penalty bills failed: %w", err)
	}

	// 机器人虚拟通道：跳过 platform.Debit，直接走虚拟钱包扣款
	if isRobot {
		if err := s.virtualBalance.Deduct(ctx, req.UserID, req.Amount); err != nil {
			s.billMgr.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
			return fmt.Errorf("robot virtual deduct penalty failed: %w", err)
		}
		balanceAfter, _ := s.virtualBalance.GetBalance(ctx, req.UserID)
		return s.billMgr.UpdateBillSuccess(ctx, playerBill.ID, dto.BillStatusProcessing, 0, balanceAfter)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, req.UserID)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
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
		s.billMgr.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error())
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
	if err := s.billMgr.UpdateBillSuccess(ctx, playerBill.ID, dto.BillStatusProcessing, 0, balanceAfter); err != nil {
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

func (s *SettlementService) DistributePenaltyFromPlatform(ctx context.Context, req *dto.PenaltyDistributeRequest) error {
	roundTraceID := s.traceIDGen.GeneratePenaltyDistTraceID(req.RoomID, req.SessionID)

	// 幂等检查：若已存在同 traceID + BillType + PlatformAccountID 的 Success 状态 bill，直接返回 nil。
	// DistributePenaltyFromPlatform 的所有 bill 都是 Success 状态（不涉及平台调用），所以一次成功即可跳过。
	existingBill, err := s.billMgr.GetBillByTraceTypeAndUser(ctx, roundTraceID, dto.BillTypePenaltyDistribute, dto.PlatformAccountID)
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

	return s.billMgr.CreateBillsInTransaction(ctx, allBills)
}

func (s *SettlementService) SettleGame(ctx context.Context, sessionID int64) error {
	return s.gameSettleSvc.SettleGame(ctx, sessionID)
}

func (s *SettlementService) GetBillByTraceID(ctx context.Context, traceID string) (*model.BillRecord, error) {
	return s.billMgr.GetBillByTraceID(ctx, traceID)
}

func (s *SettlementService) GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*model.BillRecord, error) {
	return s.billMgr.GetBillsByUserID(ctx, userID, limit, offset)
}

func (s *SettlementService) GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error) {
	return s.billMgr.GetBillsByRoundID(ctx, roundID)
}

func (s *SettlementService) GetRoundSettlement(ctx context.Context, roundID int64) (*model.RoundSettlement, error) {
	return s.billMgr.GetRoundSettlementByRoundID(ctx, roundID)
}

func (s *SettlementService) CheckBalance(ctx context.Context, userID int64, requiredAmount int64) (int64, bool, error) {
	// 机器人虚拟通道
	if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, userID) {
		balance, err := s.virtualBalance.GetBalance(ctx, userID)
		if err != nil {
			return 0, false, fmt.Errorf("get robot virtual balance failed: %w", err)
		}
		return balance, balance >= requiredAmount, nil
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, userID)
	if err != nil {
		return 0, false, fmt.Errorf("get platform user id failed: %w", err)
	}

	result, err := s.platform.GetBalance(ctx, &platform.BalanceRequest{
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
	})
	if err != nil {
		return 0, false, fmt.Errorf("check balance failed: %w", err)
	}

	balance, err := platform.ParseAmount(result.Data.Balance.Amount)
	if err != nil {
		return 0, false, fmt.Errorf("parse balance amount failed: %w", err)
	}
	return balance, balance >= requiredAmount, nil
}

func (s *SettlementService) GetUserBalance(ctx context.Context, userID int64) (int64, error) {
	// 机器人虚拟通道
	if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, userID) {
		return s.virtualBalance.GetBalance(ctx, userID)
	}

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("get platform user id failed: %w", err)
	}

	result, err := s.platform.GetBalance(ctx, &platform.BalanceRequest{
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
	})
	if err != nil {
		return 0, fmt.Errorf("check balance failed: %w", err)
	}

	return platform.ParseAmount(result.Data.Balance.Amount)
}
