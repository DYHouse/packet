package algorithm

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"sync/atomic"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/reward"
)

type PacketGenerator struct {
	config            atomic.Pointer[Config]
	packetCache       repository.PacketCacheRepository
	rewardCache       repository.RewardCacheRepository
	straightGenerator atomic.Pointer[StraightGenerator]
	leopardGenerator  atomic.Pointer[LeopardGenerator]
	rewardController  atomic.Pointer[RewardController]
	roomRepo          repository.RoomRepository
}

func NewPacketGenerator(config *Config, packetCache repository.PacketCacheRepository, rewardCache repository.RewardCacheRepository, roomRepo repository.RoomRepository) *PacketGenerator {
	g := &PacketGenerator{
		packetCache: packetCache,
		rewardCache: rewardCache,
		roomRepo:    roomRepo,
	}
	g.config.Store(config)
	g.straightGenerator.Store(NewStraightGenerator(config))
	g.leopardGenerator.Store(NewLeopardGenerator(config))
	g.rewardController.Store(NewRewardController(config.RewardControl, rewardCache))
	return g
}

func (g *PacketGenerator) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResult, error) {
	// Load 一次快照，保证整个 Generate 期间使用同一份 config 与 generator，
	// 避免 nacos 推送在执行过程中替换 config 导致校验/计算不一致。
	config := g.config.Load()
	straightGen := g.straightGenerator.Load()
	leopardGen := g.leopardGenerator.Load()
	rewardCtrl := g.rewardController.Load()

	if err := g.validateRequest(config, req); err != nil {
		return nil, err
	}

	roomMeta, err := g.roomRepo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, err
	}

	cached, err := g.packetCache.Get(ctx, req.RoundID)
	if err == nil {
		var result GenerateResult
		if err := json.Unmarshal([]byte(cached), &result); err == nil {
			return &result, nil
		}
	}

	traceID := req.RoundID

	currentRoundNo := roomMeta.CurrentRound
	maxRounds := roomMeta.MaxRounds

	rewardType, _ := rewardCtrl.DetermineRewardType(
		ctx, req.RoomID, roomMeta.CurrentSessionID, currentRoundNo, maxRounds,
	)

	var result *GenerateResult
	var genErr error

	switch rewardType {
	case reward.RewardTypeStraight:
		result, genErr = straightGen.Generate(ctx, req, traceID)
		if genErr != nil {
			result, genErr = g.generateNormalPackets(config, req, traceID)
			if result != nil {
				result.RewardType = RewardTypeNone
			}
		}
	case reward.RewardTypeLeopard:
		result, genErr = leopardGen.Generate(ctx, req, traceID)
		if genErr != nil {
			result, genErr = g.generateNormalPackets(config, req, traceID)
			if result != nil {
				result.RewardType = RewardTypeNone
			}
		}
	default:
		result, genErr = g.generateNormalPackets(config, req, traceID)
	}

	if genErr != nil {
		return nil, genErr
	}

	resultJSON, _ := json.Marshal(result)
	success, err := g.packetCache.SetNX(ctx, req.RoundID, string(resultJSON), config.PacketCacheTTL)
	if err == nil && !success {
		if cached, err = g.packetCache.Get(ctx, req.RoundID); err == nil {
			json.Unmarshal([]byte(cached), &result)
		}
	}

	return result, nil
}

func (g *PacketGenerator) validateRequest(config *Config, req *GenerateRequest) error {
	if req.TotalAmount <= 0 {
		return NewError(ErrCodeInvalidTotalAmount, "total amount must be positive")
	}

	if req.PacketCount <= 0 || req.PacketCount > 100 {
		return NewError(ErrCodeInvalidPacketCount, "packet count must be between 1 and 100")
	}

	minTotal := int64(req.PacketCount) * config.MinPacketAmount
	if req.TotalAmount < minTotal {
		return NewError(ErrCodeAmountTooSmall, fmt.Sprintf("total amount must be at least %d", minTotal))
	}

	return nil
}

func (g *PacketGenerator) generateNormalPackets(config *Config, req *GenerateRequest, traceID string) (*GenerateResult, error) {
	amounts := make([]int64, req.PacketCount)

	minAmount := g.calculateDynamicMinAmount(config, req.TotalAmount, req.PacketCount)

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
		// PacketCount=1 时无需随机分配，全部金额给唯一红包，避免金额丢失
		amounts[0] = req.TotalAmount
	}

	g.shuffle(amounts)

	return &GenerateResult{
		PacketAmounts: amounts,
		RewardType:    RewardTypeNone,
		RewardAmount:  0,
		TraceID:       traceID,
	}, nil
}

func (g *PacketGenerator) calculateDynamicMinAmount(config *Config, totalAmount int64, packetCount int) int64 {
	avgAmount := totalAmount / int64(packetCount)

	configMin := config.MinPacketAmount

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

// randomInt 使用 crypto/rand 生成随机数。crand.Reader 本身并发安全，无需额外加锁。
func (g *PacketGenerator) randomInt(max int) int {
	if max <= 1 {
		return 0
	}
	n, _ := crand.Int(crand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}

func (g *PacketGenerator) randomRange(min, max int64) int64 {
	if min >= max {
		return min
	}

	rangeSize := max - min + 1
	randomOffset, _ := crand.Int(crand.Reader, big.NewInt(rangeSize))
	return min + randomOffset.Int64()
}

func (g *PacketGenerator) shuffle(arr []int64) {
	n := len(arr)
	for i := n - 1; i > 0; i-- {
		jBig, _ := crand.Int(crand.Reader, big.NewInt(int64(i+1)))
		j := int(jBig.Int64())
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func (g *PacketGenerator) ValidatePackets(amounts []int64, totalAmount int64) bool {
	config := g.config.Load()
	sum := int64(0)
	for _, amount := range amounts {
		if amount < config.MinPacketAmount {
			return false
		}
		sum += amount
	}
	return sum == totalAmount
}

func (g *PacketGenerator) RecordProfit(ctx context.Context, betAmount, winAmount, rewardAmount int64) error {
	return g.rewardController.Load().RecordProfit(ctx, betAmount, winAmount, rewardAmount)
}

func (g *PacketGenerator) OnSessionEnd(ctx context.Context, roomID, sessionID string) {
	g.rewardController.Load().OnSessionEnd(ctx, roomID, sessionID)
}

func (g *PacketGenerator) GetRewardController() *RewardController {
	return g.rewardController.Load()
}

// UpdateConfig 通过 atomic.Store 原子替换 config 与依赖 generator/controller。
// 读侧 (Generate) 通过 atomic.Load 拿到的是完整一致的一组指针，不会读到中间状态。
func (g *PacketGenerator) UpdateConfig(config *Config) {
	g.config.Store(config)
	g.straightGenerator.Store(NewStraightGenerator(config))
	g.leopardGenerator.Store(NewLeopardGenerator(config))
	g.rewardController.Store(NewRewardController(config.RewardControl, g.rewardCache))
}
