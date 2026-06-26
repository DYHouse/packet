package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/config"
	"github.com/cashparty/backend/settlement/dto"
)

type BalanceService struct {
	platform          platform.Client
	cfg               *config.PlatformConfig
	userIDConvert     *UserIDConvertService
	virtualBalanceSvc *VirtualBalanceService
	robotChecker      RobotChecker
}

func NewBalanceService(platformClient platform.Client, cfg *config.PlatformConfig, userIDConvert *UserIDConvertService, virtualBalanceSvc *VirtualBalanceService, robotChecker RobotChecker) *BalanceService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &BalanceService{
		platform:          platformClient,
		cfg:               cfg,
		userIDConvert:     userIDConvert,
		virtualBalanceSvc: virtualBalanceSvc,
		robotChecker:      robotChecker,
	}
}

func (s *BalanceService) CheckBalanceForReady(ctx context.Context, req *dto.BalanceCheckRequest) (*dto.BalanceCheckResult, error) {
	requiredFee := s.CalculateRequiredFee(req.RoomFee, req.MaxPlayers, req.MaxRounds)

	// 检查是否为机器人，使用虚拟余额
	if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, req.UserID) {
		if s.virtualBalanceSvc != nil {
			balance, err := s.virtualBalanceSvc.GetBalance(ctx, req.UserID)
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

func (s *BalanceService) CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64 {
	if maxPlayers <= 0 || maxRounds <= 0 {
		return 0
	}

	firstRoundFee := roomFee / int64(maxPlayers)
	laterRoundsFee := roomFee * int64(maxRounds-1)

	return firstRoundFee + laterRoundsFee
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
