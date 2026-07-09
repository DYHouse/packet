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

// newRoundSettleService 构造测试用 RoundSettleService。
// redis/gameSettleSvc 传 nil：SettleRound 路径不直接使用这两个依赖。
func newRoundSettleService(billRepo *mockBillRepo, roundSettlementRepo *mockRoundSettlementRepo, robotChecker *mockRobotChecker, rewardSettler *RewardSettler) *RoundSettleService {
	return NewRoundSettleService(billRepo, roundSettlementRepo, nil, newTestTraceIDGen(), rewardSettler, nil, nil, robotChecker)
}

// newTestSettlement 构造测试用回合结算记录（状态为 Deducted，可进入 SettleRound 流程）。
func newTestSettlement() *domain.RoundSettlement {
	return &domain.RoundSettlement{
		ID:           1,
		RoundTraceID: "RT_1000_1",
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10000,
		RoundNo:      1,
		Status:       domain.RoundStatusDeducted,
	}
}

// newTestPlayers 构造测试用玩家结算信息列表。
func newTestPlayers() []*dto.PlayerSettleInfo {
	return []*dto.PlayerSettleInfo{
		{UserID: 1001, Amount: 500},
		{UserID: 1002, Amount: 300},
	}
}

// newTestRoundSettleRequest 构造测试用 RoundSettleRequest（不含佣金与奖励）。
func newTestRoundSettleRequest() *dto.RoundSettleRequest {
	return &dto.RoundSettleRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10000,
		RoundNo:      1,
		SenderID:     2000,
		SenderType:   "user",
		TotalAmount:  800,
		Commission:   0,
		MinPlayerID:  1001,
		Players:      newTestPlayers(),
		RewardType:   0,
		RewardAmount: 0,
	}
}

// makeRoundSettleMocks 构造一组全新的 mock 桩件，确保每个测试用例状态隔离。
func makeRoundSettleMocks() (*mockBillRepo, *mockRoundSettlementRepo, *mockRobotChecker) {
	billRepo := &mockBillRepo{
		getBillByRoundTypeAndUserResult: nil,
	}
	roundSettlementRepo := &mockRoundSettlementRepo{
		getRoundSettlementByRoundIDResult: newTestSettlement(),
	}
	robotChecker := &mockRobotChecker{
		isRobotRes: false,
	}
	return billRepo, roundSettlementRepo, robotChecker
}

// ============================================================================
// 测试用例
// ============================================================================

// TestSettleRound_Success_RealPlayer 验证真人玩家结算成功路径。
// 覆盖 P0-1：service 接受 tx 参数，通过 tx.BillRepo() 与 tx.RoundSettlementRepo() 访问子 repo。
func TestSettleRound_Success_RealPlayer(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.SettleRound(context.Background(), tx, newTestRoundSettleRequest())
	if err != nil {
		t.Fatalf("SettleRound 失败: %v", err)
	}

	// 验证 grab bill 已创建（2 个玩家）
	if billRepo.createBillsCalls != 1 {
		t.Errorf("CreateBills 调用次数 = %d, 期望 1", billRepo.createBillsCalls)
	}
	if len(billRepo.createBillsArg) != 2 {
		t.Errorf("创建的 bill 数量 = %d, 期望 2", len(billRepo.createBillsArg))
	}

	// 验证 bill 状态为 Success 且 IsRobot=false（真人玩家）
	for _, b := range billRepo.createBillsArg {
		if b.Status != domain.BillStatusSuccess {
			t.Errorf("bill 状态 = %d, 期望 %d (Success)", b.Status, domain.BillStatusSuccess)
		}
		if b.IsRobot {
			t.Errorf("真人玩家 bill.IsRobot 应为 false")
		}
	}

	// 验证回合结算信息已更新
	if roundSettlementRepo.updateRoundSettlementSettleInfoCalls != 1 {
		t.Errorf("UpdateRoundSettlementSettleInfo 调用次数 = %d, 期望 1", roundSettlementRepo.updateRoundSettlementSettleInfoCalls)
	}

	// 验证回合已标记为 Credited
	if roundSettlementRepo.updateRoundSettlementCreditedCalls != 1 {
		t.Errorf("UpdateRoundSettlementCredited 调用次数 = %d, 期望 1", roundSettlementRepo.updateRoundSettlementCreditedCalls)
	}
	if roundSettlementRepo.lastCreditedSettleAmount != 800 {
		t.Errorf("结算总金额 = %d, 期望 800", roundSettlementRepo.lastCreditedSettleAmount)
	}
	if roundSettlementRepo.lastCreditedSettleUserCount != 2 {
		t.Errorf("结算用户数 = %d, 期望 2", roundSettlementRepo.lastCreditedSettleUserCount)
	}
}

