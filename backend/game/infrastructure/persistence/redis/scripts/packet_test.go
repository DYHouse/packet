package scripts

import (
	"context"
	"strings"
	"testing"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain"
)

// 本文件复用 round_test.go 中的 setupMiniRedis / must / resultArr / codeOf / parseTestLuaCode / resultCode 共享辅助函数。
// setupMiniRedis 返回 3 个值：(*miniredis.Miniredis, cRedis.RedisClient, context.Context)，并在内部调用 cRedis.SetUseEvalSHA(false)。

// 测试用常量
const (
	testRoomID        = "room-1"
	testRoundID       = "round-1"
	testUserID        = "user-1"
	testRobotUserID   = "robot-1"
	testPacketID      = "1001"
	testPacketDataTTL = 300
	testGrabTimeout   = 30
	testNow           = int64(1000000)
)

// =============================================================================
// luaGrabPacket 测试
// KEYS: [availablePacketsKey, userGrabKey, grabbersKey, roundStateKey, playersKey, packetInfoKey, packetAvailableKey]
// ARGV: [userID, now, grabTimeout, roomID, packetID, packetDataTTL]
// =============================================================================

func runGrabPacket(t *testing.T, ctx context.Context, c cRedis.RedisClient, userID string) []interface{} {
	t.Helper()
	keys := []string{
		rediskeys.RoundAvailablePacketsKey(testRoomID, testRoundID),
		rediskeys.RoundGrabbedKey(testRoomID, testRoundID, userID),
		rediskeys.RoundGrabbersKey(testRoomID, testRoundID),
		rediskeys.RoundStateKey(testRoomID, testRoundID),
		rediskeys.RoomPlayersKey(testRoomID),
		rediskeys.PacketInfoKey(testRoomID, testPacketID),
		rediskeys.PacketAvailableKey(testRoomID, testPacketID),
	}
	args := []interface{}{
		userID,
		testNow,
		testGrabTimeout,
		testRoomID,
		testPacketID,
		testPacketDataTTL,
	}
	return resultArr(t, GrabPacket.Run(ctx, c, keys, args...))
}

// setupGrabPacketReady 预置 phase=GRABBING + 玩家数据（luaGrabPacket 成功路径的前置）。
func setupGrabPacketReady(t *testing.T, ctx context.Context, c cRedis.RedisClient) {
	t.Helper()
	must(t, c.HSet(ctx, rediskeys.RoomPlayersKey(testRoomID), testUserID, `{"user_id":"user-1"}`).Err())
	must(t, c.HSet(ctx, rediskeys.RoundStateKey(testRoomID, testRoundID),
		"phase", "GRABBING",
		"grab_end_time", testNow+60000,
		"packet_count", 5,
	).Err())
}

func TestGrabPacketSuccess(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	setupGrabPacketReady(t, ctx, c)
	// 预置红包信息 + 可用标记
	must(t, c.Set(ctx, rediskeys.PacketInfoKey(testRoomID, testPacketID), `{"packet_id":1001,"amount":100,"position":1,"is_grabbed":false}`, 0).Err())
	must(t, c.Set(ctx, rediskeys.PacketAvailableKey(testRoomID, testPacketID), "1", 0).Err())

	arr := runGrabPacket(t, ctx, c, testUserID)

	if code := codeOf(t, arr); code != domain.LuaErrSuccess {
		t.Fatalf("expected code %d, got %d (full: %v)", domain.LuaErrSuccess, code, arr)
	}
	if got := asInt64Elem(t, arr, 1); got != 1001 {
		t.Errorf("expected packetID 1001, got %d", got)
	}
	if got := asInt64Elem(t, arr, 2); got != 100 {
		t.Errorf("expected amount 100, got %d", got)
	}
	if got := asInt64Elem(t, arr, 3); got != 1 {
		t.Errorf("expected position 1, got %d", got)
	}

	// 副作用校验：availableKey 应被删除
	if n, _ := c.Exists(ctx, rediskeys.PacketAvailableKey(testRoomID, testPacketID)).Result(); n != 0 {
		t.Errorf("expected packetAvailableKey deleted, exists=%d", n)
	}
	// userGrabKey 应已设置
	if n, _ := c.Exists(ctx, rediskeys.RoundGrabbedKey(testRoomID, testRoundID, testUserID)).Result(); n != 1 {
		t.Errorf("expected userGrabKey set, exists=%d", n)
	}
	// grabbersKey 应包含 userID
	members, _ := c.SMembers(ctx, rediskeys.RoundGrabbersKey(testRoomID, testRoundID)).Result()
	if !sliceContains(members, testUserID) {
		t.Errorf("expected grabbers to contain %s, got %v", testUserID, members)
	}
	// 红包信息应被更新为 is_grabbed=true
	raw, _ := c.Get(ctx, rediskeys.PacketInfoKey(testRoomID, testPacketID)).Result()
	if !strings.Contains(raw, `"is_grabbed":true`) {
		t.Errorf("expected packet is_grabbed=true, got %s", raw)
	}
}

