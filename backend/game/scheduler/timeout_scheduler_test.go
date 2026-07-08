package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/redis/go-redis/v9"
)

// setupTestRedis 创建 miniredis 实例并返回包装后的 cRedis.RedisClient 与 context。
// TimeoutScheduler 仅依赖 cRedis.RedisClient 接口，可直接用 miniredis 测试。
func setupTestRedis(t *testing.T) (cRedis.RedisClient, context.Context) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis 启动失败: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// miniredis 对 EVALSHA 支持不稳定，统一回退到 EVAL 路径
	cRedis.SetUseEvalSHA(false)

	client := cRedis.NewClientFromRaw(rdb)
	return client, context.Background()
}

// newTestScheduler 构造测试用 TimeoutScheduler，使用极短的 CheckInterval 以加速测试。
func newTestScheduler(client cRedis.RedisClient) *TimeoutScheduler {
	return NewTimeoutScheduler(client, &Config{
		Seat:           30 * time.Second,
		Ready:          3 * time.Second,
		Grab:           20 * time.Second,
		Send:           30 * time.Second,
		Replace:        30 * time.Second,
		Robot:          5 * time.Second,
		CheckInterval:  10 * time.Millisecond,
		HandlerTimeout: 5 * time.Second,
	})
}

// timeoutKey 获取超时类型的 Redis ZSET key。
func timeoutKey(timeoutType TimeoutType) string {
	return rediskeys.TimeoutKey(string(timeoutType))
}

// =============================================================================
// SetTimeout / ClearTimeout 测试
// =============================================================================

// TestSetTimeout 验证 SetTimeout 将成员写入正确的 ZSET 且 score 为过期时间戳。
func TestSetTimeout(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	roomID := "room-1"
	data := "user-1"
	duration := 30 * time.Second

	before := time.Now().Unix()
	s.SetTimeout(ctx, TimeoutTypeGrab, roomID, data, duration)
	after := time.Now().Unix()

	key := timeoutKey(TimeoutTypeGrab)
	member := roomID + ":" + data

	score, err := client.ZScore(ctx, key, member).Result()
	if err != nil {
		t.Fatalf("ZScore 失败，member 未写入: %v", err)
	}

	expireAt := int64(score)
	if expireAt < before+int64(duration.Seconds())-1 || expireAt > after+int64(duration.Seconds())+1 {
		t.Errorf("过期时间戳不在预期范围，got %d, want ~%d-%d",
			expireAt, before+int64(duration.Seconds()), after+int64(duration.Seconds()))
	}
}

// TestSetTimeoutUsesDefaultDuration 验证不传 customDuration 时使用配置中的默认时长。
func TestSetTimeoutUsesDefaultDuration(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	roomID := "room-default"
	data := "user-default"

	before := time.Now().Unix()
	s.SetTimeout(ctx, TimeoutTypeReady, roomID, data) // 不传 customDuration
	after := time.Now().Unix()

	key := timeoutKey(TimeoutTypeReady)
	member := roomID + ":" + data

	score, err := client.ZScore(ctx, key, member).Result()
	if err != nil {
		t.Fatalf("ZScore 失败: %v", err)
	}

	// Ready 默认时长 = 3s
	expireAt := int64(score)
	expectedMin := before + 3
	expectedMax := after + 3
	if expireAt < expectedMin-1 || expireAt > expectedMax+1 {
		t.Errorf("默认时长过期时间戳不在预期范围，got %d, want ~%d-%d",
			expireAt, expectedMin, expectedMax)
	}
}

// TestClearTimeout 验证 ClearTimeout 从 ZSET 中移除指定成员。
func TestClearTimeout(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	roomID := "room-clear"
	data := "user-clear"

	s.SetTimeout(ctx, TimeoutTypeSend, roomID, data, 30*time.Second)
	s.ClearTimeout(ctx, TimeoutTypeSend, roomID, data)

	key := timeoutKey(TimeoutTypeSend)
	member := roomID + ":" + data

	n, _ := client.ZCard(ctx, key).Result()
	if n != 0 {
		t.Errorf("ClearTimeout 后 ZSET 应为空，got card=%d", n)
	}

	// 验证成员确实被移除
	score, err := client.ZScore(ctx, key, member).Result()
	if err == nil {
		t.Errorf("成员应已移除，但仍存在 score=%f", score)
	}
}

