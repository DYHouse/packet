package algorithm

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cashparty/backend/game/domain/reward"
)

// TestLeopardGenerator_Generate_Success 验证豹子模式生成成功。
// 所有红包金额相等，总和守恒，数量匹配。
func TestLeopardGenerator_Generate_Success(t *testing.T) {
	cfg := DefaultConfig()
	g := NewLeopardGenerator(cfg)

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
		wantBase    int64
	}{
		{"5 个红包 1000 分 - 每个 200", 1000, 5, 200},
		{"3 个红包 900 分 - 每个 300", 900, 3, 300},
		{"10 个红包 10000 分 - 每个 1000", 10000, 10, 1000},
		{"2 个红包 100 分 - 每个 50", 100, 2, 50},
		{"1 个红包 500 分 - 每个 500", 500, 1, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			result, err := g.Generate(context.Background(), req, "leopard-trace")
			if err != nil {
				t.Fatalf("Generate failed: %v", err)
			}
			if len(result.PacketAmounts) != tt.packetCount {
				t.Fatalf("红包数量 = %d, want %d", len(result.PacketAmounts), tt.packetCount)
			}
			var sum int64
			for i, amt := range result.PacketAmounts {
				if amt != tt.wantBase {
					t.Errorf("packet[%d] = %d, want %d (豹子模式所有金额应相等)", i, amt, tt.wantBase)
				}
				sum += amt
			}
			if sum != tt.totalAmount {
				t.Errorf("总金额 = %d, want %d", sum, tt.totalAmount)
			}
			if result.RewardType != reward.RewardTypeLeopard {
				t.Errorf("RewardType = %d, want %d (Leopard)", result.RewardType, reward.RewardTypeLeopard)
			}
			if result.TraceID != "leopard-trace" {
				t.Errorf("TraceID = %q, want %q", result.TraceID, "leopard-trace")
			}
		})
	}
}

// TestLeopardGenerator_Generate_RewardAmount 验证豹子奖励金额 = 本金 × LeopardMultiplier。
// 规约 P0-5：LeopardRewardMultiplier 定义在 game/domain（值为 10.0）。
// 默认配置的 LeopardMultiplier 必须与 reward.LeopardRewardMultiplier 一致。
func TestLeopardGenerator_Generate_RewardAmount(t *testing.T) {
	cfg := DefaultConfig()
	g := NewLeopardGenerator(cfg)

	// 验证默认配置的 LeopardMultiplier 与 domain 常量一致
	if cfg.RewardControl.LeopardMultiplier != int64(reward.LeopardRewardMultiplier) {
		t.Fatalf("默认 LeopardMultiplier = %d, reward.LeopardRewardMultiplier = %f, 二者应一致",
			cfg.RewardControl.LeopardMultiplier, reward.LeopardRewardMultiplier)
	}

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
	}{
		{"1000 分 - 奖励 10000", 1000, 5},
		{"500 分 - 奖励 5000", 500, 5},
		{"10000 分 - 奖励 100000", 10000, 10},
		{"100 分 - 奖励 1000", 100, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			result, err := g.Generate(context.Background(), req, "trace")
			if err != nil {
				t.Fatalf("Generate failed: %v", err)
			}
			wantReward := tt.totalAmount * int64(reward.LeopardRewardMultiplier)
			if result.RewardAmount != wantReward {
				t.Errorf("RewardAmount = %d, want %d (totalAmount × LeopardRewardMultiplier)",
					result.RewardAmount, wantReward)
			}
			// 同时验证与 reward.CalculateRewardAmount 一致
			domainReward := reward.CalculateRewardAmount(reward.RewardTypeLeopard, tt.totalAmount)
			if result.RewardAmount != domainReward {
				t.Errorf("RewardAmount = %d, reward.CalculateRewardAmount = %d, 二者应一致",
					result.RewardAmount, domainReward)
			}
		})
	}
}

// TestLeopardGenerator_Generate_InvalidPacketCount 验证非法红包数量返回错误。
func TestLeopardGenerator_Generate_InvalidPacketCount(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
		wantErrCode int
	}{
		{"数量为零", 1000, 0, ErrCodeInvalidPacketCount},
		{"数量为负", 1000, -1, ErrCodeInvalidPacketCount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			_, err := g.Generate(context.Background(), req, "trace")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var algErr *Error
			if !errors.As(err, &algErr) {
				t.Fatalf("expected *algorithm.Error, got %T: %v", err, err)
			}
			if algErr.Code != tt.wantErrCode {
				t.Errorf("error code = %d, want %d", algErr.Code, tt.wantErrCode)
			}
		})
	}
}

// TestLeopardGenerator_Generate_InvalidTotalAmount 验证非法总金额返回错误。
func TestLeopardGenerator_Generate_InvalidTotalAmount(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
		wantErrCode int
	}{
		{"零金额", 0, 5, ErrCodeInvalidPacketCount},
		{"负金额", -100, 5, ErrCodeInvalidPacketCount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			_, err := g.Generate(context.Background(), req, "trace")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var algErr *Error
			if !errors.As(err, &algErr) {
				t.Fatalf("expected *algorithm.Error, got %T: %v", err, err)
			}
			if algErr.Code != tt.wantErrCode {
				t.Errorf("error code = %d, want %d", algErr.Code, tt.wantErrCode)
			}
		})
	}
}

