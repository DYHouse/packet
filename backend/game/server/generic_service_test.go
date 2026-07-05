package server

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cashparty/backend/common/limiter"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	commonPb "github.com/cashparty/backend/proto/common"
	"github.com/redis/go-redis/v9"
)

// newTestRedis 创建测试用 miniredis 和 cRedis.Client
// 复用 common/limiter/limiter_test.go 中的创建模式
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

// TestCheckRateLimit_UnconfiguredCmd 验证未配置限流的命令默认放行
func TestCheckRateLimit_UnconfiguredCmd(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	ul := limiter.NewUserLimiter(client, map[string]limiter.LimitConfig{
		"grab": {Key: "grab", Limit: 10, Window: time.Second},
	})
	s := &GenericServiceServer{userLimiter: ul, grabFailOpen: false, financialFailOpen: false}

	req := &commonPb.ForwardRequest{UserId: "user1", Cmd: "unknown_cmd", RequestId: "req1"}
	resp, ok := s.checkRateLimit(ctx, req, "unknown_cmd", false)
	if !ok {
		t.Fatal("expected ok=true for unconfigured cmd")
	}
	if resp != nil {
		t.Fatal("expected nil resp for unconfigured cmd")
	}
}

// TestCheckRateLimit_GrabExceeded 验证 grab 命令超出限流时返回 CodeRateLimitExceeded
func TestCheckRateLimit_GrabExceeded(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	ul := limiter.NewUserLimiter(client, map[string]limiter.LimitConfig{
		"grab": {Key: "grab", Limit: 1, Window: time.Second},
	})
	s := &GenericServiceServer{userLimiter: ul, grabFailOpen: false}

	req := &commonPb.ForwardRequest{UserId: "user1", Cmd: "grab_packet", RequestId: "req1"}

	// 第 1 次调用:放行
	resp, ok := s.checkRateLimit(ctx, req, "grab", false)
	if !ok {
		t.Fatal("first call: expected ok=true")
	}
	if resp != nil {
		t.Fatal("first call: expected nil resp")
	}

	// 第 2 次调用:被限流
	resp, ok = s.checkRateLimit(ctx, req, "grab", false)
	if ok {
		t.Fatal("second call: expected ok=false")
	}
	if resp == nil {
		t.Fatal("second call: expected non-nil resp")
	}
	if resp.Code != int32(message.CodeRateLimitExceeded) {
		t.Fatalf("second call: expected CodeRateLimitExceeded, got %d", resp.Code)
	}
}

// TestCheckRateLimit_RedisError_FailClosed 验证 Redis 故障时 fail-closed 返回 CodeSystemError
func TestCheckRateLimit_RedisError_FailClosed(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()
	ul := limiter.NewUserLimiter(client, map[string]limiter.LimitConfig{
		"grab": {Key: "grab", Limit: 10, Window: time.Second},
	})
	s := &GenericServiceServer{userLimiter: ul, grabFailOpen: false}

	// 关闭 miniredis 模拟 Redis 故障
	mr.Close()

	req := &commonPb.ForwardRequest{UserId: "user1", Cmd: "grab_packet", RequestId: "req1"}
	resp, ok := s.checkRateLimit(ctx, req, "grab", false)
	if ok {
		t.Fatal("expected ok=false for fail-closed")
	}
	if resp == nil {
		t.Fatal("expected non-nil resp")
	}
	if resp.Code != int32(message.CodeSystemError) {
		t.Fatalf("expected CodeSystemError, got %d", resp.Code)
	}
}

// TestCheckRateLimit_RedisError_FailOpen 验证 Redis 故障时 fail-open 放行请求
func TestCheckRateLimit_RedisError_FailOpen(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()
	ul := limiter.NewUserLimiter(client, map[string]limiter.LimitConfig{
		"grab": {Key: "grab", Limit: 10, Window: time.Second},
	})
	s := &GenericServiceServer{userLimiter: ul, grabFailOpen: true}

	mr.Close()

	req := &commonPb.ForwardRequest{UserId: "user1", Cmd: "grab_packet", RequestId: "req1"}
	resp, ok := s.checkRateLimit(ctx, req, "grab", true)
	if !ok {
		t.Fatal("expected ok=true for fail-open")
	}
	if resp != nil {
		t.Fatal("expected nil resp for fail-open")
	}
}

// TestCheckRateLimit_SendPacketExceeded 验证 send_packet 命令超出限流时返回 CodeRateLimitExceeded
func TestCheckRateLimit_SendPacketExceeded(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	ul := limiter.NewUserLimiter(client, map[string]limiter.LimitConfig{
		"send_packet": {Key: "send_packet", Limit: 1, Window: time.Second},
	})
	s := &GenericServiceServer{userLimiter: ul, financialFailOpen: false}

	req := &commonPb.ForwardRequest{UserId: "user1", Cmd: "send_packet", RequestId: "req1"}

	// 第 1 次调用:放行
	resp, ok := s.checkRateLimit(ctx, req, "send_packet", false)
	if !ok {
		t.Fatal("first call: expected ok=true")
	}
	if resp != nil {
		t.Fatal("first call: expected nil resp")
	}

	// 第 2 次调用:被限流
	resp, ok = s.checkRateLimit(ctx, req, "send_packet", false)
	if ok {
		t.Fatal("second call: expected ok=false")
	}
	if resp == nil {
		t.Fatal("second call: expected non-nil resp")
	}
	if resp.Code != int32(message.CodeRateLimitExceeded) {
		t.Fatalf("second call: expected CodeRateLimitExceeded, got %d", resp.Code)
	}
}

// TestCheckRateLimit_JoinRoomExceeded 验证 join_room 命令超出限流时返回 CodeRateLimitExceeded
// join_room 在 handleJoinRoom 中 fail-open 硬编码为 true,这里直接测试 checkRateLimit 的限流行为
func TestCheckRateLimit_JoinRoomExceeded(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	ul := limiter.NewUserLimiter(client, map[string]limiter.LimitConfig{
		"join_room": {Key: "join_room", Limit: 1, Window: time.Second},
	})
	s := &GenericServiceServer{userLimiter: ul}

	req := &commonPb.ForwardRequest{UserId: "user1", Cmd: "join_room", RequestId: "req1"}

	// 第 1 次调用:放行
	resp, ok := s.checkRateLimit(ctx, req, "join_room", true)
	if !ok {
		t.Fatal("first call: expected ok=true")
	}
	if resp != nil {
		t.Fatal("first call: expected nil resp")
	}

	// 第 2 次调用:被限流(即使 failOpen=true,正常限流拒绝仍返回 CodeRateLimitExceeded)
	resp, ok = s.checkRateLimit(ctx, req, "join_room", true)
	if ok {
		t.Fatal("second call: expected ok=false")
	}
	if resp == nil {
		t.Fatal("second call: expected non-nil resp")
	}
	if resp.Code != int32(message.CodeRateLimitExceeded) {
		t.Fatalf("second call: expected CodeRateLimitExceeded, got %d", resp.Code)
	}
}
