package service

import (
	"context"
	"errors"
	"testing"

	"github.com/cashparty/backend/settlement/domain"
)

// ============================================================================
// 测试辅助函数
// ============================================================================

// newTestSessionPayoutService 构造测试用 SessionPayoutService。
// 使用接口桩件构造 ExceptionRepository 和 PlatformCallLogRepository，无需真实 MySQL。
func newTestSessionPayoutService(t *testing.T, billRepo *mockBillRepo, robotChecker *mockRobotChecker, virtualBalance *mockVirtualBalance, platformClient *mockPlatformClient) *SessionPayoutService {
	t.Helper()
	userConvert := newTestUserIDConvert(&domain.PlatformUser{UserID: "platform_user_123"}, nil)
	callMgr := newTestCallLogRepo()
	exceptionMgr := newTestExceptionRepo()
	return NewSessionPayoutService(platformClient, billRepo, newTestTraceIDGen(), nil, userConvert, callMgr, robotChecker, virtualBalance, exceptionMgr)
}

// makePayoutMocks 构造一组全新的 session payout 测试 mock 桩件。
func makePayoutMocks() (*mockBillRepo, *mockRobotChecker, *mockVirtualBalance, *mockPlatformClient) {
	billRepo := &mockBillRepo{
		getBillsBySessionTypeAndUserRes: nil,
	}
	robotChecker := &mockRobotChecker{isRobotRes: false}
	virtualBalance := &mockVirtualBalance{getBalanceRes: 50000}
	platformClient := &mockPlatformClient{
		creditResult: makeCommonResponse("100.00"),
	}
	return billRepo, robotChecker, virtualBalance, platformClient
}

// ============================================================================
// 测试用例
// ============================================================================

// TestCreditSessionPayouts_Success_RealPlayer 验证真人玩家会话级派奖成功路径。
// 覆盖 platform.Credit + ParseAmount 成功 → UpdateBillSuccess。
func TestCreditSessionPayouts_Success_RealPlayer(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 500}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err != nil {
		t.Fatalf("CreditSessionPayouts 失败: %v", err)
	}

	// 验证 bill 已创建
	if billRepo.createBillCalls != 1 {
		t.Errorf("CreateBill 调用次数 = %d, 期望 1", billRepo.createBillCalls)
	}

	// 验证 platform.Credit 被调用
	if platformClient.creditCalls != 1 {
		t.Errorf("Credit 调用次数 = %d, 期望 1", platformClient.creditCalls)
	}

	// 验证 bill 标记为 Success
	if billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", billRepo.updateBillSuccessCalls)
	}

	// 验证虚拟余额未被调用（真人玩家不走虚拟钱包）
	if virtualBalance.creditCalls != 0 {
		t.Errorf("真人玩家不应调用 VirtualBalance.Credit, 实际调用 %d 次", virtualBalance.creditCalls)
	}
}

// TestCreditSessionPayouts_Success_RobotPlayer 验证 P0-4：机器人派奖走 VirtualBalance.Credit。
// 期望：platform.Credit 不被调用，VirtualBalance.Credit 被调用。
func TestCreditSessionPayouts_Success_RobotPlayer(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	robotChecker.isRobotRes = true
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 500}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err != nil {
		t.Fatalf("CreditSessionPayouts 失败: %v", err)
	}

	// 验证 VirtualBalance.Credit 被调用（P0-4）
	if virtualBalance.creditCalls != 1 {
		t.Errorf("VirtualBalance.Credit 调用次数 = %d, 期望 1", virtualBalance.creditCalls)
	}
	if virtualBalance.lastCreditUserID != 1001 {
		t.Errorf("VirtualBalance.Credit userID = %d, 期望 1001", virtualBalance.lastCreditUserID)
	}
	if virtualBalance.lastCreditAmount != 500 {
		t.Errorf("VirtualBalance.Credit amount = %d, 期望 500", virtualBalance.lastCreditAmount)
	}

	// 验证 platform.Credit 未被调用（机器人走虚拟通道）
	if platformClient.creditCalls != 0 {
		t.Errorf("机器人不应调用 platform.Credit, 实际调用 %d 次", platformClient.creditCalls)
	}

	// 验证 bill 标记为 Success
	if billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", billRepo.updateBillSuccessCalls)
	}
}

// TestCreditSessionPayouts_IsRobotError_FailClosed 验证 P0-2：IsRobot 返回 error 时 fail-closed。
// 期望：CreditSessionPayouts 返回 error，不调用 platform.Credit，不调用 VirtualBalance.Credit。
func TestCreditSessionPayouts_IsRobotError_FailClosed(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	robotChecker.isRobotErr = errors.New("redis 连接失败")
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 500}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err == nil {
		t.Fatal("IsRobot 返回 error 时应返回 error（fail-closed）")
	}

	// 验证未调用 platform.Credit
	if platformClient.creditCalls != 0 {
		t.Errorf("fail-closed 时不应调用 platform.Credit, 实际调用 %d 次", platformClient.creditCalls)
	}
	// 验证未调用 VirtualBalance.Credit
	if virtualBalance.creditCalls != 0 {
		t.Errorf("fail-closed 时不应调用 VirtualBalance.Credit, 实际调用 %d 次", virtualBalance.creditCalls)
	}
	// 验证未标记 bill Success
	if billRepo.updateBillSuccessCalls != 0 {
		t.Errorf("fail-closed 时不应调用 UpdateBillSuccess, 实际调用 %d 次", billRepo.updateBillSuccessCalls)
	}
}

