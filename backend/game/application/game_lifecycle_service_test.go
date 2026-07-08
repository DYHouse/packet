package application

import (
	"context"
	"testing"
	"time"

	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/room"
)

// setupRoomHashForStartGame 在 miniredis 中预置房间哈希使 TryStartGame Lua 返回成功。
// 需要 status=2(Playing), countdown_end_time=过去时间, 无 started_at。
func setupRoomHashForStartGame(t *testing.T, roomID string) {
	t.Helper()
	rdb := testRedisClient.Raw()
	ctx := context.Background()

	roomHashKey := rediskeys.RoomHashKey(roomID)
	pastTime := time.Now().Add(-60 * time.Second).Unix()
	rdb.HSet(ctx, roomHashKey, "status", 2, "countdown_end_time", pastTime)
}

// setupRoomHashForAlreadyStarted 预置房间哈希使 TryStartGame 返回"已启动"。
func setupRoomHashForAlreadyStarted(t *testing.T, roomID string) {
	t.Helper()
	rdb := testRedisClient.Raw()
	ctx := context.Background()

	roomHashKey := rediskeys.RoomHashKey(roomID)
	pastTime := time.Now().Add(-60 * time.Second).Unix()
	rdb.HSet(ctx, roomHashKey, "status", 2, "countdown_end_time", pastTime, "started_at", pastTime)
}

// =============================================================================
// StartGame 测试
// =============================================================================

// TestStartGameSuccess 验证 StartGame 成功路径：
// TryStartGame Lua 返回 0，生成 sessionID，广播 PushGameStart，调用 packetInitiator.StartFirstRound。
func TestStartGameSuccess(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-start-ok"

	setupRoomHashForStartGame(t, roomID)

	meta := &room.RoomMeta{
		RoomID:     roomID,
		RoomNo:     "R001",
		ConfigID:   1,
		ConfigName: "TestConfig",
		RoomFee:    500,
		MaxPlayers: 5,
		MaxRounds:  10,
		Status:     room.RoomStatusPlaying,
	}

	repo := &mockRoomRepository{meta: meta}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{strID: "session-start-ok"}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)

	// 注入 mock PacketInitiator
	packetInit := &mockPacketInitiator{}
	svc.SetPacketInitiator(packetInit)

	svc.StartGame(ctx, roomID)

	// 验证广播了 PushGameStart
	call := broadcaster.findByEvent(message.PushGameStart)
	if call == nil {
		t.Fatal("期望广播 PushGameStart，但未找到")
	}
	if call.RoomID != roomID {
		t.Errorf("PushGameStart roomID got %q, want %q", call.RoomID, roomID)
	}

	// 验证 packetInitiator.StartFirstRound 被调用
	if packetInit.firstRoundCalls != 1 {
		t.Errorf("期望 StartFirstRound 被调用 1 次，got %d", packetInit.firstRoundCalls)
	}
	if packetInit.lastRoomID != roomID {
		t.Errorf("StartFirstRound roomID got %q, want %q", packetInit.lastRoomID, roomID)
	}

	// 验证 roomHashKey 中的 started_at 已设置
	rdb := testRedisClient.Raw()
	startedAt, err := rdb.HGet(ctx, rediskeys.RoomHashKey(roomID), "started_at").Result()
	if err != nil {
		t.Fatalf("HGet started_at 失败: %v", err)
	}
	if startedAt == "0" || startedAt == "" {
		t.Errorf("started_at 应为非零时间戳，got %q", startedAt)
	}
}

// TestStartGameAlreadyStarted 验证 TryStartGame 返回非零时 StartGame 跳过启动。
func TestStartGameAlreadyStarted(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-start-skip"

	setupRoomHashForAlreadyStarted(t, roomID)

	repo := &mockRoomRepository{}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	packetInit := &mockPacketInitiator{}
	svc.SetPacketInitiator(packetInit)

	svc.StartGame(ctx, roomID)

	// 不应广播
	if broadcaster.callCount() != 0 {
		t.Errorf("已启动的房间不应广播，但 got %d 次调用", broadcaster.callCount())
	}
	// 不应调用 StartFirstRound
	if packetInit.firstRoundCalls != 0 {
		t.Errorf("已启动的房间不应调用 StartFirstRound，got %d", packetInit.firstRoundCalls)
	}
}

