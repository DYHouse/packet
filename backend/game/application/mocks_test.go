package application

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/snowflake"
	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/lock"
	cRedis "github.com/cashparty/backend/common/redis"
	eventsDom "github.com/cashparty/backend/game/domain/events"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/game/scheduler"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// 测试环境初始化
// ============================================================================

// testMiniredis 测试用 miniredis 实例，供 lock.WithRedisLock 与 Lua 脚本使用。
var testMiniredis *miniredis.Miniredis

// testRedisClient 测试用 Redis 客户端，连接到 testMiniredis。
var testRedisClient cRedis.RedisClient

// TestMain 启动 miniredis 并初始化分布式锁。
// lock.InitLocker 使用 sync.Once，整个测试进程只需初始化一次。
// 所有需要分布式锁与 Lua 脚本的测试共享同一个 miniredis 实例。
func TestMain(m *testing.M) {
	mr, err := miniredis.Run()
	if err != nil {
		panic("miniredis 启动失败: " + err.Error())
	}
	testMiniredis = mr

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cRedis.SetUseEvalSHA(false)
	testRedisClient = cRedis.NewClientFromRaw(rdb)

	// 初始化分布式锁（lock.WithRedisLock 依赖此初始化）
	lock.InitLocker(testRedisClient)

	code := m.Run()
	rdb.Close()
	mr.Close()
	os.Exit(code)
}

// newTestCtx 返回测试用 context，并在测试结束后清理 miniredis 数据以隔离测试。
func newTestCtx(t *testing.T) context.Context {
	t.Helper()
	t.Cleanup(testMiniredis.FlushAll)
	return context.Background()
}

// newTestTimeoutScheduler 构造测试用 TimeoutScheduler（基于共享 miniredis）。
func newTestTimeoutScheduler() *scheduler.TimeoutScheduler {
	return scheduler.NewTimeoutScheduler(testRedisClient, &scheduler.Config{
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

// newTestTimeoutCfg 构造带默认值的超时配置。
func newTestTimeoutCfg() *config.TimeoutConfig {
	cfg := &config.TimeoutConfig{}
	config.SetTimeoutDefaults(cfg)
	return cfg
}

// newTestLockCfg 构造带默认值的锁配置。
func newTestLockCfg() *config.LockConfig {
	cfg := &config.LockConfig{}
	config.SetLockDefaults(cfg)
	return cfg
}

// newTestRedisTTL 构造带默认值的 Redis TTL 配置。
func newTestRedisTTL() config.RedisTTLConfig {
	cfg := config.RedisTTLConfig{}
	config.SetRedisTTLDefaults(&cfg)
	return cfg
}

// ============================================================================
// repository.RoomRepository 桩件
// ============================================================================

// mockRoomRepository 房间仓储桩件。
// 通过嵌入 nil 接口满足完整契约，仅覆盖测试所需方法。
type mockRoomRepository struct {
	repository.RoomRepository
	meta          *room.RoomMeta
	metaErr       error
	player        *room.Player
	playerErr     error
	nextSenderID  string
	nextSenderErr error
	stateData     *repository.RoomStateData
	stateErr      error
	sessionErr    error
}

func (r *mockRoomRepository) GetRoomMeta(_ context.Context, _ string) (*room.RoomMeta, error) {
	if r.metaErr != nil {
		return nil, r.metaErr
	}
	return r.meta, nil
}

func (r *mockRoomRepository) GetPlayer(_ context.Context, _, _ string) (*room.Player, error) {
	if r.playerErr != nil {
		return nil, r.playerErr
	}
	return r.player, nil
}

func (r *mockRoomRepository) GetNextSenderID(_ context.Context, _ string) (string, error) {
	return r.nextSenderID, r.nextSenderErr
}

func (r *mockRoomRepository) GetRoomStateData(_ context.Context, _ string) (*repository.RoomStateData, error) {
	if r.stateErr != nil {
		return nil, r.stateErr
	}
	return r.stateData, nil
}

func (r *mockRoomRepository) UpdateRoomSessionID(_ context.Context, _, _ string) error {
	return r.sessionErr
}

// ============================================================================
// eventsDom.Broadcaster 桩件
// ============================================================================

// broadcastCall 记录一次 Broadcast 调用的参数。
type broadcastCall struct {
	RoomID        string
	Event         string
	Data          interface{}
	ExcludeUserID string
}

// mockBroadcaster 广播桩件，记录所有 Broadcast 调用供测试断言。
type mockBroadcaster struct {
	mu    sync.Mutex
	calls []broadcastCall
}

func (b *mockBroadcaster) Broadcast(_ context.Context, roomID, event string, data interface{}, excludeUserID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, broadcastCall{roomID, event, data, excludeUserID})
	return nil
}

func (b *mockBroadcaster) BroadcastToUser(_ context.Context, _ string, _ string, _ interface{}) error {
	return nil
}

func (b *mockBroadcaster) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

func (b *mockBroadcaster) lastCall() *broadcastCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		return nil
	}
	return &b.calls[len(b.calls)-1]
}

