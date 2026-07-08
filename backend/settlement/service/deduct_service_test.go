package service

import (
	"context"
	"errors"
	"testing"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
)

// ============================================================================
// 测试辅助函数
// ============================================================================

// newTestDeductService 构造测试用 DeductService。
// 使用 dry-run DB 构造 ExceptionManager 和 PlatformCallManager，无需真实 MySQL。
// creditRetrySvc 为真实实例（CreateDebitFailedException 需调用 ExceptionManager.Create）。
func newTestDeductService(t *testing.T, billRepo *mockBillRepo, roundSettlementRepo *mockRoundSettlementRepo, robotChecker *mockRobotChecker, virtualBalance *mockVirtualBalance, platformClient *mockPlatformClient) *DeductService {
	t.Helper()
	dbRepo := &mockDBRepository{
		billRepo:            billRepo,
		roundSettlementRepo: roundSettlementRepo,
	}
	userConvert := newTestUserIDConvert(&domain.PlatformUser{UserID: "platform_user_123"}, nil)
	callMgr := newTestCallMgr(t)
	exceptionMgr := newTestExceptionMgr(t)
	creditRetrySvc := NewCreditRetryService(billRepo, platformClient, nil, newTestTraceIDGen(), nil, nil, exceptionMgr, userConvert, callMgr)
	return NewDeductService(
		platformClient, dbRepo, billRepo, roundSettlementRepo, nil, nil,
		newTestTraceIDGen(), nil, nil, creditRetrySvc, userConvert, callMgr, robotChecker, virtualBalance,
	)
}

// newTestLaterRoundDeductRequest 构造测试用 LaterRoundDeductRequest。
func newTestLaterRoundDeductRequest() *dto.LaterRoundDeductRequest {
	return &dto.LaterRoundDeductRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10001,
		RoundNo:      2,
		RoomFee:      200,
		MinPlayerID:  1001,
		RoundTraceID: "",
	}
}

// makeDeductMocks 构造一组全新的 deduct 测试 mock 桩件。
func makeDeductMocks() (*mockBillRepo, *mockRoundSettlementRepo, *mockRobotChecker, *mockVirtualBalance, *mockPlatformClient) {
	billRepo := &mockBillRepo{
		existsByRoundAndTypeResult: false,
	}
	roundSettlementRepo := &mockRoundSettlementRepo{
		existsRoundSettlementResult:      false,
		createRoundSettlementAndBillsErr: nil,
	}
	robotChecker := &mockRobotChecker{isRobotRes: false}
	virtualBalance := &mockVirtualBalance{getBalanceRes: 50000}
	platformClient := &mockPlatformClient{
		debitResult: makeCommonResponse("100.00"),
	}
	return billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient
}

// newTestSystemPacketDeductRequest 构造测试用 SystemPacketDeductRequest。
func newTestSystemPacketDeductRequest() *dto.SystemPacketDeductRequest {
	return &dto.SystemPacketDeductRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10002,
		RoundNo:      3,
		TotalAmount:  500,
		RoundTraceID: "RT_1000_3",
		Reason:       "系统发红包测试",
	}
}

// ============================================================================
// 测试用例
// ============================================================================

// TestDeductForLaterRound_Success_RealPlayer 验证真人玩家扣款成功路径。
// 覆盖 platform.Debit + ParseAmount 成功 → UpdateBillSuccess。
func TestDeductForLaterRound_Success_RealPlayer(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)

	err := svc.DeductForLaterRound(context.Background(), newTestLaterRoundDeductRequest())
	if err != nil {
		t.Fatalf("DeductForLaterRound 失败: %v", err)
	}

	// 验证 platform.Debit 被调用
	if platformClient.debitCalls != 1 {
		t.Errorf("Debit 调用次数 = %d, 期望 1", platformClient.debitCalls)
	}

	// 验证 bill 标记为 Success
	if billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", billRepo.updateBillSuccessCalls)
	}

	// 验证虚拟余额未被调用（真人玩家不走虚拟钱包）
	if virtualBalance.deductCalls != 0 {
		t.Errorf("真人玩家不应调用 VirtualBalance.Deduct, 实际调用 %d 次", virtualBalance.deductCalls)
	}

	// 验证回合结算状态更新为 Deducted
	if roundSettlementRepo.updateRoundSettlementStatusCalls != 1 {
		t.Errorf("UpdateRoundSettlementStatus 调用次数 = %d, 期望 1", roundSettlementRepo.updateRoundSettlementStatusCalls)
	}
	if roundSettlementRepo.lastStatusStatus != dto.RoundStatusDeducted {
		t.Errorf("回合状态 = %d, 期望 %d (Deducted)", roundSettlementRepo.lastStatusStatus, dto.RoundStatusDeducted)
	}
}

