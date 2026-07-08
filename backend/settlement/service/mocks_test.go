package service

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cashparty/backend/api/platform"
	"github.com/cashparty/backend/common/lock"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/model"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// ============================================================================
// 测试环境初始化
// ============================================================================

// testMiniredis 测试用 miniredis 实例，供 lock.WithRedisLock 使用。
var testMiniredis *miniredis.Miniredis

// TestMain 启动 miniredis 并初始化分布式锁。
// lock.InitLocker 使用 sync.Once，整个测试进程只需初始化一次。
// 所有需要分布式锁的测试共享同一个 miniredis 实例。
func TestMain(m *testing.M) {
	mr, err := miniredis.Run()
	if err != nil {
		panic("miniredis 启动失败: " + err.Error())
	}
	testMiniredis = mr

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	client := cRedis.NewClientFromRaw(rdb)
	lock.InitLocker(client)

	code := m.Run()
	rdb.Close()
	mr.Close()
	os.Exit(code)
}

// noopDialector 空操作 Dialector，不建立真实 DB 连接。
// 用于测试场景下构造 DryRun gorm.DB：ExceptionManager/PlatformCallManager
// 的 Create/Update 操作在 DryRun 模式下仅生成 SQL 不执行，无需 MySQL 实例。
type noopDialector struct{}

func (noopDialector) Name() string                                          { return "noop" }
func (noopDialector) Initialize(db *gorm.DB) error                          { return nil }
func (noopDialector) Migrator(db *gorm.DB) gorm.Migrator                    { return nil }
func (noopDialector) DataTypeOf(*schema.Field) string                       { return "" }
func (noopDialector) DefaultValueOf(*schema.Field) clause.Expression        { return nil }
func (noopDialector) BindVarTo(clause.Writer, *gorm.Statement, interface{}) {}
func (noopDialector) QuoteTo(clause.Writer, string)                         {}
func (noopDialector) Explain(string, ...interface{}) string                 { return "" }

// newDryRunDB 创建 dry-run 模式的 gorm.DB，用于 ExceptionManager 和 PlatformCallManager。
// 使用 noopDialector 避免 MySQL 连接；DryRun 模式下 gorm 生成 SQL 但不执行。
// 适用于测试中需要构造真实 ExceptionManager/PlatformCallManager 但不依赖 DB 的场景。
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

// newTestTraceIDGen 创建带桩件 IDGenerator 的 TraceIDGenerator。
func newTestTraceIDGen() *TraceIDGenerator {
	return NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})
}

// ============================================================================
// domain.Transaction 桩件
// ============================================================================

// mockTransaction 事务桩件，返回预配置的子 repo。
// 用于测试 P0-1：service 通过 tx.BillRepo() 等访问子 repo。
type mockTransaction struct {
	billRepo            domain.BillRepository
	roundSettlementRepo domain.RoundSettlementRepository
	refundAuditRepo     domain.RefundAuditRepository
	settlementQueryRepo domain.SettlementQueryRepository
}

func (m *mockTransaction) BillRepo() domain.BillRepository {
	return m.billRepo
}

func (m *mockTransaction) RoundSettlementRepo() domain.RoundSettlementRepository {
	return m.roundSettlementRepo
}

func (m *mockTransaction) RefundAuditRepo() domain.RefundAuditRepository {
	return m.refundAuditRepo
}

func (m *mockTransaction) SettlementQueryRepo() domain.SettlementQueryRepository {
	return m.settlementQueryRepo
}

// ============================================================================
// domain.DBRepository 桩件
// ============================================================================

// mockDBRepository 数据库仓储桩件，WithTransaction 直接调用 fn 并传入 mockTransaction。
type mockDBRepository struct {
	billRepo            domain.BillRepository
	roundSettlementRepo domain.RoundSettlementRepository
	refundAuditRepo     domain.RefundAuditRepository
	settlementQueryRepo domain.SettlementQueryRepository
	tx                  domain.Transaction
	// withTxErr 控制事务回调是否注入错误（模拟事务失败）
	withTxErr error
}

