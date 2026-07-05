package idgen

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/snowflake"
)

// TestNewSnowflakeGenerator_NodeIDBoundary 测试 nodeID 边界值。
// 规约 SID-T4：MUST 覆盖 0、1023（合法）、-1、1024（非法，返回 error）。
func TestNewSnowflakeGenerator_NodeIDBoundary(t *testing.T) {
	tests := []struct {
		name    string
		nodeID  int64
		wantErr error
	}{
		{"min valid (0)", 0, nil},
		{"max valid (1023)", 1023, nil},
		{"negative (-1)", -1, ErrNodeIDInvalid},
		{"over max (1024)", 1024, ErrNodeIDInvalid},
		{"way over max (99999)", 99999, ErrNodeIDInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen, err := NewSnowflakeGenerator(tt.nodeID)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gen == nil {
				t.Fatal("expected non-nil generator")
			}
			if got := gen.GetNodeID(); got != tt.nodeID {
				t.Fatalf("GetNodeID() = %d, want %d", got, tt.nodeID)
			}
		})
	}
}

// TestSnowflakeGenerator_UniquenessSingleThread 测试单线程生成 ID 的唯一性。
// 规约 SID-T1：MUST 覆盖单线程唯一性。
func TestSnowflakeGenerator_UniquenessSingleThread(t *testing.T) {
	gen, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	const count = 10000
	seen := make(map[int64]struct{}, count)
	for i := 0; i < count; i++ {
		id, err := gen.GenerateInt64()
		if err != nil {
			t.Fatalf("GenerateInt64 failed at %d: %v", i, err)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate id at index %d: %d", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestSnowflakeGenerator_UniquenessConcurrent 测试多线程并发生成 ID 的唯一性。
// 规约 SID-T1：MUST 覆盖多线程并发唯一性。
func TestSnowflakeGenerator_UniquenessConcurrent(t *testing.T) {
	gen, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	const goroutines = 100
	const perGoroutine = 1000
	const total = goroutines * perGoroutine

	var mu sync.Mutex
	seen := make(map[int64]struct{}, total)
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id, err := gen.GenerateInt64()
				if err != nil {
					t.Errorf("GenerateInt64 failed: %v", err)
					return
				}
				mu.Lock()
				if _, exists := seen[id]; exists {
					t.Errorf("duplicate id: %d", id)
					mu.Unlock()
					return
				}
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != total {
		t.Errorf("expected %d unique ids, got %d", total, len(seen))
	}
}

// TestSnowflakeGenerator_GenerateString 测试 GenerateString 返回十进制字符串。
func TestSnowflakeGenerator_GenerateString(t *testing.T) {
	gen, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	id1, err := gen.GenerateInt64()
	if err != nil {
		t.Fatalf("GenerateInt64 failed: %v", err)
	}
	str1, err := gen.GenerateString()
	if err != nil {
		t.Fatalf("GenerateString failed: %v", err)
	}

	// 字符串应该可解析回 int64
	parsed, err := parseInt64(str1)
	if err != nil {
		t.Fatalf("failed to parse %q: %v", str1, err)
	}

	// 两次生成的 ID 应该不同（时间戳或序列号不同）
	if id1 == parsed {
		t.Errorf("expected different ids, both = %d", id1)
	}
}

// TestSnowflakeGenerator_GenerateID 测试 GenerateID 返回 snowflake.ID 类型。
// 验证 ID 的 Node()/Time()/Step() 反解析能力。
func TestSnowflakeGenerator_GenerateID(t *testing.T) {
	const nodeID = int64(7)
	gen, err := NewSnowflakeGenerator(nodeID)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	before := time.Now().UnixMilli()
	id, err := gen.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID failed: %v", err)
	}
	after := time.Now().UnixMilli()

	// 验证 Node() 反解析
	if got := id.Node(); got != nodeID {
		t.Errorf("id.Node() = %d, want %d", got, nodeID)
	}

	// 验证 Time() 反解析（毫秒时间戳，应在 before/after 之间）
	idTime := int64(id.Time())
	if idTime < before || idTime > after {
		t.Errorf("id.Time() = %d, not in [%d, %d]", idTime, before, after)
	}

	// 验证 Step() 反解析（序列号 >= 0）
	if step := id.Step(); step < 0 {
		t.Errorf("id.Step() = %d, should be >= 0", step)
	}
}

// TestSnowflakeGenerator_TimestampMonotonic 测试时间戳单调递增（无回拨时）。
// 规约 SID-T5：MUST 验证时间戳单调递增（无回拨时）。
func TestSnowflakeGenerator_TimestampMonotonic(t *testing.T) {
	gen, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	const count = 100
	prevTime := int64(0)
	for i := 0; i < count; i++ {
		id, err := gen.GenerateID()
		if err != nil {
			t.Fatalf("GenerateID failed at %d: %v", i, err)
		}
		curTime := int64(id.Time())
		if curTime < prevTime {
			t.Fatalf("timestamp moved backwards at %d: prev=%d cur=%d", i, prevTime, curTime)
		}
		prevTime = curTime
	}
}

// TestSnowflakeGenerator_SequenceOverflow 测试序列号溢出由库处理（阻塞等待下一毫秒）。
// 规约 SID-T3：MUST 覆盖序列号溢出。
func TestSnowflakeGenerator_SequenceOverflow(t *testing.T) {
	gen, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	// 同毫秒内生成超过 4096 个 ID（bwmarrin 库会阻塞等待下一毫秒）
	// 这里生成 5000 个 ID，验证全部唯一且无 error
	const count = 5000
	seen := make(map[int64]struct{}, count)
	for i := 0; i < count; i++ {
		id, err := gen.GenerateInt64()
		if err != nil {
			t.Fatalf("GenerateInt64 failed at %d: %v", i, err)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate id at index %d: %d", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestSnowflakeGenerator_DifferentNodeIDsDifferent 验证不同 nodeID 生成不同 ID。
// 同毫秒、同 sequence 下，不同 nodeID 生成的 ID 必须不同。
func TestSnowflakeGenerator_DifferentNodeIDsDifferent(t *testing.T) {
	gen1, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator(1) failed: %v", err)
	}
	gen2, err := NewSnowflakeGenerator(2)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator(2) failed: %v", err)
	}

	id1, err := gen1.GenerateInt64()
	if err != nil {
		t.Fatalf("gen1.GenerateInt64 failed: %v", err)
	}
	id2, err := gen2.GenerateInt64()
	if err != nil {
		t.Fatalf("gen2.GenerateInt64 failed: %v", err)
	}

	// 提取 nodeID 部分（位 12-21）
	const nodeIDShift = 12
	const nodeIDMask = 0x3FF // 10 bits
	node1 := (id1 >> nodeIDShift) & nodeIDMask
	node2 := (id2 >> nodeIDShift) & nodeIDMask

	if node1 != 1 {
		t.Errorf("id1 node = %d, want 1", node1)
	}
	if node2 != 2 {
		t.Errorf("id2 node = %d, want 2", node2)
	}
}

// TestSnowflakeGenerator_GetNodeID 测试 GetNodeID 返回构造时传入的 nodeID。
func TestSnowflakeGenerator_GetNodeID(t *testing.T) {
	tests := []int64{0, 1, 512, 1023}
	for _, nodeID := range tests {
		gen, err := NewSnowflakeGenerator(nodeID)
		if err != nil {
			t.Fatalf("NewSnowflakeGenerator(%d) failed: %v", nodeID, err)
		}
		if got := gen.GetNodeID(); got != nodeID {
			t.Errorf("GetNodeID() = %d, want %d", got, nodeID)
		}
	}
}

// TestSnowflakeGenerator_IDJSONMarshal 测试 snowflake.ID 的 JSON Marshal/Unmarshal。
// 规约 SID-T7：SHOULD 验证 snowflake.ID 的 JSON Marshal/Unmarshal 正确性。
func TestSnowflakeGenerator_IDJSONMarshal(t *testing.T) {
	gen, err := NewSnowflakeGenerator(1)
	if err != nil {
		t.Fatalf("NewSnowflakeGenerator failed: %v", err)
	}

	id, err := gen.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID failed: %v", err)
	}

	data, err := id.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	var id2 snowflake.ID
	if err := id2.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}

	if id != id2 {
		t.Errorf("roundtrip mismatch: original=%d, unmarshaled=%d", id, id2)
	}
}

// TestSnowflakeGenerator_ImplementsInterface 验证 SnowflakeGenerator 实现 IDGenerator 接口。
func TestSnowflakeGenerator_ImplementsInterface(t *testing.T) {
	var _ IDGenerator = (*SnowflakeGenerator)(nil)
}

// parseInt64 解析十进制字符串为 int64（避免引入 strconv 包仅为此一处）。
func parseInt64(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid digit")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
