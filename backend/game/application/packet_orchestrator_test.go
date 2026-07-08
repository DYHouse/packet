package application

import (
	"context"
	"testing"

	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain/reward"
	"github.com/cashparty/backend/game/domain/room"
)

// =============================================================================
// SendPacket 参数校验错误路径测试
// =============================================================================

// TestSendPacketRoomNotFound 验证 GetRoomMeta 返回错误时 SendPacket 返回 CodeRoomNotFound。
// 此路径在调用 concrete 依赖（grabService/packetGenerator/settleAppService）前返回，可用 nil 桩件测试。
func TestSendPacketRoomNotFound(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-send-not-found"
	userID := "user-send-1"

	// repo 返回错误
	repo := &mockRoomRepository{metaErr: context.DeadlineExceeded}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, broadcaster, publisher, taskRunner, idGen, nil)

	result, err := svc.SendPacket(ctx, &SendPacketRequest{RoomID: roomID, UserID: userID})
	if err == nil {
		t.Fatal("期望返回错误，got nil")
	}
	if !message.IsErrorCode(err, message.CodeRoomNotFound) {
		t.Errorf("期望错误码 CodeRoomNotFound(%d)，got %v", message.CodeRoomNotFound, err)
	}
	if result != nil {
		t.Errorf("期望 result 为 nil，got %+v", result)
	}

	// 不应广播
	if broadcaster.callCount() != 0 {
		t.Errorf("GetRoomMeta 失败时不应广播，got %d 次调用", broadcaster.callCount())
	}
	// 不应提交异步任务
	if taskRunner.taskCount() != 0 {
		t.Errorf("GetRoomMeta 失败时不应提交异步任务，got %d", taskRunner.taskCount())
	}
}

// TestSendPacketGameNotStarted 验证房间状态非 Playing 时 SendPacket 返回 CodeGameNotStarted。
func TestSendPacketGameNotStarted(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-send-not-started"
	userID := "user-send-2"

	// 房间状态为 Waiting
	repo := &mockRoomRepository{meta: &room.RoomMeta{
		RoomID: roomID,
		Status: room.RoomStatusWaiting,
	}}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, broadcaster, publisher, taskRunner, idGen, nil)

	_, err := svc.SendPacket(ctx, &SendPacketRequest{RoomID: roomID, UserID: userID})
	if !message.IsErrorCode(err, message.CodeGameNotStarted) {
		t.Errorf("期望错误码 CodeGameNotStarted(%d)，got %v", message.CodeGameNotStarted, err)
	}

	if broadcaster.callCount() != 0 {
		t.Errorf("游戏未开始时不应广播，got %d", broadcaster.callCount())
	}
}

// TestSendPacketPacketsAlreadyExist 验证 CurrentRoundID 非空时 SendPacket 返回 CodePacketsAlreadyExist。
// 此场景表示当前回合已有红包，不可重复发送。
func TestSendPacketPacketsAlreadyExist(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-send-packets-exist"
	userID := "user-send-3"

	repo := &mockRoomRepository{meta: &room.RoomMeta{
		RoomID:         roomID,
		Status:         room.RoomStatusPlaying,
		CurrentRoundID: "round-existing",
	}}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, broadcaster, publisher, taskRunner, idGen, nil)

	_, err := svc.SendPacket(ctx, &SendPacketRequest{RoomID: roomID, UserID: userID})
	if !message.IsErrorCode(err, message.CodePacketsAlreadyExist) {
		t.Errorf("期望错误码 CodePacketsAlreadyExist(%d)，got %v", message.CodePacketsAlreadyExist, err)
	}

	if broadcaster.callCount() != 0 {
		t.Errorf("红包已存在时不应广播，got %d", broadcaster.callCount())
	}
}

// TestSendPacketNotInRoom 验证 GetPlayer 返回 nil/error 时 SendPacket 返回 CodeNotInRoom。
func TestSendPacketNotInRoom(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-send-not-in-room"
	userID := "user-send-4"

	// player=nil 表示玩家不在房间
	repo := &mockRoomRepository{
		meta: &room.RoomMeta{
			RoomID:         roomID,
			Status:         room.RoomStatusPlaying,
			CurrentRoundID: "",
			CurrentRound:   0,
		},
		player: nil,
	}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, broadcaster, publisher, taskRunner, idGen, nil)

	_, err := svc.SendPacket(ctx, &SendPacketRequest{RoomID: roomID, UserID: userID})
	if !message.IsErrorCode(err, message.CodeNotInRoom) {
		t.Errorf("期望错误码 CodeNotInRoom(%d)，got %v", message.CodeNotInRoom, err)
	}
}

// TestSendPacketNotYourTurn 验证后续轮次（round>1）且 nextSenderID 不匹配时
// SendPacket 返回 CodeNotYourTurn。
// 此路径在调用 initLaterRoundAndDeduct（依赖 settleAppService）前返回，可用 nil 桩件测试。
func TestSendPacketNotYourTurn(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-send-not-your-turn"
	userID := "user-send-5"

	// CurrentRound=1 → nextRound=2 (>1)，触发 GetNextSenderID 检查
	// player 必须非 nil，否则先返回 CodeNotInRoom
	repo := &mockRoomRepository{
		meta: &room.RoomMeta{
			RoomID:         roomID,
			Status:         room.RoomStatusPlaying,
			CurrentRoundID: "",
			CurrentRound:   1,
		},
		player: &room.Player{
			UserID:   userID,
			Nickname: "TestPlayer",
		},
		nextSenderID: "other-user", // 不匹配 userID
	}
	broadcaster := &mockBroadcaster{}
	publisher := &mockGameEventPublisher{}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, broadcaster, publisher, taskRunner, idGen, nil)

	// 注入 mock deductFailureHandler 验证校验错误不触发它
	deductHandler := &mockDeductFailureHandler{}
	svc.SetDeductFailureHandler(deductHandler)

	_, err := svc.SendPacket(ctx, &SendPacketRequest{RoomID: roomID, UserID: userID})
	if !message.IsErrorCode(err, message.CodeNotYourTurn) {
		t.Errorf("期望错误码 CodeNotYourTurn(%d)，got %v", message.CodeNotYourTurn, err)
	}
	if deductHandler.calls != 0 {
		t.Errorf("非扣款失败的校验错误不应调用 HandleDeductFailure，got %d", deductHandler.calls)
	}
}

