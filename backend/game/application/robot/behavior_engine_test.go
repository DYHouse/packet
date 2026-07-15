package robot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cashparty/backend/common/config"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/scheduler"
	"github.com/redis/go-redis/v9"
)

// mockRobotActionExecutor 实现 RobotActionExecutor 接口，记录调用并返回预设错误。
type mockRobotActionExecutor struct {
	mu             sync.Mutex
	selectSeatErr  error
	leaveRoomCalls []string
	leaveRoomErr   error
}

func (m *mockRobotActionExecutor) SelectSeat(_ context.Context, _ string, _ string) error {
	return m.selectSeatErr
}

func (m *mockRobotActionExecutor) Ready(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockRobotActionExecutor) GrabPacket(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (m *mockRobotActionExecutor) SendPacket(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockRobotActionExecutor) LeaveRoom(_ context.Context, _ string, robotUserID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leaveRoomCalls = append(m.leaveRoomCalls, robotUserID)
	return m.leaveRoomErr
}

func (m *mockRobotActionExecutor) LeaveRoomCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.leaveRoomCalls)
}

// mockIdleMarker 实现 RobotIdleMarker 接口。
type mockIdleMarker struct{}

func (m *mockIdleMarker) MarkRobotIdle(_ context.Context, _ int64) error {
	return nil
}

// mockRobotSchedulerRepo 实现 repository.RobotSchedulerRepository 接口，
// 仅提供 handleLeaveAction 路径所需的方法行为。
type mockRobotSchedulerRepo struct {
	roomRobots []int64
}

func (m *mockRobotSchedulerRepo) AddRobotToRoom(_ context.Context, _ string, _ int64) error {
	return nil
}
func (m *mockRobotSchedulerRepo) RemoveRobotFromRoom(_ context.Context, _ string, _ int64) error {
	return nil
}
func (m *mockRobotSchedulerRepo) GetRoomRobots(_ context.Context, roomID string) ([]int64, error) {
	return m.roomRobots, nil
}
func (m *mockRobotSchedulerRepo) GetRoomPlayerRobots(_ context.Context, _ string) ([]int64, error) {
	return nil, nil
}
func (m *mockRobotSchedulerRepo) AcquireAssignLock(_ context.Context, _ int64, _ string, _ time.Duration) (bool, string, error) {
	return false, "", nil
}
func (m *mockRobotSchedulerRepo) ReleaseAssignLock(_ context.Context, _ int64, _ string) error {
	return nil
}
func (m *mockRobotSchedulerRepo) AcquireRoomAssignLock(_ context.Context, _ string, _ time.Duration) (bool, string, error) {
	return false, "", nil
}
func (m *mockRobotSchedulerRepo) ReleaseRoomAssignLock(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockRobotSchedulerRepo) AddToActiveSet(_ context.Context, _ int64) error {
	return nil
}
func (m *mockRobotSchedulerRepo) RemoveFromActiveSet(_ context.Context, _ int64) error {
	return nil
}
func (m *mockRobotSchedulerRepo) ScanRoomIDs(_ context.Context, _ string, _ int64) ([]string, error) {
	return nil, nil
}

// newBehaviorEngineForTest 构造测试用 RobotBehaviorEngine，使用 mock 依赖。
// robotUserID 必须为纯数字字符串（converter.ParseID 解析）。
func newBehaviorEngineForTest(t *testing.T, selectSeatErr error, leaveRoomErr error, roomRobots []int64) (*RobotBehaviorEngine, *mockRobotActionExecutor) {
	t.Helper()

	mockPlayer := &mockRobotActionExecutor{
		selectSeatErr: selectSeatErr,
		leaveRoomErr:  leaveRoomErr,
	}
	mockAccount := &mockIdleMarker{}
	mockRepo := &mockRobotSchedulerRepo{roomRobots: roomRobots}

	// 使用 miniredis 构造真实的 TimeoutScheduler（需验证 retry 没有被调度）
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis start failed: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cRedis.SetUseEvalSHA(false)
	redisClient := cRedis.NewClientFromRaw(rdb)

	ts := scheduler.NewTimeoutScheduler(redisClient, &scheduler.Config{
		Robot:          5 * time.Second,
		CheckInterval:  10 * time.Millisecond,
		HandlerTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := ts.Start(ctx); err != nil {
		t.Fatalf("timeout scheduler start failed: %v", err)
	}
	t.Cleanup(ts.Stop)

	engine := NewRobotBehaviorEngine(
		&config.RobotConfig{
			Behavior: config.BehaviorConfig{
				ActionRetryMax:   2,
				ActionRetryDelay: 2 * time.Second,
			},
		},
		mockPlayer,
		ts,
		mockAccount,
		nil, // grabSvc 不参与 seat 路径
		mockRepo,
	)
	return engine, mockPlayer
}

// TestHandleRobotTimeout_SeatNoEmptySeat_LeavesImmediately 验证 seat 动作遇到
// ErrNoEmptySeat 时立即离开房间，不调度重试。
func TestHandleRobotTimeout_SeatNoEmptySeat_LeavesImmediately(t *testing.T) {
	const robotUserID = "203286303167483904"
	engine, mockPlayer := newBehaviorEngineForTest(t, ErrNoEmptySeat, nil, []int64{203286303167483904})

	ctx := context.Background()
	// 模拟 timeout 调度器回调：seat 动作，retry_count=0
	engine.HandleRobotTimeout(ctx, "room123", robotUserID+":seat:uuid123:0")

	// 机器人应立即离开房间
	if count := mockPlayer.LeaveRoomCallCount(); count != 1 {
		t.Fatalf("expected LeaveRoom to be called once, got %d", count)
	}

	// 等待足够时间确认没有 retry 被调度（retry delay=2s，等待 3s 足够覆盖）
	time.Sleep(3 * time.Second)

	// retry 不会触发第二次 SelectSeat（LeaveRoom 仍只被调用一次）
	if count := mockPlayer.LeaveRoomCallCount(); count != 1 {
		t.Errorf("expected LeaveRoom call count to remain 1 after retry window, got %d", count)
	}
}

// TestHandleRobotTimeout_SeatTransientError_SchedulesRetry 验证 seat 动作遇到
// 非 ErrNoEmptySeat 的瞬时错误时正常调度重试（不立即离开）。
func TestHandleRobotTimeout_SeatTransientError_SchedulesRetry(t *testing.T) {
	const robotUserID = "203286303167483904"
	transientErr := errTransient("some transient error")
	engine, mockPlayer := newBehaviorEngineForTest(t, transientErr, nil, []int64{203286303167483904})

	ctx := context.Background()
	engine.HandleRobotTimeout(ctx, "room123", robotUserID+":seat:uuid123:0")

	// 瞬时错误不应立即离开
	if count := mockPlayer.LeaveRoomCallCount(); count != 0 {
		t.Fatalf("expected LeaveRoom not to be called for transient error, got %d", count)
	}
}

// errTransient 用于测试的瞬时错误类型。
type errTransient string

func (e errTransient) Error() string { return string(e) }
