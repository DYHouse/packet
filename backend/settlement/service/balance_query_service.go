// 本文件定义 BalanceQueryService，负责"通用只读查询"语义下的余额与账单查询。
//
// 职责边界（与 balance_service.go 中的 BalanceService 形成对照）：
//   - 本 Service 面向通用只读查询，不附带业务规则计算；
//   - BalanceService 面向具体业务用例的余额校验（含开局费用计算等业务规则）。
//
// 主要用例：
//   - 账单查询：GetBillByTraceID / GetBillsByUserID / GetBillsByRoundID（纯转发到 BillRepository）；
//   - 局结算查询：GetRoundSettlement（纯转发到 RoundSettlementRepository）；
//   - 通用余额校验：CheckBalance（含机器人虚拟通道，校验余额是否满足所需金额）；
//   - 通用用户余额查询：GetUserBalance（含机器人虚拟通道，被通用查询场景调用）。
//
// 与 BalanceService 的差异：
//   - 本 Service 依赖 BillRepository / RoundSettlementRepository，提供账单与局结算查询；
//   - 本 Service 不依赖 FeeCalculator，不进行开局费用计算；
//   - 余额查询语义不同：本 Service 的 GetUserBalance 含机器人虚拟通道，BalanceService.CheckUserBalance 仅查真实玩家。
//   - 本 Service 不包含 CheckBalanceForReady 等业务规则编排。
//
// 所有方法均为只读用例，无需事务编排。
package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
)

// BalanceQueryService 负责通用只读查询：账单查询（GetBill*）、局结算查询
// （GetRoundSettlement）、通用余额校验（CheckBalance）、通用用户余额查询
// （GetUserBalance）。
//
// 本 Service 不附带业务规则计算；与面向业务校验的 BalanceService 区分
// （后者负责开局准备 CheckBalanceForReady、真实玩家余额查询 CheckUserBalance）。
// 从原 SettlementService 拆分而来（P0-10）。所有方法均为只读用例，无需事务编排。
type BalanceQueryService struct {
	platform            platform.Client
	billRepo            repository.BillRepository
	roundSettlementRepo repository.RoundSettlementRepository
	cfg                 *config.PlatformConfig
	userIDConvert       *UserIDConvertService
	robotChecker        RobotChecker
	virtualBalance      domain.VirtualBalanceService
}

// NewBalanceQueryService 构造 BalanceQueryService 实例。
func NewBalanceQueryService(
	platformClient platform.Client,
	billRepo repository.BillRepository,
	roundSettlementRepo repository.RoundSettlementRepository,
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

func (s *BalanceQueryService) GetBillByTraceID(ctx context.Context, traceID string) (*domain.BillRecord, error) {
	return s.billRepo.GetBillByTraceID(ctx, traceID)
}

func (s *BalanceQueryService) GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*domain.BillRecord, error) {
	return s.billRepo.GetBillsByUserID(ctx, userID, limit, offset)
}

func (s *BalanceQueryService) GetBillsByRoundID(ctx context.Context, roundID int64) ([]*domain.BillRecord, error) {
	return s.billRepo.GetBillsByRoundID(ctx, roundID)
}

func (s *BalanceQueryService) GetRoundSettlement(ctx context.Context, roundID int64) (*domain.RoundSettlement, error) {
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
