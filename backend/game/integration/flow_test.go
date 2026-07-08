package integration

import (
	"testing"

	gameAlg "github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/domain/reward"
	"github.com/cashparty/backend/settlement/dto"
)

// ============================================================================
// 端到端集成测试：发包 → 抢包 → 结算 → 入账
// ============================================================================

// TestIntegration_SendPacket_GrabPacket_Settle_Account 验证完整游戏流程的 Happy Path。
// 流程：发包（真实 PacketGenerator）→ 抢包（金额分配）→ 结算（真实 CalculateRewardAmount/DetermineGameResult）→ 入账（真实 DeductService）。
// 验证点：
// 1. 红包金额总数与分包数正确
// 2. 抢包金额之和等于红包总额
// 3. 奖励金额计算正确（Straight/Leopard 倍数）
// 4. 胜负判定正确（win/lose）
// 5. 真人玩家扣款成功，账单标记 Success，platform.Debit 被调用
func TestIntegration_SendPacket_GrabPacket_Settle_Account(t *testing.T) {
	ctx := newTestCtx(t)
	env := newIntegrationEnv(t, false, "100.00")

	// === 步骤 1：发包（Send Packet）===
	// 使用真实 PacketGenerator 生成红包，TotalAmount=1000 分（10.00 元），PacketCount=5
	totalAmount := int64(1000)
	packetCount := 5
	result, err := env.packetGen.Generate(ctx, &gameAlg.GenerateRequest{
		TotalAmount: totalAmount,
		PacketCount: packetCount,
		RoomID:      "room_1",
		RoundID:     "round_1",
	})
	if err != nil {
		t.Fatalf("发包失败: %v", err)
	}

	// 验证红包金额数量
	if len(result.PacketAmounts) != packetCount {
		t.Fatalf("红包数量 = %d, 期望 %d", len(result.PacketAmounts), packetCount)
	}

	// 验证红包金额之和等于总额
	var sum int64
	for _, amt := range result.PacketAmounts {
		sum += amt
	}
	if sum != totalAmount {
		t.Fatalf("红包金额之和 = %d, 期望 %d", sum, totalAmount)
	}

	// 验证每个红包金额 >= MinPacketAmount（默认 1 分）
	for i, amt := range result.PacketAmounts {
		if amt < 1 {
			t.Fatalf("红包[%d] 金额 = %d, 期望 >= 1", i, amt)
		}
	}

	// === 步骤 2：抢包（Grab Packet）===
	// 模拟 5 位玩家抢红包，记录每位玩家抢到的金额
	playerIDs := []int64{1001, 1002, 1003, 1004, 1005}
	grabs := make(map[int64]int64, len(playerIDs))
	for i, playerID := range playerIDs {
		grabs[playerID] = result.PacketAmounts[i]
	}

	// 验证抢包金额之和等于红包总额
	var grabSum int64
	for _, amt := range grabs {
		grabSum += amt
	}
	if grabSum != totalAmount {
		t.Fatalf("抢包金额之和 = %d, 期望 %d", grabSum, totalAmount)
	}

	// === 步骤 3：结算（Settlement）===
	// 使用真实 game/domain.CalculateRewardAmount 验证奖励金额计算（P0-5）
	rewardTests := []struct {
		name        string
		rewardType  int
		expectedAmt int64
	}{
		{"无奖励", reward.RewardTypeStraight, int64(float64(totalAmount) * reward.StraightRewardMultiplier)},
		{"豹子奖励", reward.RewardTypeLeopard, int64(float64(totalAmount) * reward.LeopardRewardMultiplier)},
	}
	for _, tt := range rewardTests {
		got := reward.CalculateRewardAmount(tt.rewardType, totalAmount)
		if got != tt.expectedAmt {
			t.Errorf("CalculateRewardAmount(%d, %d) = %d, 期望 %d (%s)",
				tt.rewardType, totalAmount, got, tt.expectedAmt, tt.name)
		}
	}

	// 使用真实 game/domain.DetermineGameResult 验证胜负判定（P0-5）
	gameResultTests := []struct {
		name     string
		payOut   int64
		betAmt   int64
		expected string
	}{
		{"豹子赢", 10000, totalAmount, reward.GameResultWin},
		{"平局输", totalAmount, totalAmount, reward.GameResultLose},
		{"无奖励输", 0, totalAmount, reward.GameResultLose},
	}
	for _, tt := range gameResultTests {
		got := reward.DetermineGameResult(tt.payOut, tt.betAmt)
		if got != tt.expected {
			t.Errorf("DetermineGameResult(%d, %d) = %q, 期望 %q (%s)",
				tt.payOut, tt.betAmt, got, tt.expected, tt.name)
		}
	}

	// === 步骤 4：入账（Accounting）===
	// 使用真实 DeductService 对真人玩家执行扣款
	deductReq := &dto.LaterRoundDeductRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10001,
		RoundNo:      2,
		RoomFee:      200,
		MinPlayerID:  1001,
		RoundTraceID: "",
	}
	if err := env.deductSvc.DeductForLaterRound(ctx, deductReq); err != nil {
		t.Fatalf("入账失败: %v", err)
	}

	// 验证账单标记为 Success
	if env.billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", env.billRepo.updateBillSuccessCalls)
	}

	// 验证 platform.Debit 被调用（真人玩家走平台通道）
	if env.platformClient.debitCalls != 1 {
		t.Errorf("platform.Debit 调用次数 = %d, 期望 1", env.platformClient.debitCalls)
	}

	// 验证 VirtualBalance.Deduct 未被调用（真人玩家不走虚拟钱包，P0-4）
	if env.virtualBalance.deductCalls != 0 {
		t.Errorf("真人玩家不应调用 VirtualBalance.Deduct, 实际调用 %d 次", env.virtualBalance.deductCalls)
	}

	// 验证回合结算状态更新为 Deducted
	if env.roundSettleRepo.updateRoundSettlementStatusCalls != 1 {
		t.Errorf("UpdateRoundSettlementStatus 调用次数 = %d, 期望 1",
			env.roundSettleRepo.updateRoundSettlementStatusCalls)
	}
	if env.roundSettleRepo.lastStatusStatus != dto.RoundStatusDeducted {
		t.Errorf("回合状态 = %d, 期望 %d (Deducted)",
			env.roundSettleRepo.lastStatusStatus, dto.RoundStatusDeducted)
	}
}