// TestStartGameStatusNotCountdown 验证房间状态非 Playing(2) 时 StartGame 跳过。
func TestStartGameStatusNotCountdown(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-start-wrong-status"

	// status=1 (Waiting)，不是 2
	rdb := testRedisClient.Raw()
	roomHashKey := rediskeys.RoomHashKey(roomID)
	rdb.HSet(ctx, roomHashKey, "status", 1, "countdown_end_time", time.Now().Add(-60*time.Second).Unix())

	repo := &mockRoomRepository{}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	packetInit := &mockPacketInitiator{}
	svc.SetPacketInitiator(packetInit)

	svc.StartGame(ctx, roomID)

	// 不应广播
	if broadcaster.callCount() != 0 {
		t.Errorf("状态非 Playing 的房间不应广播，got %d", broadcaster.callCount())
	}
	if packetInit.firstRoundCalls != 0 {
		t.Errorf("状态非 Playing 的房间不应调用 StartFirstRound，got %d", packetInit.firstRoundCalls)
	}
}

// TestStartGameCountdownNotFinished 验证倒计时未结束时 StartGame 跳过。
func TestStartGameCountdownNotFinished(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-start-countdown"

	// countdown_end_time 设为未来时间
	rdb := testRedisClient.Raw()
	roomHashKey := rediskeys.RoomHashKey(roomID)
	futureTime := time.Now().Add(60 * time.Second).Unix()
	rdb.HSet(ctx, roomHashKey, "status", 2, "countdown_end_time", futureTime)

	repo := &mockRoomRepository{}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	packetInit := &mockPacketInitiator{}
	svc.SetPacketInitiator(packetInit)

	svc.StartGame(ctx, roomID)

	if broadcaster.callCount() != 0 {
		t.Errorf("倒计时未结束不应广播，got %d", broadcaster.callCount())
	}
}

// =============================================================================
// ResumeGame 测试
// =============================================================================

// TestResumeGameSuccess 验证 ResumeGame 成功路径：
// 广播 PushGameResumed，调用 packetInitiator.SystemSendRound。
func TestResumeGameSuccess(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-resume-ok"

	meta := &room.RoomMeta{
		RoomID:       roomID,
		RoomNo:       "R002",
		ConfigID:     2,
		ConfigName:   "ResumeConfig",
		RoomFee:      300,
		MaxPlayers:   5,
		MaxRounds:    8,
		Status:       room.RoomStatusPlaying,
		CurrentRound: 3,
	}

	repo := &mockRoomRepository{meta: meta}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	packetInit := &mockPacketInitiator{}
	svc.SetPacketInitiator(packetInit)

	err := svc.ResumeGame(ctx, &ResumeGameRequest{
		RoomID:       roomID,
		CurrentRound: 3,
	})
	if err != nil {
		t.Fatalf("ResumeGame 返回错误: %v", err)
	}

	// 验证广播了 PushGameResumed
	call := broadcaster.findByEvent(message.PushGameResumed)
	if call == nil {
		t.Fatal("期望广播 PushGameResumed，但未找到")
	}
	if call.RoomID != roomID {
		t.Errorf("PushGameResumed roomID got %q, want %q", call.RoomID, roomID)
	}

	// 验证 packetInitiator.SystemSendRound 被调用
	if packetInit.systemSendCalls != 1 {
		t.Errorf("期望 SystemSendRound 被调用 1 次，got %d", packetInit.systemSendCalls)
	}
	if packetInit.lastRoundNo != 3 {
		t.Errorf("SystemSendRound roundNo got %d, want 3", packetInit.lastRoundNo)
	}
}

