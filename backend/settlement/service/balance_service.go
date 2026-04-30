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
	platform      platform.Client
	cfg           *config.PlatformConfig
	userIDConvert *UserIDConvertService
}

func NewBalanceService(platformClient platform.Client, cfg *config.PlatformConfig, userIDConvert *UserIDConvertService) *BalanceService {
	if cfg == nil {
		cfg = config.DefaultPlatformConfig()
	}
	return &BalanceService{
		platform:      platformClient,
		cfg:           cfg,
		userIDConvert: userIDConvert,
	}
}

func (s *BalanceService) CheckBalanceForReady(ctx context.Context, req *dto.BalanceCheckRequest) (*dto.BalanceCheckResult, error) {
	requiredFee := s.CalculateRequiredFee(req.RoomFee, req.MaxPlayers, req.MaxRounds)

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
