package service

import (
	"context"
	"fmt"
	"sync"
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

const defaultMaxConcurrentDeduct = 20

type DeductService struct {
	platform            platform.Client
	billMgr             *BillManager
	redis               *cRedis.Client
	traceIDGen          *TraceIDGenerator
	cfg                 *config.PlatformConfig
	creditRetrySvc      *CreditRetryService
	userIDConvert       *UserIDConvertService
	callMgr             *PlatformCallManager
	maxConcurrentDeduct int
	robotChecker        RobotChecker
	virtualBalance      *VirtualBalanceService
}

func NewDeductService(
	platformClient platform.Client,
	billMgr *BillManager,
	redis *cRedis.Client,
	traceIDGen *TraceIDGenerator,
	cfg *config.PlatformConfig,
	creditRetrySvc *CreditRetryService,
	userIDConvert *UserIDConvertService,
	callMgr *PlatformCallManager,
	robotChecker RobotChecker,
	virtualBalance *VirtualBalanceService,
) *DeductService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &DeductService{
		platform:            platformClient,
		billMgr:             billMgr,
		redis:               redis,
		traceIDGen:          traceIDGen,
		cfg:                 cfg,
		creditRetrySvc:      creditRetrySvc,
		userIDConvert:       userIDConvert,
		callMgr:             callMgr,
		maxConcurrentDeduct: defaultMaxConcurrentDeduct,
		robotChecker:        robotChecker,
		virtualBalance:      virtualBalance,
	}
}

func (s *DeductService) DeductForFirstRound(ctx context.Context, req *dto.FirstRoundDeductRequest) (*dto.FirstRoundDeductResult, error) {
	// 幂等性检查：基于 roundID 而非随机 batchID
	if exists, _ := s.billMgr.ExistsRoundSettlement(ctx, req.RoundID); exists {
		return s.getExistingFirstRoundResult(ctx, req.RoundID)
	}

	lockKey := redis.FirstRoundDeductLockKey(req.SessionID)
	var result *dto.FirstRoundDeductResult

	err := lock.WithRedisLock(ctx, s.redis, lockKey, 60, func() error {
		// 锁内二次检查
		if exists, _ := s.billMgr.ExistsRoundSettlement(ctx, req.RoundID); exists {
			var err error
			result, err = s.getExistingFirstRoundResult(ctx, req.RoundID)
			return err
		}

		roundTraceID := s.traceIDGen.GenerateRoundTraceID(req.SessionID, req.RoundNo)
		batchID := s.traceIDGen.GenerateBatchID()

		settlement := &model.RoundSettlement{
			RoundTraceID:       roundTraceID,
			RoomID:             req.RoomID,
			SessionID:          req.SessionID,
			RoundID:            req.RoundID,
			RoundNo:            req.RoundNo,
			DeductScene:        dto.DeductSceneFirstRoundShare,
			DeductAmount:       req.RoomFeePerPlayer * int64(len(req.Players)),
			DeductUserCount:    len(req.Players),
			DeductSuccessCount: 0,
			Status:             dto.RoundStatusDeducting,
		}

		bills := make([]*model.BillRecord, 0, len(req.Players))
		for _, player := range req.Players {
			bizOrderNo := s.traceIDGen.GenerateBizOrderNo("DEDUCT_FIRST", player.UserID)
			bill := &model.BillRecord{
				RoundTraceID: roundTraceID,
				BizOrderNo:   bizOrderNo,
				BillType:     dto.BillTypeFirstRoundDeduct,
				DeductScene:  dto.DeductSceneFirstRoundShare,
				RoomID:       req.RoomID,
				SessionID:    req.SessionID,
				RoundID:      req.RoundID,
				RoundNo:      req.RoundNo,
				UserID:       player.UserID,
				BatchID:      batchID,
				Amount:       -req.RoomFeePerPlayer,
				Status:       dto.BillStatusProcessing,
				Remark:       fmt.Sprintf("首回合平摊扣款,会话ID:%d,回合:%d", req.SessionID, req.RoundNo),
			}
			bill.IsRobot = s.robotChecker != nil && s.robotChecker.IsRobot(ctx, player.UserID)
			bills = append(bills, bill)
		}

		if err := s.billMgr.CreateRoundSettlementAndBills(ctx, settlement, bills); err != nil {
			return fmt.Errorf("create round settlement and bills failed: %w", err)
		}

		result = s.executeBatchDeduct(ctx, bills, req.RoomFeePerPlayer, roundTraceID)

		if !result.AllSuccess {
			s.handleFirstRoundDeductFailure(ctx, roundTraceID, batchID, result)
		}

		return nil
	})

	return result, err
}

