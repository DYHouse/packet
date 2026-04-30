package algorithm

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"gorm.io/gorm"
)

type PacketGenerator struct {
	config            *Config
	redis             *cRedis.Client
	db                *gorm.DB
	straightGenerator *StraightGenerator
	leopardGenerator  *LeopardGenerator
	rewardController  *RewardController
	roomRepo          domain.RoomRepository
	rngMu             sync.Mutex
}

func NewPacketGenerator(config *Config, redis *cRedis.Client, db *gorm.DB, roomRepo domain.RoomRepository) *PacketGenerator {
	g := &PacketGenerator{
		config:   config,
		redis:    redis,
		db:       db,
		roomRepo: roomRepo,
	}
	g.straightGenerator = NewStraightGenerator(config)
	g.leopardGenerator = NewLeopardGenerator(config)
	g.rewardController = NewRewardController(config.RewardControl, redis)
	return g
}

func (g *PacketGenerator) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResult, error) {
	if err := g.validateRequest(req); err != nil {
		return nil, err
	}

	roomMeta, err := g.roomRepo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, err
	}

	cacheKey := redis.RoundPacketsKey(req.RoundID)

	cached, err := g.redis.Get(ctx, cacheKey).Result()
	if err == nil {
		var result GenerateResult
		if err := json.Unmarshal([]byte(cached), &result); err == nil {
			return &result, nil
		}
	}

	traceID := req.RoundID

	currentRoundNo := roomMeta.CurrentRound
	maxRounds := roomMeta.MaxRounds

	rewardType, _ := g.rewardController.DetermineRewardType(
		ctx, req.RoomID, roomMeta.CurrentSessionID, currentRoundNo, maxRounds,
	)

	var result *GenerateResult
	var genErr error

	switch rewardType {
	case RewardTypeStraight:
		result, genErr = g.generateStraightPackets(ctx, req, traceID)
		if genErr != nil {
			result, genErr = g.generateNormalPackets(ctx, req, traceID)
			if result != nil {
				result.RewardType = RewardTypeNone
			}
		}
	case RewardTypeLeopard:
		result, genErr = g.generateLeopardPackets(ctx, req, traceID)
		if genErr != nil {
			result, genErr = g.generateNormalPackets(ctx, req, traceID)
			if result != nil {
				result.RewardType = RewardTypeNone
			}
		}
	default:
		result, genErr = g.generateNormalPackets(ctx, req, traceID)
	}

	if genErr != nil {
		return nil, genErr
	}

	resultJSON, _ := json.Marshal(result)
	success, err := g.redis.SetNX(ctx, cacheKey, resultJSON, time.Hour).Result()
	if err == nil && !success {
		if cached, err = g.redis.Get(ctx, cacheKey).Result(); err == nil {
			json.Unmarshal([]byte(cached), &result)
		}
	}

	return result, nil
}

func (g *PacketGenerator) validateRequest(req *GenerateRequest) error {
	if req.TotalAmount <= 0 {
		return NewError(ErrCodeInvalidTotalAmount, "total amount must be positive")
	}

	if req.PacketCount <= 0 || req.PacketCount > 100 {
		return NewError(ErrCodeInvalidPacketCount, "packet count must be between 1 and 100")
	}

	minTotal := int64(req.PacketCount) * g.config.MinPacketAmount
	if req.TotalAmount < minTotal {
		return NewError(ErrCodeAmountTooSmall, fmt.Sprintf("total amount must be at least %d", minTotal))
	}

	return nil
}

