// 本文件定义 BalanceService，负责"业务校验"语义下的余额检查场景。
//
// 职责边界（与 balance_query_service.go 中的 BalanceQueryService 形成对照）：
//   - 本 Service 面向具体业务用例的余额校验，附带业务规则计算；
//   - BalanceQueryService 面向通用只读查询（账单查询、局结算查询、通用余额校验）。
//
// 主要用例：
//   - CheckBalanceForReady：开局/入房/座位准备前的余额校验，结合 FeeCalculator 计算所需费用，
//     并透明处理机器人虚拟余额通道，返回 BalanceCheckResult；
//   - CheckUserBalance：查询真实玩家平台余额（仅真实玩家，不处理机器人虚拟通道），
//     被 gRPC 接口（game/server/generic_service.go）直接调用。
//
// 与 BalanceQueryService 的差异：
//   - 本 Service 不依赖 BillRepository / RoundSettlementRepository，不提供账单与局结算查询；
//   - 本 Service 额外依赖 FeeCalculator，用于开局费用计算；
//   - 余额查询语义不同：本 Service 的 CheckUserBalance 仅查真实玩家，BalanceQueryService.GetUserBalance 含机器人虚拟通道。
package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
)

// BalanceService 负责业务校验语义下的余额检查（开局准备 CheckBalanceForReady、
// 真实玩家平台余额查询 CheckUserBalance）。
//
// 本 Service 面向具体业务用例，附带 FeeCalculator 业务规则计算；与通用只读查询服务
// BalanceQueryService 区分（后者负责账单/局结算/通用余额查询）。
// 所有方法均为只读用例，无需事务编排。
type BalanceService struct {
	platform       platform.Client
	cfg            *config.PlatformConfig
	userIDConvert  *UserIDConvertService
	virtualBalance domain.VirtualBalanceService
	robotChecker   RobotChecker
	feeCalculator  domain.FeeCalculator
}

func NewBalanceService(platformClient platform.Client, cfg *config.PlatformConfig, userIDConvert *UserIDConvertService, virtualBalance domain.VirtualBalanceService, robotChecker RobotChecker, feeCalculator domain.FeeCalculator) *BalanceService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &BalanceService{
		platform:       platformClient,
		cfg:            cfg,
		userIDConvert:  userIDConvert,
		virtualBalance: virtualBalance,
		robotChecker:   robotChecker,
		feeCalculator:  feeCalculator,
	}
}

func (s *BalanceService) CheckBalanceForReady(ctx context.Context, req *dto.BalanceCheckRequest) (*dto.BalanceCheckResult, error) {
	requiredFee := s.feeCalculator.CalculateRequiredFee(req.RoomFee, req.MaxPlayers, req.MaxRounds)

	// 检查是否为机器人，使用虚拟余额
	if s.robotChecker == nil {
		return nil, fmt.Errorf("robot checker is nil")
	}
	isRobot, err := s.robotChecker.IsRobot(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("check robot failed: %w", err)
	}
	if isRobot {
		if s.virtualBalance != nil {
			balance, err := s.virtualBalance.GetBalance(ctx, req.UserID)
			if err != nil {
				logger.Error("get virtual balance failed",
					"user_id", req.UserID,
					"required_fee", requiredFee,
					"error", err)
				return nil, fmt.Errorf("get virtual balance failed: %w", err)
			}
			return &dto.BalanceCheckResult{
				UserID:       req.UserID,
				Balance:      balance,
				RequiredFee:  requiredFee,
				IsSufficient: balance >= requiredFee,
			}, nil
		}
	}

	// 真实玩家使用平台余额
	balance, err := s.CheckUserBalance(ctx, req.UserID)
	if err != nil {
		logger.Error("check balance failed",
			"user_id", req.UserID,
			"required_fee", requiredFee,
			"error", err)
		return nil, fmt.Errorf("check balance failed: %w", err)
	}

	return &dto.BalanceCheckResult{
		UserID:       req.UserID,
		Balance:      balance,
		RequiredFee:  requiredFee,
		IsSufficient: balance >= requiredFee,
	}, nil
}

func (s *BalanceService) CheckUserBalance(ctx context.Context, userID int64) (int64, error) {
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
