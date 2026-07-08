package algorithm

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cashparty/backend/game/domain/room"
)

// TestPacketGenerator_ValidateRequest 校验请求参数合法性。
// 覆盖正常路径与各类非法输入（金额/数量/最小总额）。
func TestPacketGenerator_ValidateRequest(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	tests := []struct {
		name        string
		req         *GenerateRequest
		wantErrCode int
		wantErr     bool
	}{
		{
			name:        "总金额为零 - 非法",
			req:         &GenerateRequest{TotalAmount: 0, PacketCount: 5},
			wantErr:     true,
			wantErrCode: ErrCodeInvalidTotalAmount,
		},
		{
			name:        "总金额为负 - 非法",
			req:         &GenerateRequest{TotalAmount: -100, PacketCount: 5},
			wantErr:     true,
			wantErrCode: ErrCodeInvalidTotalAmount,
		},
		{
			name:        "红包数量为零 - 非法",
			req:         &GenerateRequest{TotalAmount: 1000, PacketCount: 0},
			wantErr:     true,
			wantErrCode: ErrCodeInvalidPacketCount,
		},
		{
			name:        "红包数量为负 - 非法",
			req:         &GenerateRequest{TotalAmount: 1000, PacketCount: -1},
			wantErr:     true,
			wantErrCode: ErrCodeInvalidPacketCount,
		},
		{
			name:        "红包数量超过 100 - 非法",
			req:         &GenerateRequest{TotalAmount: 100000, PacketCount: 101},
			wantErr:     true,
			wantErrCode: ErrCodeInvalidPacketCount,
		},
		{
			name:        "总金额小于最小总额（count × min） - 非法",
			req:         &GenerateRequest{TotalAmount: 3, PacketCount: 5},
			wantErr:     true,
			wantErrCode: ErrCodeAmountTooSmall,
		},
		{
			name:    "正常请求 - 合法",
			req:     &GenerateRequest{TotalAmount: 1000, PacketCount: 5},
			wantErr: false,
		},
		{
			name:    "最小合法边界 - 单红包 1 分",
			req:     &GenerateRequest{TotalAmount: 1, PacketCount: 1},
			wantErr: false,
		},
		{
			name:    "最大合法红包数量 100",
			req:     &GenerateRequest{TotalAmount: 100000, PacketCount: 100},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.validateRequest(cfg, tt.req)
			if tt.wantErr {
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
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestPacketGenerator_GenerateNormalPackets_TotalAmountCorrect 验证拆分后总金额守恒。
// 因使用 crypto/rand，此处测试不变量（总和、数量、最小值）而非具体值。
func TestPacketGenerator_GenerateNormalPackets_TotalAmountCorrect(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
	}{
		{"5 个红包 1000 分", 1000, 5},
		{"3 个红包 800 分", 800, 3},
		{"10 个红包 10000 分", 10000, 10},
		{"2 个红包 100 分", 100, 2},
		{"20 个红包 50000 分", 50000, 20},
		{"100 个红包 1000000 分（大金额）", 1000000, 100},
		{"5 个红包 5 分（最小金额边界）", 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			result, err := g.generateNormalPackets(cfg, req, "test-trace")
			if err != nil {
				t.Fatalf("generateNormalPackets failed: %v", err)
			}

			var sum int64
			for _, amt := range result.PacketAmounts {
				sum += amt
			}
			if sum != tt.totalAmount {
				t.Errorf("总金额 = %d, want %d (amounts=%v)", sum, tt.totalAmount, result.PacketAmounts)
			}
		})
	}
}

// TestPacketGenerator_GenerateNormalPackets_CountMatches 验证生成红包数量与请求数量一致。
func TestPacketGenerator_GenerateNormalPackets_CountMatches(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
	}{
		{"单红包", 1000, 1},
		{"两个红包", 1000, 2},
		{"五个红包", 1000, 5},
		{"十个红包", 10000, 10},
		{"一百个红包", 100000, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: tt.packetCount}
			result, err := g.generateNormalPackets(cfg, req, "test-trace")
			if err != nil {
				t.Fatalf("generateNormalPackets failed: %v", err)
			}
			if len(result.PacketAmounts) != tt.packetCount {
				t.Errorf("红包数量 = %d, want %d", len(result.PacketAmounts), tt.packetCount)
			}
		})
	}
}