func (s *DeductService) executeBatchDeduct(ctx context.Context, bills []*model.BillRecord, amount int64, roundTraceID string) *dto.FirstRoundDeductResult {
	result := &dto.FirstRoundDeductResult{
		BatchID:        bills[0].BatchID,
		SuccessCount:   0,
		FailedCount:    0,
		SuccessPlayers: make([]int64, 0),
		FailedPlayers:  make([]*dto.FailedPlayerInfo, 0),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	maxConcurrent := s.maxConcurrentDeduct
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentDeduct
	}
	sem := make(chan struct{}, maxConcurrent)

	for _, bill := range bills {
		wg.Add(1)
		sem <- struct{}{}
		go func(b *model.BillRecord) {
			defer wg.Done()
			defer func() { <-sem }()

			err := s.executeSingleDeduct(ctx, b, amount)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				result.FailedCount++
				result.FailedPlayers = append(result.FailedPlayers, &dto.FailedPlayerInfo{
					UserID:    b.UserID,
					ErrorCode: "DEDUCT_FAILED",
					ErrorMsg:  err.Error(),
				})
			} else {
				result.SuccessCount++
				result.SuccessPlayers = append(result.SuccessPlayers, b.UserID)
			}
		}(bill)
	}

	wg.Wait()

	result.AllSuccess = result.FailedCount == 0

	if result.AllSuccess {
		now := time.Now()
		if err := s.billMgr.UpdateRoundSettlementDeductSuccess(ctx, roundTraceID, result.SuccessCount, now); err != nil {
			logger.Error("update round settlement deduct success failed", "round_trace_id", roundTraceID, "error", err)
		}
		if err := s.billMgr.UpdateRoundSettlementStatus(ctx, roundTraceID, dto.RoundStatusDeducted, ""); err != nil {
			logger.Error("update round settlement status to deducted failed", "round_trace_id", roundTraceID, "error", err)
		}
	} else {
		if err := s.billMgr.UpdateRoundSettlementStatus(ctx, roundTraceID, dto.RoundStatusFailed, "部分玩家扣款失败"); err != nil {
			logger.Error("update round settlement status to failed error", "round_trace_id", roundTraceID, "error", err)
		}
	}

	return result
}

