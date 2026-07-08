package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
)

// RoundSettleService 负责单局结算相关操作：写 grab/commission BillRecord、触发奖励结算、
// 委托 GameSettleReportingService 完成会话级结算。从原 SettlementService 拆分而来（P0-10）。
type RoundSettleService struct {
	billRepo            domain.BillRepository
	roundSettlementRepo domain.RoundSettlementRepository
	redis               cRedis.RedisClient
	traceIDGen          *TraceIDGenerator
	rewardSettler       *RewardSettler
	gameSettleSvc       *GameSettleReportingService
	lockCfg             *config.LockConfig
	robotChecker        RobotChecker
}

// NewRoundSettleService 构造 RoundSettleService 实例。
func NewRoundSettleService(
	billRepo domain.BillRepository,
	roundSettlementRepo domain.RoundSettlementRepository,
	redis cRedis.RedisClient,
	traceIDGen *TraceIDGenerator,
	rewardSettler *RewardSettler,
	gameSettleSvc *GameSettleReportingService,
	lockCfg *config.LockConfig,
	robotChecker RobotChecker,
) *RoundSettleService {
	if lockCfg == nil {
		lockCfg = config.DefaultLockConfig()
	}

	return &RoundSettleService{
		billRepo:            billRepo,
		roundSettlementRepo: roundSettlementRepo,
		redis:               redis,
		traceIDGen:          traceIDGen,
		rewardSettler:       rewardSettler,
		gameSettleSvc:       gameSettleSvc,
		lockCfg:             lockCfg,
		robotChecker:        robotChecker,
	}
}

func (s *RoundSettleService) SettleRound(ctx context.Context, tx domain.Transaction, req *dto.RoundSettleRequest) error {
	roundSettlementRepo := tx.RoundSettlementRepo()
	settlement, err := roundSettlementRepo.GetRoundSettlementByRoundID(ctx, req.RoundID)
	if err == nil && settlement != nil && settlement.Status == dto.RoundStatusCredited {
		return nil
	}

	lockKey := rediskeys.SettleRoundLockKey(req.RoundID)
	return lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.SettleRoundLockTTL.Seconds()), func() error {
		existingSettlement, err := roundSettlementRepo.GetRoundSettlementByRoundID(ctx, req.RoundID)
		if err != nil {
			return fmt.Errorf("get round settlement failed: %w", err)
		}
		if existingSettlement == nil {
			return fmt.Errorf("round settlement not found for roundID: %d", req.RoundID)
		}

		if existingSettlement.Status == dto.RoundStatusCredited {
			return nil
		}

		if err := roundSettlementRepo.UpdateRoundSettlementSettleInfo(ctx, existingSettlement.RoundTraceID,
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
		totalSettleAmount, settleUserCount, err := s.creditRound(ctx, tx, existingSettlement, req.Players)
		if err != nil {
			return err
		}

		if req.RewardType > 0 && req.RewardAmount > 0 {
			if err := s.rewardSettler.SettleReward(ctx, tx, existingSettlement, req.Players); err != nil {
				// 上抛 err 触发 caller (game_event_consumer) 事务回滚，并保证 Kafka 重试时
				// round_settlement.status 仍非 Credited，SettleReward 能被重新调用。
				return fmt.Errorf("settle system reward failed: %w", err)
			}
		}

		// 所有子结算成功后才标记 round_settlement.status = Credited
		now := time.Now()
		if err := roundSettlementRepo.UpdateRoundSettlementCredited(ctx, existingSettlement.RoundTraceID, totalSettleAmount, settleUserCount, &now); err != nil {
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
// tx 由 SettleRound 从 AppService 事务回调传入，所有 DB 操作纳入同一事务。
func (s *RoundSettleService) creditRound(ctx context.Context, tx domain.Transaction, settlement *model.RoundSettlement, players []*dto.PlayerSettleInfo) (int64, int, error) {
	billRepo := tx.BillRepo()
	if settlement.Commission > 0 {
		if err := s.settleCommission(ctx, tx, settlement); err != nil {
			logger.Error("settle commission failed", "round_id", settlement.RoundID, "error", err)
			return 0, 0, fmt.Errorf("settle commission failed: %w", err)
		}
	}

	bills := make([]*model.BillRecord, 0)
	var totalSettleAmount int64
	var settleUserCount int

	for _, player := range players {
		if player.Amount <= 0 {
			continue
		}

		existingBill, err := billRepo.GetBillByRoundTypeAndUser(ctx, settlement.RoundID, dto.BillTypeGrabPacket, player.UserID)
		if err == nil && existingBill != nil {
			if existingBill.Status == dto.BillStatusSuccess {
				totalSettleAmount += player.Amount
				settleUserCount++
			}
			continue
		}

		if s.robotChecker == nil {
			return 0, 0, fmt.Errorf("robot checker is nil")
		}
		isRobot, err := s.robotChecker.IsRobot(ctx, player.UserID)
		if err != nil {
			return 0, 0, fmt.Errorf("check robot failed: %w", err)
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
			IsRobot:      isRobot,
		}
		bills = append(bills, bill)
		totalSettleAmount += player.Amount
		settleUserCount++
	}

	if len(bills) > 0 {
		if err := billRepo.CreateBills(ctx, bills); err != nil {
			return 0, 0, fmt.Errorf("create grab bills failed: %w", err)
		}
	}

	return totalSettleAmount, settleUserCount, nil
}

func (s *RoundSettleService) settleCommission(ctx context.Context, tx domain.Transaction, settlement *model.RoundSettlement) error {
	billRepo := tx.BillRepo()
	existingBill, err := billRepo.GetBillByRoundTypeAndUser(ctx, settlement.RoundID, dto.BillTypeCommission, dto.PlatformAccountID)
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

	if err := billRepo.CreateBill(ctx, commissionBill); err != nil {
		return fmt.Errorf("create commission bill failed: %w", err)
	}

	return nil
}

func (s *RoundSettleService) SettleGame(ctx context.Context, sessionID int64) error {
	return s.gameSettleSvc.SettleGame(ctx, sessionID)
}
