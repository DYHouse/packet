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
	platform      platform.Client
	billMgr       *BillManager
	redis         *cRedis.Client
	traceIDGen    *TraceIDGenerator
	deductSvc     *DeductService
	rewardSettler *RewardSettler
	cfg           *config.PlatformConfig
	userIDConvert *UserIDConvertService
	gameSettleSvc *GameSettleService
	callMgr       *PlatformCallManager
}

func NewSettlementService(
	platformClient platform.Client,
	billMgr *BillManager,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	deductSvc *DeductService,
	rewardSettler *RewardSettler,
	gameSettleSvc *GameSettleService,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
) *SettlementService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}

	return &SettlementService{
		platform:      platformClient,
		billMgr:       billMgr,
		redis:         redis,
		traceIDGen:    traceIDGen,
		deductSvc:     deductSvc,
		rewardSettler: rewardSettler,
		cfg:           cfg,
		userIDConvert: userIDConvert,
		gameSettleSvc: gameSettleSvc,
		callMgr:       callMgr,
	}
}

func (s *SettlementService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) error {
	settlement, err := s.billMgr.GetRoundSettlementByRoundID(ctx, req.RoundID)
	if err == nil && settlement != nil && settlement.Status == dto.RoundStatusCredited {
		return nil
	}

	lockKey := redis.SettleRoundLockKey(req.RoundID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
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

		if err := s.creditRound(ctx, existingSettlement, req.Players); err != nil {
			return err
		}

		if req.RewardType > 0 && req.RewardAmount > 0 {
			if err := s.rewardSettler.SettleReward(ctx, existingSettlement, req.Players); err != nil {
				logger.Error("settle system reward failed", "round_id", req.RoundID, "error", err)
			}
		}

		return nil
	})
}

// creditRound handles round-level internal bookkeeping: creates BillRecord entries with Success status
// without calling platform.Credit. Actual fund movement happens at session level via net settlement.
func (s *SettlementService) creditRound(ctx context.Context, settlement *model.RoundSettlement, players []*dto.PlayerSettleInfo) error {
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
			BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("CREDIT", player.UserID),
			BillType:     dto.BillTypeGrabPacket,
			RoomID:       settlement.RoomID,
			SessionID:    settlement.SessionID,
			RoundID:      settlement.RoundID,
			RoundNo:      settlement.RoundNo,
			UserID:       player.UserID,
			Amount:       player.Amount,
			Status:       dto.BillStatusSuccess,
			Remark:       fmt.Sprintf("抢红包收入(待局级净额结算),局ID:%d", settlement.RoundID),
		}
		bills = append(bills, bill)
		totalSettleAmount += player.Amount
		settleUserCount++
	}

	if len(bills) == 0 {
		now := time.Now()
		return s.billMgr.UpdateRoundSettlementCredited(ctx, settlement.RoundTraceID, totalSettleAmount, settleUserCount, &now)
	}

	now := time.Now()
	return s.billMgr.CreateBillsAndUpdateSettlement(ctx, settlement.RoundTraceID, totalSettleAmount, settleUserCount, now, bills)
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
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("COMMISSION", settlement.RoundID),
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

	playerBill := &model.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("PENALTY", req.UserID),
		BillType:     dto.BillTypePenaltyIncome,
		RoomID:       req.RoomID,
		SessionID:    req.SessionID,
		RoundNo:      req.RoundNo,
		UserID:       req.UserID,
		Amount:       -req.Amount,
		Status:       dto.BillStatusProcessing,
		Remark:       fmt.Sprintf("惩罚扣款,类型:%s,回合:%d", req.PenaltyType, req.RoundNo),
	}

	platformBill := &model.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("PLATFORM_IN", req.SessionID),
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

	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, req.UserID)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusFailed, err.Error())
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

	callLog, _ := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeDebit,
		BizOrderNo: playerBill.BizOrderNo,
		ReqBody:    debitReq,
	})

	result, err := s.platform.Debit(ctx, debitReq)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, playerBill.ID, dto.BillStatusFailed, err.Error())
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
	if err := s.billMgr.UpdateBillSuccess(ctx, playerBill.ID, 0, balanceAfter); err != nil {
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

	platformBill := &model.BillRecord{
		RoundTraceID: roundTraceID,
		BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("PENALTY_DIST", req.RoomID),
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
				BizOrderNo:   s.traceIDGen.GenerateBizOrderNo("PENALTY_SHARE", recipientID),
				BillType:     dto.BillTypePenaltyDistribute,
				RoomID:       req.RoomID,
				SessionID:    req.SessionID,
				RoundID:      req.RoundID,
				UserID:       recipientID,
				Amount:       amount,
				Status:       dto.BillStatusSuccess,
				Remark:       fmt.Sprintf("罚款分红,总额:%d,原因:%s", req.Amount, req.Reason),
			}
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
