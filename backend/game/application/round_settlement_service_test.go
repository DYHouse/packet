package application

import (
	"context"
	"testing"

	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/scheduler"
)

// setupRoundState 在 miniredis 中预置回合状态供 SettleRound 测试使用。
// roomID/roundID 标识房间与回合，phase/roundNo/senderID/totalAmount 写入 roundStateKey。
func setupRoundState(t *testing.T, roomID, roundID, phase string, roundNo int, senderID string, totalAmount int64) {
	t.Helper()
	rdb := testRedisClient.Raw()
	ctx := context.Background()

	roundStateKey := rediskeys.RoundStateKey(roundID)
	rdb.HSet(ctx, roundStateKey, "phase", phase, "round_no", roundNo, "sender_id", senderID, "total_amount", totalAmount)
}

// setupRoomHash 在 miniredis 中预置房间哈希供 SettleRound 测试使用。
func setupRoomHash(t *testing.T, roomID string, maxRounds int) {
	t.Helper()
	rdb := testRedisClient.Raw()
	ctx := context.Background()

	roomHashKey := rediskeys.RoomHashKey(roomID)
	rdb.HSet(ctx, roomHashKey, "max_rounds", maxRounds)
}

// setupPlayerAndGrabber 在 miniredis 中预置玩家与抢包记录。
func setupPlayerAndGrabber(t *testing.T, roomID, roundID, userID string) {
	t.Helper()
	rdb := testRedisClient.Raw()
	ctx := context.Background()

	playersKey := rediskeys.RoomPlayersKey(roomID)
	rdb.HSet(ctx, playersKey, userID, `{"user_id":"`+userID+`","nickname":"TestPlayer","avatar":"avatar.png"}`)

	grabbersKey := rediskeys.RoundGrabbersKey(roundID)
	rdb.SAdd(ctx, grabbersKey, userID)
}

// =============================================================================
// SettleRound 成功路径测试
// =============================================================================

// TestSettleRoundSuccess 验证 SettleRound 成功路径：phase=GRABBING → SETTLED，
// 广播 PushRoundEnd，并通过异步任务发布 GameEventRoundSettle。
func TestSettleRoundSuccess(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-ok"
	roundID := "round-settle-ok"

	// 预置回合状态：phase=GRABBING, round_no=1, sender_id=user-1, total_amount=500
	setupRoundState(t, roomID, roundID, "GRABBING", 1, "user-1", 500)
	setupRoomHash(t, roomID, 10)
	setupPlayerAndGrabber(t, roomID, roundID, "user-1")

	// 构造桩件
	repo := &mockRoomRepository{meta: &room.RoomMeta{RoomID: roomID, CurrentSessionID: "session-1"}}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestRoundSettlementService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	svc.SettleRound(ctx, roomID, roundID)

	// 验证 phase 变为 SETTLED
	rdb := testRedisClient.Raw()
	phase, err := rdb.HGet(ctx, rediskeys.RoundStateKey(roundID), "phase").Result()
	if err != nil {
		t.Fatalf("HGet phase 失败: %v", err)
	}
	if phase != "SETTLED" {
		t.Errorf("phase got %q, want SETTLED", phase)
	}

	// 验证广播了 PushRoundEnd
	if broadcaster.callCount() == 0 {
		t.Error("期望广播 PushRoundEnd，但无广播调用")
	}
	call := broadcaster.lastCall()
	if call == nil || call.Event != message.PushRoundEnd {
		t.Errorf("期望事件 %s，got %+v", message.PushRoundEnd, call)
	}

	// 验证异步任务已提交并同步执行了事件发布
	if publisher.eventCount() != 1 {
		t.Errorf("期望发布 1 个 GameEvent，got %d", publisher.eventCount())
	}
}

// TestSettleRoundIdempotent 验证 SettleRound 幂等性：phase=SETTLED 时返回 code=2，不广播。
func TestSettleRoundIdempotent(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-idem"
	roundID := "round-settle-idem"

	// 预置已结算状态
	setupRoundState(t, roomID, roundID, "SETTLED", 1, "user-1", 500)

	repo := &mockRoomRepository{meta: &room.RoomMeta{RoomID: roomID}}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestRoundSettlementService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	svc.SettleRound(ctx, roomID, roundID)

	// 幂等返回不应广播
	if broadcaster.callCount() != 0 {
		t.Errorf("幂等返回不应广播，但 got %d 次调用", broadcaster.callCount())
	}
	if publisher.eventCount() != 0 {
		t.Errorf("幂等返回不应发布事件，但 got %d 个事件", publisher.eventCount())
	}
}

// =============================================================================
// SettleRound 游戏结束路径测试
// =============================================================================

// TestSettleRoundGameEnd 验证 SettleRound 在 round_no >= max_rounds 时触发游戏结束：
// isGameEnd=1，通过异步任务调用 GameEnder.EndGameWithOptions。
func TestSettleRoundGameEnd(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-end"
	roundID := "round-settle-end"

	// 预置状态：round_no=5, max_rounds=5 → isGameEnd=1
	setupRoundState(t, roomID, roundID, "GRABBING", 5, "user-1", 500)
	setupRoomHash(t, roomID, 5)
	setupPlayerAndGrabber(t, roomID, roundID, "user-1")

	repo := &mockRoomRepository{meta: &room.RoomMeta{RoomID: roomID, CurrentSessionID: "session-end"}}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestRoundSettlementService(repo, broadcaster, publisher, nil, taskRunner, idGen)

	// 注入 mock GameEnder
	gameEnder := &mockGameEnder{}
	svc.SetGameEnder(gameEnder)

	svc.SettleRound(ctx, roomID, roundID)

	// 验证 gameEnder 被调用
	if gameEnder.callCount() != 1 {
		t.Errorf("期望 gameEnder 被调用 1 次，got %d", gameEnder.callCount())
	}
	if gameEnder.lastRoomID != roomID {
		t.Errorf("gameEnder roomID got %q, want %q", gameEnder.lastRoomID, roomID)
	}
	if gameEnder.lastOpts == nil {
		t.Fatal("gameEnder opts 不应为 nil")
	}
	if gameEnder.lastOpts.ActualRounds != 5 {
		t.Errorf("ActualRounds got %d, want 5", gameEnder.lastOpts.ActualRounds)
	}
}