// =============================================================================
// 边界场景与 nil 依赖安全测试
// =============================================================================

// TestSendPacketNilDepsSafety 验证 broadcaster/eventPublisher/scheduler 均为 nil 时
// 参数校验错误路径不 panic（确保 nil 安全）。
func TestSendPacketNilDepsSafety(t *testing.T) {
	ctx := newTestCtx(t)
	roomID := "room-send-nil-deps"
	userID := "user-send-6"

	// 所有可选依赖均为 nil
	repo := &mockRoomRepository{metaErr: context.DeadlineExceeded}
	taskRunner := &mockTaskRunner{syncRun: true}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, nil, nil, taskRunner, idGen, nil)

	// 不应 panic
	_, err := svc.SendPacket(ctx, &SendPacketRequest{RoomID: roomID, UserID: userID})
	if err == nil {
		t.Fatal("期望返回错误，got nil")
	}
	if !message.IsErrorCode(err, message.CodeRoomNotFound) {
		t.Errorf("期望 CodeRoomNotFound，got %v", err)
	}
}

// TestSetDeductFailureHandler 验证 SetDeductFailureHandler 正确注入处理器。
func TestSetDeductFailureHandler(t *testing.T) {
	repo := &mockRoomRepository{}
	taskRunner := &mockTaskRunner{}
	idGen := &mockIDGenerator{}

	svc := newTestPacketOrchestrator(repo, &mockDBRepository{}, nil, nil, taskRunner, idGen, nil)

	if svc.deductFailureHandler != nil {
		t.Fatal("初始 deductFailureHandler 应为 nil")
	}

	handler := &mockDeductFailureHandler{}
	svc.SetDeductFailureHandler(handler)

	if svc.deductFailureHandler != handler {
		t.Error("SetDeductFailureHandler 未正确注入处理器")
	}
}

// =============================================================================
// P0-5 迁移验证：reward.CalculateRewardAmount
// =============================================================================

// TestCalculateRewardAmountP0_5 验证 P0-5 迁移后的 reward.CalculateRewardAmount 行为正确。
// 顺子（type=1）= totalAmount × 1.0；豹子（type=2）= totalAmount × 10.0；其他=0。
// packet_orchestrator.go:131 调用 reward.CalculateRewardAmount，本测试确认该领域函数语义。
func TestCalculateRewardAmountP0_5(t *testing.T) {
	tests := []struct {
		name        string
		rewardType  int
		totalAmount int64
		want        int64
	}{
		{
			name:        "顺子奖励=本金×1.0",
			rewardType:  reward.RewardTypeStraight,
			totalAmount: 500,
			want:        500,
		},
		{
			name:        "豹子奖励=本金×10.0",
			rewardType:  reward.RewardTypeLeopard,
			totalAmount: 500,
			want:        5000,
		},
		{
			name:        "未知奖励类型返回0",
			rewardType:  99,
			totalAmount: 500,
			want:        0,
		},
		{
			name:        "零本金顺子返回0",
			rewardType:  reward.RewardTypeStraight,
			totalAmount: 0,
			want:        0,
		},
		{
			name:        "零本金豹子返回0",
			rewardType:  reward.RewardTypeLeopard,
			totalAmount: 0,
			want:        0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reward.CalculateRewardAmount(tt.rewardType, tt.totalAmount)
			if got != tt.want {
				t.Errorf("CalculateRewardAmount(%d, %d) = %d, want %d",
					tt.rewardType, tt.totalAmount, got, tt.want)
			}
		})
	}
}

// TestCalculateRequiredFeeP0_5 验证 P0-5 迁移后的 room.CalculateRequiredFee 行为正确。
// 公式：首局费用 = 房费 / 最大玩家数；后续局费用 = 房费 × (最大局数 - 1)。
func TestCalculateRequiredFeeP0_5(t *testing.T) {
	tests := []struct {
		name      string
		roomFee   int64
		maxPlayer int
		maxRounds int
		want      int64
	}{
		{
			name:      "标准配置(房费500/5人/10局)",
			roomFee:   500,
			maxPlayer: 5,
			maxRounds: 10,
			want:      100 + 4500, // 500/5 + 500*9
		},
		{
			name:      "最大玩家数为0返回0",
			roomFee:   500,
			maxPlayer: 0,
			maxRounds: 10,
			want:      0,
		},
		{
			name:      "最大局数为0返回0",
			roomFee:   500,
			maxPlayer: 5,
			maxRounds: 0,
			want:      0,
		},
		{
			name:      "单局配置(房费300/3人/1局)",
			roomFee:   300,
			maxPlayer: 3,
			maxRounds: 1,
			want:      100 + 0, // 300/3 + 300*0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := room.CalculateRequiredFee(tt.roomFee, tt.maxPlayer, tt.maxRounds)
			if got != tt.want {
				t.Errorf("CalculateRequiredFee(%d, %d, %d) = %d, want %d",
					tt.roomFee, tt.maxPlayer, tt.maxRounds, got, tt.want)
			}
		})
	}
}