// TestSettleRound_Success_RobotPlayer 验证机器人玩家结算路径。
// bill.IsRobot 应被标记为 true。
func TestSettleRound_Success_RobotPlayer(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	robotChecker.isRobotRes = true
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.SettleRound(context.Background(), tx, newTestRoundSettleRequest())
	if err != nil {
		t.Fatalf("SettleRound 失败: %v", err)
	}

	if billRepo.createBillsCalls != 1 {
		t.Fatalf("CreateBills 调用次数 = %d, 期望 1", billRepo.createBillsCalls)
	}
	for _, b := range billRepo.createBillsArg {
		if !b.IsRobot {
			t.Errorf("机器人玩家 bill.IsRobot 应为 true")
		}
	}
	// 验证 IsRobot 被调用（每个玩家一次）
	if robotChecker.callCount != 2 {
		t.Errorf("IsRobot 调用次数 = %d, 期望 2", robotChecker.callCount)
	}
}

// TestSettleRound_Success_WithCommission 验证含佣金的结算路径。
// 佣金 bill 应通过 CreateBill 创建，且 UserID 为 PlatformAccountID。
func TestSettleRound_Success_WithCommission(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	req := newTestRoundSettleRequest()
	req.Commission = 100

	err := svc.SettleRound(context.Background(), tx, req)
	if err != nil {
		t.Fatalf("SettleRound 失败: %v", err)
	}

	// 验证佣金 bill 已创建（CreateBill 单条）
	if billRepo.createBillCalls != 1 {
		t.Errorf("CreateBill 佣金调用次数 = %d, 期望 1", billRepo.createBillCalls)
	}
	if billRepo.createBillArg == nil {
		t.Fatalf("佣金 bill 参数为 nil")
	}
	if billRepo.createBillArg.BillType != domain.BillTypeCommission {
		t.Errorf("佣金 bill 类型 = %d, 期望 %d", billRepo.createBillArg.BillType, domain.BillTypeCommission)
	}
	if billRepo.createBillArg.UserID != dto.PlatformAccountID {
		t.Errorf("佣金 bill UserID = %d, 期望 %d (PlatformAccountID)", billRepo.createBillArg.UserID, dto.PlatformAccountID)
	}
	if billRepo.createBillArg.Amount != 100 {
		t.Errorf("佣金 bill 金额 = %d, 期望 100", billRepo.createBillArg.Amount)
	}
}

// TestSettleRound_IsRobotError_FailClosed 验证 P0-2：IsRobot 返回 error 时 fail-closed。
// 期望：SettleRound 返回 error，不创建任何 bill，不标记 Credited。
func TestSettleRound_IsRobotError_FailClosed(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	robotChecker.isRobotErr = errors.New("redis 不可用")
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.SettleRound(context.Background(), tx, newTestRoundSettleRequest())
	if err == nil {
		t.Fatal("IsRobot 返回 error 时 SettleRound 应返回 error（fail-closed）")
	}

	// 验证未创建任何 bill
	if billRepo.createBillsCalls != 0 {
		t.Errorf("fail-closed 时不应调用 CreateBills, 实际调用 %d 次", billRepo.createBillsCalls)
	}
	// 验证未标记 Credited
	if roundSettlementRepo.updateRoundSettlementCreditedCalls != 0 {
		t.Errorf("fail-closed 时不应调用 UpdateRoundSettlementCredited, 实际调用 %d 次", roundSettlementRepo.updateRoundSettlementCreditedCalls)
	}
}

