package algorithm

import (
	"context"
	"errors"
	"testing"

	"github.com/cashparty/backend/game/domain/reward"
)

// TestRewardController_DetermineRewardType_NoRoomConfig 验证无房间配置时返回 None。
func TestRewardController_DetermineRewardType_NoRoomConfig(t *testing.T) {
	cfg := &RewardControlConfig{
		GlobalSwitchEnabled: true,
		RoomConfigs:         map[int64]*RoomRewardConfig{},
	}
	c := NewRewardController(cfg, &rewardCacheStub{})

	tests := []struct {
		name           string
		roomID         string
		sessionID      string
		currentRound   int
		maxRounds      int
		wantRewardType int
		wantTrigger    int
	}{
		{"无房间配置 - 返回 None", "999", "sess-1", 1, 5, RewardTypeNone, 0},
		{"空房间 ID - 返回 None", "", "sess-1", 1, 5, RewardTypeNone, 0},
		{"最后一局但无配置 - 返回 None", "999", "sess-1", 5, 5, RewardTypeNone, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, trigger := c.DetermineRewardType(
				context.Background(), tt.roomID, tt.sessionID, tt.currentRound, tt.maxRounds,
			)
			if rt != tt.wantRewardType {
				t.Errorf("rewardType = %d, want %d", rt, tt.wantRewardType)
			}
			if trigger != tt.wantTrigger {
				t.Errorf("trigger = %d, want %d", trigger, tt.wantTrigger)
			}
		})
	}
}

// TestRewardController_DetermineRewardType_EmptyConfig 验证空配置（无房间配置）不 panic 且返回 None。
func TestRewardController_DetermineRewardType_EmptyConfig(t *testing.T) {
	// 使用空但非 nil 的配置（生产代码 DetermineRewardType 在 config==nil 时存在 nil-deref，
	// 属预存问题，本测试覆盖空配置场景）
	cfg := &RewardControlConfig{
		GlobalSwitchEnabled: true,
		RoomConfigs:         map[int64]*RoomRewardConfig{},
	}
	c := NewRewardController(cfg, &rewardCacheStub{})

	rt, trigger := c.DetermineRewardType(
		context.Background(), "1", "sess-1", 1, 5,
	)
	if rt != RewardTypeNone {
		t.Errorf("rewardType = %d, want %d (None)", rt, RewardTypeNone)
	}
	if trigger != 0 {
		t.Errorf("trigger = %d, want 0", trigger)
	}
}

// TestRewardController_DetermineRewardType_GuaranteeStraight 验证保底顺子在最后一局触发。
func TestRewardController_DetermineRewardType_GuaranteeStraight(t *testing.T) {
	cfg := &RewardControlConfig{
		GlobalSwitchEnabled: true,
		RoomConfigs: map[int64]*RoomRewardConfig{
			1: {
				GuaranteeEnabled:  true,
				GuaranteeStraight: true,
				GuaranteeLeopard:  false,
			},
		},
	}
	cache := &rewardCacheStub{}
	c := NewRewardController(cfg, cache)

	// 最后一局（remainingRounds=1），shouldTriggerGuarantee 恒返回 true
	rt, trigger := c.DetermineRewardType(context.Background(), "1", "sess-1", 5, 5)
	if rt != reward.RewardTypeStraight {
		t.Errorf("rewardType = %d, want %d (Straight)", rt, reward.RewardTypeStraight)
	}
	if trigger != reward.TriggerTypeGuarantee {
		t.Errorf("trigger = %d, want %d (Guarantee)", trigger, reward.TriggerTypeGuarantee)
	}
	if cache.setCycleWonCalls == 0 {
		t.Errorf("SetCycleWon 应被调用")
	}
}