func (s *DeductService) executeSingleDeduct(ctx context.Context, bill *model.BillRecord, amount int64) error {
	// 机器人虚拟通道
	if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, bill.UserID) {
		if err := s.virtualBalance.Deduct(ctx, bill.UserID, amount); err != nil {
			s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
			return fmt.Errorf("robot virtual deduct failed: %w", err)
		}
		balanceAfter, _ := s.virtualBalance.GetBalance(ctx, bill.UserID)
		return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter)
	}

	// 原流程不变（真人玩家）
	platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, bill.UserID)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
		s.creditRetrySvc.CreateDebitFailedException(ctx, bill)
		return fmt.Errorf("get platform user id failed: %w", err)
	}

	debitReq := &platform.DebitRequest{
		BizID:    bill.BizOrderNo,
		RoundID:  fmt.Sprintf("%d", bill.SessionID),
		GameID:   s.cfg.GameID,
		GameCode: s.cfg.GameCode,
		UserID:   platformUserID,
		Currency: s.cfg.Currency,
		Amount:   platform.FormatAmount(amount),
		Reason:   bill.Remark,
		GameName: s.cfg.GameName,
	}

	callLog, _ := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
		CallType:   model.CallTypeDebit,
		BizOrderNo: bill.BizOrderNo,
		ReqBody:    debitReq,
	})

	result, err := s.platform.Debit(ctx, debitReq)
	if err != nil {
		s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
		s.creditRetrySvc.CreateDebitFailedException(ctx, bill)
		if callLog != nil {
			s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
				ID:           callLog.ID,
				Status:       model.CallLogStatusFailed,
				ErrorMessage: err.Error(),
			})
		}
		return fmt.Errorf("debit failed: %w", err)
	}

	balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
	if err != nil {
		logger.Error("parse balance amount failed after successful debit, mark bill as success with balance=0",
			"bill_id", bill.ID, "raw_amount", result.Data.Balance.Amount, "error", err)
		if updateErr := s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, 0); updateErr != nil {
			logger.Error("update bill success failed after parse amount error", "bill_id", bill.ID, "error", updateErr)
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
	if err := s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter); err != nil {
		logger.Error("update bill success failed", "bill_id", bill.ID, "error", err)
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

func (s *DeductService) handleFirstRoundDeductFailure(ctx context.Context, roundTraceID string, batchID string, result *dto.FirstRoundDeductResult) {
	for _, userID := range result.SuccessPlayers {
		bill, err := s.billMgr.GetBillByBatchAndUser(ctx, batchID, userID)
		if err != nil {
			logger.Error("get bill by batch and user failed", "user_id", userID, "batch_id", batchID, "error", err)
			continue
		}

		if bill.Status == dto.BillStatusSuccess {
			refundOrderNo := s.traceIDGen.GenerateRefundOrderNo(userID)
			refundAudit := &model.RefundAudit{
				RefundOrderNo: refundOrderNo,
				RoundTraceID:  roundTraceID,
				BatchID:       batchID,
				RoomID:        bill.RoomID,
				SessionID:     bill.SessionID,
				RoundID:       bill.RoundID,
				UserID:        userID,
				BillID:        bill.ID,
				BillOrderNo:   bill.BizOrderNo,
				RefundAmount:  -bill.Amount,
				RefundReason:  "首回合扣款失败，需要退款",
				RefundType:    dto.RefundTypeFirstRoundFail,
				Status:        dto.RefundStatusPending,
				AppliedAt:     time.Now(),
				AppliedBy:     0,
			}

			if err := s.billMgr.CreateRefundAudit(ctx, refundAudit); err != nil {
				logger.Error("create refund audit failed", "user_id", userID, "batch_id", batchID, "error", err)
				continue
			}

			s.billMgr.UpdateBillRefundStatus(ctx, bill.ID, dto.RefundStatusPending, refundOrderNo)
		}
	}
}

// getExistingFirstRoundResult 查询已有 round 对应的扣款结果（幂等返回）
func (s *DeductService) getExistingFirstRoundResult(ctx context.Context, roundID int64) (*dto.FirstRoundDeductResult, error) {
	bills, err := s.billMgr.GetBillsByRoundID(ctx, roundID)
	if err != nil {
		return nil, err
	}

	result := &dto.FirstRoundDeductResult{
		BatchID:        "",
		SuccessCount:   0,
		FailedCount:    0,
		SuccessPlayers: make([]int64, 0),
		FailedPlayers:  make([]*dto.FailedPlayerInfo, 0),
	}

	for _, bill := range bills {
		if bill.BillType != dto.BillTypeFirstRoundDeduct {
			continue
		}
		if bill.BatchID != "" && result.BatchID == "" {
			result.BatchID = bill.BatchID
		}
		if bill.Status == dto.BillStatusSuccess {
			result.SuccessCount++
			result.SuccessPlayers = append(result.SuccessPlayers, bill.UserID)
		} else if bill.Status == dto.BillStatusFailed {
			result.FailedCount++
			result.FailedPlayers = append(result.FailedPlayers, &dto.FailedPlayerInfo{
				UserID:    bill.UserID,
				ErrorCode: "DEDUCT_FAILED",
				ErrorMsg:  bill.ErrorMessage,
			})
		}
	}

	result.AllSuccess = result.FailedCount == 0

	return result, nil
}

func (s *DeductService) getBatchDeductResult(ctx context.Context, batchID string) (*dto.FirstRoundDeductResult, error) {
	bills, err := s.billMgr.GetBillsByBatchID(ctx, batchID)
	if err != nil {
		return nil, err
	}

	result := &dto.FirstRoundDeductResult{
		BatchID:        batchID,
		SuccessCount:   0,
		FailedCount:    0,
		SuccessPlayers: make([]int64, 0),
		FailedPlayers:  make([]*dto.FailedPlayerInfo, 0),
	}

	for _, bill := range bills {
		if bill.Status == dto.BillStatusSuccess {
			result.SuccessCount++
			result.SuccessPlayers = append(result.SuccessPlayers, bill.UserID)
		} else if bill.Status == dto.BillStatusFailed {
			result.FailedCount++
			result.FailedPlayers = append(result.FailedPlayers, &dto.FailedPlayerInfo{
				UserID:    bill.UserID,
				ErrorCode: "DEDUCT_FAILED",
				ErrorMsg:  bill.ErrorMessage,
			})
		}
	}

	result.AllSuccess = result.FailedCount == 0

	return result, nil
}

func (s *DeductService) DeductForLaterRound(ctx context.Context, req *dto.LaterRoundDeductRequest) error {
	deductReq := &dto.SingleDeductRequest{
		RoomID:       req.RoomID,
		SessionID:    req.SessionID,
		RoundID:      req.RoundID,
		RoundNo:      req.RoundNo,
		UserID:       req.MinPlayerID,
		Amount:       req.RoomFee,
		BillType:     dto.BillTypeLaterRoundDeduct,
		DeductScene:  dto.DeductSceneLaterRoundMin,
		RoundTraceID: req.RoundTraceID,
		Remark:       fmt.Sprintf("后续回合房费扣款,最低金额玩家:%d", req.MinPlayerID),
	}
	return s.deductSingleUser(ctx, deductReq, redis.LaterRoundDeductLockKey(req.RoundID))
}

func (s *DeductService) DeductForSystemPacket(ctx context.Context, req *dto.SystemPacketDeductRequest) error {
	if exists, _ := s.billMgr.ExistsByRoundAndType(ctx, req.RoundID, dto.BillTypeSystemPacket); exists {
		return nil
	}

	lockKey := redis.SystemPacketDeductLockKey(req.RoundID)
	return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
		if exists, _ := s.billMgr.ExistsByRoundAndType(ctx, req.RoundID, dto.BillTypeSystemPacket); exists {
			return nil
		}

		roundTraceID := req.RoundTraceID
		if roundTraceID == "" {
			roundTraceID = s.traceIDGen.GenerateRoundTraceID(req.SessionID, req.RoundNo)
		}

		settlement := &model.RoundSettlement{
			RoundTraceID:       roundTraceID,
			RoomID:             req.RoomID,
			SessionID:          req.SessionID,
			RoundID:            req.RoundID,
			RoundNo:            req.RoundNo,
			DeductScene:        dto.DeductSceneSystemPacket,
			DeductAmount:       req.TotalAmount,
			DeductUserCount:    1,
			DeductSuccessCount: 1,
			Status:             dto.RoundStatusDeducted,
		}

		bizOrderNo := s.traceIDGen.GenerateBizOrderNo("SYS_PACKET", req.RoundID)
		bill := &model.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   bizOrderNo,
			BillType:     dto.BillTypeSystemPacket,
			DeductScene:  dto.DeductSceneSystemPacket,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      req.RoundID,
			RoundNo:      req.RoundNo,
			UserID:       dto.PlatformAccountID,
			Amount:       -req.TotalAmount,
			Status:       dto.BillStatusSuccess,
			Remark:       fmt.Sprintf("系统发红包,原因:%s", req.Reason),
		}

		if err := s.billMgr.CreateRoundSettlementAndBills(ctx, settlement, []*model.BillRecord{bill}); err != nil {
			return fmt.Errorf("create round settlement and bill failed: %w", err)
		}

		return nil
	})
}