// TestSettleRound_CreateBillsError 验证 bill 创建失败时事务回滚。
// 期望：SettleRound 返回 error，不标记 Credited。
func TestSettleRound_CreateBillsError(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	billRepo.createBillsErr = errors.New("数据库写入失败")
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.SettleRound(context.Background(), tx, newTestRoundSettleRequest())
	if err == nil {
		t.Fatal("CreateBills 失败时 SettleRound 应返回 error")
	}

	// 验证未标记 Credited（creditRound 失败后不应继续）
	if roundSettlementRepo.updateRoundSettlementCreditedCalls != 0 {
		t.Errorf("bill 创建失败时不应调用 UpdateRoundSettlementCredited, 实际调用 %d 次", roundSettlementRepo.updateRoundSettlementCreditedCalls)
	}
}

// TestSettleRound_Idempotent_AlreadyCredited 验证幂等性：已 Credited 的回合直接返回。
func TestSettleRound_Idempotent_AlreadyCredited(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	// 预检查阶段即返回 Credited 状态，直接短路返回
	roundSettlementRepo.getRoundSettlementByRoundIDResult = &domain.RoundSettlement{
		ID:      1,
		RoundID: 10000,
		Status:  domain.RoundStatusCredited,
	}
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.SettleRound(context.Background(), tx, newTestRoundSettleRequest())
	if err != nil {
		t.Fatalf("已 Credited 的回合应直接返回 nil, 实际返回: %v", err)
	}

	// 验证未执行任何写操作
	if billRepo.createBillsCalls != 0 {
		t.Errorf("幂等返回时不应调用 CreateBills, 实际调用 %d 次", billRepo.createBillsCalls)
	}
	if roundSettlementRepo.updateRoundSettlementSettleInfoCalls != 0 {
		t.Errorf("幂等返回时不应调用 UpdateRoundSettlementSettleInfo, 实际调用 %d 次", roundSettlementRepo.updateRoundSettlementSettleInfoCalls)
	}
}

// TestSettleRound_SettlementNotFound 验证回合结算记录不存在时返回 error。
func TestSettleRound_SettlementNotFound(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	roundSettlementRepo.getRoundSettlementByRoundIDResult = nil
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	err := svc.SettleRound(context.Background(), tx, newTestRoundSettleRequest())
	if err == nil {
		t.Fatal("结算记录不存在时应返回 error")
	}

	// 验证未执行任何写操作
	if billRepo.createBillsCalls != 0 {
		t.Errorf("结算记录不存在时不应调用 CreateBills, 实际调用 %d 次", billRepo.createBillsCalls)
	}
}

// TestSettleRound_EmptyPlayerList 验证空玩家列表的边界场景。
// 期望：不创建 bill，但仍正常标记 Credited（settleUserCount=0）。
func TestSettleRound_EmptyPlayerList(t *testing.T) {
	billRepo, roundSettlementRepo, robotChecker := makeRoundSettleMocks()
	svc := newRoundSettleService(billRepo, roundSettlementRepo, robotChecker, nil)
	tx := &mockTransaction{billRepo: billRepo, roundSettlementRepo: roundSettlementRepo}

	req := newTestRoundSettleRequest()
	req.Players = []*dto.PlayerSettleInfo{}

	err := svc.SettleRound(context.Background(), tx, req)
	if err != nil {
		t.Fatalf("空玩家列表时 SettleRound 不应失败: %v", err)
	}

	// 验证未创建 grab bill（无玩家）
	if billRepo.createBillsCalls != 0 {
		t.Errorf("空玩家列表时不应调用 CreateBills, 实际调用 %d 次", billRepo.createBillsCalls)
	}
	// 验证仍标记 Credited（settleUserCount=0）
	if roundSettlementRepo.updateRoundSettlementCreditedCalls != 1 {
		t.Errorf("空玩家列表时仍应调用 UpdateRoundSettlementCredited, 实际调用 %d 次", roundSettlementRepo.updateRoundSettlementCreditedCalls)
	}
	if roundSettlementRepo.lastCreditedSettleUserCount != 0 {
		t.Errorf("空玩家列表结算用户数 = %d, 期望 0", roundSettlementRepo.lastCreditedSettleUserCount)
	}
}