// TestResumeGameNilPacketInitiator 验证 packetInitiator=nil 时 ResumeGame 不 panic。
func TestResumeGameNilPacketInitiator(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-resume-nil-init"

	meta := &room.RoomMeta{RoomID: roomID, Status: room.RoomStatusPlaying}
	repo := &mockRoomRepository{meta: meta}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	// 不注入 packetInitiator（保持 nil）

	err := svc.ResumeGame(ctx, &ResumeGameRequest{
		RoomID:       roomID,
		CurrentRound: 1,
	})
	if err != nil {
		t.Fatalf("ResumeGame 返回错误: %v", err)
	}

	// 广播仍应正常
	if broadcaster.callCount() == 0 {
		t.Error("packetInitiator=nil 时仍应广播 PushGameResumed")
	}
}

// =============================================================================
// HandleDeductFailure 测试（P0-1 facade 模式验证）
// =============================================================================

// TestHandleDeductFailure 验证扣款失败处理器：
// 广播 PushGameInterrupted，通过异步任务调用 EndGameWithOptions（P0-1 facade 模式）。
func TestHandleDeductFailure(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-deduct-fail"

	meta := &room.RoomMeta{
		RoomID:           roomID,
		Status:           room.RoomStatusPlaying,
		CurrentSessionID: "session-fail",
		CurrentRound:     2,
	}

	repo := &mockRoomRepository{meta: meta}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: false} // 不执行任务，仅验证提交
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)

	svc.HandleDeductFailure(ctx, roomID, meta, message.ReasonLaterRoundDeductFailed, context.DeadlineExceeded)

	// 验证广播了 PushGameInterrupted
	call := broadcaster.findByEvent(message.PushGameInterrupted)
	if call == nil {
		t.Fatal("期望广播 PushGameInterrupted，但未找到")
	}
	if call.RoomID != roomID {
		t.Errorf("PushGameInterrupted roomID got %q, want %q", call.RoomID, roomID)
	}

	// 验证异步任务已提交（P0-1：通过 TaskRunner 编排跨 Service 调用）
	if taskRunner.taskCount() != 1 {
		t.Errorf("期望提交 1 个异步任务，got %d", taskRunner.taskCount())
	}
}

// =============================================================================
// StartGame 边界场景测试
// =============================================================================

// TestStartGameNilPacketInitiator 验证 packetInitiator=nil 时 StartGame 不 panic。
func TestStartGameNilPacketInitiator(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-start-nil-init"

	setupRoomHashForStartGame(t, roomID)

	meta := &room.RoomMeta{
		RoomID:     roomID,
		MaxRounds:  10,
		MaxPlayers: 5,
		Status:     room.RoomStatusPlaying,
	}

	repo := &mockRoomRepository{meta: meta}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{strID: "session-nil-init"}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	// 不注入 packetInitiator

	svc.StartGame(ctx, roomID)

	// 广播仍应正常
	call := broadcaster.findByEvent(message.PushGameStart)
	if call == nil {
		t.Error("packetInitiator=nil 时仍应广播 PushGameStart")
	}
}

// TestStartGameRepoError 验证 GetRoomMeta 返回错误时 StartGame 不 panic（记录日志后返回）。
func TestStartGameRepoError(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-start-repo-err"

	setupRoomHashForStartGame(t, roomID)

	// repo 返回错误
	repo := &mockRoomRepository{metaErr: context.DeadlineExceeded}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestGameLifecycleService(repo, broadcaster, publisher, nil, taskRunner, idGen)
	packetInit := &mockPacketInitiator{}
	svc.SetPacketInitiator(packetInit)

	// 不应 panic
	svc.StartGame(ctx, roomID)

	// meta 获取失败后不应继续启动流程
	if packetInit.firstRoundCalls != 0 {
		t.Errorf("GetRoomMeta 失败时不应调用 StartFirstRound，got %d", packetInit.firstRoundCalls)
	}
}
