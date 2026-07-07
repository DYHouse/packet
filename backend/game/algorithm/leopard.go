package algorithm

import (
	"context"

	"github.com/cashparty/backend/game/domain"
)

type LeopardGenerator struct {
	config *Config
}

func NewLeopardGenerator(config *Config) *LeopardGenerator {
	return &LeopardGenerator{config: config}
}

func (g *LeopardGenerator) Generate(ctx context.Context, req *GenerateRequest, traceID string) (*GenerateResult, error) {
	// 入口防御性校验，避免 PacketCount<=0 导致除零 panic
	if req.PacketCount <= 0 || req.TotalAmount <= 0 {
		return nil, NewError(ErrCodeInvalidPacketCount, "invalid packet count or total amount")
	}

	if req.TotalAmount%int64(req.PacketCount) != 0 {
		return nil, NewError(ErrCodeAmountTooSmall, "total amount cannot be evenly divided for leopard pattern")
	}

	baseAmount := req.TotalAmount / int64(req.PacketCount)

	if baseAmount < g.config.MinPacketAmount {
		return nil, NewError(ErrCodeAmountTooSmall, "base amount too small for leopard pattern")
	}

	amounts := make([]int64, req.PacketCount)
	for i := 0; i < req.PacketCount; i++ {
		amounts[i] = baseAmount
	}

	rewardAmount := req.TotalAmount * g.config.RewardControl.LeopardMultiplier

	return &GenerateResult{
		PacketAmounts: amounts,
		RewardType:    domain.RewardTypeLeopard,
		RewardAmount:  rewardAmount,
		TraceID:       traceID,
	}, nil
}

func (g *LeopardGenerator) Check(amounts []int64) bool {
	if len(amounts) < 2 {
		return false
	}

	first := amounts[0]
	for _, amount := range amounts[1:] {
		if amount != first {
			return false
		}
	}

	return true
}