// TestIntegration_RobotPlayer_Flow 验证 P0-4：机器人玩家扣款走 VirtualBalance.Deduct。
// 期望：platform.Debit 不被调用，VirtualBalance.Deduct 被调用，账单标记 Success。
func TestIntegration_RobotPlayer_Flow(t *testing.T) {
	ctx := newTestCtx(t)
	env := newIntegrationEnv(t, true, "100.00") // isRobot=true

	// === 发包 ===
	result, err := env.packetGen.Generate(ctx, &gameAlg.GenerateRequest{
		TotalAmount: 1000,
		PacketCount: 3,
		RoomID:      "room_1",
		RoundID:     "round_robot",
	})
	if err != nil {
		t.Fatalf("发包失败: %v", err)
	}
	if len(result.PacketAmounts) != 3 {
		t.Fatalf("红包数量 = %d, 期望 3", len(result.PacketAmounts))
	}

	// === 入账（机器人扣款）===
	deductReq := &dto.LaterRoundDeductRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10002,
		RoundNo:      2,
		RoomFee:      200,
		MinPlayerID:  2001,
		RoundTraceID: "",
	}
	if err := env.deductSvc.DeductForLaterRound(ctx, deductReq); err != nil {
		t.Fatalf("机器人入账失败: %v", err)
	}

	// 验证 VirtualBalance.Deduct 被调用（P0-4）
	if env.virtualBalance.deductCalls != 1 {
		t.Errorf("VirtualBalance.Deduct 调用次数 = %d, 期望 1", env.virtualBalance.deductCalls)
	}
	if env.virtualBalance.lastDeductUserID != 2001 {
		t.Errorf("VirtualBalance.Deduct userID = %d, 期望 2001", env.virtualBalance.lastDeductUserID)
	}
	if env.virtualBalance.lastDeductAmount != 200 {
		t.Errorf("VirtualBalance.Deduct amount = %d, 期望 200", env.virtualBalance.lastDeductAmount)
	}

	// 验证 platform.Debit 未被调用（机器人走虚拟通道，不走平台）
	if env.platformClient.debitCalls != 0 {
		t.Errorf("机器人不应调用 platform.Debit, 实际调用 %d 次", env.platformClient.debitCalls)
	}

	// 验证账单标记为 Success
	if env.billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", env.billRepo.updateBillSuccessCalls)
	}

	// 验证回合结算状态更新为 Deducted
	if env.roundSettleRepo.lastStatusStatus != dto.RoundStatusDeducted {
		t.Errorf("回合状态 = %d, 期望 %d (Deducted)",
			env.roundSettleRepo.lastStatusStatus, dto.RoundStatusDeducted)
	}
}

