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
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"github.com/redis/go-redis/v9"
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

// noopDialector 及 dry-run gorm.DB 辅助已移除：service 层不再持有 *gorm.DB，
// 异常记录与平台调用日志通过 repository 接口注入，测试改用接口桩件（见下方 mock repo）。

// newTestTraceIDGen 创建带桩件 IDGenerator 的 TraceIDGenerator。
func newTestTraceIDGen() *TraceIDGenerator {
	return NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})
}

// ============================================================================
// repository.Transaction 桩件
// ============================================================================

// mockTransaction 事务桩件，返回预配置的子 repo。
// 用于测试 P0-1：service 通过 tx.BillRepo() 等访问子 repo。
type mockTransaction struct {
	billRepo            repository.BillRepository
	roundSettlementRepo repository.RoundSettlementRepository
	refundAuditRepo     repository.RefundAuditRepository
	settlementQueryRepo repository.SettlementQueryRepository
}

func (m *mockTransaction) BillRepo() repository.BillRepository {
	return m.billRepo
}

func (m *mockTransaction) RoundSettlementRepo() repository.RoundSettlementRepository {
	return m.roundSettlementRepo
}

func (m *mockTransaction) RefundAuditRepo() repository.RefundAuditRepository {
	return m.refundAuditRepo
}

func (m *mockTransaction) SettlementQueryRepo() repository.SettlementQueryRepository {
	return m.settlementQueryRepo
}

func (m *mockTransaction) ExceptionRepo() repository.ExceptionRepository {
	return nil
}

func (m *mockTransaction) PlatformCallLogRepo() repository.PlatformCallLogRepository {
	return nil
}

// ============================================================================
// repository.DBRepository 桩件
// ============================================================================

// mockDBRepository 数据库仓储桩件，WithTransaction 直接调用 fn 并传入 mockTransaction。
type mockDBRepository struct {
	billRepo            repository.BillRepository
	roundSettlementRepo repository.RoundSettlementRepository
	refundAuditRepo     repository.RefundAuditRepository
	settlementQueryRepo repository.SettlementQueryRepository
	tx                  repository.Transaction
	// withTxErr 控制事务回调是否注入错误（模拟事务失败）
	withTxErr error
}

func (m *mockDBRepository) BillRepo() repository.BillRepository { return m.billRepo }
func (m *mockDBRepository) RoundSettlementRepo() repository.RoundSettlementRepository {
	return m.roundSettlementRepo
}
func (m *mockDBRepository) RefundAuditRepo() repository.RefundAuditRepository {
	return m.refundAuditRepo
}
func (m *mockDBRepository) SettlementQueryRepo() repository.SettlementQueryRepository {
	return m.settlementQueryRepo
}

func (m *mockDBRepository) ExceptionRepo() repository.ExceptionRepository {
	return nil
}

func (m *mockDBRepository) PlatformCallLogRepo() repository.PlatformCallLogRepository {
	return nil
}

func (m *mockDBRepository) WithTransaction(_ context.Context, fn func(tx repository.Transaction) error) error {
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
// repository.BillRepository 桩件
// ============================================================================

// mockBillRepo 账单仓储桩件。
// 通过嵌入 nil 接口满足完整接口契约，仅覆盖测试所需方法。
// 未覆盖方法若被调用会 panic（测试 bug 预警）。
type mockBillRepo struct {
	repository.BillRepository
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
// repository.RoundSettlementRepository 桩件
// ============================================================================

// mockRoundSettlementRepo 回合结算仓储桩件。
type mockRoundSettlementRepo struct {
	repository.RoundSettlementRepository
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
// repository.RefundAuditRepository 桩件
// ============================================================================

// mockRefundAuditRepo 退款审核仓储桩件。
type mockRefundAuditRepo struct {
	repository.RefundAuditRepository
	createRefundAuditErr error
}

func (m *mockRefundAuditRepo) CreateRefundAuditAndUpdateBillRefundStatus(_ context.Context, _ *model.RefundAudit, _ int64, _, _ int, _ string) error {
	return m.createRefundAuditErr
}

// ============================================================================
// repository.SettlementQueryRepository 桩件
// ============================================================================

// mockSettlementQueryRepo 结算查询仓储桩件。
type mockSettlementQueryRepo struct {
	repository.SettlementQueryRepository
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

// ============================================================================
// repository.ExceptionRepository / PlatformCallLogRepository 桩件
// ============================================================================

// mockExceptionRepo 异常记录仓储桩件，实现 repository.ExceptionRepository。
// Create 默认返回 nil error，使调用方继续执行后续 DB 操作（如 UpdateBillExceptionID）。
type mockExceptionRepo struct {
	createErr error
}

func (m *mockExceptionRepo) Create(_ context.Context, _ *model.ExceptionRecord) error {
	return m.createErr
}

// mockPlatformCallLogRepo 平台调用日志仓储桩件，实现 repository.PlatformCallLogRepository。
// CreateLog 默认返回非 nil 日志（带 ID），使调用方进入 UpdateLog 路径。
type mockPlatformCallLogRepo struct {
	createLogErr error
	updateLogErr error
}

func (m *mockPlatformCallLogRepo) CreateLog(_ context.Context, _ *dto.CallLogCreateParams) (*model.PlatformCallLog, error) {
	if m.createLogErr != nil {
		return nil, m.createLogErr
	}
	return &model.PlatformCallLog{ID: 1}, nil
}

func (m *mockPlatformCallLogRepo) UpdateLog(_ context.Context, _ *dto.CallLogUpdateParams) error {
	return m.updateLogErr
}

func (m *mockPlatformCallLogRepo) GetLogByID(_ context.Context, _ int64) (*model.PlatformCallLog, error) {
	return nil, nil
}

func (m *mockPlatformCallLogRepo) GetFailedLogs(_ context.Context, _ int) ([]*model.PlatformCallLog, error) {
	return nil, nil
}

// newTestExceptionRepo 创建异常记录仓储桩件。
func newTestExceptionRepo() repository.ExceptionRepository {
	return &mockExceptionRepo{}
}

// newTestCallLogRepo 创建平台调用日志仓储桩件。
func newTestCallLogRepo() repository.PlatformCallLogRepository {
	return &mockPlatformCallLogRepo{}
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
