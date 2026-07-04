package scripts

import (
	"testing"

	"github.com/cashparty/backend/game/domain"
)

// TestHandlePenaltySuccess 测试 luaHandlePenalty 成功路径：返回 code=0，计数递增。
func TestHandlePenaltySuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_penalty"
	userID := "user_penalty"
	penaltyCountKey := "cashparty:penalty:count:" + roomID + ":" + userID
	roomHashKey := "cashparty:room:hash:" + roomID
	playersKey := "cashparty:room:players:" + roomID
	roundStateKey := "cashparty:round:state:" + roomID
	penaltyRecordKey := "cashparty:penalty:record:" + roomID + ":" + userID

	rdb := client.Raw()
	rdb.HSet(ctx, playersKey, userID, `{"user_id":"user_penalty","nickname":"Alice"}`)

	keys := []string{penaltyCountKey, roomHashKey, playersKey, roundStateKey, penaltyRecordKey}
	args := []interface{}{userID, int64(100), int64(1000000), "timeout", int64(3600), int64(7200)}

	res, err := HandlePenalty.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("HandlePenalty.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}

	if count := parseTestLuaCode(res[1]); count != 1 {
		t.Errorf("expected penalty count=1, got %d", count)
	}

	// roomFee=100，首次惩罚 kickRequired=0（count < 2）
	if kickRequired := parseTestLuaCode(res[3]); kickRequired != 0 {
		t.Errorf("expected kickRequired=0, got %d", kickRequired)
	}
}

// TestDistributePenaltySuccess 测试 luaDistributePenalty 成功路径：
// 3 个玩家中排除 1 个，剩余 2 人平分 300，每人 150。
func TestDistributePenaltySuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_distribute"
	roomHashKey := "cashparty:room:hash:" + roomID
	playersKey := "cashparty:room:players:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, playersKey, "user1", `{"user_id":"user1","nickname":"Alice"}`)
	rdb.HSet(ctx, playersKey, "user2", `{"user_id":"user2","nickname":"Bob"}`)
	rdb.HSet(ctx, playersKey, "user3", `{"user_id":"user3","nickname":"Charlie"}`)

	keys := []string{roomHashKey, playersKey}
	args := []interface{}{int64(300), `["user1"]`}

	res, err := DistributePenalty.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("DistributePenalty.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}

	// shareAmount = math.floor(300 / 2) = 150
	if shareAmount := parseTestLuaCode(res[1]); shareAmount != 150 {
		t.Errorf("expected shareAmount=150, got %d", shareAmount)
	}

	// recipientCount = 2（排除了 user1）
	if recipientCount := parseTestLuaCode(res[2]); recipientCount != 2 {
		t.Errorf("expected recipientCount=2, got %d", recipientCount)
	}
}
