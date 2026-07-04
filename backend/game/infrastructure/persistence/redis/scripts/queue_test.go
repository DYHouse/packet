package scripts

import (
	"testing"

	"github.com/cashparty/backend/game/domain"
	"github.com/redis/go-redis/v9"
)

// TestEnqueueSuccess 测试 luaEnqueue 成功路径：观众加入排队队列，返回 code=0 和位置 1。
func TestEnqueueSuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_enq_ok"
	userID := "user_enq"
	queueKey := "cashparty:room:queue:" + roomID
	spectatorsKey := "cashparty:room:spectators:" + roomID
	playersKey := "cashparty:room:players:" + roomID
	roomHashKey := "cashparty:room:hash:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 1)
	rdb.HSet(ctx, spectatorsKey, userID, `{"user_id":"user_enq","nickname":"Alice","is_robot":false}`)

	keys := []string{queueKey, spectatorsKey, playersKey, roomHashKey}
	args := []interface{}{userID, int64(1000000), int64(3600)}

	res, err := Enqueue.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("Enqueue.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}

	if position := parseTestLuaCode(res[1]); position != 1 {
		t.Errorf("expected queue position=1, got %d", position)
	}
}

// TestEnqueueAlreadyQueued 测试 luaEnqueue 已在队列：返回 code=70 (LuaErrAlreadyQueued)。
func TestEnqueueAlreadyQueued(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_enq_dup"
	userID := "user_enq_dup"
	queueKey := "cashparty:room:queue:" + roomID
	spectatorsKey := "cashparty:room:spectators:" + roomID
	playersKey := "cashparty:room:players:" + roomID
	roomHashKey := "cashparty:room:hash:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 1)
	rdb.HSet(ctx, spectatorsKey, userID, `{"user_id":"user_enq_dup","nickname":"Bob","is_robot":false}`)
	// 预先将用户加入队列
	rdb.ZAdd(ctx, queueKey, redis.Z{Score: 1000000, Member: userID})

	keys := []string{queueKey, spectatorsKey, playersKey, roomHashKey}
	args := []interface{}{userID, int64(2000000), int64(3600)}

	res, err := Enqueue.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("Enqueue.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrAlreadyQueued {
		t.Errorf("expected code=%d (AlreadyQueued), got %d", domain.LuaErrAlreadyQueued, code)
	}
}

// TestDequeueSuccess 测试 luaDequeue 成功路径：用户在队列中，移除后返回 code=0。
func TestDequeueSuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_deq_ok"
	userID := "user_deq"
	queueKey := "cashparty:room:queue:" + roomID
	roomHashKey := "cashparty:room:hash:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 1)
	rdb.ZAdd(ctx, queueKey, redis.Z{Score: 1000000, Member: userID})

	keys := []string{queueKey, roomHashKey}
	args := []interface{}{userID}

	res, err := Dequeue.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("Dequeue.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}
}

// TestDequeueNotQueued 测试 luaDequeue 不在队列：返回 code=71 (LuaErrNotQueued)。
func TestDequeueNotQueued(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_deq_missing"
	userID := "user_deq_missing"
	queueKey := "cashparty:room:queue:" + roomID
	roomHashKey := "cashparty:room:hash:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 1)

	keys := []string{queueKey, roomHashKey}
	args := []interface{}{userID}

	res, err := Dequeue.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("Dequeue.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrNotQueued {
		t.Errorf("expected code=%d (NotQueued), got %d", domain.LuaErrNotQueued, code)
	}
}

// TestAutoSubstituteSuccess 测试 luaAutoSubstitute 成功路径：
// 队列中有候选观众，替补到空座位，返回 code=0。
func TestAutoSubstituteSuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_sub_ok"
	userID := "user_sub"
	queueKey := "cashparty:room:queue:" + roomID
	roomHashKey := "cashparty:room:hash:" + roomID
	playersKey := "cashparty:room:players:" + roomID
	spectatorsKey := "cashparty:room:spectators:" + roomID
	seatsKey := "cashparty:room:seats:" + roomID
	seatOwnerKey := "cashparty:room:seat:owner:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 2, "max_players", 5)
	rdb.HSet(ctx, spectatorsKey, userID, `{"user_id":"user_sub","nickname":"Alice","avatar":"a1","seat_no":0,"is_robot":false}`)
	rdb.ZAdd(ctx, queueKey, redis.Z{Score: 1000000, Member: userID})

	keys := []string{queueKey, roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey}
	args := []interface{}{1, int64(1000000), int64(3600)}

	res, err := AutoSubstitute.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("AutoSubstitute.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}

	// res[1] = substituteUserID
	substituteUserID, ok := res[1].(string)
	if !ok {
		t.Errorf("expected res[1] to be string, got %T", res[1])
	} else if substituteUserID != userID {
		t.Errorf("expected substituteUserID=%s, got %s", userID, substituteUserID)
	}

	// res[2] = playerCount（替补后玩家数为 1）
	if playerCount := parseTestLuaCode(res[2]); playerCount != 1 {
		t.Errorf("expected playerCount=1, got %d", playerCount)
	}
}

// TestAutoSubstituteQueueEmpty 测试 luaAutoSubstitute 队列为空：返回 code=75 (LuaErrQueueEmpty)。
func TestAutoSubstituteQueueEmpty(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_sub_empty"
	queueKey := "cashparty:room:queue:" + roomID
	roomHashKey := "cashparty:room:hash:" + roomID
	playersKey := "cashparty:room:players:" + roomID
	spectatorsKey := "cashparty:room:spectators:" + roomID
	seatsKey := "cashparty:room:seats:" + roomID
	seatOwnerKey := "cashparty:room:seat:owner:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 2, "max_players", 5)
	// 队列为空

	keys := []string{queueKey, roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey}
	args := []interface{}{1, int64(1000000), int64(3600)}

	res, err := AutoSubstitute.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("AutoSubstitute.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrQueueEmpty {
		t.Errorf("expected code=%d (QueueEmpty), got %d", domain.LuaErrQueueEmpty, code)
	}
}