// TestDeductForLaterRound_Success_RobotPlayer 验证 P0-4：机器人扣款走 VirtualBalance.Deduct。
// 期望：platform.Debit 不被调用，VirtualBalance.Deduct 被调用。
func TestDeductForLaterRound_Success_RobotPlayer(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	robotChecker.isRobotRes = true
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)

	err := svc.DeductForLaterRound(context.Background(), newTestLaterRoundDeductRequest())
	if err != nil {
		t.Fatalf("DeductForLaterRound 失败: %v", err)
	}

	// 验证 VirtualBalance.Deduct 被调用（P0-4）
	if virtualBalance.deductCalls != 1 {
		t.Errorf("VirtualBalance.Deduct 调用次数 = %d, 期望 1", virtualBalance.deductCalls)
	}
	if virtualBalance.lastDeductUserID != 1001 {
		t.Errorf("VirtualBalance.Deduct userID = %d, 期望 1001", virtualBalance.lastDeductUserID)
	}
	if virtualBalance.lastDeductAmount != 200 {
		t.Errorf("VirtualBalance.Deduct amount = %d, 期望 200", virtualBalance.lastDeductAmount)
	}

	// 验证 platform.Debit 未被调用（机器人走虚拟通道）
	if platformClient.debitCalls != 0 {
		t.Errorf("机器人不应调用 platform.Debit, 实际调用 %d 次", platformClient.debitCalls)
	}

	// 验证 bill 标记为 Success
	if billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", billRepo.updateBillSuccessCalls)
	}
}

// TestDeductForLaterRound_IsRobotError_FailClosed 验证 P0-2：IsRobot 返回 error 时 fail-closed。
// 期望：方法返回 error，不调用 platform.Debit，不调用 VirtualBalance.Deduct。
func TestDeductForLaterRound_IsRobotError_FailClosed(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	robotChecker.isRobotErr = errors.New("redis 连接失败")
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)

	err := svc.DeductForLaterRound(context.Background(), newTestLaterRoundDeductRequest())
	if err == nil {
		t.Fatal("IsRobot 返回 error 时应返回 error（fail-closed）")
	}

	// 验证未调用 platform.Debit
	if platformClient.debitCalls != 0 {
		t.Errorf("fail-closed 时不应调用 platform.Debit, 实际调用 %d 次", platformClient.debitCalls)
	}
	// 验证未调用 VirtualBalance.Deduct
	if virtualBalance.deductCalls != 0 {
		t.Errorf("fail-closed 时不应调用 VirtualBalance.Deduct, 实际调用 %d 次", virtualBalance.deductCalls)
	}
	// 验证未标记 bill Success
	if billRepo.updateBillSuccessCalls != 0 {
		t.Errorf("fail-closed 时不应调用 UpdateBillSuccess, 实际调用 %d 次", billRepo.updateBillSuccessCalls)
	}
}