// TestRewardController_DetermineRewardType_GuaranteeLeopard 验证保底豹子在最后一局触发。
func TestRewardController_DetermineRewardType_GuaranteeLeopard(t *testing.T) {
	cfg := &RewardControlConfig{
		GlobalSwitchEnabled: true,
		RoomConfigs: map[int64]*RoomRewardConfig{
			1: {
				GuaranteeEnabled:  true,
				GuaranteeStraight: false,
				GuaranteeLeopard:  true,
			},
		},
	}
	cache := &rewardCacheStub{}
	c := NewRewardController(cfg, cache)

	// 最后一局，shouldTriggerGuarantee 恒返回 true
	rt, trigger := c.DetermineRewardType(context.Background(), "1", "sess-1", 5, 5)
	if rt != reward.RewardTypeLeopard {
		t.Errorf("rewardType = %d, want %d (Leopard)", rt, reward.RewardTypeLeopard)
	}
	if trigger != reward.TriggerTypeGuarantee {
		t.Errorf("trigger = %d, want %d (Guarantee)", trigger, reward.TriggerTypeGuarantee)
	}
}

// TestRewardController_DetermineRewardType_GuaranteeDisabled 验证保底关闭时不触发。
func TestRewardController_DetermineRewardType_GuaranteeDisabled(t *testing.T) {
	cfg := &RewardControlConfig{
		GlobalSwitchEnabled: true,
		RoomConfigs: map[int64]*RoomRewardConfig{
			1: {
				GuaranteeEnabled:  false,
				GuaranteeStraight: true,
				GuaranteeLeopard:  true,
				// ProbabilityEnabled=false，因此概率路径也不触发
			},
		},
	}
	c := NewRewardController(cfg, &rewardCacheStub{})

	rt, _ := c.DetermineRewardType(context.Background(), "1", "sess-1", 5, 5)
	if rt != RewardTypeNone {
		t.Errorf("rewardType = %d, want %d (None，保底与概率均关闭)", rt, RewardTypeNone)
	}
}