// TestPacketGenerator_GenerateNormalPackets_RespectsMinAmount 验证每个红包不低于配置最小金额。
func TestPacketGenerator_GenerateNormalPackets_RespectsMinAmount(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	// 多次运行以覆盖 crypto/rand 的随机性
	for run := 0; run < 50; run++ {
		req := &GenerateRequest{TotalAmount: 1000, PacketCount: 10}
		result, err := g.generateNormalPackets(cfg, req, "test-trace")
		if err != nil {
			t.Fatalf("run %d: generateNormalPackets failed: %v", run, err)
		}
		for i, amt := range result.PacketAmounts {
			if amt < cfg.MinPacketAmount {
				t.Fatalf("run %d: packet[%d] = %d, 小于最小金额 %d", run, i, amt, cfg.MinPacketAmount)
			}
		}
	}
}

// TestPacketGenerator_GenerateNormalPackets_SinglePacket 验证单红包场景：全部金额归唯一红包。
func TestPacketGenerator_GenerateNormalPackets_SinglePacket(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	tests := []struct {
		name        string
		totalAmount int64
	}{
		{"单红包 1 分（最小）", 1},
		{"单红包 100 分", 100},
		{"单红包 1000000 分（大金额）", 1000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{TotalAmount: tt.totalAmount, PacketCount: 1}
			result, err := g.generateNormalPackets(cfg, req, "test-trace")
			if err != nil {
				t.Fatalf("generateNormalPackets failed: %v", err)
			}
			if len(result.PacketAmounts) != 1 {
				t.Fatalf("红包数量 = %d, want 1", len(result.PacketAmounts))
			}
			if result.PacketAmounts[0] != tt.totalAmount {
				t.Errorf("单红包金额 = %d, want %d", result.PacketAmounts[0], tt.totalAmount)
			}
		})
	}
}

// TestPacketGenerator_GenerateNormalPackets_RewardTypeNone 验证普通拆分不返回奖励类型。
func TestPacketGenerator_GenerateNormalPackets_RewardTypeNone(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	req := &GenerateRequest{TotalAmount: 1000, PacketCount: 5}
	result, err := g.generateNormalPackets(cfg, req, "test-trace")
	if err != nil {
		t.Fatalf("generateNormalPackets failed: %v", err)
	}
	if result.RewardType != RewardTypeNone {
		t.Errorf("RewardType = %d, want %d (RewardTypeNone)", result.RewardType, RewardTypeNone)
	}
	if result.RewardAmount != 0 {
		t.Errorf("RewardAmount = %d, want 0", result.RewardAmount)
	}
	if result.TraceID != "test-trace" {
		t.Errorf("TraceID = %q, want %q", result.TraceID, "test-trace")
	}
}

