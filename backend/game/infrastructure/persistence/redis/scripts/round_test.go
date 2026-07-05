package scripts

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain"
	"github.com/redis/go-redis/v9"
)

// setupMiniRedis 创建一个 miniredis 实例并返回包装后的 cRedis.Client 和 context。
// 如果 miniredis 不可用则跳过测试。
// packet_test.go / room_seat_test.go / round_test.go / penalty_test.go / queue_test.go 共用。
func setupMiniRedis(t *testing.T) (*miniredis.Miniredis, *cRedis.Client, context.Context) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis not available: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// miniredis 对 EVALSHA 支持不稳定，统一回退到 EVAL 路径
	cRedis.SetUseEvalSHA(false)

	client := cRedis.NewClientFromRaw(rdb)
	return mr, client, context.Background()
}

// parseTestLuaCode 将 Lua 脚本返回值解析为 int（用于错误码提取）。
func parseTestLuaCode(val interface{}) int {
	switch v := val.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	case nil:
		return 0
	default:
		return -1
	}
}

// must 在 setup 阶段失败时立即终止测试（room_seat_test.go 使用）。
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
}

// resultArr 执行 redis 命令并以 []interface{} 形式返回结果（room_seat_test.go 使用）。
func resultArr(t *testing.T, cmd *redis.Cmd) []interface{} {
	t.Helper()
	arr, err := cmd.Slice()
	if err != nil {
		t.Fatalf("script run failed: %v", err)
	}
	return arr
}

// codeOf 从 Lua 返回数组中提取错误码（room_seat_test.go 使用）。
func codeOf(t *testing.T, arr []interface{}) int {
	t.Helper()
	if len(arr) == 0 {
		t.Fatalf("empty result array")
	}
	return parseTestLuaCode(arr[0])
}

// resultCode 从 Lua 脚本 .Result() 返回值中提取错误码（packet_test.go 使用）。
func resultCode(t *testing.T, res interface{}) int {
	t.Helper()
	arr, ok := res.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T: %v", res, res)
	}
	return codeOf(t, arr)
}

// asInt64Elem 安全取 Lua 返回数组中的 int64 元素（packet_test.go 使用）。
func asInt64Elem(t *testing.T, arr []interface{}, idx int) int64 {
	t.Helper()
	if idx >= len(arr) {
		t.Fatalf("index %d out of range (len=%d)", idx, len(arr))
	}
	v, ok := arr[idx].(int64)
	if !ok {
		t.Fatalf("arr[%d] expected int64, got %T: %v", idx, arr[idx], arr[idx])
	}
	return v
}

// asStringElem 安全取 Lua 返回数组中的 string 元素（packet_test.go 使用）。
func asStringElem(t *testing.T, arr []interface{}, idx int) string {
	t.Helper()
	if idx >= len(arr) {
		t.Fatalf("index %d out of range (len=%d)", idx, len(arr))
	}
	v, ok := arr[idx].(string)
	if !ok {
		t.Fatalf("arr[%d] expected string, got %T: %v", idx, arr[idx], arr[idx])
	}
	return v
}

// sliceContains 检查字符串切片是否包含目标（packet_test.go 使用）。
func sliceContains(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

// TestEndGameSuccess 测试 luaEndGame 成功路径：房间状态为 Playing(2)，结束后置为 Waiting(1)。
func TestEndGameSuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_endgame_ok"
	roomHashKey := "cashparty:room:hash:" + roomID
	playersKey := "cashparty:room:players:" + roomID
	spectatorsKey := "cashparty:room:spectators:" + roomID
	seatsKey := "cashparty:room:seats:" + roomID
	seatOwnerKey := "cashparty:room:seat:owner:" + roomID

	rdb := client.Raw()
	rdb.HSet(ctx, roomHashKey, "status", 2)
	rdb.HSet(ctx, playersKey, "user1", `{"user_id":"user1","nickname":"Alice","avatar":"a1","seat_no":1}`)

	keys := []string{roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey}
	args := []interface{}{int64(1000000), 2, int64(3600)}

	res, err := EndGame.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("EndGame.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}

	status, err := rdb.HGet(ctx, roomHashKey, "status").Int()
	if err != nil {
		t.Fatalf("HGet status failed: %v", err)
	}
	if status != 1 {
		t.Errorf("expected status=1 (Waiting) after end game, got %d", status)
	}
}

// TestEndGameRoomNotFound 测试 luaEndGame 房间不存在（状态不匹配）返回 code=1。
func TestEndGameRoomNotFound(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roomID := "room_endgame_missing"
	keys := []string{
		"cashparty:room:hash:" + roomID,
		"cashparty:room:players:" + roomID,
		"cashparty:room:spectators:" + roomID,
		"cashparty:room:seats:" + roomID,
		"cashparty:room:seat:owner:" + roomID,
	}
	args := []interface{}{int64(1000000), 2, int64(3600)}

	res, err := EndGame.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("EndGame.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrRoomNotFound {
		t.Errorf("expected code=%d (RoomNotFound), got %d", domain.LuaErrRoomNotFound, code)
	}
}

// TestSettleRoundSuccess 测试 luaSettleRound 成功路径：phase=GRABBING → SETTLED。
func TestSettleRoundSuccess(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roundID := "round_settle_ok"
	roundStateKey := "cashparty:round:state:" + roundID
	grabbersKey := "cashparty:round:grabbers:" + roundID
	playersKey := "cashparty:room:players:room1"
	roomHashKey := "cashparty:room:hash:room1"
	availablePacketsKey := "cashparty:round:available_packets:" + roundID

	rdb := client.Raw()
	rdb.HSet(ctx, roundStateKey, "phase", "GRABBING", "round_no", 1, "sender_id", "user1", "total_amount", 100)

	keys := []string{roundStateKey, grabbersKey, playersKey, roomHashKey, availablePacketsKey, ""}
	args := []interface{}{roundID, int64(1000000), rediskeys.KeyPacketInfoPrefix}

	res, err := SettleRound.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("SettleRound.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrSuccess {
		t.Errorf("expected code=%d, got %d", domain.LuaErrSuccess, code)
	}

	phase, err := rdb.HGet(ctx, roundStateKey, "phase").Result()
	if err != nil {
		t.Fatalf("HGet phase failed: %v", err)
	}
	if phase != "SETTLED" {
		t.Errorf("expected phase=SETTLED, got %s", phase)
	}
}

// TestSettleRoundIdempotent 测试 luaSettleRound 幂等性：phase=SETTLED 时返回 code=2。
func TestSettleRoundIdempotent(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)

	roundID := "round_settle_idem"
	roundStateKey := "cashparty:round:state:" + roundID

	rdb := client.Raw()
	rdb.HSet(ctx, roundStateKey, "phase", "SETTLED")

	keys := []string{
		roundStateKey,
		"cashparty:round:grabbers:" + roundID,
		"cashparty:room:players:room1",
		"cashparty:room:hash:room1",
		"cashparty:round:available_packets:" + roundID,
		"",
	}
	args := []interface{}{roundID, int64(1000000), rediskeys.KeyPacketInfoPrefix}

	res, err := SettleRound.Run(ctx, client, keys, args...).Slice()
	if err != nil {
		t.Fatalf("SettleRound.Run failed: %v", err)
	}

	if code := parseTestLuaCode(res[0]); code != domain.LuaErrIdempotent {
		t.Errorf("expected code=%d (Idempotent), got %d", domain.LuaErrIdempotent, code)
	}
}