// TestLeopardGenerator_Generate_NotDivisible 验证金额不能整除时返回错误。
func TestLeopardGenerator_Generate_NotDivisible(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
	}{
		{"1000 分 3 包 - 不能整除", 1000, 3},
		{"100 分 3 包 - 不能整除", 100, 3},
		{"7 分 2 包 - 不能整除", 7, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			_, err := g.Generate(context.Background(), req, "trace")
			if err == nil {
				t.Fatalf("expected error for non-divisible amount")
			}
			var algErr *Error
			if !errors.As(err, &algErr) {
				t.Fatalf("expected *algorithm.Error, got %T: %v", err, err)
			}
			if algErr.Code != ErrCodeAmountTooSmall {
				t.Errorf("error code = %d, want %d", algErr.Code, ErrCodeAmountTooSmall)
			}
		})
	}
}

// TestLeopardGenerator_Generate_BaseAmountTooSmall 验证基础金额过小时返回错误。
func TestLeopardGenerator_Generate_BaseAmountTooSmall(t *testing.T) {
	// 配置最小金额为 10
	cfg := DefaultConfig()
	cfg.MinPacketAmount = 10
	g := NewLeopardGenerator(cfg)

	// 5 分 ÷ 5 包 = 1 分 < 10，应报错
	req := &GenerateRequest{TotalAmount: 5, PacketCount: 5}
	_, err := g.Generate(context.Background(), req, "trace")
	if err == nil {
		t.Fatalf("expected error for base amount too small")
	}
	var algErr *Error
	if !errors.As(err, &algErr) {
		t.Fatalf("expected *algorithm.Error, got %T: %v", err, err)
	}
	if algErr.Code != ErrCodeAmountTooSmall {
		t.Errorf("error code = %d, want %d", algErr.Code, ErrCodeAmountTooSmall)
	}
}

// TestLeopardGenerator_Generate_Concurrent 验证并发生成的安全性。
// 规约 §13.10：MUST 使用 -race 检测并发安全。
func TestLeopardGenerator_Generate_Concurrent(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	const goroutines = 100
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*opsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				req := &GenerateRequest{TotalAmount: 1000, PacketCount: 5}
				result, err := g.Generate(context.Background(), req, "trace")
				if err != nil {
					errCh <- err
					return
				}
				if len(result.PacketAmounts) != 5 {
					errCh <- errors.New("红包数量不匹配")
					return
				}
				if result.PacketAmounts[0] != 200 {
					errCh <- errors.New("豹子金额不正确")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("并发生成错误: %v", err)
	}
}

// TestLeopardGenerator_Check 验证豹子模式检测逻辑。
func TestLeopardGenerator_Check(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	tests := []struct {
		name     string
		amounts  []int64
		expected bool
	}{
		{
			name:     "豹子 - 全部相等（5 个 200）",
			amounts:  []int64{200, 200, 200, 200, 200},
			expected: true,
		},
		{
			name:     "豹子 - 全部相等（3 个 100）",
			amounts:  []int64{100, 100, 100},
			expected: true,
		},
		{
			name:     "豹子 - 两个相等",
			amounts:  []int64{500, 500},
			expected: true,
		},
		{
			name:     "非豹子 - 不全相等",
			amounts:  []int64{100, 200, 300, 200, 100},
			expected: false,
		},
		{
			name:     "非豹子 - 仅首尾不同",
			amounts:  []int64{200, 200, 200, 200, 201},
			expected: false,
		},
		{
			name:     "单个红包 - 不是豹子（需 ≥2）",
			amounts:  []int64{100},
			expected: false,
		},
		{
			name:     "空数组 - 不是豹子",
			amounts:  []int64{},
			expected: false,
		},
		{
			name:     "nil 数组 - 不是豹子",
			amounts:  nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.Check(tt.amounts)
			if got != tt.expected {
				t.Errorf("Check(%v) = %v, want %v", tt.amounts, got, tt.expected)
			}
		})
	}
}

// TestLeopardGenerator_GenerateAndCheck 验证生成的豹子红包能通过 Check 检测。
func TestLeopardGenerator_GenerateAndCheck(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	req := &GenerateRequest{TotalAmount: 1000, PacketCount: 5}
	result, err := g.Generate(context.Background(), req, "leopard-trace")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !g.Check(result.PacketAmounts) {
		t.Errorf("生成的豹子红包未通过 Check 检测: %v", result.PacketAmounts)
	}
}

// TestLeopardGenerator_Generate_SinglePacket 验证单红包豹子模式。
func TestLeopardGenerator_Generate_SinglePacket(t *testing.T) {
	g := NewLeopardGenerator(DefaultConfig())

	req := &GenerateRequest{TotalAmount: 500, PacketCount: 1}
	result, err := g.Generate(context.Background(), req, "trace")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(result.PacketAmounts) != 1 {
		t.Fatalf("红包数量 = %d, want 1", len(result.PacketAmounts))
	}
	if result.PacketAmounts[0] != 500 {
		t.Errorf("单红包金额 = %d, want 500", result.PacketAmounts[0])
	}
	// 奖励金额 = 500 × 10 = 5000
	if result.RewardAmount != 5000 {
		t.Errorf("RewardAmount = %d, want 5000", result.RewardAmount)
	}
}