// TestClearRoomTimeouts 验证 ClearRoomTimeouts 移除指定房间的所有超时成员，保留其他房间。
func TestClearRoomTimeouts(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	// 为 room-A 设置 3 个超时
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-A", "user-1", 30*time.Second)
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-A", "user-2", 30*time.Second)
	// 为 room-B 设置 1 个超时
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-B", "user-3", 30*time.Second)

	s.ClearRoomTimeouts(ctx, TimeoutTypeGrab, "room-A")

	key := timeoutKey(TimeoutTypeGrab)
	card, _ := client.ZCard(ctx, key).Result()
	if card != 1 {
		t.Errorf("清除 room-A 后应剩 1 个成员（room-B），got card=%d", card)
	}

	// 验证 room-B 的成员仍在
	memberB := "room-B:user-3"
	if _, err := client.ZScore(ctx, key, memberB).Result(); err != nil {
		t.Errorf("room-B 的成员不应被清除: %v", err)
	}
}

// TestClearAllRoomTimeouts 验证 ClearAllRoomTimeouts 跨所有超时类型清除指定房间的超时。
func TestClearAllRoomTimeouts(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	// 为 room-X 在多个类型上设置超时
	s.SetTimeout(ctx, TimeoutTypeSeat, "room-X", "user-1", 30*time.Second)
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-X", "user-1", 30*time.Second)
	s.SetTimeout(ctx, TimeoutTypeSend, "room-X", "round-1", 30*time.Second)
	// 为 room-Y 设置超时（不应被清除）
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-Y", "user-2", 30*time.Second)

	s.ClearAllRoomTimeouts(ctx, "room-X")

	for _, tt := range []TimeoutType{TimeoutTypeSeat, TimeoutTypeGrab, TimeoutTypeSend} {
		key := timeoutKey(tt)
		card, _ := client.ZCard(ctx, key).Result()
		// room-Y 的成员可能仍在 Grab 类型中
		if tt == TimeoutTypeGrab {
			if card != 1 {
				t.Errorf("类型 %s 清除 room-X 后应剩 1 个成员（room-Y），got card=%d", tt, card)
			}
		} else {
			if card != 0 {
				t.Errorf("类型 %s 清除 room-X 后应为空，got card=%d", tt, card)
			}
		}
	}
}

// TestClearAllUserTimeouts 验证 ClearAllUserTimeouts 跨所有类型清除指定房间+用户的超时。
func TestClearAllUserTimeouts(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	s.SetTimeout(ctx, TimeoutTypeSeat, "room-U", "user-target", 30*time.Second)
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-U", "user-target", 30*time.Second)
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-U", "user-other", 30*time.Second)

	s.ClearAllUserTimeouts(ctx, "room-U", "user-target")

	// user-target 的超时应被清除
	grabKey := timeoutKey(TimeoutTypeGrab)
	score, err := client.ZScore(ctx, grabKey, "room-U:user-target").Result()
	if err == nil {
		t.Errorf("user-target 在 Grab 类型应已移除，但 score=%f", score)
	}

	// user-other 的超时应保留
	scoreOther, err := client.ZScore(ctx, grabKey, "room-U:user-other").Result()
	if err != nil {
		t.Errorf("user-other 在 Grab 类型应保留: %v", err)
	}
	if scoreOther == 0 {
		t.Error("user-other 的 score 不应为 0")
	}
}

// =============================================================================
// GetRemainingTime 测试
// =============================================================================

