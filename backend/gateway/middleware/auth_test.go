package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/redis/go-redis/v9"
)

// newAuthTestClient 创建 miniredis 与 cRedis.Client。
// 复用 common/limiter/scripts/sliding_window_test.go 中的创建模式。
func newAuthTestClient(t *testing.T) (*miniredis.Miniredis, cRedis.RedisClient) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return mr, cRedis.NewClientFromRaw(rdb)
}

// newAuthMiddleware 构造用于测试的 AuthMiddleware。
// tokenService 传 nil，因为被测方法（isLocked/recordFailedAttempt/clearFailedAttempts）不依赖它。
func newAuthMiddleware(client cRedis.RedisClient, cfg AuthLockConfig) *AuthMiddleware {
	return NewAuthMiddleware(nil, client, cfg)
}

// TestAuthMiddleware_IsLocked_ChecksRedis 验证 isLocked 从 Redis 读取锁定状态。
func TestAuthMiddleware_IsLocked_ChecksRedis(t *testing.T) {
	_, client := newAuthTestClient(t)
	m := newAuthMiddleware(client, AuthLockConfig{
		MaxAttempts:   3,
		LockDuration:  1 * time.Minute,
		CounterWindow: 5 * time.Minute,
	})
	ctx := context.Background()
	ip := "1.2.3.4"

	// 未设置锁时，应返回 false
	if m.isLocked(ctx, ip) {
		t.Fatal("expected isLocked=false when no lock key in redis")
	}

	// 在 Redis 中设置 rediskeys.GatewayLockedIPKey
	lockKey := rediskeys.GatewayLockedIPKey(ip)
	if err := client.Set(ctx, lockKey, "1", time.Minute).Err(); err != nil {
		t.Fatalf("failed to set lock key: %v", err)
	}

	// 设置锁后，应返回 true
	if !m.isLocked(ctx, ip) {
		t.Fatal("expected isLocked=true when lock key exists in redis")
	}
}

// TestAuthMiddleware_RecordFailedAttempt_PersistsToRedis 验证 recordFailedAttempt 用 INCR 持久化失败计数到 Redis。
func TestAuthMiddleware_RecordFailedAttempt_PersistsToRedis(t *testing.T) {
	_, client := newAuthTestClient(t)
	m := newAuthMiddleware(client, AuthLockConfig{
		MaxAttempts:   3,
		LockDuration:  1 * time.Minute,
		CounterWindow: 5 * time.Minute,
	})
	ctx := context.Background()
	ip := "1.2.3.4"
	counterKey := rediskeys.GatewayAuthFailKey(ip)

	// 第一次失败尝试
	m.recordFailedAttempt(ctx, ip)
	val, err := client.Get(ctx, counterKey).Result()
	if err != nil {
		t.Fatalf("failed to get counter after first attempt: %v", err)
	}
	if val != "1" {
		t.Fatalf("expected counter=1 after first attempt, got %s", val)
	}

	// 第二次失败尝试
	m.recordFailedAttempt(ctx, ip)
	val, err = client.Get(ctx, counterKey).Result()
	if err != nil {
		t.Fatalf("failed to get counter after second attempt: %v", err)
	}
	if val != "2" {
		t.Fatalf("expected counter=2 after second attempt, got %s", val)
	}
}

// TestAuthMiddleware_LockAfterMaxAttempts 验证达到 MaxAttempts 后会设置 IP 锁。
func TestAuthMiddleware_LockAfterMaxAttempts(t *testing.T) {
	_, client := newAuthTestClient(t)
	m := newAuthMiddleware(client, AuthLockConfig{
		MaxAttempts:   3,
		LockDuration:  1 * time.Minute,
		CounterWindow: 5 * time.Minute,
	})
	ctx := context.Background()
	ip := "1.2.3.4"
	lockKey := rediskeys.GatewayLockedIPKey(ip)

	// 失败 3 次（达到 MaxAttempts）
	for i := 0; i < 3; i++ {
		m.recordFailedAttempt(ctx, ip)
	}

	// 验证锁定 key 存在
	exists, err := client.Exists(ctx, lockKey).Result()
	if err != nil {
		t.Fatalf("failed to check lock key: %v", err)
	}
	if exists != 1 {
		t.Fatalf("expected lock key exists after max attempts, got exists=%d", exists)
	}

	// 验证 isLocked 返回 true
	if !m.isLocked(ctx, ip) {
		t.Fatal("expected isLocked=true after max attempts")
	}
}

// TestAuthMiddleware_ClearFailedAttempts 验证 clearFailedAttempts 同时清理失败计数与锁定 key。
func TestAuthMiddleware_ClearFailedAttempts(t *testing.T) {
	_, client := newAuthTestClient(t)
	m := newAuthMiddleware(client, AuthLockConfig{
		MaxAttempts:   3,
		LockDuration:  1 * time.Minute,
		CounterWindow: 5 * time.Minute,
	})
	ctx := context.Background()
	ip := "1.2.3.4"
	counterKey := rediskeys.GatewayAuthFailKey(ip)
	lockKey := rediskeys.GatewayLockedIPKey(ip)

	// 累积失败并触发锁定
	for i := 0; i < 3; i++ {
		m.recordFailedAttempt(ctx, ip)
	}

	// 确认 key 已存在
	if exists, _ := client.Exists(ctx, counterKey).Result(); exists != 1 {
		t.Fatalf("expected counter key exists before clear, got exists=%d", exists)
	}
	if exists, _ := client.Exists(ctx, lockKey).Result(); exists != 1 {
		t.Fatalf("expected lock key exists before clear, got exists=%d", exists)
	}

	// 执行清理
	m.clearFailedAttempts(ctx, ip)

	// 验证失败计数 key 与锁定 key 均已删除
	if exists, _ := client.Exists(ctx, counterKey).Result(); exists != 0 {
		t.Fatalf("expected counter key deleted, got exists=%d", exists)
	}
	if exists, _ := client.Exists(ctx, lockKey).Result(); exists != 0 {
		t.Fatalf("expected lock key deleted, got exists=%d", exists)
	}

	// 验证 isLocked 返回 false
	if m.isLocked(ctx, ip) {
		t.Fatal("expected isLocked=false after clearFailedAttempts")
	}
}

// TestAuthMiddleware_RedisError_FailOpen 验证 Redis 故障时 isLocked 选择 fail-open（返回 false）。
func TestAuthMiddleware_RedisError_FailOpen(t *testing.T) {
	mr, client := newAuthTestClient(t)
	m := newAuthMiddleware(client, AuthLockConfig{
		MaxAttempts:   3,
		LockDuration:  1 * time.Minute,
		CounterWindow: 5 * time.Minute,
	})
	ctx := context.Background()
	ip := "1.2.3.4"

	// 关闭 miniredis 模拟 Redis 故障
	mr.Close()

	// isLocked 应返回 false（fail-open，避免锁死所有用户）
	if m.isLocked(ctx, ip) {
		t.Fatal("expected isLocked=false when redis is down (fail-open)")
	}
}