func (m *mockDBRepository) BillRepo() domain.BillRepository { return m.billRepo }
func (m *mockDBRepository) RoundSettlementRepo() domain.RoundSettlementRepository {
	return m.roundSettlementRepo
}
func (m *mockDBRepository) RefundAuditRepo() domain.RefundAuditRepository { return m.refundAuditRepo }
func (m *mockDBRepository) SettlementQueryRepo() domain.SettlementQueryRepository {
	return m.settlementQueryRepo
}

func (m *mockDBRepository) WithTransaction(_ context.Context, fn func(tx domain.Transaction) error) error {
	if m.withTxErr != nil {
		return m.withTxErr
	}
	tx := m.tx
	if tx == nil {
		tx = &mockTransaction{
			billRepo:            m.billRepo,
			roundSettlementRepo: m.roundSettlementRepo,
			refundAuditRepo:     m.refundAuditRepo,
			settlementQueryRepo: m.settlementQueryRepo,
		}
	}
	return fn(tx)
}

// ============================================================================
// domain.BillRepository 桩件
// ============================================================================

// mockBillRepo 账单仓储桩件。
// 通过嵌入 nil 接口满足完整接口契约，仅覆盖测试所需方法。
// 未覆盖方法若被调用会 panic（测试 bug 预警）。
type mockBillRepo struct {
	domain.BillRepository
	mu sync.Mutex

	// 可配置返回值
	getBillByRoundTypeAndUserResult *model.BillRecord
	getBillByRoundTypeAndUserErr    error
	createBillsErr                  error
	createBillErr                   error
	updateBillStatusErr             error
	updateBillSuccessErr            error
	existsByRoundAndTypeResult      bool
	existsByRoundAndTypeErr         error
	getBillsByRoundIDResult         []*model.BillRecord
	getBillsByRoundIDErr            error
	getBillsByBatchIDResult         []*model.BillRecord
	getBillsByBatchIDErr            error
	getBillByBatchAndUserResult     *model.BillRecord
	getBillByBatchAndUserErr        error
	getBillsBySessionTypeAndUserRes []*model.BillRecord
	getBillsBySessionTypeAndUserErr error
	incrementRetryCountErr          error
	updateBillExceptionIDErr        error
	getBillByIDResult               *model.BillRecord
	getBillByIDErr                  error

	// 调用计数（用于验证 P0-2/P0-3/P0-4 行为）
	createBillsCalls           int
	createBillCalls            int
	updateBillStatusCalls      int
	updateBillSuccessCalls     int
	updateBillExceptionIDCalls int
	incrementRetryCountCalls   int
	createBillArg              *model.BillRecord
	createBillsArg             []*model.BillRecord

	// 记录最后一次 UpdateBillStatus 调用参数
	lastUpdateStatusBillID  int64
	lastUpdateStatusFrom    int
	lastUpdateStatusTo      int
	lastUpdateStatusErrMsg  string
	lastUpdateSuccessBillID int64
	lastUpdateSuccessBal    int64
	lastUpdateExceptionID   int64
}

func (m *mockBillRepo) CreateBill(_ context.Context, bill *model.BillRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createBillCalls++
	m.createBillArg = bill
	return m.createBillErr
}

func (m *mockBillRepo) CreateBills(_ context.Context, bills []*model.BillRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createBillsCalls++
	m.createBillsArg = bills
	return m.createBillsErr
}

func (m *mockBillRepo) UpdateBillStatus(_ context.Context, billID int64, fromStatus, toStatus int, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBillStatusCalls++
	m.lastUpdateStatusBillID = billID
	m.lastUpdateStatusFrom = fromStatus
	m.lastUpdateStatusTo = toStatus
	m.lastUpdateStatusErrMsg = errMsg
	return m.updateBillStatusErr
}

func (m *mockBillRepo) UpdateBillSuccess(_ context.Context, billID int64, fromStatus int, _, balanceAfter int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBillSuccessCalls++
	m.lastUpdateSuccessBillID = billID
	m.lastUpdateSuccessBal = balanceAfter
	return m.updateBillSuccessErr
}