// TestGetRemainingTime 验证 GetRemainingTime 返回正确的剩余时长。
func TestGetRemainingTime(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	roomID := "room-remain"
	data := "user-remain"
	duration := 60 * time.Second

	s.SetTimeout(ctx, TimeoutTypeGrab, roomID, data, duration)

	remaining := s.GetRemainingTime(ctx, TimeoutTypeGrab, roomID, data)

	// 剩余时间应接近 60s（允许 5s 容差）
	if remaining <= 0 || remaining > duration+5*time.Second {
		t.Errorf("剩余时间不在预期范围，got %v, want (0, %v]", remaining, duration+5*time.Second)
	}
}

// TestGetRemainingTimeNotFound 验证不存在的成员返回 0。
func TestGetRemainingTimeNotFound(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	remaining := s.GetRemainingTime(ctx, TimeoutTypeGrab, "nonexistent-room", "nonexistent-user")
	if remaining != 0 {
		t.Errorf("不存在的成员应返回 0，got %v", remaining)
	}
}

// TestGetRemainingTimeExpired 验证已过期的成员返回 0。
func TestGetRemainingTimeExpired(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	roomID := "room-expired"
	data := "user-expired"

	// 直接写入一个已过期的 score
	key := timeoutKey(TimeoutTypeGrab)
	member := roomID + ":" + data
	pastTime := time.Now().Add(-60 * time.Second).Unix()
	client.ZAdd(ctx, key, cRedis.Z{
		Score:  float64(pastTime),
		Member: member,
	})

	remaining := s.GetRemainingTime(ctx, TimeoutTypeGrab, roomID, data)
	if remaining != 0 {
		t.Errorf("已过期成员应返回 0，got %v", remaining)
	}
}

// =============================================================================
// parseMember 测试
// =============================================================================

// TestParseMember 验证 ZSET member 字符串解析为 (roomID, data)。
func TestParseMember(t *testing.T) {
	client, _ := setupTestRedis(t)
	s := newTestScheduler(client)

	tests := []struct {
		name     string
		member   string
		wantRoom string
		wantData string
	}{
		{
			name:     "标准格式 roomID:userID",
			member:   "room-1:user-1",
			wantRoom: "room-1",
			wantData: "user-1",
		},
		{
			name:     "data 包含冒号 roomID:user:action:retry",
			member:   "room-1:user-1:grab:2",
			wantRoom: "room-1",
			wantData: "user-1:grab:2",
		},
		{
			name:     "无冒号仅有 roomID",
			member:   "room-only",
			wantRoom: "room-only",
			wantData: "",
		},
		{
			name:     "空字符串",
			member:   "",
			wantRoom: "",
			wantData: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roomID, data := s.parseMember(tt.member)
			if roomID != tt.wantRoom {
				t.Errorf("roomID got %q, want %q", roomID, tt.wantRoom)
			}
			if data != tt.wantData {
				t.Errorf("data got %q, want %q", data, tt.wantData)
			}
		})
	}
}

// =============================================================================
// SetTimeout 边界场景测试
// =============================================================================

// TestSetTimeoutUnknownType 验证对未知超时类型的 SetTimeout 是安全的 no-op。
func TestSetTimeoutUnknownType(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	// 使用未注册的超时类型
	s.SetTimeout(ctx, TimeoutType("unknown"), "room-1", "user-1", 30*time.Second)

	// 不应有 panic，且不应写入任何 key
	key := timeoutKey(TimeoutType("unknown"))
	card, _ := client.ZCard(ctx, key).Result()
	if card != 0 {
		t.Errorf("未知类型不应写入 ZSET，got card=%d", card)
	}
}

// TestSetTimeoutZeroDuration 验证零时长 SetTimeout 仍能写入（过期时间戳为当前时间）。
func TestSetTimeoutZeroDuration(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	before := time.Now().Unix()
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-zero", "user-zero", 0)
	after := time.Now().Unix()

	key := timeoutKey(TimeoutTypeGrab)
	member := "room-zero:user-zero"

	score, err := client.ZScore(ctx, key, member).Result()
	if err != nil {
		t.Fatalf("零时长 SetTimeout 后成员应存在: %v", err)
	}

	expireAt := int64(score)
	if expireAt < before-1 || expireAt > after+1 {
		t.Errorf("零时长过期时间戳应接近 now，got %d, want %d-%d", expireAt, before, after)
	}
}