// TestPacketGenerator_GenerateNormalPackets_Concurrent 验证并发生成的安全性。
// 规约 §13.10：MUST 使用 -race 检测并发安全，≥100 goroutine。
func TestPacketGenerator_GenerateNormalPackets_Concurrent(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	const goroutines = 100
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*opsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				req := &GenerateRequest{TotalAmount: 1000, PacketCount: 10}
				result, err := g.generateNormalPackets(cfg, req, "test-trace")
				if err != nil {
					errCh <- err
					return
				}
				var sum int64
				for _, amt := range result.PacketAmounts {
					sum += amt
				}
				if sum != 1000 {
					errCh <- errors.New("总金额不守恒")
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

// TestPacketGenerator_ValidatePackets 验证 ValidatePackets 校验逻辑。
func TestPacketGenerator_ValidatePackets(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	tests := []struct {
		name        string
		amounts     []int64
		totalAmount int64
		want        bool
	}{
		{
			name:        "合法 - 总和匹配且不小于最小值",
			amounts:     []int64{100, 200, 300, 400},
			totalAmount: 1000,
			want:        true,
		},
		{
			name:        "非法 - 总和不匹配",
			amounts:     []int64{100, 200, 300, 300},
			totalAmount: 1000,
			want:        false,
		},
		{
			name:        "非法 - 单个红包小于最小金额",
			amounts:     []int64{0, 500, 500},
			totalAmount: 1000,
			want:        false,
		},
		{
			name:        "合法 - 单红包",
			amounts:     []int64{1000},
			totalAmount: 1000,
			want:        true,
		},
		{
			name:        "合法 - 空数组与零总额",
			amounts:     []int64{},
			totalAmount: 0,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.ValidatePackets(tt.amounts, tt.totalAmount)
			if got != tt.want {
				t.Errorf("ValidatePackets() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPacketGenerator_CalculateDynamicMinAmount 验证动态最小金额计算逻辑。
func TestPacketGenerator_CalculateDynamicMinAmount(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	tests := []struct {
		name        string
		totalAmount int64
		packetCount int
		wantMin     int64
		wantMax     int64
	}{
		{
			name:        "5 个红包 1000 分 - 最小值在 [1, 66] 区间",
			totalAmount: 1000,
			packetCount: 5,
			wantMin:     1,
			wantMax:     66,
		},
		{
			name:        "10 个红包 10000 分 - 最小值在 [1, 333] 区间",
			totalAmount: 10000,
			packetCount: 10,
			wantMin:     1,
			wantMax:     333,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 多次运行验证 crypto/rand 生成结果落在预期区间
			for run := 0; run < 30; run++ {
				got := g.calculateDynamicMinAmount(cfg, tt.totalAmount, tt.packetCount)
				if got < tt.wantMin || got > tt.wantMax {
					t.Errorf("run %d: calculateDynamicMinAmount() = %d, want in [%d, %d]",
						run, got, tt.wantMin, tt.wantMax)
				}
			}
		})
	}
}

// TestPacketGenerator_UpdateConfig 验证原子配置更新后行为一致。
func TestPacketGenerator_UpdateConfig(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	// 更新配置
	newCfg := DefaultConfig()
	newCfg.MinPacketAmount = 5
	g.UpdateConfig(newCfg)

	// 验证新配置生效：用小于新最小总额的请求触发错误
	req := &GenerateRequest{TotalAmount: 10, PacketCount: 5}
	err := g.validateRequest(newCfg, req)
	if err == nil {
		t.Fatalf("更新配置后应校验失败（总额 < count × min）")
	}

	// 验证 ValidatePackets 使用新配置
	if g.ValidatePackets([]int64{1, 9}, 10) {
		t.Errorf("更新 MinPacketAmount=5 后，金额 1 应判定为非法")
	}
	if !g.ValidatePackets([]int64{5, 5}, 10) {
		t.Errorf("更新 MinPacketAmount=5 后，金额 5 应判定为合法")
	}
}

// TestPacketGenerator_UpdateConfig_Concurrent 验证并发更新配置的安全性。
func TestPacketGenerator_UpdateConfig_Concurrent(t *testing.T) {
	cfg := DefaultConfig()
	g := NewPacketGenerator(cfg, nil, nil, nil)

	const goroutines = 50
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			newCfg := DefaultConfig()
			newCfg.MinPacketAmount = int64(1 + (n % 10))
			g.UpdateConfig(newCfg)
		}(i)
	}
	wg.Wait()
}

// TestPacketGenerator_Generate_CacheHit 验证缓存命中时直接返回缓存结果。
func TestPacketGenerator_Generate_CacheHit(t *testing.T) {
	cfg := DefaultConfig()
	cachedJSON := `{"packet_amounts":[100,200,300,400],"reward_type":0,"reward_amount":0,"trace_id":"cached-trace"}`
	cache := &packetCacheStub{
		getResp:  map[string]string{"round-1": cachedJSON},
		setNXOk:  false,
		setNXErr: nil,
	}
	roomRepo := &roomRepoStub{
		meta: &room.RoomMeta{RoomID: "room-1", CurrentRound: 1, MaxRounds: 5},
	}
	g := newTestPacketGenerator(cfg, cache, &rewardCacheStub{}, roomRepo)

	req := &GenerateRequest{TotalAmount: 1000, PacketCount: 4, RoomID: "room-1", RoundID: "round-1"}
	result, err := g.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if result.TraceID != "cached-trace" {
		t.Errorf("TraceID = %q, want %q (应返回缓存)", result.TraceID, "cached-trace")
	}
	if len(result.PacketAmounts) != 4 {
		t.Errorf("红包数量 = %d, want 4", len(result.PacketAmounts))
	}
}

// TestPacketGenerator_Generate_RoomRepoError 验证房间仓储查询失败时返回错误。
func TestPacketGenerator_Generate_RoomRepoError(t *testing.T) {
	cfg := DefaultConfig()
	roomRepo := &roomRepoStub{
		err: errors.New("redis unavailable"),
	}
	g := newTestPacketGenerator(cfg, &packetCacheStub{}, &rewardCacheStub{}, roomRepo)

	req := &GenerateRequest{TotalAmount: 1000, PacketCount: 5, RoomID: "room-1", RoundID: "round-1"}
	_, err := g.Generate(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error when room repo fails")
	}
}

// TestPacketGenerator_Generate_InvalidRequest 验证 Generate 入口对非法请求返回错误。
func TestPacketGenerator_Generate_InvalidRequest(t *testing.T) {
	cfg := DefaultConfig()
	g := newTestPacketGenerator(cfg, &packetCacheStub{}, &rewardCacheStub{}, &roomRepoStub{})

	tests := []struct {
		name string
		req  *GenerateRequest
	}{
		{"零金额", &GenerateRequest{TotalAmount: 0, PacketCount: 5}},
		{"零数量", &GenerateRequest{TotalAmount: 1000, PacketCount: 0}},
		{"数量超限", &GenerateRequest{TotalAmount: 1000, PacketCount: 200}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := g.Generate(context.Background(), tt.req)
			if err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

// TestPacketGenerator_Generate_NormalPath 验证正常生成路径（无缓存命中、无奖励触发）。
func TestPacketGenerator_Generate_NormalPath(t *testing.T) {
	cfg := DefaultConfig()
	// 配置中不含房间配置，DetermineRewardType 返回 None
	cache := &packetCacheStub{
		setNXOk: true,
	}
	roomRepo := &roomRepoStub{
		meta: &room.RoomMeta{RoomID: "room-1", CurrentRound: 1, MaxRounds: 5},
	}
	g := newTestPacketGenerator(cfg, cache, &rewardCacheStub{}, roomRepo)

	req := &GenerateRequest{TotalAmount: 1000, PacketCount: 5, RoomID: "room-1", RoundID: "round-1"}
	result, err := g.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(result.PacketAmounts) != 5 {
		t.Errorf("红包数量 = %d, want 5", len(result.PacketAmounts))
	}
	var sum int64
	for _, amt := range result.PacketAmounts {
		sum += amt
	}
	if sum != 1000 {
		t.Errorf("总金额 = %d, want 1000", sum)
	}
	if result.RewardType != RewardTypeNone {
		t.Errorf("RewardType = %d, want %d", result.RewardType, RewardTypeNone)
	}
}

// TestPacketGenerator_RandomInt 验证 randomInt 返回值在 [0, max) 区间内。
func TestPacketGenerator_RandomInt(t *testing.T) {
	g := &PacketGenerator{}

	tests := []int{2, 5, 10, 100, 1000}
	for _, max := range tests {
		t.Run("max="+itoa(max), func(t *testing.T) {
			for i := 0; i < 100; i++ {
				got := g.randomInt(max)
				if got < 0 || got >= max {
					t.Errorf("randomInt(%d) = %d, want in [0, %d)", max, got, max)
				}
			}
		})
	}
}

// TestPacketGenerator_RandomInt_EdgeCases 验证 randomInt 边界输入。
func TestPacketGenerator_RandomInt_EdgeCases(t *testing.T) {
	g := &PacketGenerator{}

	// max <= 1 时应恒返回 0
	for _, max := range []int{0, 1, -1} {
		got := g.randomInt(max)
		if got != 0 {
			t.Errorf("randomInt(%d) = %d, want 0", max, got)
		}
	}
}

// TestPacketGenerator_RandomRange 验证 randomRange 返回值在 [min, max] 区间内。
func TestPacketGenerator_RandomRange(t *testing.T) {
	g := &PacketGenerator{}

	tests := []struct {
		min, max int64
	}{
		{1, 10},
		{100, 1000},
		{1, 2},
	}

	for _, tt := range tests {
		t.Run("range", func(t *testing.T) {
			for i := 0; i < 100; i++ {
				got := g.randomRange(tt.min, tt.max)
				if got < tt.min || got > tt.max {
					t.Errorf("randomRange(%d, %d) = %d, want in [%d, %d]",
						tt.min, tt.max, got, tt.min, tt.max)
				}
			}
		})
	}
}

// TestPacketGenerator_RandomRange_MinGreaterEqualMax 验证 min >= max 时返回 min。
func TestPacketGenerator_RandomRange_MinGreaterEqualMax(t *testing.T) {
	g := &PacketGenerator{}

	tests := []struct {
		min, max int64
	}{
		{10, 10}, // min == max
		{20, 10}, // min > max
	}
	for _, tt := range tests {
		got := g.randomRange(tt.min, tt.max)
		if got != tt.min {
			t.Errorf("randomRange(%d, %d) = %d, want %d", tt.min, tt.max, got, tt.min)
		}
	}
}

// TestPacketGenerator_Shuffle 验证 shuffle 后数组元素不变（仅打乱顺序）。
func TestPacketGenerator_Shuffle(t *testing.T) {
	g := &PacketGenerator{}

	original := []int64{100, 200, 300, 400, 500}
	sumOriginal := int64(100 + 200 + 300 + 400 + 500)

	arr := make([]int64, len(original))
	copy(arr, original)
	g.shuffle(arr)

	if len(arr) != len(original) {
		t.Fatalf("shuffle 后长度 = %d, want %d", len(arr), len(original))
	}

	var sumAfter int64
	for _, v := range arr {
		sumAfter += v
	}
	if sumAfter != sumOriginal {
		t.Errorf("shuffle 后总和 = %d, want %d", sumAfter, sumOriginal)
	}

	// 验证元素集合相同（每个原元素都还在）
	counts := make(map[int64]int)
	for _, v := range original {
		counts[v]++
	}
	for _, v := range arr {
		counts[v]--
		if counts[v] < 0 {
			t.Errorf("shuffle 后出现新元素 %d", v)
		}
	}
}

// TestPacketGenerator_Shuffle_SingleElement 验证单元素数组 shuffle 不出错。
func TestPacketGenerator_Shuffle_SingleElement(t *testing.T) {
	g := &PacketGenerator{}

	arr := []int64{42}
	g.shuffle(arr)
	if arr[0] != 42 {
		t.Errorf("单元素 shuffle 后 = %d, want 42", arr[0])
	}
}

// itoa 简易整数转字符串，避免引入 strconv 仅为测试命名使用。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