func (m *mockBillRepo) ExistsByRoundAndType(_ context.Context, _ int64, _ int) (bool, error) {
	return m.existsByRoundAndTypeResult, m.existsByRoundAndTypeErr
}

func (m *mockBillRepo) GetBillByRoundTypeAndUser(_ context.Context, _ int64, _ int, _ int64) (*model.BillRecord, error) {
	return m.getBillByRoundTypeAndUserResult, m.getBillByRoundTypeAndUserErr
}

func (m *mockBillRepo) GetBillsByRoundID(_ context.Context, _ int64) ([]*model.BillRecord, error) {
	return m.getBillsByRoundIDResult, m.getBillsByRoundIDErr
}

func (m *mockBillRepo) GetBillsByBatchID(_ context.Context, _ string) ([]*model.BillRecord, error) {
	return m.getBillsByBatchIDResult, m.getBillsByBatchIDErr
}

func (m *mockBillRepo) GetBillByBatchAndUser(_ context.Context, _ string, _ int64) (*model.BillRecord, error) {
	return m.getBillByBatchAndUserResult, m.getBillByBatchAndUserErr
}

func (m *mockBillRepo) GetBillsBySessionTypeAndUser(_ context.Context, _ int64, _ int, _ int64) ([]*model.BillRecord, error) {
	return m.getBillsBySessionTypeAndUserRes, m.getBillsBySessionTypeAndUserErr
}

func (m *mockBillRepo) IncrementRetryCountWithNextRetryTime(_ context.Context, billID int64, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incrementRetryCountCalls++
	m.lastUpdateSuccessBillID = billID
	return m.incrementRetryCountErr
}

func (m *mockBillRepo) UpdateBillExceptionID(_ context.Context, billID, exceptionID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateBillExceptionIDCalls++
	m.lastUpdateExceptionID = exceptionID
	m.lastUpdateSuccessBillID = billID
	return m.updateBillExceptionIDErr
}

func (m *mockBillRepo) GetBillByID(_ context.Context, _ int64) (*model.BillRecord, error) {
	return m.getBillByIDResult, m.getBillByIDErr
}

// ============================================================================
// domain.RoundSettlementRepository 桩件
// ============================================================================

// mockRoundSettlementRepo 回合结算仓储桩件。
type mockRoundSettlementRepo struct {
	domain.RoundSettlementRepository
	mu sync.Mutex

	// 可配置返回值
	getRoundSettlementByRoundIDResult     *model.RoundSettlement
	getRoundSettlementByRoundIDErr        error
	existsRoundSettlementResult           bool
	existsRoundSettlementErr              error
	createRoundSettlementAndBillsErr      error
	updateRoundSettlementCreditedErr      error
	updateRoundSettlementStatusErr        error
	updateRoundSettlementSettleInfoErr    error
	updateRoundSettlementDeductSuccessErr error
	getAllRoundSettlementsBySessionResult []*model.RoundSettlement
	getAllRoundSettlementsBySessionErr    error

	// 调用计数
	createRoundSettlementAndBillsCalls      int
	updateRoundSettlementCreditedCalls      int
	updateRoundSettlementStatusCalls        int
	updateRoundSettlementSettleInfoCalls    int
	updateRoundSettlementDeductSuccessCalls int

	// 记录参数
	lastCreditedTraceID         string
	lastCreditedSettleAmount    int64
	lastCreditedSettleUserCount int
	lastStatusTraceID           string
	lastStatusStatus            int
	lastSettleInfoTraceID       string
	lastSettleInfoSenderID      int64
}