// TestRewardController_GetCurrentProfitRatio 验证利润率计算。
func TestRewardController_GetCurrentProfitRatio(t *testing.T) {
	tests := []struct {
		name      string
		cache     *rewardCacheStub
		wantRatio float64
		wantErr   bool
	}{
		{
			name: "零下注 - 返回 0",
			cache: &rewardCacheStub{
				dailyBet: 0,
			},
			wantRatio: 0,
		},
		{
			name: "正常利润 - profit=1000, bet=10000, ratio=0.1",
			cache: &rewardCacheStub{
				dailyBet:    10000,
				dailyWin:    3000,
				dailyReward: 6000,
			},
			wantRatio: 0.1, // (10000-3000-6000)/10000 = 0.1
		},
		{
			name: "负利润（亏损） - profit=-5000, bet=10000, ratio=-0.5",
			cache: &rewardCacheStub{
				dailyBet:    10000,
				dailyWin:    12000,
				dailyReward: 3000,
			},
			wantRatio: -0.5, // (10000-12000-3000)/10000 = -0.5
		},
		{
			name: "仓储错误 - 返回 error",
			cache: &rewardCacheStub{
				dailyErr: errors.New("redis unavailable"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewRewardController(&RewardControlConfig{}, tt.cache)
			ratio, err := c.GetCurrentProfitRatio(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ratio != tt.wantRatio {
				t.Errorf("ratio = %f, want %f", ratio, tt.wantRatio)
			}
		})
	}
}

// TestRewardController_RecordProfit 验证利润上报委托给仓储。
func TestRewardController_RecordProfit(t *testing.T) {
	cache := &rewardCacheStub{}
	c := NewRewardController(&RewardControlConfig{}, cache)

	err := c.RecordProfit(context.Background(), 10000, 3000, 6000)
	if err != nil {
		t.Fatalf("RecordProfit failed: %v", err)
	}
	if cache.recordCalls != 1 {
		t.Errorf("recordCalls = %d, want 1", cache.recordCalls)
	}
	if cache.lastRecordBet != 10000 {
		t.Errorf("lastRecordBet = %d, want 10000", cache.lastRecordBet)
	}
	if cache.lastRecordWin != 3000 {
		t.Errorf("lastRecordWin = %d, want 3000", cache.lastRecordWin)
	}
	if cache.lastRecordReward != 6000 {
		t.Errorf("lastRecordReward = %d, want 6000", cache.lastRecordReward)
	}
}

// TestRewardController_OnSessionEnd 验证会话结束清理委托给仓储。
func TestRewardController_OnSessionEnd(t *testing.T) {
	cache := &rewardCacheStub{}
	c := NewRewardController(&RewardControlConfig{}, cache)

	c.OnSessionEnd(context.Background(), "room-1", "sess-1")
	if cache.clearCalls != 1 {
		t.Errorf("clearCalls = %d, want 1", cache.clearCalls)
	}
}

// TestRewardController_ShouldTriggerGuarantee_LastRound 验证最后一局恒触发保底。
func TestRewardController_ShouldTriggerGuarantee_LastRound(t *testing.T) {
	c := NewRewardController(&RewardControlConfig{}, &rewardCacheStub{})

	// remainingRounds=1 (maxRounds - currentRound + 1 = 5-5+1=1)
	if !c.shouldTriggerGuarantee(1) {
		t.Errorf("shouldTriggerGuarantee(1) = false, want true（最后一局恒触发）")
	}
}

// TestRewardController_IsProbabilityAllowed 验证概率开关逻辑。
func TestRewardController_IsProbabilityAllowed(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *RewardControlConfig
		cache *rewardCacheStub
		want  bool
	}{
		{
			name: "全局开关关闭 - 不允许",
			cfg: &RewardControlConfig{
				GlobalSwitchEnabled: false,
			},
			cache: &rewardCacheStub{},
			want:  false,
		},
		{
			name: "开关开启且无阈值 - 允许",
			cfg: &RewardControlConfig{
				GlobalSwitchEnabled:  true,
				ProfitRatioThreshold: 0,
			},
			cache: &rewardCacheStub{},
			want:  true,
		},
		{
			name: "开关开启且利润率达标 - 允许",
			cfg: &RewardControlConfig{
				GlobalSwitchEnabled:  true,
				ProfitRatioThreshold: 0.05,
			},
			cache: &rewardCacheStub{
				dailyBet:    10000,
				dailyWin:    3000,
				dailyReward: 6000,
			},
			want: true, // ratio=0.1 > 0.05
		},
		{
			name: "开关开启但利润率不达标 - 不允许",
			cfg: &RewardControlConfig{
				GlobalSwitchEnabled:  true,
				ProfitRatioThreshold: 0.2,
			},
			cache: &rewardCacheStub{
				dailyBet:    10000,
				dailyWin:    3000,
				dailyReward: 6000,
			},
			want: false, // ratio=0.1 < 0.2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewRewardController(tt.cfg, tt.cache)
			got := c.isProbabilityAllowed(context.Background())
			if got != tt.want {
				t.Errorf("isProbabilityAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRewardController_RandomFloat 验证 randomFloat 返回值在 [0, 1) 区间。
// 因使用 crypto/rand，测试不变量而非具体值。
func TestRewardController_RandomFloat(t *testing.T) {
	c := NewRewardController(&RewardControlConfig{}, &rewardCacheStub{})

	for i := 0; i < 1000; i++ {
		v := c.randomFloat()
		if v < 0 || v >= 1 {
			t.Errorf("randomFloat() = %f, want in [0, 1)", v)
		}
	}
}

// TestRewardController_CheckProbability 验证概率奖励判定逻辑。
// LeopardProbability=1.0 时恒返回 Leopard；两者均为 0 时返回 None。
func TestRewardController_CheckProbability(t *testing.T) {
	tests := []struct {
		name string
		cfg  *RoomRewardConfig
		// 因为 randomFloat 使用 crypto/rand 不可控，此处通过极端概率值测试边界
		wantEither []int // 允许的返回值集合
	}{
		{
			name: "豹子概率=1.0 - 恒返回 Leopard",
			cfg: &RoomRewardConfig{
				LeopardProbability:  1.0,
				StraightProbability: 0,
			},
			wantEither: []int{reward.RewardTypeLeopard},
		},
		{
			name: "豹子概率=0 顺子概率=1.0 - 恒返回 Straight",
			cfg: &RoomRewardConfig{
				LeopardProbability:  0,
				StraightProbability: 1.0,
			},
			wantEither: []int{reward.RewardTypeStraight},
		},
		{
			name: "两者概率均为 0 - 恒返回 None",
			cfg: &RoomRewardConfig{
				LeopardProbability:  0,
				StraightProbability: 0,
			},
			wantEither: []int{RewardTypeNone},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewRewardController(&RewardControlConfig{}, &rewardCacheStub{})
			// 多次运行验证不变量
			for i := 0; i < 50; i++ {
				got := c.checkProbability(tt.cfg)
				found := false
				for _, w := range tt.wantEither {
					if got == w {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("checkProbability() = %d, want one of %v", got, tt.wantEither)
				}
			}
		})
	}
}

// TestDomainRewardConstants 验证领域奖励常量符合预期。
// 规约 P0-5：StraightRewardMultiplier/LeopardRewardMultiplier 必须定义在 game/domain。
func TestDomainRewardConstants(t *testing.T) {
	if reward.StraightRewardMultiplier != 1.0 {
		t.Errorf("StraightRewardMultiplier = %f, want 1.0", reward.StraightRewardMultiplier)
	}
	if reward.LeopardRewardMultiplier != 10.0 {
		t.Errorf("LeopardRewardMultiplier = %f, want 10.0", reward.LeopardRewardMultiplier)
	}
}

// TestDomainCalculateRewardAmount 验证领域奖励金额计算函数。
// 覆盖顺子（×1.0）、豹子（×10.0）、未知类型（返回 0）与零/负金额边界。
func TestDomainCalculateRewardAmount(t *testing.T) {
	tests := []struct {
		name        string
		rewardType  int
		totalAmount int64
		want        int64
	}{
		{
			name:        "顺子 - 奖励 = 本金 × 1.0",
			rewardType:  reward.RewardTypeStraight,
			totalAmount: 1000,
			want:        1000,
		},
		{
			name:        "豹子 - 奖励 = 本金 × 10.0",
			rewardType:  reward.RewardTypeLeopard,
			totalAmount: 1000,
			want:        10000,
		},
		{
			name:        "未知奖励类型 - 返回 0",
			rewardType:  99,
			totalAmount: 1000,
			want:        0,
		},
		{
			name:        "None 类型 - 返回 0",
			rewardType:  RewardTypeNone,
			totalAmount: 1000,
			want:        0,
		},
		{
			name:        "顺子零金额 - 返回 0",
			rewardType:  reward.RewardTypeStraight,
			totalAmount: 0,
			want:        0,
		},
		{
			name:        "豹子大金额 - 1000000 × 10 = 10000000",
			rewardType:  reward.RewardTypeLeopard,
			totalAmount: 1000000,
			want:        10000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reward.CalculateRewardAmount(tt.rewardType, tt.totalAmount)
			if got != tt.want {
				t.Errorf("CalculateRewardAmount(%d, %d) = %d, want %d",
					tt.rewardType, tt.totalAmount, got, tt.want)
			}
		})
	}
}

// TestDomainDetermineGameResult 验证游戏输赢判定。
func TestDomainDetermineGameResult(t *testing.T) {
	tests := []struct {
		name      string
		payOut    int64
		betAmount int64
		want      string
	}{
		{"派奖大于下注 - win", 2000, 1000, reward.GameResultWin},
		{"派奖等于下注 - lose", 1000, 1000, reward.GameResultLose},
		{"派奖小于下注 - lose", 500, 1000, reward.GameResultLose},
		{"零派奖 - lose", 0, 1000, reward.GameResultLose},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reward.DetermineGameResult(tt.payOut, tt.betAmount)
			if got != tt.want {
				t.Errorf("DetermineGameResult(%d, %d) = %q, want %q",
					tt.payOut, tt.betAmount, got, tt.want)
			}
		})
	}
}