func (b *mockBroadcaster) findByEvent(event string) *broadcastCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.calls) - 1; i >= 0; i-- {
		if b.calls[i].Event == event {
			return &b.calls[i]
		}
	}
	return nil
}

// ============================================================================
// eventsDom.GameEventPublisher 桩件
// ============================================================================

// mockGameEventPublisher 游戏事件发布桩件，记录所有 PublishGameEvent 调用。
type mockGameEventPublisher struct {
	mu     sync.Mutex
	events []*eventsDom.GameEvent
	err    error
}

func (p *mockGameEventPublisher) PublishGameEvent(_ context.Context, event *eventsDom.GameEvent) error {
	if p.err != nil {
		return p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
	return nil
}

func (p *mockGameEventPublisher) eventCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

// ============================================================================
// async.TaskRunner 桩件
// ============================================================================

// submittedTask 记录一次 Submit 调用的参数。
type submittedTask struct {
	taskID string
	ttl    time.Duration
	task   func(ctx context.Context)
}

// mockTaskRunner 异步任务运行器桩件。
// syncRun=true 时同步执行任务（便于测试断言副作用）；
// syncRun=false 时仅记录任务不执行。
type mockTaskRunner struct {
	mu      sync.Mutex
	syncRun bool
	tasks   []submittedTask
	closed  bool
}

func (r *mockTaskRunner) Start() error { return nil }

func (r *mockTaskRunner) Submit(taskID string, ttl time.Duration, task func(ctx context.Context)) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("task runner is closed, rejected task: %s", taskID)
	}
	r.tasks = append(r.tasks, submittedTask{taskID, ttl, task})
	r.mu.Unlock()

	if r.syncRun {
		ctx, cancel := context.WithTimeout(context.Background(), ttl)
		defer cancel()
		task(ctx)
	}
	return nil
}

func (r *mockTaskRunner) Stop() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
}

func (r *mockTaskRunner) Wait() {}

func (r *mockTaskRunner) taskCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tasks)
}

// ============================================================================
// idgen.IDGenerator 桩件
// ============================================================================

// mockIDGenerator 雪花 ID 生成器桩件，返回递增的固定格式 ID。
type mockIDGenerator struct {
	mu     sync.Mutex
	id     int64
	strID  string
	nodeID int64
	err    error
}

func (g *mockIDGenerator) GenerateInt64() (int64, error) {
	if g.err != nil {
		return 0, g.err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.id++
	return g.id, nil
}

func (g *mockIDGenerator) GenerateString() (string, error) {
	if g.err != nil {
		return "", g.err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.strID != "" {
		return g.strID, nil
	}
	g.id++
	return fmt.Sprintf("test-id-%d", g.id), nil
}

func (g *mockIDGenerator) GenerateID() (snowflake.ID, error) {
	if g.err != nil {
		return 0, g.err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.id++
	return snowflake.ID(g.id), nil
}

func (g *mockIDGenerator) GetNodeID() int64 {
	return g.nodeID
}

// ============================================================================
// repository.DBRepository 桩件
// ============================================================================

// mockDBRepository 数据库仓储桩件，返回预配置的子 repo。
type mockDBRepository struct {
	repository.DBRepository
	roundRepo repository.RoundDBRepository
}

func (d *mockDBRepository) RoundDBRepo() repository.RoundDBRepository {
	return d.roundRepo
}

// mockRoundDBRepository 回合数据库仓储桩件，记录 UpdateRoundStatus/UpdateRoundFailed 调用。
type mockRoundDBRepository struct {
	repository.RoundDBRepository
	mu                sync.Mutex
	updateStatusCalls int
	updateFailedCalls int
	err               error
}

func (r *mockRoundDBRepository) UpdateRoundStatus(_ context.Context, _ int64, _ model.RoundStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateStatusCalls++
	return r.err
}

func (r *mockRoundDBRepository) UpdateRoundFailed(_ context.Context, _ int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateFailedCalls++
	return r.err
}

func (r *mockRoundDBRepository) updateStatusCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updateStatusCalls
}

// ============================================================================
// application 层接口桩件
// ============================================================================

// mockPacketInitiator 发包启动器桩件，实现 PacketInitiator 接口。
type mockPacketInitiator struct {
	mu              sync.Mutex
	firstRoundCalls int
	systemSendCalls int
	lastRoomID      string
	lastMeta        *room.RoomMeta
	lastRoundNo     int
}

func (p *mockPacketInitiator) StartFirstRound(_ context.Context, roomID string, meta *room.RoomMeta) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.firstRoundCalls++
	p.lastRoomID = roomID
	p.lastMeta = meta
}

func (p *mockPacketInitiator) HandleSystemSendTimeout(_ context.Context, _ string) {}

func (p *mockPacketInitiator) ForceSendPacketForPlayer(_ context.Context, _, _ string, _ int64) {}

func (p *mockPacketInitiator) SystemSendRound(_ context.Context, roomID string, meta *room.RoomMeta, roundNo int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.systemSendCalls++
	p.lastRoomID = roomID
	p.lastMeta = meta
	p.lastRoundNo = roundNo
}

// mockGameEnder 游戏结束处理器桩件，实现 GameEnder 接口。
type mockGameEnder struct {
	mu           sync.Mutex
	endGameCalls int
	lastRoomID   string
	lastOpts     *EndGameOptions
	err          error
}

func (e *mockGameEnder) EndGameWithOptions(_ context.Context, roomID string, opts *EndGameOptions) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.endGameCalls++
	e.lastRoomID = roomID
	e.lastOpts = opts
	return e.err
}

