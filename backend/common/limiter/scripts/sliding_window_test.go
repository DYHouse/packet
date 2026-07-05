package scripts

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/redis/go-redis/v9"
)

func newSlidingWindowTestClient(t *testing.T) (*miniredis.Miniredis, *cRedis.Client) {
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

// TestSlidingWindowScript_WithinWindowAllowed verifies that requests within
// the limit are all admitted.
func TestSlidingWindowScript_WithinWindowAllowed(t *testing.T) {
	mr, client := newSlidingWindowTestClient(t)
	ctx := context.Background()

	const limit = 10
	window := 60 * time.Second
	// Use a small base time so nanosecond values stay below 2^53; Lua numbers
	// are doubles and would lose precision on real-world UnixNano timestamps.
	baseTime := time.Unix(1000, 0)
	mr.SetTime(baseTime)

	key := "ratelimit:test:within"

	for i := 0; i < 9; i++ {
		now := baseTime.Add(time.Duration(i) * time.Nanosecond).UnixNano()
		member := fmt.Sprintf("%d-%d", now, int64(i))
		result, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member).Int()
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if result != 1 {
			t.Fatalf("request %d: expected 1 (allowed), got %d", i+1, result)
		}
	}
}

// TestSlidingWindowScript_OverLimitRejected verifies that the request
// exceeding the limit is rejected.
func TestSlidingWindowScript_OverLimitRejected(t *testing.T) {
	mr, client := newSlidingWindowTestClient(t)
	ctx := context.Background()

	const limit = 10
	window := 60 * time.Second
	baseTime := time.Unix(1000, 0)
	mr.SetTime(baseTime)

	key := "ratelimit:test:over"

	for i := 0; i < limit; i++ {
		now := baseTime.Add(time.Duration(i) * time.Nanosecond).UnixNano()
		member := fmt.Sprintf("%d-%d", now, int64(i))
		result, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member).Int()
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if result != 1 {
			t.Fatalf("request %d: expected 1 (allowed), got %d", i+1, result)
		}
	}

	// 11th request should be rejected.
	now := baseTime.Add(time.Duration(limit) * time.Nanosecond).UnixNano()
	member := fmt.Sprintf("%d-%d", now, int64(limit))
	result, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member).Int()
	if err != nil {
		t.Fatalf("request 11: %v", err)
	}
	if result != 0 {
		t.Fatalf("request 11: expected 0 (rejected), got %d", result)
	}
}

// TestSlidingWindowScript_WindowSlidAllowed verifies that after the window
// slides past the recorded timestamps, new requests are admitted again.
func TestSlidingWindowScript_WindowSlidAllowed(t *testing.T) {
	mr, client := newSlidingWindowTestClient(t)
	ctx := context.Background()

	const limit = 2
	window := 1 * time.Second
	baseTime := time.Unix(1000, 0)
	mr.SetTime(baseTime)

	key := "ratelimit:test:slid"

	// First two requests are admitted.
	for i := 0; i < limit; i++ {
		now := baseTime.Add(time.Duration(i) * time.Nanosecond).UnixNano()
		member := fmt.Sprintf("%d-%d", now, int64(i))
		result, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member).Int()
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if result != 1 {
			t.Fatalf("request %d: expected 1 (allowed), got %d", i+1, result)
		}
	}

	// Advance simulated time past the window.
	advance := 1100 * time.Millisecond
	mr.FastForward(advance)
	baseTime = baseTime.Add(advance)

	// Third request should be admitted after the window slides.
	now := baseTime.UnixNano()
	member := fmt.Sprintf("%d-%d", now, int64(limit))
	result, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member).Int()
	if err != nil {
		t.Fatalf("request 3: %v", err)
	}
	if result != 1 {
		t.Fatalf("request 3: expected 1 (allowed after window slide), got %d", result)
	}
}

// TestSlidingWindowScript_SameNanosecond verifies that two requests with the
// same `now` timestamp but different members are both admitted. Without a
// unique member, the second ZADD would overwrite the first (same score+member),
// leaving ZCARD=1 and silently under-counting concurrent requests within the
// same nanosecond.
func TestSlidingWindowScript_SameNanosecond(t *testing.T) {
	mr, client := newSlidingWindowTestClient(t)
	ctx := context.Background()

	const limit = 10
	window := 60 * time.Second
	baseTime := time.Unix(1000, 0)
	mr.SetTime(baseTime)

	key := "ratelimit:test:same_ns"
	now := baseTime.UnixNano()

	// Two requests at the same nanosecond with distinct members.
	member1 := fmt.Sprintf("%d-%d", now, int64(1))
	member2 := fmt.Sprintf("%d-%d", now, int64(2))

	if _, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member1).Int(); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if _, err := SlidingWindowScript.Run(ctx, client, []string{key}, limit, int64(window), now, member2).Int(); err != nil {
		t.Fatalf("request 2: %v", err)
	}

	count, err := client.ZCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("ZCARD: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected ZCARD=2 for same-nanosecond distinct members, got %d", count)
	}
}