func TestGrabPacketNonPlayer(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	// 不设置 playersKey 中的 userID（设置 round state 以排除 phase 干扰）
	must(t, c.HSet(ctx, rediskeys.RoundStateKey(testRoomID, testRoundID),
		"phase", "GRABBING",
		"grab_end_time", testNow+60000,
		"packet_count", 5,
	).Err())

	arr := runGrabPacket(t, ctx, c, testUserID)

	if code := codeOf(t, arr); code != domain.LuaErrOnlyPlayerCanGrab {
		t.Fatalf("expected code %d (OnlyPlayerCanGrab), got %d (full: %v)",
			domain.LuaErrOnlyPlayerCanGrab, code, arr)
	}
}

func TestGrabPacketAlreadyGrabbed(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	setupGrabPacketReady(t, ctx, c)
	must(t, c.Set(ctx, rediskeys.PacketInfoKey(testRoomID, testPacketID), `{"packet_id":1001,"amount":100,"position":1,"is_grabbed":false}`, 0).Err())
	must(t, c.Set(ctx, rediskeys.PacketAvailableKey(testRoomID, testPacketID), "1", 0).Err())
	// 模拟已抢
	must(t, c.Set(ctx, rediskeys.RoundGrabbedKey(testRoomID, testRoundID, testUserID), "1", 0).Err())

	arr := runGrabPacket(t, ctx, c, testUserID)

	if code := codeOf(t, arr); code != domain.LuaErrAlreadyGrabbed {
		t.Fatalf("expected code %d (AlreadyGrabbed), got %d (full: %v)",
			domain.LuaErrAlreadyGrabbed, code, arr)
	}
}

func TestGrabPacketInfoNotFound(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	setupGrabPacketReady(t, ctx, c)
	must(t, c.Set(ctx, rediskeys.PacketAvailableKey(testRoomID, testPacketID), "1", 0).Err())
	// 故意不设置 packetInfoKey，触发 code=23

	arr := runGrabPacket(t, ctx, c, testUserID)

	if code := codeOf(t, arr); code != domain.LuaErrPacketInfoNotFound {
		t.Fatalf("expected code %d (PacketInfoNotFound), got %d (full: %v)",
			domain.LuaErrPacketInfoNotFound, code, arr)
	}
}

// =============================================================================
// luaSendPacket 测试
// KEYS: [roomHashKey, playersKey, roundStateKey, availablePacketsKey, grabbersKey]
// ARGV: [senderID, senderType, totalAmount, commission, actualAmount, roundNo, now,
//        grabTimeout, packetInfoPrefix, packetAvailablePrefix, globalPacketIDKey,
//        packetAmountsJson, roundID, roomID, scenario, rewardType, rewardAmount,
//        packetDataTTL, roundStateTTL]
// =============================================================================

func runSendPacket(t *testing.T, ctx context.Context, c cRedis.RedisClient, scenario, roundNo int) []interface{} {
	t.Helper()
	keys := []string{
		rediskeys.RoomHashKey(testRoomID),
		rediskeys.RoomPlayersKey(testRoomID),
		rediskeys.RoundStateKey(testRoomID, testRoundID),
		rediskeys.RoundAvailablePacketsKey(testRoomID, testRoundID),
		rediskeys.RoundGrabbersKey(testRoomID, testRoundID),
	}
	args := []interface{}{
		testUserID,                         // senderID
		1,                                  // senderType
		500,                                // totalAmount
		50,                                 // commission
		450,                                // actualAmount
		roundNo,                            // roundNo
		testNow,                            // now
		testGrabTimeout,                    // grabTimeout
		rediskeys.PacketInfoPrefix(testRoomID),      // packetInfoPrefix
		rediskeys.PacketAvailablePrefix(testRoomID), // packetAvailablePrefix
		rediskeys.KeyGlobalPacketID,        // globalPacketIDKey
		`[100,100,100,100,100]`,            // packetAmountsJson
		testRoundID,                        // roundID
		testRoomID,                         // roomID
		scenario,                           // scenario
		0,                                  // rewardType
		0,                                  // rewardAmount
		testPacketDataTTL,                  // packetDataTTL
		600,                                // roundStateTTL
	}
	return resultArr(t, SendPacket.Run(ctx, c, keys, args...))
}