// =============================================================================
// SettleRound nil 依赖安全测试
// =============================================================================

// TestSettleRoundNilBroadcaster 验证 broadcaster=nil 时不 panic。
func TestSettleRoundNilBroadcaster(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-nil-bc"
	roundID := "round-settle-nil-bc"

	setupRoundState(t, roomID, roundID, "GRABBING", 1, "user-1", 500)
	setupRoomHash(t, roomID, 10)

	repo := &mockRoomRepository{meta: &room.RoomMeta{RoomID: roomID}}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	// broadcaster=nil
	svc := newTestRoundSettlementService(repo, nil, publisher, nil, taskRunner, idGen)

	// 不应 panic
	svc.SettleRound(ctx, roomID, roundID)

	// phase 仍应变为 SETTLED
	rdb := testRedisClient.Raw()
	phase, _ := rdb.HGet(ctx, rediskeys.RoundStateKey(roundID), "phase").Result()
	if phase != "SETTLED" {
		t.Errorf("phase got %q, want SETTLED", phase)
	}
}

// TestSettleRoundNilEventPublisher 验证 eventPublisher=nil 时不 panic。
func TestSettleRoundNilEventPublisher(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-nil-ep"
	roundID := "round-settle-nil-ep"

	setupRoundState(t, roomID, roundID, "GRABBING", 1, "user-1", 500)
	setupRoomHash(t, roomID, 10)

	repo := &mockRoomRepository{meta: &room.RoomMeta{RoomID: roomID}}
	broadcaster := &mockBroadcaster{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	// eventPublisher=nil
	svc := newTestRoundSettlementService(repo, broadcaster, nil, nil, taskRunner, idGen)

	// 不应 panic
	svc.SettleRound(ctx, roomID, roundID)

	// 广播仍应正常
	if broadcaster.callCount() == 0 {
		t.Error("broadcaster 不为 nil 时应正常广播")
	}
}

// =============================================================================
// SettleRound 仓储错误测试
// =============================================================================

// TestSettleRoundRepoError 验证 GetRoomMeta 返回错误时 SettleRound 仍正常执行（meta=nil 时走默认路径）。
func TestSettleRoundRepoError(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-repo-err"
	roundID := "round-settle-repo-err"

	setupRoundState(t, roomID, roundID, "GRABBING", 1, "user-1", 500)
	setupRoomHash(t, roomID, 10)

	// repo 返回错误
	repo := &mockRoomRepository{metaErr: context.DeadlineExceeded}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestRoundSettlementService(repo, broadcaster, publisher, nil, taskRunner, idGen)

	// 不应 panic
	svc.SettleRound(ctx, roomID, roundID)

	// meta=nil 时仍应执行 Lua（sessionPlayerTotalsKey 为空字符串）
	rdb := testRedisClient.Raw()
	phase, _ := rdb.HGet(ctx, rediskeys.RoundStateKey(roundID), "phase").Result()
	if phase != "SETTLED" {
		t.Errorf("phase got %q, want SETTLED（meta=nil 不应阻止 Lua 执行）", phase)
	}
}

// =============================================================================
// SettleRound 调度器集成测试
// =============================================================================

// TestSettleRoundSchedulerSetTimeout 验证非游戏结束时调度器被调用来设置发送超时。
func TestSettleRoundSchedulerSetTimeout(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-settle-sched"
	roundID := "round-settle-sched"

	// round_no=1, max_rounds=10 → isGameEnd=0
	setupRoundState(t, roomID, roundID, "GRABBING", 1, "user-1", 500)
	setupRoomHash(t, roomID, 10)
	setupPlayerAndGrabber(t, roomID, roundID, "user-1")

	repo := &mockRoomRepository{meta: &room.RoomMeta{RoomID: roomID, CurrentSessionID: "session-sched"}}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	// 使用真实 TimeoutScheduler（基于共享 miniredis）
	sched := newTestTimeoutScheduler()

	svc := newTestRoundSettlementService(repo, broadcaster, publisher, sched, taskRunner, idGen)
	svc.SettleRound(ctx, roomID, roundID)

	// 验证调度器设置了 send 超时（ZSET 中应有成员）
	// minAmountPlayer='0'（allSameAmount=true，无抢包金额差异），所以 member 为 "roomID:0"
	sendKey := rediskeys.TimeoutKey(string(scheduler.TimeoutTypeSend))
	members, err := testRedisClient.ZRangeByScore(ctx, sendKey, &cRedis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	if err != nil {
		t.Fatalf("ZRangeByScore 失败: %v", err)
	}

	found := false
	for _, m := range members {
		if m == roomID+":0" || m == roomID+":user-1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望调度器设置 send 超时（roomID=%s），ZSET members=%v", roomID, members)
	}
}
