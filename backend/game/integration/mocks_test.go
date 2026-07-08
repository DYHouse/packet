package integration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/snowflake"
	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	cRedis "github.com/cashparty/backend/common/redis"
	gameAlg "github.com/cashparty/backend/game/algorithm"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/settlement/domain"
	settlementRepository "github.com/cashparty/backend/settlement/domain/repository"
	settlementMysqlRepo "github.com/cashparty/backend/settlement/infrastructure/persistence/mysql"
	"github.com/cashparty/backend/settlement/model"
	"github.com/cashparty/backend/settlement/service"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
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

// ============================================================================
// 游戏领域桩件（game/domain）
// ============================================================================

// packetCacheStub 红包缓存仓储桩件，用于测试 PacketGenerator 的缓存交互。
type packetCacheStub struct {
	mu       sync.Mutex
	getResp  map[string]string
	getErr   error
	setNXOk  bool
	setNXErr error
}

func (s *packetCacheStub) Get(_ context.Context, roundID string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	if v, ok := s.getResp[roundID]; ok {
		return v, nil
	}
	return "", s.getErr
}

func (s *packetCacheStub) SetNX(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setNXErr != nil {
		return false, s.setNXErr
	}
	return s.setNXOk, nil
}

func (s *packetCacheStub) GetPacketInfo(_ context.Context, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func (s *packetCacheStub) GetAvailablePacketIDs(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// rewardCacheStub 奖励缓存仓储桩件，用于测试 RewardController。
type rewardCacheStub struct {
	mu              sync.Mutex
	setCycleWonCall int
	clearCall       int
	recordCall      int
}

func (s *rewardCacheStub) GetCycleWon(_ context.Context, _, _ string, _ repository.RewardCycleType) (int, error) {
	return 0, nil
}

func (s *rewardCacheStub) SetCycleWon(_ context.Context, _, _ string, _ repository.RewardCycleType, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCycleWonCall++
	return nil
}

func (s *rewardCacheStub) ClearRewardCycles(_ context.Context, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCall++
	return nil
}

func (s *rewardCacheStub) GetDailyProfit(_ context.Context, _ string) (int64, int64, int64, error) {
	return 0, 0, 0, nil
}

func (s *rewardCacheStub) RecordDailyProfit(_ context.Context, _ string, _, _, _ int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordCall++
	return nil
}

// roomRepoStub 房间仓储桩件。
// 通过嵌入 nil 接口满足完整契约，仅覆盖测试所需的 GetRoomMeta 方法。
type roomRepoStub struct {
	repository.RoomRepository
	meta *room.RoomMeta
	err  error
}

func (r *roomRepoStub) GetRoomMeta(_ context.Context, _ string) (*room.RoomMeta, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.meta, nil
}

// ============================================================================
// 结算领域桩件（settlement/domain）
// ============================================================================

// mockBillRepo 账单仓储桩件。
// 通过嵌入 nil 接口满足完整接口契约，仅覆盖测试所需方法。
type mockBillRepo struct {
	settlementRepository.BillRepository
	mu sync.Mutex

	existsByRoundAndTypeResult bool
	existsByRoundAndTypeErr    error
	createBillsErr             error
	updateBillStatusErr        error
	updateBillSuccessErr       error
	updateBillExceptionIDErr   error

	updateBillStatusCalls      int
	updateBillSuccessCalls     int
	updateBillExceptionIDCalls int
	createBillsCalls           int

	lastUpdateStatusTo     int
	lastUpdateStatusErrMsg string
	lastUpdateSuccessBal   int64
	lastUpdateExceptionID  int64
	lastCreateBillsArg     []*model.BillRecord
}

func (m *mockBillRepo) CreateBills(_ context.Context, bills []*model.BillRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createBillsCalls++
	m.lastCreateBillsArg = bills
	return m.createBillsErr
}

func (m *mockBillRepo) UpdateBillStatus(_ context.Context, _ int64, _, toStatus int, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBillStatusCalls++
	m.lastUpdateStatusTo = toStatus
	m.lastUpdateStatusErrMsg = errMsg
	return m.updateBillStatusErr
}

func (m *mockBillRepo) UpdateBillSuccess(_ context.Context, _ int64, _ int, _, balanceAfter int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBillSuccessCalls++
	m.lastUpdateSuccessBal = balanceAfter
	return m.updateBillSuccessErr
}

func (m *mockBillRepo) ExistsByRoundAndType(_ context.Context, _ int64, _ int) (bool, error) {
	return m.existsByRoundAndTypeResult, m.existsByRoundAndTypeErr
}

func (m *mockBillRepo) UpdateBillExceptionID(_ context.Context, _, exceptionID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBillExceptionIDCalls++
	m.lastUpdateExceptionID = exceptionID
	return m.updateBillExceptionIDErr
}

// mockRoundSettlementRepo 回合结算仓储桩件。
type mockRoundSettlementRepo struct {
	settlementRepository.RoundSettlementRepository
	mu sync.Mutex

	existsRoundSettlementResult      bool
	existsRoundSettlementErr         error
	createRoundSettlementAndBillsErr error
	updateRoundSettlementStatusErr   error
	updateRoundSettlementDeductErr   error

	createRoundSettlementAndBillsCalls int
	updateRoundSettlementStatusCalls   int
	updateRoundSettlementDeductCalls   int

	lastStatusStatus int
}

func (m *mockRoundSettlementRepo) ExistsRoundSettlement(_ context.Context, _ int64) (bool, error) {
	return m.existsRoundSettlementResult, m.existsRoundSettlementErr
}

func (m *mockRoundSettlementRepo) CreateRoundSettlementAndBills(_ context.Context, _ *model.RoundSettlement, _ []*model.BillRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createRoundSettlementAndBillsCalls++
	return m.createRoundSettlementAndBillsErr
}

func (m *mockRoundSettlementRepo) UpdateRoundSettlementStatus(_ context.Context, _ string, status int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRoundSettlementStatusCalls++
	m.lastStatusStatus = status
	return m.updateRoundSettlementStatusErr
}

func (m *mockRoundSettlementRepo) UpdateRoundSettlementDeductSuccess(_ context.Context, _ string, _ int, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRoundSettlementDeductCalls++
	return m.updateRoundSettlementDeductErr
}

// mockVirtualBalance 虚拟余额服务桩件。
// 用于验证 P0-4：机器人扣款/入账走 VirtualBalance.Deduct/Credit。
type mockVirtualBalance struct {
	mu sync.Mutex

	deductErr     error
	getBalanceRes int64

	deductCalls      int
	lastDeductUserID int64
	lastDeductAmount int64
}

func (m *mockVirtualBalance) Deduct(_ context.Context, userID int64, amount int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deductCalls++
	m.lastDeductUserID = userID
	m.lastDeductAmount = amount
	return m.deductErr
}

func (m *mockVirtualBalance) Credit(_ context.Context, _ int64, _ int64) error { return nil }

func (m *mockVirtualBalance) GetBalance(_ context.Context, _ int64) (int64, error) {
	return m.getBalanceRes, nil
}

func (m *mockVirtualBalance) SyncToDB(_ context.Context) error               { return nil }
func (m *mockVirtualBalance) AddToRobotSet(_ context.Context, _ int64) error { return nil }
func (m *mockVirtualBalance) IsRobot(_ context.Context, _ int64) (bool, error) {
	return false, nil
}
func (m *mockVirtualBalance) SetBalance(_ context.Context, _ int64, _ int64) error { return nil }

// mockRefundAuditRepo 退款审核仓储桩件。
type mockRefundAuditRepo struct {
	settlementRepository.RefundAuditRepository
	createErr error
}

func (m *mockRefundAuditRepo) CreateRefundAuditAndUpdateBillRefundStatus(_ context.Context, _ *model.RefundAudit, _ int64, _, _ int, _ string) error {
	return m.createErr
}

// mockSettlementQueryRepo 结算查询仓储桩件。
type mockSettlementQueryRepo struct {
	settlementRepository.SettlementQueryRepository
}

func (m *mockSettlementQueryRepo) AggregateBetBySession(_ context.Context, _ int64) (map[int64]int64, error) {
	return nil, nil
}

func (m *mockSettlementQueryRepo) AggregatePayOutBySession(_ context.Context, _ int64) (map[int64]int64, error) {
	return nil, nil
}

func (m *mockSettlementQueryRepo) IsPlayerGameSettled(_ context.Context, _ int64, _ int64) (bool, error) {
	return false, nil
}

// mockTransaction 事务桩件，返回预配置的子 repo。
type mockTransaction struct {
	billRepo            settlementRepository.BillRepository
	roundSettlementRepo settlementRepository.RoundSettlementRepository
	refundAuditRepo     settlementRepository.RefundAuditRepository
	settlementQueryRepo settlementRepository.SettlementQueryRepository
}

func (m *mockTransaction) BillRepo() settlementRepository.BillRepository { return m.billRepo }
func (m *mockTransaction) RoundSettlementRepo() settlementRepository.RoundSettlementRepository {
	return m.roundSettlementRepo
}
func (m *mockTransaction) RefundAuditRepo() settlementRepository.RefundAuditRepository {
	return m.refundAuditRepo
}
func (m *mockTransaction) SettlementQueryRepo() settlementRepository.SettlementQueryRepository {
	return m.settlementQueryRepo
}

func (m *mockTransaction) ExceptionRepo() settlementRepository.ExceptionRepository {
	return nil
}

func (m *mockTransaction) PlatformCallLogRepo() settlementRepository.PlatformCallLogRepository {
	return nil
}

// mockDBRepository 数据库仓储桩件，WithTransaction 直接调用 fn 并传入 mockTransaction。
type mockDBRepository struct {
	billRepo            settlementRepository.BillRepository
	roundSettlementRepo settlementRepository.RoundSettlementRepository
	refundAuditRepo     settlementRepository.RefundAuditRepository
	settlementQueryRepo settlementRepository.SettlementQueryRepository
}

func (m *mockDBRepository) BillRepo() settlementRepository.BillRepository { return m.billRepo }
func (m *mockDBRepository) RoundSettlementRepo() settlementRepository.RoundSettlementRepository {
	return m.roundSettlementRepo
}
func (m *mockDBRepository) RefundAuditRepo() settlementRepository.RefundAuditRepository {
	return m.refundAuditRepo
}
func (m *mockDBRepository) SettlementQueryRepo() settlementRepository.SettlementQueryRepository {
	return m.settlementQueryRepo
}

func (m *mockDBRepository) ExceptionRepo() settlementRepository.ExceptionRepository {
	return nil
}

func (m *mockDBRepository) PlatformCallLogRepo() settlementRepository.PlatformCallLogRepository {
	return nil
}

func (m *mockDBRepository) WithTransaction(_ context.Context, fn func(tx settlementRepository.Transaction) error) error {
	tx := &mockTransaction{
		billRepo:            m.billRepo,
		roundSettlementRepo: m.roundSettlementRepo,
		refundAuditRepo:     m.refundAuditRepo,
		settlementQueryRepo: m.settlementQueryRepo,
	}
	return fn(tx)
}

// stubUserService 用户服务桩件，返回预配置的 PlatformUser。
type stubUserService struct {
	user *domain.PlatformUser
	err  error
}

func (s *stubUserService) GetUserById(_ context.Context, _ string) (*domain.PlatformUser, error) {
	return s.user, s.err
}

// ============================================================================
// 服务桩件（settlement/service & api/platform）
// ============================================================================

// mockRobotChecker 机器人身份识别桩件。
// 用于验证 P0-2：IsRobot 返回 (bool, error)，error 时 fail-closed。
type mockRobotChecker struct {
	mu         sync.Mutex
	isRobotRes bool
	isRobotErr error
	callCount  int
}

func (m *mockRobotChecker) IsRobot(_ context.Context, _ int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	return m.isRobotRes, m.isRobotErr
}

// mockPlatformClient 平台客户端桩件。
// 用于模拟 platform.Debit/Credit 成功与失败（含 ParseAmount 失败场景 P0-3）。
type mockPlatformClient struct {
	mu          sync.Mutex
	debitResult *platform.CommonResponse
	debitErr    error
	debitCalls  int
}

func (m *mockPlatformClient) GetBalance(_ context.Context, _ *platform.BalanceRequest) (*platform.BalanceResponse, error) {
	return nil, nil
}

func (m *mockPlatformClient) Debit(_ context.Context, _ *platform.DebitRequest) (*platform.CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debitCalls++
	return m.debitResult, m.debitErr
}

func (m *mockPlatformClient) Credit(_ context.Context, _ *platform.CreditRequest) (*platform.CommonResponse, error) {
	return nil, nil
}

func (m *mockPlatformClient) Settle(_ context.Context, _ *platform.SettleRequest) (*platform.CommonResponse, error) {
	return nil, nil
}

// ============================================================================
// 测试辅助构造器
// ============================================================================

// noopDialector 空操作 Dialector，不建立真实 DB 连接。
// 用于测试场景下构造 DryRun gorm.DB：repository 实现的
// Create/Update 操作在 DryRun 模式下仅生成 SQL 不执行，无需 MySQL 实例。
type noopDialector struct{}

func (noopDialector) Name() string                                          { return "noop" }
func (noopDialector) Initialize(db *gorm.DB) error                          { return nil }
func (noopDialector) Migrator(db *gorm.DB) gorm.Migrator                    { return nil }
func (noopDialector) DataTypeOf(*schema.Field) string                       { return "" }
func (noopDialector) DefaultValueOf(*schema.Field) clause.Expression        { return nil }
func (noopDialector) BindVarTo(clause.Writer, *gorm.Statement, interface{}) {}
func (noopDialector) QuoteTo(clause.Writer, string)                         {}
func (noopDialector) Explain(string, ...interface{}) string                 { return "" }

// newDryRunDB 创建 dry-run 模式的 gorm.DB，用于 repository 实现。
func newDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(noopDialector{}, &gorm.Config{
		DryRun:                 true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("创建 dry-run gorm.DB 失败: %v", err)
	}
	return db
}

// stubIDGenerator 测试用的 IDGenerator 桩件，返回固定 ID。
// 注：此处 import bwmarrin/snowflake 是 SID-2 的已记录例外。
type stubIDGenerator struct {
	id     snowflake.ID
	nodeID int64
}

func (s *stubIDGenerator) GenerateInt64() (int64, error)     { return int64(s.id), nil }
func (s *stubIDGenerator) GenerateString() (string, error)   { return s.id.String(), nil }
func (s *stubIDGenerator) GenerateID() (snowflake.ID, error) { return s.id, nil }
func (s *stubIDGenerator) GetNodeID() int64                  { return s.nodeID }

// newTestTraceIDGen 创建带桩件 IDGenerator 的 TraceIDGenerator。
func newTestTraceIDGen() *service.TraceIDGenerator {
	return service.NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})
}

