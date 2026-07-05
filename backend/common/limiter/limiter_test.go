package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/redis/go-redis/v9"
)

// newTestRedis 创建测试用 miniredis 和 cRedis.Client
// 复用 scripts/sliding_window_test.go 中的创建模式
func newTestRedis(t *testing.T) (*cRedis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return cRedis.NewClientFromRaw(rdb), mr
}

// TestUserLimiter_Allow_Concurrent 验证并发场景下限流正确,不 panic 且放行数不超过 limit
func TestUserLimiter_Allow_Concurrent(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	configs := map[string]LimitConfig{
		"grab": {Key: "grab", Limit: 10, Window: time.Second},
	}
	ul := NewUserLimiter(client, configs)

	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := ul.Allow(ctx, "userA", "grab")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if ok {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	// 限流上限为 10,放行数不应超过 10
	if allowed > 10 {
		t.Errorf("allowed = %d, expected <= 10", allowed)
	}
	// 至少应有部分请求被放行,验证限流器正常工作
	if allowed == 0 {
		t.Error("expected some requests to be allowed, got 0")
	}
}

// TestUserLimiter_Allow_UnconfiguredCmd 验证未配置限流的命令默认放行
func TestUserLimiter_Allow_UnconfiguredCmd(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	configs := map[string]LimitConfig{
		"grab": {Key: "grab", Limit: 10, Window: time.Second},
	}
	ul := NewUserLimiter(client, configs)

	ok, err := ul.Allow(ctx, "userA", "unknown_cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected unconfigured cmd to be allowed, got rejected")
	}
}

// TestUserLimiter_UpdateConfigs 验证热更新配置后新 limit 生效
func TestUserLimiter_UpdateConfigs(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	configs := map[string]LimitConfig{
		"grab": {Key: "grab", Limit: 10, Window: time.Second},
	}
	ul := NewUserLimiter(client, configs)

	// 初始配置 limit=10,调用 5 次全部放行
	for i := 0; i < 5; i++ {
		ok, err := ul.Allow(ctx, "userA", "grab")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if !ok {
			t.Fatalf("call %d: expected allowed, got rejected", i+1)
		}
	}

	// 热更新为 limit=2
	ul.UpdateConfigs(map[string]LimitConfig{
		"grab": {Key: "grab", Limit: 2, Window: time.Second},
	})

	// 使用新用户避免受之前请求计数影响,验证新 limit 生效
	var allowed int64
	for i := 0; i < 3; i++ {
		ok, err := ul.Allow(ctx, "userB", "grab")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Errorf("after UpdateConfigs: allowed = %d, expected 2", allowed)
	}
}

// TestRateLimiter_Allow_RedisError_ReturnsError 验证 Redis 故障时返回 error 且 fail-open
func TestRateLimiter_Allow_RedisError_ReturnsError(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()
	rl := NewRateLimiter(client, WithFailOpen(true))

	// 关闭 miniredis 模拟 Redis 故障
	mr.Close()

	cfg := &LimitConfig{Key: "ratelimit:test:err", Limit: 5, Window: time.Second}
	ok, err := rl.Allow(ctx, cfg)
	if err == nil {
		t.Fatal("expected error when redis is down, got nil")
	}
	if !ok {
		t.Error("expected fail-open (true), got false")
	}
}

// TestRateLimiter_Allow_Allowed 验证未超 limit 时放行
func TestRateLimiter_Allow_Allowed(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	rl := NewRateLimiter(client)

	cfg := &LimitConfig{Key: "ratelimit:test:allowed", Limit: 5, Window: time.Second}
	ok, err := rl.Allow(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allowed, got rejected")
	}
}

// TestRateLimiter_Allow_Rejected 验证超过 limit 时拒绝
func TestRateLimiter_Allow_Rejected(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	rl := NewRateLimiter(client)

	cfg := &LimitConfig{Key: "ratelimit:test:rejected", Limit: 1, Window: time.Second}

	// 第 1 次放行
	ok1, err1 := rl.Allow(ctx, cfg)
	if err1 != nil {
		t.Fatalf("first call: unexpected error: %v", err1)
	}
	if !ok1 {
		t.Fatal("first call: expected allowed")
	}

	// 第 2 次拒绝
	ok2, err2 := rl.Allow(ctx, cfg)
	if err2 != nil {
		t.Fatalf("second call: unexpected error: %v", err2)
	}
	if ok2 {
		t.Fatal("second call: expected rejected")
	}
}

// TestRateLimiter_FailOpen_True 验证 failOpen=true 时 Redis 故障放行并返回 error
func TestRateLimiter_FailOpen_True(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()
	rl := NewRateLimiter(client, WithFailOpen(true))

	mr.Close()

	cfg := &LimitConfig{Key: "ratelimit:test:failopen_true", Limit: 5, Window: time.Second}
	ok, err := rl.Allow(ctx, cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !ok {
		t.Error("expected fail-open (true), got false")
	}
}

// TestRateLimiter_FailOpen_False 验证 failOpen=false 时 Redis 故障拒绝并返回 error
func TestRateLimiter_FailOpen_False(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()
	rl := NewRateLimiter(client, WithFailOpen(false))

	mr.Close()

	cfg := &LimitConfig{Key: "ratelimit:test:failopen_false", Limit: 5, Window: time.Second}
	ok, err := rl.Allow(ctx, cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ok {
		t.Error("expected fail-closed (false), got true")
	}
}

// TestRateLimiter_Metrics 验证指标统计(allow/reject/error)正确
func TestRateLimiter_Metrics(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()
	rl := NewRateLimiter(client)

	cfg := &LimitConfig{Key: "ratelimit:test:metrics", Limit: 2, Window: time.Second}

	// 调用 3 次: 2 放行 1 拒绝
	for i := 0; i < 3; i++ {
		_, err := rl.Allow(ctx, cfg)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}

	allow, reject, errCount := rl.metrics.Snapshot()
	if allow != 2 {
		t.Errorf("AllowCount = %d, expected 2", allow)
	}
	if reject != 1 {
		t.Errorf("RejectCount = %d, expected 1", reject)
	}
	if errCount != 0 {
		t.Errorf("ErrorCount = %d, expected 0", errCount)
	}

	// 关闭 redis 后调用,验证 ErrorCount 增加
	mr.Close()
	_, err := rl.Allow(ctx, cfg)
	if err == nil {
		t.Fatal("expected error after redis down, got nil")
	}

	_, _, errCount = rl.metrics.Snapshot()
	if errCount != 1 {
		t.Errorf("ErrorCount = %d, expected 1", errCount)
	}
}
