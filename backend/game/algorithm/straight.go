package algorithm

import (
	"context"
	"math/rand"
	"sort"
	"time"
)

type StraightGenerator struct {
	config *Config
}

func NewStraightGenerator(config *Config) *StraightGenerator {
	return &StraightGenerator{config: config}
}

func (g *StraightGenerator) Generate(ctx context.Context, req *GenerateRequest, traceID string) (*GenerateResult, error) {
	n := int64(req.PacketCount)

	minIntSum := (1 + n) * n / 2

	minAmount := minIntSum * 100
	if req.TotalAmount < minAmount {
		return nil, NewError(ErrCodeAmountTooSmall, "total amount too small for straight pattern")
	}

	totalYuan := req.TotalAmount / 100

	startInt := (totalYuan - minIntSum) / n
	if startInt < 1 {
		startInt = 1
	}

	intSum := (startInt + startInt + n - 1) * n / 2 * 100

	remainingAmount := req.TotalAmount - intSum

	avgDecimal := remainingAmount / n
	extraDecimal := remainingAmount % n

	amounts := make([]int64, req.PacketCount)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	indices := r.Perm(int(n))

	for i := 0; i < req.PacketCount; i++ {
		intPart := (startInt + int64(i)) * 100

		decimalPart := avgDecimal
		if int64(indices[i]) < extraDecimal {
			decimalPart++
		}

		amounts[i] = intPart + decimalPart
	}

	rewardAmount := req.TotalAmount

	return &GenerateResult{
		PacketAmounts: amounts,
		RewardType:    RewardTypeStraight,
		RewardAmount:  rewardAmount,
		TraceID:       traceID,
	}, nil
}

func (g *StraightGenerator) Check(amounts []int64) bool {
	if len(amounts) < 2 {
		return false
	}

	intParts := make([]int64, len(amounts))
	for i, amount := range amounts {
		intParts[i] = amount / 100
	}

	sort.Slice(intParts, func(i, j int) bool {
		return intParts[i] < intParts[j]
	})

	for i := 1; i < len(intParts); i++ {
		if intParts[i] != intParts[i-1]+1 {
			return false
		}
	}

	return true
}