// newTestExceptionRepo 创建使用 dry-run DB 的异常记录仓储。
func newTestExceptionRepo(t *testing.T) settlementRepository.ExceptionRepository {
	return settlementMysqlRepo.NewExceptionRepository(newDryRunDB(t))
}

// newTestCallLogRepo 创建使用 dry-run DB 的平台调用日志仓储。
func newTestCallLogRepo(t *testing.T) settlementRepository.PlatformCallLogRepository {
	return settlementMysqlRepo.NewPlatformCallLogRepository(newDryRunDB(t))
}

// newTestUserIDConvert 创建带桩件 UserService 的 UserIDConvertService。
func newTestUserIDConvert() *service.UserIDConvertService {
	return service.NewUserIDConvertService(&stubUserService{
		user: &domain.PlatformUser{UserID: "platform_user_123"},
	})
}

// makeCommonResponse 构造平台响应，amount 为余额字符串。
func makeCommonResponse(amount string) *platform.CommonResponse {
	resp := &platform.CommonResponse{}
	resp.Data.Balance.Amount = amount
	return resp
}

// ============================================================================
// 集成测试环境
// ============================================================================

// integrationEnv 集成测试环境，持有真实组件与桩件引用。
type integrationEnv struct {
	// 真实组件
	packetGen *gameAlg.PacketGenerator
	deductSvc *service.DeductService

	// 桩件引用（供测试断言）
	billRepo        *mockBillRepo
	roundSettleRepo *mockRoundSettlementRepo
	robotChecker    *mockRobotChecker
	virtualBalance  *mockVirtualBalance
	platformClient  *mockPlatformClient
	packetCache     *packetCacheStub
	roomRepo        *roomRepoStub
}