func (m *mockRoundSettlementRepo) GetRoundSettlementByRoundID(_ context.Context, _ int64) (*model.RoundSettlement, error) {
	return m.getRoundSettlementByRoundIDResult, m.getRoundSettlementByRoundIDErr
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

func (m *mockRoundSettlementRepo) UpdateRoundSettlementCredited(_ context.Context, traceID string, settleAmount int64, settleUserCount int, _ *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRoundSettlementCreditedCalls++
	m.lastCreditedTraceID = traceID
	m.lastCreditedSettleAmount = settleAmount
	m.lastCreditedSettleUserCount = settleUserCount
	return m.updateRoundSettlementCreditedErr
}

func (m *mockRoundSettlementRepo) UpdateRoundSettlementStatus(_ context.Context, traceID string, status int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRoundSettlementStatusCalls++
	m.lastStatusTraceID = traceID
	m.lastStatusStatus = status
	return m.updateRoundSettlementStatusErr
}

func (m *mockRoundSettlementRepo) UpdateRoundSettlementSettleInfo(_ context.Context, traceID string, senderID int64, _ string, _, _ int64, _ int, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRoundSettlementSettleInfoCalls++
	m.lastSettleInfoTraceID = traceID
	m.lastSettleInfoSenderID = senderID
	return m.updateRoundSettlementSettleInfoErr
}

func (m *mockRoundSettlementRepo) UpdateRoundSettlementDeductSuccess(_ context.Context, _ string, _ int, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRoundSettlementDeductSuccessCalls++
	return m.updateRoundSettlementDeductSuccessErr
}

func (m *mockRoundSettlementRepo) GetAllRoundSettlementsBySession(_ context.Context, _ int64) ([]*model.RoundSettlement, error) {
	return m.getAllRoundSettlementsBySessionResult, m.getAllRoundSettlementsBySessionErr
}

// ============================================================================
// domain.VirtualBalanceService 桩件
// ============================================================================

// mockVirtualBalance 虚拟余额服务桩件。
// 用于验证 P0-4：机器人扣款/入账走 VirtualBalance.Deduct/Credit。
type mockVirtualBalance struct {
	mu sync.Mutex

	deductErr     error
	creditErr     error
	getBalanceRes int64
	getBalanceErr error
	syncToDBErr   error
	addToRobotErr error
	isRobotResult bool
	isRobotErr    error
	setBalanceErr error

	deductCalls     int
	creditCalls     int
	getBalanceCalls int

	lastDeductUserID int64
	lastDeductAmount int64
	lastCreditUserID int64
	lastCreditAmount int64
}

func (m *mockVirtualBalance) Deduct(_ context.Context, userID int64, amount int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deductCalls++
	m.lastDeductUserID = userID
	m.lastDeductAmount = amount
	return m.deductErr
}

func (m *mockVirtualBalance) Credit(_ context.Context, userID int64, amount int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creditCalls++
	m.lastCreditUserID = userID
	m.lastCreditAmount = amount
	return m.creditErr
}

func (m *mockVirtualBalance) GetBalance(_ context.Context, _ int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getBalanceCalls++
	return m.getBalanceRes, m.getBalanceErr
}

func (m *mockVirtualBalance) SyncToDB(_ context.Context) error               { return m.syncToDBErr }
func (m *mockVirtualBalance) AddToRobotSet(_ context.Context, _ int64) error { return m.addToRobotErr }
func (m *mockVirtualBalance) IsRobot(_ context.Context, _ int64) (bool, error) {
	return m.isRobotResult, m.isRobotErr
}
func (m *mockVirtualBalance) SetBalance(_ context.Context, _ int64, _ int64) error {
	return m.setBalanceErr
}

// ============================================================================
// RobotChecker 桩件
// ============================================================================

// mockRobotChecker 机器人身份识别桩件。
// 用于验证 P0-2：IsRobot 返回 (bool, error)，error 时 fail-closed。
type mockRobotChecker struct {
	mu          sync.Mutex
	isRobotRes  bool
	isRobotErr  error
	callCount   int
	calledUsers []int64
}

func (m *mockRobotChecker) IsRobot(_ context.Context, userID int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.calledUsers = append(m.calledUsers, userID)
	return m.isRobotRes, m.isRobotErr
}

// ============================================================================
// platform.Client 桩件
// ============================================================================

// mockPlatformClient 平台客户端桩件。
// 用于模拟 platform.Debit/Credit 成功与失败（含 ParseAmount 失败场景 P0-3）。
type mockPlatformClient struct {
	mu sync.Mutex

	debitResult   *platform.CommonResponse
	debitErr      error
	creditResult  *platform.CommonResponse
	creditErr     error
	settleResult  *platform.CommonResponse
	settleErr     error
	balanceResult *platform.BalanceResponse
	balanceErr    error

	debitCalls  int
	creditCalls int
	settleCalls int
}

func (m *mockPlatformClient) GetBalance(_ context.Context, _ *platform.BalanceRequest) (*platform.BalanceResponse, error) {
	return m.balanceResult, m.balanceErr
}

func (m *mockPlatformClient) Debit(_ context.Context, _ *platform.DebitRequest) (*platform.CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debitCalls++
	return m.debitResult, m.debitErr
}

func (m *mockPlatformClient) Credit(_ context.Context, _ *platform.CreditRequest) (*platform.CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creditCalls++
	return m.creditResult, m.creditErr
}

func (m *mockPlatformClient) Settle(_ context.Context, _ *platform.SettleRequest) (*platform.CommonResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settleCalls++
	return m.settleResult, m.settleErr
}

// ============================================================================
// domain.UserService 桩件
// ============================================================================

// stubUserService 用户服务桩件，返回预配置的 PlatformUser。
type stubUserService struct {
	user *domain.PlatformUser
	err  error
}

func (s *stubUserService) GetUserById(_ context.Context, _ string) (*domain.PlatformUser, error) {
	return s.user, s.err
}

// ============================================================================
// domain.RefundAuditRepository 桩件
// ============================================================================

// mockRefundAuditRepo 退款审核仓储桩件。
type mockRefundAuditRepo struct {
	domain.RefundAuditRepository
	createRefundAuditErr error
}

func (m *mockRefundAuditRepo) CreateRefundAuditAndUpdateBillRefundStatus(_ context.Context, _ *model.RefundAudit, _ int64, _, _ int, _ string) error {
	return m.createRefundAuditErr
}

// ============================================================================
// domain.SettlementQueryRepository 桩件
// ============================================================================

// mockSettlementQueryRepo 结算查询仓储桩件。
type mockSettlementQueryRepo struct {
	domain.SettlementQueryRepository
	aggregateBetResult     map[int64]int64
	aggregateBetErr        error
	aggregatePayOutResult  map[int64]int64
	aggregatePayOutErr     error
	isPlayerGameSettledRes bool
	isPlayerGameSettledErr error
}

func (m *mockSettlementQueryRepo) AggregateBetBySession(_ context.Context, _ int64) (map[int64]int64, error) {
	return m.aggregateBetResult, m.aggregateBetErr
}

func (m *mockSettlementQueryRepo) AggregatePayOutBySession(_ context.Context, _ int64) (map[int64]int64, error) {
	return m.aggregatePayOutResult, m.aggregatePayOutErr
}

func (m *mockSettlementQueryRepo) IsPlayerGameSettled(_ context.Context, _ int64, _ int64) (bool, error) {
	return m.isPlayerGameSettledRes, m.isPlayerGameSettledErr
}

// ============================================================================
// 测试辅助构造函数
// ============================================================================

// newTestExceptionMgr 创建使用 dry-run DB 的 ExceptionManager。
func newTestExceptionMgr(t *testing.T) *ExceptionManager {
	return NewExceptionManager(newDryRunDB(t))
}

// newTestCallMgr 创建使用 dry-run DB 的 PlatformCallManager。
func newTestCallMgr(t *testing.T) *PlatformCallManager {
	return NewPlatformCallManager(newDryRunDB(t))
}

// newTestUserIDConvert 创建带桩件 UserService 的 UserIDConvertService。
func newTestUserIDConvert(user *domain.PlatformUser, err error) *UserIDConvertService {
	return NewUserIDConvertService(&stubUserService{user: user, err: err})
}

// makeCommonResponse 构造平台响应，amount 为余额字符串。
func makeCommonResponse(amount string) *platform.CommonResponse {
	resp := &platform.CommonResponse{}
	resp.Data.Balance.Amount = amount
	return resp
}