func TestSendPacketSuccess(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	// room status=2 (playing), current_round=0
	must(t, c.HSet(ctx, rediskeys.RoomHashKey(testRoomID), "status", 2, "current_round", 0).Err())
	// 至少一个玩家
	must(t, c.HSet(ctx, rediskeys.RoomPlayersKey(testRoomID), testUserID, `{"user_id":"user-1"}`).Err())

	arr := runSendPacket(t, ctx, c, 1, 1) // scenario=1 (first_round)

	if code := codeOf(t, arr); code != domain.LuaErrSuccess {
		t.Fatalf("expected code 0, got %d (full: %v)", code, arr)
	}
	if got := asStringElem(t, arr, 1); got != testRoundID {
		t.Errorf("expected roundID %s, got %s", testRoundID, got)
	}
	// packetIDs 应为非空 JSON 数组
	packetIDsJSON := asStringElem(t, arr, 2)
	if !strings.HasPrefix(packetIDsJSON, "[") || !strings.HasSuffix(packetIDsJSON, "]") {
		t.Errorf("expected packetIDs to be JSON array, got %s", packetIDsJSON)
	}
	// 验证 5 个红包已写入 availablePacketsKey
	n, _ := c.Raw().LLen(ctx, rediskeys.RoundAvailablePacketsKey(testRoomID, testRoundID)).Result()
	if n != 5 {
		t.Errorf("expected 5 available packets, got %d", n)
	}
	// 验证 round state 已写入 phase=GRABBING
	phase, _ := c.HGet(ctx, rediskeys.RoundStateKey(testRoomID, testRoundID), "phase").Result()
	if phase != "GRABBING" {
		t.Errorf("expected phase GRABBING, got %s", phase)
	}
}

func TestSendPacketNotFirstRound(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	// current_round=1 (非零)，scenario=1 应返回 50
	must(t, c.HSet(ctx, rediskeys.RoomHashKey(testRoomID), "status", 2, "current_round", 1).Err())

	arr := runSendPacket(t, ctx, c, 1, 1)

	if code := codeOf(t, arr); code != domain.LuaErrNotFirstRound {
		t.Fatalf("expected code %d (NotFirstRound), got %d (full: %v)",
			domain.LuaErrNotFirstRound, code, arr)
	}
}

func TestSendPacketPacketsAlreadyExist(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	must(t, c.HSet(ctx, rediskeys.RoomHashKey(testRoomID), "status", 2, "current_round", 0).Err())
	must(t, c.HSet(ctx, rediskeys.RoomPlayersKey(testRoomID), testUserID, `{"user_id":"user-1"}`).Err())
	// 预置可用红包，触发 LLEN > 0
	must(t, c.RPush(ctx, rediskeys.RoundAvailablePacketsKey(testRoomID, testRoundID), testPacketID).Err())

	arr := runSendPacket(t, ctx, c, 1, 1)

	if code := codeOf(t, arr); code != domain.LuaErrPacketsAlreadyExist {
		t.Fatalf("expected code %d (PacketsAlreadyExist), got %d (full: %v)",
			domain.LuaErrPacketsAlreadyExist, code, arr)
	}
}

// =============================================================================
// luaRobotGrabPacket 测试
// KEYS: [availablePacketsKey, userGrabKey, grabbersKey, roundStateKey, playersKey]
// ARGV: [userID, now, grabTimeout, roomID, packetInfoPrefix, packetAvailablePrefix, randOffset, packetDataTTL]
// =============================================================================