func (g *PacketGenerator) generateNormalPackets(ctx context.Context, req *GenerateRequest, traceID string) (*GenerateResult, error) {
	amounts := make([]int64, req.PacketCount)

	minAmount := g.calculateDynamicMinAmount(req.TotalAmount, req.PacketCount)

	minIndex := g.randomInt(req.PacketCount)

	remaining := req.TotalAmount - minAmount
	otherCount := req.PacketCount - 1

	if otherCount > 0 {
		otherMinAmount := minAmount + 1
		tempAmounts := make([]int64, otherCount)
		tempRemaining := remaining

		for i := 0; i < otherCount-1; i++ {
			remainingCount := otherCount - i
			maxAmount := tempRemaining - int64(remainingCount-1)*otherMinAmount

			avgAmount := tempRemaining / int64(remainingCount)
			upperBound := avgAmount * 2
			if upperBound > maxAmount {
				upperBound = maxAmount
			}

			amount := g.randomRange(otherMinAmount, upperBound)
			tempAmounts[i] = amount
			tempRemaining -= amount
		}
		tempAmounts[otherCount-1] = tempRemaining

		j := 0
		for i := 0; i < req.PacketCount; i++ {
			if i == minIndex {
				amounts[i] = minAmount
			} else {
				amounts[i] = tempAmounts[j]
				j++
			}
		}
	} else {
		amounts[0] = minAmount
	}

	g.shuffle(amounts)

	return &GenerateResult{
		PacketAmounts: amounts,
		RewardType:    RewardTypeNone,
		RewardAmount:  0,
		TraceID:       traceID,
	}, nil
}

func (g *PacketGenerator) calculateDynamicMinAmount(totalAmount int64, packetCount int) int64 {
	avgAmount := totalAmount / int64(packetCount)

	configMin := g.config.MinPacketAmount

	maxPossibleMin := (totalAmount - int64(packetCount-1)) / int64(packetCount)
	if maxPossibleMin < configMin {
		return configMin
	}

	minLower := configMin
	minUpper := avgAmount / 3
	if minUpper < configMin {
		minUpper = configMin
	}
	if minUpper > maxPossibleMin {
		minUpper = maxPossibleMin
	}

	if minLower >= minUpper {
		return minLower
	}

	return g.randomRange(minLower, minUpper)
}

func (g *PacketGenerator) randomInt(max int) int {
	if max <= 1 {
		return 0
	}
	g.rngMu.Lock()
	defer g.rngMu.Unlock()
	n, _ := crand.Int(crand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}

func (g *PacketGenerator) randomRange(min, max int64) int64 {
	if min >= max {
		return min
	}

	g.rngMu.Lock()
	defer g.rngMu.Unlock()

	rangeSize := max - min + 1
	randomOffset, _ := crand.Int(crand.Reader, big.NewInt(rangeSize))
	return min + randomOffset.Int64()
}

func (g *PacketGenerator) shuffle(arr []int64) {
	g.rngMu.Lock()
	defer g.rngMu.Unlock()

	n := len(arr)
	for i := n - 1; i > 0; i-- {
		jBig, _ := crand.Int(crand.Reader, big.NewInt(int64(i+1)))
		j := int(jBig.Int64())
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func (g *PacketGenerator) ValidatePackets(amounts []int64, totalAmount int64) bool {
	sum := int64(0)
	for _, amount := range amounts {
		if amount < g.config.MinPacketAmount {
			return false
		}
		sum += amount
	}
	return sum == totalAmount
}

func (g *PacketGenerator) generateStraightPackets(ctx context.Context, req *GenerateRequest, traceID string) (*GenerateResult, error) {
	return g.straightGenerator.Generate(ctx, req, traceID)
}

func (g *PacketGenerator) generateLeopardPackets(ctx context.Context, req *GenerateRequest, traceID string) (*GenerateResult, error) {
	return g.leopardGenerator.Generate(ctx, req, traceID)
}

func (g *PacketGenerator) RecordProfit(ctx context.Context, betAmount, winAmount, rewardAmount int64) error {
	return g.rewardController.RecordProfit(ctx, betAmount, winAmount, rewardAmount)
}

func (g *PacketGenerator) OnSessionEnd(ctx context.Context, roomID, sessionID string) {
	g.rewardController.OnSessionEnd(ctx, roomID, sessionID)
}

func (g *PacketGenerator) GetRewardController() *RewardController {
	return g.rewardController
}

func (g *PacketGenerator) UpdateConfig(config *Config) {
	g.rngMu.Lock()
	defer g.rngMu.Unlock()
	g.config = config
	g.straightGenerator = NewStraightGenerator(config)
	g.leopardGenerator = NewLeopardGenerator(config)
	g.rewardController = NewRewardController(config.RewardControl, g.redis)
}