// TestSetTimeoutNegativeDuration 验证负时长 SetTimeout 仍能写入（过期时间戳在过去）。
func TestSetTimeoutNegativeDuration(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	s.SetTimeout(ctx, TimeoutTypeGrab, "room-neg", "user-neg", -10*time.Second)

	key := timeoutKey(TimeoutTypeGrab)
	member := "room-neg:user-neg"

	score, err := client.ZScore(ctx, key, member).Result()
	if err != nil {
		t.Fatalf("负时长 SetTimeout 后成员应存在: %v", err)
	}

	expireAt := int64(score)
	now := time.Now().Unix()
	if expireAt >= now {
		t.Errorf("负时长过期时间戳应在过去，got %d, now=%d", expireAt, now)
	}
}

// =============================================================================
// 超时触发 + handler 回调测试
// =============================================================================

// TestTimeoutExpiration 验证超时到期后 handler 被回调。
// 使用极短的 CheckInterval 和 duration，通过 channel 同步等待 handler 调用（不使用 time.Sleep）。
func TestTimeoutExpiration(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	handlerCalled := make(chan struct {
		roomID string
		data   string
	}, 1)

	s.RegisterHandler(TimeoutTypeGrab, func(ctx context.Context, roomID, data string) {
		handlerCalled <- struct {
			roomID string
			data   string
		}{roomID: roomID, data: data}
	})

	// 启动调度器
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(s.Stop)

	// 设置极短的超时（1ms），使 checkTimeouts 立即捕获
	s.SetTimeout(ctx, TimeoutTypeGrab, "room-trigger", "user-trigger", 1*time.Millisecond)

	// 等待 handler 被调用（最多等待 3s）
	select {
	case result := <-handlerCalled:
		if result.roomID != "room-trigger" {
			t.Errorf("handler roomID got %q, want %q", result.roomID, "room-trigger")
		}
		if result.data != "user-trigger" {
			t.Errorf("handler data got %q, want %q", result.data, "user-trigger")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待 handler 回调超时（3s），handler 未被调用")
	}
}

// TestTimeoutExpirationRemovesMember 验证超时触发后 ZSET 成员被移除（ZRem 原子去重）。
func TestTimeoutExpirationRemovesMember(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	handlerDone := make(chan struct{}, 1)
	s.RegisterHandler(TimeoutTypeSend, func(ctx context.Context, roomID, data string) {
		handlerDone <- struct{}{}
	})

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(s.Stop)

	s.SetTimeout(ctx, TimeoutTypeSend, "room-rm", "user-rm", 1*time.Millisecond)

	// 等待 handler 执行
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("等待 handler 回调超时")
	}

	// 成员应已被 ZRem 移除
	key := timeoutKey(TimeoutTypeSend)
	member := "room-rm:user-rm"
	score, err := client.ZScore(ctx, key, member).Result()
	if err == nil {
		t.Errorf("超时触发后成员应被移除，但仍存在 score=%f", score)
	}
}

// TestTimeoutExpirationNoHandler 验证无注册 handler 时超时仍正常移除成员（不 panic）。
func TestTimeoutExpirationNoHandler(t *testing.T) {
	client, ctx := setupTestRedis(t)
	s := newTestScheduler(client)

	// 不注册任何 handler
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(s.Stop)

	s.SetTimeout(ctx, TimeoutTypeReplace, "room-no-handler", "user-no-handler", 1*time.Millisecond)

	// 等待足够的 checkInterval 周期让 checkTimeouts 执行
	select {
	case <-time.After(200 * time.Millisecond):
	}

	key := timeoutKey(TimeoutTypeReplace)
	member := "room-no-handler:user-no-handler"
	score, err := client.ZScore(ctx, key, member).Result()
	if err == nil {
		t.Errorf("无 handler 时超时成员仍应被移除，但 score=%f", score)
	}
}