// TestDeductForLaterRound_ParseAmountFailure 验证 P0-3：ParseAmount 失败时标记 BillStatusFailed + 创建异常记录。
// 期望：platform.Debit 成功但余额无法解析 → bill 标记 Failed + CreateDebitFailedException 调用。
func TestDeductForLaterRound_ParseAmountFailure(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	// 返回无法解析的余额字符串，触发 ParseAmount 失败
	platformClient.debitResult = makeCommonResponse("INVALID_AMOUNT")
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)

	err := svc.DeductForLaterRound(context.Background(), newTestLaterRoundDeductRequest())
	if err == nil {
		t.Fatal("ParseAmount 失败时应返回 error（fail-closed）")
	}

	// 验证 platform.Debit 被调用（RPC 成功但响应无法解析）
	if platformClient.debitCalls != 1 {
		t.Errorf("Debit 调用次数 = %d, 期望 1", platformClient.debitCalls)
	}

	// 验证 bill 标记为 Failed（P0-3）
	if billRepo.updateBillStatusCalls == 0 {
		t.Fatal("ParseAmount 失败时应调用 UpdateBillStatus 标记 Failed")
	}
	if billRepo.lastUpdateStatusTo != dto.BillStatusFailed {
		t.Errorf("bill 最终状态 = %d, 期望 %d (Failed)", billRepo.lastUpdateStatusTo, dto.BillStatusFailed)
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

// TestDeductForLaterRound_VirtualBalanceDeductFailure 验证机器人虚拟余额扣款失败路径。
// 期望：VirtualBalance.Deduct 返回 error → bill 标记 Failed。
func TestDeductForLaterRound_VirtualBalanceDeductFailure(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	robotChecker.isRobotRes = true
	virtualBalance.deductErr = errors.New("虚拟余额不足")
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)

	err := svc.DeductForLaterRound(context.Background(), newTestLaterRoundDeductRequest())
	if err == nil {
		t.Fatal("VirtualBalance.Deduct 失败时应返回 error")
	}

	// 验证 VirtualBalance.Deduct 被调用
	if virtualBalance.deductCalls != 1 {
		t.Errorf("VirtualBalance.Deduct 调用次数 = %d, 期望 1", virtualBalance.deductCalls)
	}

	// 验证 bill 标记为 Failed
	if billRepo.lastUpdateStatusTo != dto.BillStatusFailed {
		t.Errorf("bill 最终状态 = %d, 期望 %d (Failed)", billRepo.lastUpdateStatusTo, dto.BillStatusFailed)
	}

	// 验证未标记 bill Success
	if billRepo.updateBillSuccessCalls != 0 {
		t.Errorf("VirtualBalance.Deduct 失败时不应调用 UpdateBillSuccess, 实际调用 %d 次", billRepo.updateBillSuccessCalls)
	}
}

// TestDeductForLaterRound_Idempotent 验证幂等性：已存在的扣款直接返回。
func TestDeductForLaterRound_Idempotent(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	billRepo.existsByRoundAndTypeResult = true
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)

	err := svc.DeductForLaterRound(context.Background(), newTestLaterRoundDeductRequest())
	if err != nil {
		t.Fatalf("幂等返回时应返回 nil, 实际返回: %v", err)
	}

	// 验证未执行任何扣款操作
	if platformClient.debitCalls != 0 {
		t.Errorf("幂等返回时不应调用 platform.Debit, 实际调用 %d 次", platformClient.debitCalls)
	}
	if virtualBalance.deductCalls != 0 {
		t.Errorf("幂等返回时不应调用 VirtualBalance.Deduct, 实际调用 %d 次", virtualBalance.deductCalls)
	}
}

// TestDeductForSystemPacket_Success 验证 P0-1：DeductForSystemPacket 接受 tx 参数。
// 通过 tx.BillRepo() 与 tx.RoundSettlementRepo() 访问子 repo，所有 DB 操作纳入同一事务。
func TestDeductForSystemPacket_Success(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.DeductForSystemPacket(context.Background(), tx, newTestSystemPacketDeductRequest())
	if err != nil {
		t.Fatalf("DeductForSystemPacket 失败: %v", err)
	}

	// 验证 CreateRoundSettlementAndBills 被调用（通过 tx.RoundSettlementRepo()）
	if roundSettlementRepo.createRoundSettlementAndBillsCalls != 1 {
		t.Errorf("CreateRoundSettlementAndBills 调用次数 = %d, 期望 1", roundSettlementRepo.createRoundSettlementAndBillsCalls)
	}

	// 验证幂等检查通过 tx.BillRepo() 调用 ExistsByRoundAndType
	if billRepo.existsByRoundAndTypeResult != false {
		t.Error("ExistsByRoundAndType 应返回 false 以进入创建路径")
	}
}

// TestDeductForSystemPacket_Idempotent 验证 DeductForSystemPacket 幂等性。
func TestDeductForSystemPacket_Idempotent(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient := makeDeductMocks()
	billRepo.existsByRoundAndTypeResult = true
	svc := newTestDeductService(t, billRepo, roundSettlementRepo, robotChecker, virtualBalance, platformClient)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.DeductForSystemPacket(context.Background(), tx, newTestSystemPacketDeductRequest())
	if err != nil {
		t.Fatalf("幂等返回时应返回 nil, 实际返回: %v", err)
	}

	// 验证未创建 settlement+bills
	if roundSettlementRepo.createRoundSettlementAndBillsCalls != 0 {
		t.Errorf("幂等返回时不应调用 CreateRoundSettlementAndBills, 实际调用 %d 次", roundSettlementRepo.createRoundSettlementAndBillsCalls)
	}
}