func (e *mockGameEnder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.endGameCalls
}

// mockDeductFailureHandler 扣款失败处理器桩件，实现 DeductFailureHandler 接口。
type mockDeductFailureHandler struct {
	mu         sync.Mutex
	calls      int
	lastRoomID string
	lastMeta   *room.RoomMeta
	lastReason string
	lastErr    error
}

func (h *mockDeductFailureHandler) HandleDeductFailure(_ context.Context, roomID string, meta *room.RoomMeta, reason string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	h.lastRoomID = roomID
	h.lastMeta = meta
	h.lastReason = reason
	h.lastErr = err
}

// ============================================================================
// 测试辅助构造器
// ============================================================================

// newTestRoundSettlementService 构造测试用 RoundSettlementService。
// 使用共享 miniredis 与默认配置。
func newTestRoundSettlementService(
	repo repository.RoomRepository,
	broadcaster eventsDom.Broadcaster,
	eventPublisher eventsDom.GameEventPublisher,
	schedulerInst *scheduler.TimeoutScheduler,
	taskRunner async.TaskRunner,
	idGen *mockIDGenerator,
) *RoundSettlementService {
	return NewRoundSettlementService(
		repo, broadcaster, eventPublisher,
		testRedisClient,
		newTestTimeoutCfg(), newTestLockCfg(),
		schedulerInst, taskRunner, idGen,
	)
}

// newTestGameLifecycleService 构造测试用 GameLifecycleService。
// penaltyService/settleAppService/grabService 传 nil（仅用于不依赖这些的路径测试）。
func newTestGameLifecycleService(
	repo repository.RoomRepository,
	broadcaster eventsDom.Broadcaster,
	eventPublisher eventsDom.GameEventPublisher,
	schedulerInst *scheduler.TimeoutScheduler,
	taskRunner async.TaskRunner,
	idGen *mockIDGenerator,
) *GameLifecycleService {
	return NewGameLifecycleService(
		repo, nil, broadcaster, eventPublisher,
		schedulerInst, testRedisClient,
		newTestLockCfg(), newTestTimeoutCfg(), newTestRedisTTL(),
		nil, nil, nil, // penaltyService, settleAppService, grabService（测试路径不依赖）
		taskRunner, idGen,
	)
}

// newTestPacketOrchestrator 构造测试用 PacketOrchestrator。
// settleAppService 传 nil（仅用于错误路径测试，不触发实际扣款）。
func newTestPacketOrchestrator(
	repo repository.RoomRepository,
	dbRepo repository.DBRepository,
	broadcaster eventsDom.Broadcaster,
	eventPublisher eventsDom.GameEventPublisher,
	taskRunner async.TaskRunner,
	idGen *mockIDGenerator,
	schedulerInst *scheduler.TimeoutScheduler,
) *PacketOrchestrator {
	return NewPacketOrchestrator(
		repo, dbRepo, broadcaster, eventPublisher,
		nil, nil, nil, // grabService, packetGenerator, packetCache（错误路径不依赖）
		nil, // settleAppService（错误路径不依赖）
		taskRunner, idGen,
		newTestLockCfg(), newTestTimeoutCfg(),
		schedulerInst,
	)
}
