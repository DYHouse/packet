package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/model"
)

// BalanceQueryService 负责余额与账单查询：GetBill*/GetRoundSettlement/CheckBalance/GetUserBalance。
// 从原 SettlementService 拆分而来（P0-10）。
// 注意：与 balance_service.go 中的 BalanceService 不同，本 Service 仅做查询类操作，
// 不包含 CheckBalanceForReady/CalculateRequiredFee 等业务规则。
type BalanceQueryService struct {
	platform            platform.Client
	billRepo            domain.BillRepository
	roundSettlementRepo domain.RoundSettlementRepository
	cfg                 *config.PlatformConfig
	userIDConvert       *UserIDConvertService
	robotChecker        RobotChecker
	virtualBalance      domain.VirtualBalanceService
}

// NewBalanceQueryService 构造 BalanceQueryService 实例。
func NewBalanceQueryService(
	platformClient platform.Client,
	billRepo domain.BillRepository,
	roundSettlementRepo domain.RoundSettlementRepository,
	cfg *config.PlatformConfig,
	userIDConvert *UserIDConvertService,
	robotChecker RobotChecker,
	virtualBalance domain.VirtualBalanceService,
) *BalanceQueryService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}

	return &BalanceQueryService{
		platform:            platformClient,
		billRepo:            billRepo,
		roundSettlementRepo: roundSettlementRepo,
		cfg:                 cfg,
		userIDConvert:       userIDConvert,
		robotChecker:        robotChecker,
		virtualBalance:      virtualBalance,
	}
}

func (s *BalanceQueryService) GetBillByTraceID(ctx context.Context, traceID string) (*model.BillRecord, error) {
	return s.billRepo.GetBillByTraceID(ctx, traceID)
}

func (s *BalanceQueryService) GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*model.BillRecord, error) {
	return s.billRepo.GetBillsByUserID(ctx, userID, limit, offset)
}

func (s *BalanceQueryService) GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error) {
	return s.billRepo.GetBillsByRoundID(ctx, roundID)
}

func (s *BalanceQueryService) GetRoundSettlement(ctx context.Context, roundID int64) (*model.RoundSettlement, error) {
	return s.roundSettlementRepo.GetRoundSettlementByRoundID(ctx, roundID)
}

func (s *BalanceQueryService) CheckBalance(ctx context.Context, userID int64, requiredAmount int64) (int64, bool, error) {
	// 机器人虚拟通道
	if s.robotChecker == nil {
		return 0, false, fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, userID)
	if err != nil {
		return 0, false, fmt.Errorf("check robot failed: %w", err)
	}
	if isRobot {
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

func (s *BalanceQueryService) GetUserBalance(ctx context.Context, userID int64) (int64, error) {
	// 机器人虚拟通道
	if s.robotChecker == nil {
		return 0, fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("check robot failed: %w", err)
	}
	if isRobot {
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