// TestCreditSessionPayouts_ParseAmountFailure 验证 P0-3：ParseAmount 失败时标记 BillStatusFailed + 创建异常记录。
// 期望：platform.Credit 成功但余额无法解析 → bill 标记 Failed + createExceptionRecord 调用。
func TestCreditSessionPayouts_ParseAmountFailure(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	// 返回无法解析的余额字符串，触发 ParseAmount 失败
	platformClient.creditResult = makeCommonResponse("INVALID_AMOUNT")
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 500}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err == nil {
		t.Fatal("ParseAmount 失败时应返回 error（fail-closed）")
	}

	// 验证 platform.Credit 被调用（RPC 成功但响应无法解析）
	if platformClient.creditCalls != 1 {
		t.Errorf("Credit 调用次数 = %d, 期望 1", platformClient.creditCalls)
	}

	// 验证 bill 标记为 Failed（P0-3）
	if billRepo.updateBillStatusCalls == 0 {
		t.Fatal("ParseAmount 失败时应调用 UpdateBillStatus 标记 Failed")
	}
	if billRepo.lastUpdateStatusTo != domain.BillStatusFailed {
		t.Errorf("bill 最终状态 = %d, 期望 %d (Failed)", billRepo.lastUpdateStatusTo, domain.BillStatusFailed)
	}

	// 验证创建了异常记录（UpdateBillExceptionID 被调用）
	if billRepo.updateBillExceptionIDCalls == 0 {
		t.Error("ParseAmount 失败时应创建异常记录（UpdateBillExceptionID 应被调用）")
	}

	// 验证未标记 bill Success
	if billRepo.updateBillSuccessCalls != 0 {
		t.Errorf("ParseAmount 失败时不应调用 UpdateBillSuccess, 实际调用 %d 次", billRepo.updateBillSuccessCalls)
	}
}

// TestCreditSessionPayouts_PlatformCreditFailure 验证 platform.Credit 失败路径。
// 期望：bill 标记 Failed + IncrementRetryCountWithNextRetryTime 调用。
func TestCreditSessionPayouts_PlatformCreditFailure(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	platformClient.creditErr = errors.New("平台不可用")
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 500}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err == nil {
		t.Fatal("platform.Credit 失败时应返回 error")
	}

	// 验证 platform.Credit 被调用
	if platformClient.creditCalls != 1 {
		t.Errorf("Credit 调用次数 = %d, 期望 1", platformClient.creditCalls)
	}

	// 验证 bill 标记为 Failed
	if billRepo.lastUpdateStatusTo != domain.BillStatusFailed {
		t.Errorf("bill 最终状态 = %d, 期望 %d (Failed)", billRepo.lastUpdateStatusTo, domain.BillStatusFailed)
	}

	// 验证设置了重试（IncrementRetryCountWithNextRetryTime 被调用）
	if billRepo.incrementRetryCountCalls != 1 {
		t.Errorf("IncrementRetryCountWithNextRetryTime 调用次数 = %d, 期望 1", billRepo.incrementRetryCountCalls)
	}

	// 验证未标记 bill Success
	if billRepo.updateBillSuccessCalls != 0 {
		t.Errorf("platform.Credit 失败时不应调用 UpdateBillSuccess, 实际调用 %d 次", billRepo.updateBillSuccessCalls)
	}
}

// TestCreditSessionPayouts_Idempotent 验证幂等性：已有 Success bill 的玩家直接跳过。
func TestCreditSessionPayouts_Idempotent(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	// 模拟已存在 Success 状态的 session credit bill
	billRepo.getBillsBySessionTypeAndUserRes = []*domain.BillRecord{
		{ID: 1, UserID: 1001, Status: domain.BillStatusSuccess, BillType: domain.BillTypeSessionCredit},
	}
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 500}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err != nil {
		t.Fatalf("幂等返回时应返回 nil, 实际返回: %v", err)
	}

	// 验证未创建新 bill
	if billRepo.createBillCalls != 0 {
		t.Errorf("幂等返回时不应调用 CreateBill, 实际调用 %d 次", billRepo.createBillCalls)
	}
	// 验证未调用 platform.Credit
	if platformClient.creditCalls != 0 {
		t.Errorf("幂等返回时不应调用 platform.Credit, 实际调用 %d 次", platformClient.creditCalls)
	}
}

// TestCreditSessionPayouts_ZeroPayout 验证零金额派奖被跳过。
func TestCreditSessionPayouts_ZeroPayout(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: 0}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err != nil {
		t.Fatalf("零金额派奖应返回 nil, 实际返回: %v", err)
	}

	// 验证未执行任何操作
	if billRepo.createBillCalls != 0 {
		t.Errorf("零金额时不应调用 CreateBill, 实际调用 %d 次", billRepo.createBillCalls)
	}
	if platformClient.creditCalls != 0 {
		t.Errorf("零金额时不应调用 platform.Credit, 实际调用 %d 次", platformClient.creditCalls)
	}
}

// TestCreditSessionPayouts_NegativePayout 验证负金额派奖被跳过。
func TestCreditSessionPayouts_NegativePayout(t *testing.T) {
	billRepo, robotChecker, virtualBalance, platformClient := makePayoutMocks()
	svc := newTestSessionPayoutService(t, billRepo, robotChecker, virtualBalance, platformClient)

	payOutMap := map[int64]int64{1001: -100}
	err := svc.CreditSessionPayouts(context.Background(), 1000, payOutMap, 100)
	if err != nil {
		t.Fatalf("负金额派奖应返回 nil, 实际返回: %v", err)
	}

	// 验证未执行任何操作
	if billRepo.createBillCalls != 0 {
		t.Errorf("负金额时不应调用 CreateBill, 实际调用 %d 次", billRepo.createBillCalls)
	}
	if platformClient.creditCalls != 0 {
		t.Errorf("负金额时不应调用 platform.Credit, 实际调用 %d 次", platformClient.creditCalls)
	}
}