func TestRobotGrabPacketSuccess(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	must(t, c.HSet(ctx, rediskeys.RoomPlayersKey(testRoomID), testRobotUserID, `{"user_id":"robot-1"}`).Err())
	must(t, c.HSet(ctx, rediskeys.RoundStateKey(testRoomID, testRoundID),
		"phase", "GRABBING",
		"grab_end_time", testNow+60000,
		"packet_count", 5,
	).Err())
	// 可用红包列表
	availablePacketsKey := rediskeys.RoundAvailablePacketsKey(testRoomID, testRoundID)
	must(t, c.RPush(ctx, availablePacketsKey, testPacketID).Err())

	// luaRobotGrabPacket 在 Lua 内拼接 packetAvailablePrefix..<pid> / packetInfoPrefix..<pid>
	packetInfoKey := rediskeys.PacketInfoKey(testRoomID, testPacketID)
	packetAvailableKey := rediskeys.PacketAvailableKey(testRoomID, testPacketID)
	must(t, c.Set(ctx, packetAvailableKey, "1", 0).Err())
	must(t, c.Set(ctx, packetInfoKey, `{"packet_id":1001,"amount":100,"position":1,"is_grabbed":false}`, 0).Err())

	keys := []string{
		availablePacketsKey,
		rediskeys.RoundGrabbedKey(testRoomID, testRoundID, testRobotUserID),
		rediskeys.RoundGrabbersKey(testRoomID, testRoundID),
		rediskeys.RoundStateKey(testRoomID, testRoundID),
		rediskeys.RoomPlayersKey(testRoomID),
	}
	args := []interface{}{
		testRobotUserID,
		testNow,
		testGrabTimeout,
		testRoomID,
		rediskeys.PacketInfoPrefix(testRoomID),
		rediskeys.PacketAvailablePrefix(testRoomID),
		0, // randOffset
		testPacketDataTTL,
	}

	arr := resultArr(t, RobotGrabPacket.Run(ctx, c, keys, args...))

	if code := codeOf(t, arr); code != domain.LuaErrSuccess {
		t.Fatalf("expected code 0, got %d (full: %v)", code, arr)
	}
	if got := asInt64Elem(t, arr, 1); got != 1001 {
		t.Errorf("expected chosen packetID 1001, got %d", got)
	}
}

// =============================================================================
// luaAutoDistributePackets 测试
// KEYS: [availablePacketsKey, grabbersKey, roundStateKey, playersKey, roomHashKey]
// ARGV: [now, packetInfoPrefix, packetAvailablePrefix, roundGrabbedPrefix, roundID, packetDataTTL]
// =============================================================================

func TestAutoDistributePacketsSuccess(t *testing.T) {
	_, c, ctx := setupMiniRedis(t)

	// 2 个玩家，其中 user-2 已抢
	must(t, c.HSet(ctx, rediskeys.RoomPlayersKey(testRoomID),
		testUserID, `{"user_id":"user-1"}`,
		"user-2", `{"user_id":"user-2"}`,
	).Err())
	must(t, c.SAdd(ctx, rediskeys.RoundGrabbersKey(testRoomID, testRoundID), "user-2").Err())

	// 1 个可用红包
	availablePacketsKey := rediskeys.RoundAvailablePacketsKey(testRoomID, testRoundID)
	must(t, c.RPush(ctx, availablePacketsKey, testPacketID).Err())

	// Lua 内拼接 packetAvailablePrefix..<pid> / packetInfoPrefix..<pid>
	packetInfoKey := rediskeys.PacketInfoKey(testRoomID, testPacketID)
	packetAvailableKey := rediskeys.PacketAvailableKey(testRoomID, testPacketID)
	must(t, c.Set(ctx, packetAvailableKey, "1", 0).Err())
	must(t, c.Set(ctx, packetInfoKey, `{"packet_id":1001,"amount":100,"position":1,"is_grabbed":false}`, 0).Err())

	keys := []string{
		availablePacketsKey,
		rediskeys.RoundGrabbersKey(testRoomID, testRoundID),
		rediskeys.RoundStateKey(testRoomID, testRoundID),
		rediskeys.RoomPlayersKey(testRoomID),
		rediskeys.RoomHashKey(testRoomID),
	}
	args := []interface{}{
		testNow,
		rediskeys.PacketInfoPrefix(testRoomID),
		rediskeys.PacketAvailablePrefix(testRoomID),
		rediskeys.RoundGrabbedPrefix(testRoomID),
		testRoundID,
		testPacketDataTTL,
	}

	arr := resultArr(t, AutoDistributePackets.Run(ctx, c, keys, args...))

	if code := codeOf(t, arr); code != domain.LuaErrSuccess {
		t.Fatalf("expected code 0, got %d (full: %v)", code, arr)
	}
	// distributedCount 应为 1（1 个未抢玩家 + 1 个可用红包）
	if got := asInt64Elem(t, arr, 1); got != 1 {
		t.Errorf("expected distributedCount 1, got %d", got)
	}
	// roundState phase 应被置为 SETTLING
	phase, _ := c.HGet(ctx, rediskeys.RoundStateKey(testRoomID, testRoundID), "phase").Result()
	if phase != "SETTLING" {
		t.Errorf("expected phase SETTLING, got %s", phase)
	}
	// user-1 应被加入 grabbersKey
	members, _ := c.SMembers(ctx, rediskeys.RoundGrabbersKey(testRoomID, testRoundID)).Result()
	if !sliceContains(members, testUserID) {
		t.Errorf("expected grabbers to contain %s, got %v", testUserID, members)
	}
}
