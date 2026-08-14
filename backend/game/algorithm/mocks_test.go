package algorithm

import (
	"context"
	"sync"
	"time"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/room"
)

// packetCacheStub 红包缓存仓储桩件，用于测试 PacketGenerator 的缓存交互。
// 通过字段配置返回值，支持并发安全地记录调用次数。
type packetCacheStub struct {
	mu sync.Mutex
	// getResp 按 roundID 预置的缓存内容；未命中时返回 errGet
	getResp map[string]string
	// getErr Get 默认错误（模拟 redis.Nil 时使用）
	getErr error
	// setNXOk SetNX 是否成功
	setNXOk bool
	// setNXErr SetNX 返回的错误
	setNXErr error
	// setNXCalls 记录 SetNX 调用次数
	setNXCalls int
	// packetInfoResp 按 packetID 预置的红包详情
	packetInfoResp map[string]string
	// packetInfoErr GetPacketInfo 默认错误
	packetInfoErr error
	// availableIDs 按 roundID 预置的可用红包 ID 列表
	availableIDs map[string][]string
	// availableErr GetAvailablePacketIDs 默认错误
	availableErr error
}

func (s *packetCacheStub) Get(_ context.Context, _ string, roundID string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	if v, ok := s.getResp[roundID]; ok {
		return v, nil
	}
	return "", s.getErr
}

func (s *packetCacheStub) SetNX(_ context.Context, _, roundID, _ string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setNXCalls++
	if s.setNXErr != nil {
		return false, s.setNXErr
	}
	return s.setNXOk, nil
}

func (s *packetCacheStub) GetPacketInfo(_ context.Context, _ string, packetID string) (string, error) {
	if s.packetInfoErr != nil {
		return "", s.packetInfoErr
	}
	if v, ok := s.packetInfoResp[packetID]; ok {
		return v, nil
	}
	return "", s.packetInfoErr
}

func (s *packetCacheStub) GetAvailablePacketIDs(_ context.Context, _ string, roundID string) ([]string, error) {
	if s.availableErr != nil {
		return nil, s.availableErr
	}
	if ids, ok := s.availableIDs[roundID]; ok {
		return ids, nil
	}
	return nil, s.availableErr
}

// rewardCacheStub 奖励缓存仓储桩件，用于测试 RewardController。
type rewardCacheStub struct {
	mu sync.Mutex
	// cycleWon 按 "roomID:sessionID:type" 预置的已触发标记
	cycleWon map[string]int
	// cycleWonErr GetCycleWon 返回的错误
	cycleWonErr error
	// setCycleWonCalls 记录 SetCycleWon 调用次数
	setCycleWonCalls int
	// clearCalls 记录 ClearRewardCycles 调用次数
	clearCalls int
	// dailyBet / dailyWin / dailyReward 当日利润数据
	dailyBet    int64
	dailyWin    int64
	dailyReward int64
	dailyErr    error
	// recordCalls 记录 RecordDailyProfit 调用次数
	recordCalls int
	// lastRecordBet / lastRecordWin / lastRecordReward 最近一次上报的值
	lastRecordBet    int64
	lastRecordWin    int64
	lastRecordReward int64
}

func (s *rewardCacheStub) GetCycleWon(_ context.Context, _, _ string, _ repository.RewardCycleType) (int, error) {
	if s.cycleWonErr != nil {
		return 0, s.cycleWonErr
	}
	return 0, nil
}

func (s *rewardCacheStub) SetCycleWon(_ context.Context, _, _ string, _ repository.RewardCycleType, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCycleWonCalls++
	return nil
}

func (s *rewardCacheStub) ClearRewardCycles(_ context.Context, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCalls++
	return nil
}

func (s *rewardCacheStub) GetDailyProfit(_ context.Context, _ string) (int64, int64, int64, error) {
	if s.dailyErr != nil {
		return 0, 0, 0, s.dailyErr
	}
	return s.dailyBet, s.dailyWin, s.dailyReward, nil
}

func (s *rewardCacheStub) RecordDailyProfit(_ context.Context, _ string, bet, win, reward int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordCalls++
	s.lastRecordBet = bet
	s.lastRecordWin = win
	s.lastRecordReward = reward
	return nil
}

// roomRepoStub 房间仓储桩件。
// RoomRepository 接口方法众多，通过嵌入 nil 接口满足接口契约，
// 仅覆盖测试所需的 GetRoomMeta 方法，其余方法若被调用会 panic（测试 bug 预警）。
type roomRepoStub struct {
	repository.RoomRepository
	meta *room.RoomMeta
	err  error
}

func (r *roomRepoStub) GetRoomMeta(_ context.Context, _ string) (*room.RoomMeta, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.meta, nil
}

// newTestPacketGenerator 构造带桩件依赖的 PacketGenerator，便于测试复用。
func newTestPacketGenerator(cfg *Config, packetCache repository.PacketCacheRepository, rewardCache repository.RewardCacheRepository, roomRepo repository.RoomRepository) *PacketGenerator {
	return NewPacketGenerator(cfg, packetCache, rewardCache, roomRepo)
}