// newIntegrationEnv 构造集成测试环境。
// isRobot 控制机器人身份识别返回值；debitAmount 控制平台返回的余额字符串。
func newIntegrationEnv(t *testing.T, isRobot bool, debitAmount string) *integrationEnv {
	t.Helper()

	// === 游戏领域组件 ===
	cfg := gameAlg.DefaultConfig()
	packetCache := &packetCacheStub{
		getResp: make(map[string]string),
		setNXOk: true,
	}
	rewardCache := &rewardCacheStub{}
	roomRepo := &roomRepoStub{
		meta: &room.RoomMeta{
			RoomID:           "room_1",
			ConfigID:         1,
			RoomFee:          200,
			MaxPlayers:       5,
			MaxRounds:        10,
			Status:           2,
			CurrentRound:     1,
			CurrentSessionID: "session_1",
		},
	}
	packetGen := gameAlg.NewPacketGenerator(cfg, packetCache, rewardCache, roomRepo)

	// === 结算领域桩件 ===
	billRepo := &mockBillRepo{
		existsByRoundAndTypeResult: false,
	}
	roundSettleRepo := &mockRoundSettlementRepo{
		existsRoundSettlementResult: false,
	}
	virtualBalance := &mockVirtualBalance{
		getBalanceRes: 50000,
	}
	refundAuditRepo := &mockRefundAuditRepo{}
	settlementQueryRepo := &mockSettlementQueryRepo{}

	// === 服务桩件 ===
	robotChecker := &mockRobotChecker{isRobotRes: isRobot}
	platformClient := &mockPlatformClient{
		debitResult: makeCommonResponse(debitAmount),
	}

	// === 真实结算服务 ===
	dbRepo := &mockDBRepository{
		billRepo:            billRepo,
		roundSettlementRepo: roundSettleRepo,
		refundAuditRepo:     refundAuditRepo,
		settlementQueryRepo: settlementQueryRepo,
	}
	userConvert := newTestUserIDConvert()
	callMgr := newTestCallLogRepo(t)
	exceptionMgr := newTestExceptionRepo(t)
	traceIDGen := newTestTraceIDGen()
	creditRetrySvc := service.NewCreditRetryService(
		billRepo, platformClient, testRedisClient,
		traceIDGen, nil, nil,
		exceptionMgr, userConvert, callMgr,
	)
	deductSvc := service.NewDeductService(
		platformClient, dbRepo, billRepo, roundSettleRepo, refundAuditRepo,
		testRedisClient, traceIDGen, nil, nil,
		creditRetrySvc, userConvert, callMgr,
		robotChecker, virtualBalance,
	)

	return &integrationEnv{
		packetGen:       packetGen,
		deductSvc:       deductSvc,
		billRepo:        billRepo,
		roundSettleRepo: roundSettleRepo,
		robotChecker:    robotChecker,
		virtualBalance:  virtualBalance,
		platformClient:  platformClient,
		packetCache:     packetCache,
		roomRepo:        roomRepo,
	}
}