// TestIntegration_RealPlayer_Flow 验证 P0-4：真人玩家扣款走 platform.Debit。
// 期望：platform.Debit 被调用，VirtualBalance.Deduct 不被调用，账单标记 Success。
func TestIntegration_RealPlayer_Flow(t *testing.T) {
	ctx := newTestCtx(t)
	env := newIntegrationEnv(t, false, "500.00") // isRobot=false, 余额 500.00 元

	// === 发包 ===
	result, err := env.packetGen.Generate(ctx, &gameAlg.GenerateRequest{
		TotalAmount: 2000,
		PacketCount: 4,
		RoomID:      "room_1",
		RoundID:     "round_real",
	})
	if err != nil {
		t.Fatalf("发包失败: %v", err)
	}
	if len(result.PacketAmounts) != 4 {
		t.Fatalf("红包数量 = %d, 期望 4", len(result.PacketAmounts))
	}

	// 验证红包金额之和
	var sum int64
	for _, amt := range result.PacketAmounts {
		sum += amt
	}
	if sum != 2000 {
		t.Fatalf("红包金额之和 = %d, 期望 2000", sum)
	}

	// === 入账（真人扣款）===
	deductReq := &dto.LaterRoundDeductRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10003,
		RoundNo:      3,
		RoomFee:      300,
		MinPlayerID:  3001,
		RoundTraceID: "",
	}
	if err := env.deductSvc.DeductForLaterRound(ctx, deductReq); err != nil {
		t.Fatalf("真人入账失败: %v", err)
	}

	// 验证 platform.Debit 被调用（真人走平台通道，P0-4）
	if env.platformClient.debitCalls != 1 {
		t.Errorf("platform.Debit 调用次数 = %d, 期望 1", env.platformClient.debitCalls)
	}

	// 验证 VirtualBalance.Deduct 未被调用（真人不走虚拟钱包，P0-4）
	if env.virtualBalance.deductCalls != 0 {
		t.Errorf("真人玩家不应调用 VirtualBalance.Deduct, 实际调用 %d 次", env.virtualBalance.deductCalls)
	}

	// 验证账单标记为 Success
	if env.billRepo.updateBillSuccessCalls != 1 {
		t.Errorf("UpdateBillSuccess 调用次数 = %d, 期望 1", env.billRepo.updateBillSuccessCalls)
	}

	// 验证余额解析正确（500.00 元 = 50000 分）
	if env.billRepo.lastUpdateSuccessBal != 50000 {
		t.Errorf("解析后余额 = %d, 期望 50000 (500.00 元)", env.billRepo.lastUpdateSuccessBal)
	}
}

// TestIntegration_ParseAmountFailure_Flow 验证 P0-3：ParseAmount 失败时标记 BillStatusFailed + 创建异常记录。
// 期望：platform.Debit 成功但余额无法解析 → bill 标记 Failed + UpdateBillExceptionID 被调用。
func TestIntegration_ParseAmountFailure_Flow(t *testing.T) {
	ctx := newTestCtx(t)
	// 平台返回无法解析的余额字符串，触发 ParseAmount 失败
	env := newIntegrationEnv(t, false, "INVALID_AMOUNT")

	// === 发包 ===
	result, err := env.packetGen.Generate(ctx, &gameAlg.GenerateRequest{
		TotalAmount: 1500,
		PacketCount: 3,
		RoomID:      "room_1",
		RoundID:     "round_parse_fail",
	})
	if err != nil {
		t.Fatalf("发包失败: %v", err)
	}
	if len(result.PacketAmounts) != 3 {
		t.Fatalf("红包数量 = %d, 期望 3", len(result.PacketAmounts))
	}

	// === 入账（ParseAmount 失败路径）===
	deductReq := &dto.LaterRoundDeductRequest{
		RoomID:       100,
		SessionID:    1000,
		RoundID:      10004,
		RoundNo:      4,
		RoomFee:      250,
		MinPlayerID:  4001,
		RoundTraceID: "",
	}
	err = env.deductSvc.DeductForLaterRound(ctx, deductReq)
	if err == nil {
		t.Fatal("ParseAmount 失败时应返回 error（fail-closed）")
	}

	// 验证 platform.Debit 被调用（RPC 成功但响应无法解析）
	if env.platformClient.debitCalls != 1 {
		t.Errorf("Debit 调用次数 = %d, 期望 1", env.platformClient.debitCalls)
	}

	// 验证账单标记为 Failed（P0-3）
	if env.billRepo.updateBillStatusCalls == 0 {
		t.Fatal("ParseAmount 失败时应调用 UpdateBillStatus 标记 Failed")
	}
	if env.billRepo.lastUpdateStatusTo != dto.BillStatusFailed {
		t.Errorf("bill 最终状态 = %d, 期望 %d (Failed)",
			env.billRepo.lastUpdateStatusTo, dto.BillStatusFailed)
	}

	// 验证创建了异常记录（UpdateBillExceptionID 被调用，P0-3）
	if env.billRepo.updateBillExceptionIDCalls == 0 {
		t.Error("ParseAmount 失败时应创建异常记录（UpdateBillExceptionID 应被调用）")
	}

	// 验证未标记 bill Success
	if env.billRepo.updateBillSuccessCalls != 0 {
		t.Errorf("ParseAmount 失败时不应调用 UpdateBillSuccess, 实际调用 %d 次",
			env.billRepo.updateBillSuccessCalls)
	}
}