func (s *DeductService) deductSingleUser(ctx context.Context, req *dto.SingleDeductRequest, lockKey string) error {
	if exists, _ := s.billMgr.ExistsByRoundAndType(ctx, req.RoundID, req.BillType); exists {
		return nil
	}

	return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
		if exists, _ := s.billMgr.ExistsByRoundAndType(ctx, req.RoundID, req.BillType); exists {
			return nil
		}

		roundTraceID := req.RoundTraceID
		if roundTraceID == "" {
			roundTraceID = s.traceIDGen.GenerateRoundTraceID(req.SessionID, req.RoundNo)
		}

		settlement := &model.RoundSettlement{
			RoundTraceID:       roundTraceID,
			RoomID:             req.RoomID,
			SessionID:          req.SessionID,
			RoundID:            req.RoundID,
			RoundNo:            req.RoundNo,
			DeductScene:        req.DeductScene,
			DeductAmount:       req.Amount,
			DeductUserCount:    1,
			DeductSuccessCount: 0,
			Status:             dto.RoundStatusDeducting,
		}

		if req.DeductScene == dto.DeductSceneLaterRoundMin {
			settlement.MinPlayerID = req.UserID
		}

		bizOrderNo := s.traceIDGen.GenerateBizOrderNo("DEDUCT", req.UserID)
		bill := &model.BillRecord{
			RoundTraceID: roundTraceID,
			BizOrderNo:   bizOrderNo,
			BillType:     req.BillType,
			DeductScene:  req.DeductScene,
			RoomID:       req.RoomID,
			SessionID:    req.SessionID,
			RoundID:      req.RoundID,
			RoundNo:      req.RoundNo,
			UserID:       req.UserID,
			Amount:       -req.Amount,
			Status:       dto.BillStatusProcessing,
			Remark:       req.Remark,
		}
		bill.IsRobot = s.robotChecker != nil && s.robotChecker.IsRobot(ctx, req.UserID)

		if err := s.billMgr.CreateRoundSettlementAndBills(ctx, settlement, []*model.BillRecord{bill}); err != nil {
			return fmt.Errorf("create round settlement and bill failed: %w", err)
		}

		if err := s.executeSingleDeduct(ctx, bill, req.Amount); err != nil {
			s.billMgr.UpdateRoundSettlementStatus(ctx, roundTraceID, dto.RoundStatusFailed, err.Error())
			return err
		}

		now := time.Now()
		s.billMgr.UpdateRoundSettlementDeductSuccess(ctx, roundTraceID, 1, now)
		return s.billMgr.UpdateRoundSettlementStatus(ctx, roundTraceID, dto.RoundStatusDeducted, "")
	})
}
