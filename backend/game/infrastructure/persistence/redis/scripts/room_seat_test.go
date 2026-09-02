package scripts

import (
	"context"
	"fmt"
	"testing"
	"time"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	domain "github.com/cashparty/backend/game/domain"
)

// 本文件复用 packet_test.go 中的 setupMiniRedis / must / resultArr / codeOf
// 以及 round_test.go 中的 resultCode 辅助函数。
// setupMiniRedis 返回 3 个值：(*miniredis.Miniredis, cRedis.RedisClient, context.Context)

// createRoom 在 Redis 中创建房间哈希数据，作为各脚本测试的前置条件。
func createRoom(t *testing.T, c cRedis.RedisClient, ctx context.Context, roomID string, status, maxPlayers, maxSpectators int) {
	t.Helper()
	must(t, c.HSet(ctx, rediskeys.RoomHashKey(roomID),
		"room_no", "R"+roomID,
		"config_id", 100,
		"max_players", maxPlayers,
		"max_spectators", maxSpectators,
		"status", status,
	).Err())
}

// addSpectator 把用户加入房间观众集合，seatNo 指定其当前选座。
func addSpectator(t *testing.T, c cRedis.RedisClient, ctx context.Context, roomID, userID string, seatNo int) {
	t.Helper()
	data := fmt.Sprintf(`{"user_id":%q,"nickname":"alice","avatar":"a.png","seat_no":%d,"is_robot":false}`, userID, seatNo)
	must(t, c.HSet(ctx, rediskeys.RoomSpectatorsKey(roomID), userID, data).Err())
}

// ============================================================================
// luaJoinAsSpectator
// KEYS: [roomHashKey, spectatorsKey, playersKey, userRoomKey]
// ARGV: [userID, spectatorData, now, roomIDStr, roomDataTTL, userRoomTTL]
// ============================================================================

func TestJoinAsSpectator(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "1001"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)

		keys := []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
		spectatorData := `{"user_id":"user1","nickname":"alice","seat_no":0}`
		args := []interface{}{userID, spectatorData, time.Now().Unix(), roomID, 3600, 3600}

		arr := resultArr(t, JoinAsSpectator.Run(ctx, c, keys, args...))
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d, got %d", domain.LuaErrSuccess, code)
		}
	})

	t.Run("RoomNotFound", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "nonexistent"
		userID := "user1"

		keys := []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
		args := []interface{}{userID, "{}", time.Now().Unix(), roomID, 3600, 3600}

		arr := resultArr(t, JoinAsSpectator.Run(ctx, c, keys, args...))
		if code := codeOf(t, arr); code != domain.LuaErrRoomNotFound {
			t.Errorf("expected code %d (RoomNotFound), got %d", domain.LuaErrRoomNotFound, code)
		}
	})

	t.Run("AlreadyPlayer", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "1002"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)

		// 将用户预先加入 players 集合
		playersKey := rediskeys.RoomPlayersKey(roomID)
		must(t, c.HSet(ctx, playersKey, userID, `{"user_id":"user1","seat_no":1}`).Err())

		keys := []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			playersKey,
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
		args := []interface{}{userID, "{}", time.Now().Unix(), roomID, 3600, 3600}

		arr := resultArr(t, JoinAsSpectator.Run(ctx, c, keys, args...))
		// 脚本对“已是玩家”返回 LuaErrAlreadyInRoom(4)
		if code := codeOf(t, arr); code != domain.LuaErrAlreadyInRoom {
			t.Errorf("expected code %d (AlreadyInRoom), got %d", domain.LuaErrAlreadyInRoom, code)
		}
	})

	t.Run("RoomFull", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "1003"
		userID := "user1"
		// max_players=0, max_spectators=0 -> maxTotal=0, totalInRoom(0)>=0 触发房间已满
		createRoom(t, c, ctx, roomID, 1, 0, 0)

		keys := []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
		args := []interface{}{userID, "{}", time.Now().Unix(), roomID, 3600, 3600}

		arr := resultArr(t, JoinAsSpectator.Run(ctx, c, keys, args...))
		if code := codeOf(t, arr); code != domain.LuaErrRoomFull {
			t.Errorf("expected code %d (RoomFull), got %d", domain.LuaErrRoomFull, code)
		}
	})

	// 观众满员但总容量未满时应放行:想上座的玩家不因观众席占满而被拒绝
	t.Run("SpectatorFullButTotalNotFull", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "1004"
		userID := "user11"
		// max_players=5, max_spectators=10, 已有 10 个观众 -> 总数 10 < 15, 应放行
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		for i := 1; i <= 10; i++ {
			addSpectator(t, c, ctx, roomID, fmt.Sprintf("user%d", i), 0)
		}

		keys := []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
		args := []interface{}{userID, "{}", time.Now().Unix(), roomID, 3600, 3600}

		arr := resultArr(t, JoinAsSpectator.Run(ctx, c, keys, args...))
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d (success), got %d", domain.LuaErrSuccess, code)
		}
	})

	// 总容量满员时仍拒绝:观众数不受单独限制,但 maxTotal 封顶
	t.Run("TotalFull", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "1005"
		userID := "user16"
		// max_players=5, max_spectators=10, 已有 15 个观众 -> 总数 15 >= 15, 拒绝
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		for i := 1; i <= 15; i++ {
			addSpectator(t, c, ctx, roomID, fmt.Sprintf("user%d", i), 0)
		}

		keys := []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
		args := []interface{}{userID, "{}", time.Now().Unix(), roomID, 3600, 3600}

		arr := resultArr(t, JoinAsSpectator.Run(ctx, c, keys, args...))
		if code := codeOf(t, arr); code != domain.LuaErrRoomFull {
			t.Errorf("expected code %d (RoomFull), got %d", domain.LuaErrRoomFull, code)
		}
	})
}

// ============================================================================
// luaSelectSeat
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [userID, seatNo, now, isRobot, roomDataTTL]
// ============================================================================

func TestSelectSeat(t *testing.T) {
	keys := func(roomID string) []string {
		return []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomSeatsKey(roomID),
			rediskeys.RoomSeatOwnerKey(roomID),
		}
	}

	t.Run("Success", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "2001"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		addSpectator(t, c, ctx, roomID, userID, 0)

		args := []interface{}{userID, 1, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, SelectSeat.Run(ctx, c, keys(roomID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d, got %d", domain.LuaErrSuccess, code)
		}
	})

	t.Run("RoomNotFound", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		userID := "user1"
		args := []interface{}{userID, 1, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, SelectSeat.Run(ctx, c, keys("nonexistent"), args...))
		if code := codeOf(t, arr); code != domain.LuaErrRoomNotFound {
			t.Errorf("expected code %d (RoomNotFound), got %d", domain.LuaErrRoomNotFound, code)
		}
	})

	t.Run("SeatOccupied", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "2002"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		addSpectator(t, c, ctx, roomID, userID, 0)

		// 预占座位 1
		seatsKey := rediskeys.RoomSeatsKey(roomID)
		must(t, c.Raw().SetBit(ctx, seatsKey, 1, 1).Err())

		args := []interface{}{userID, 1, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, SelectSeat.Run(ctx, c, keys(roomID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrSeatOccupied {
			t.Errorf("expected code %d (SeatOccupied), got %d", domain.LuaErrSeatOccupied, code)
		}
	})

	t.Run("NotInRoom", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "2003"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		// 不添加观众

		args := []interface{}{userID, 1, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, SelectSeat.Run(ctx, c, keys(roomID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrNotInRoom {
			t.Errorf("expected code %d (NotInRoom), got %d", domain.LuaErrNotInRoom, code)
		}
	})
}

// ============================================================================
// luaCancelSeat
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [userID]
// ============================================================================

func TestCancelSeat(t *testing.T) {
	keys := func(roomID string) []string {
		return []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomSeatsKey(roomID),
			rediskeys.RoomSeatOwnerKey(roomID),
		}
	}

	t.Run("Success", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "3001"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		addSpectator(t, c, ctx, roomID, userID, 1)

		// 标记座位 1 已占
		seatsKey := rediskeys.RoomSeatsKey(roomID)
		seatOwnerKey := rediskeys.RoomSeatOwnerKey(roomID)
		must(t, c.Raw().SetBit(ctx, seatsKey, 1, 1).Err())
		must(t, c.HSet(ctx, seatOwnerKey, "1", userID).Err())

		arr := resultArr(t, CancelSeat.Run(ctx, c, keys(roomID), userID))
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d, got %d", domain.LuaErrSuccess, code)
		}
	})

	t.Run("RoomNotFound", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		arr := resultArr(t, CancelSeat.Run(ctx, c, keys("nonexistent"), "user1"))
		if code := codeOf(t, arr); code != domain.LuaErrRoomNotFound {
			t.Errorf("expected code %d (RoomNotFound), got %d", domain.LuaErrRoomNotFound, code)
		}
	})
}

// ============================================================================
// luaLeaveRoom
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey, userRoomKey]
// ARGV: [userID, now]
// ============================================================================

func TestLeaveRoom(t *testing.T) {
	keys := func(roomID, userID string) []string {
		return []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomSeatsKey(roomID),
			rediskeys.RoomSeatOwnerKey(roomID),
			rediskeys.PlayerRoomInRoomKey(roomID, userID),
		}
	}

	t.Run("Success", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "4001"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		addSpectator(t, c, ctx, roomID, userID, 0)

		args := []interface{}{userID, time.Now().Unix()}
		arr := resultArr(t, LeaveRoom.Run(ctx, c, keys(roomID, userID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d, got %d", domain.LuaErrSuccess, code)
		}
	})

	t.Run("RoomNotFound", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		userID := "user1"
		args := []interface{}{userID, time.Now().Unix()}
		arr := resultArr(t, LeaveRoom.Run(ctx, c, keys("nonexistent", userID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrRoomNotFound {
			t.Errorf("expected code %d (RoomNotFound), got %d", domain.LuaErrRoomNotFound, code)
		}
	})
}

// ============================================================================
// luaPlayerReady
// KEYS: [roomHashKey, playersKey, spectatorsKey]
// ARGV: [userID, now]
// 注: 成功返回 code=0(LuaErrSuccess)
// ============================================================================

func TestPlayerReady(t *testing.T) {
	keys := func(roomID string) []string {
		return []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
		}
	}

	t.Run("Success", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "5001"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		addSpectator(t, c, ctx, roomID, userID, 1) // 观众已选座

		args := []interface{}{userID, time.Now().Unix()}
		arr := resultArr(t, PlayerReady.Run(ctx, c, keys(roomID), args...))
		// 成功返回 code=0(LuaErrSuccess)
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d (success), got %d", domain.LuaErrSuccess, code)
		}
	})

	t.Run("NotInRoom", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "5002"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		// 不添加观众

		args := []interface{}{userID, time.Now().Unix()}
		arr := resultArr(t, PlayerReady.Run(ctx, c, keys(roomID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrNotInRoom {
			t.Errorf("expected code %d (NotInRoom), got %d", domain.LuaErrNotInRoom, code)
		}
	})
}

// ============================================================================
// luaAutoSeatAndReady
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [userID, now, isRobot, roomDataTTL]
// ============================================================================

func TestAutoSeatAndReady(t *testing.T) {
	keys := func(roomID string) []string {
		return []string{
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.RoomSpectatorsKey(roomID),
			rediskeys.RoomSeatsKey(roomID),
			rediskeys.RoomSeatOwnerKey(roomID),
		}
	}

	t.Run("Success", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "6001"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		addSpectator(t, c, ctx, roomID, userID, 0)

		args := []interface{}{userID, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, AutoSeatAndReady.Run(ctx, c, keys(roomID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrSuccess {
			t.Errorf("expected code %d, got %d", domain.LuaErrSuccess, code)
		}
	})

	t.Run("RoomNotFound", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		userID := "user1"
		args := []interface{}{userID, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, AutoSeatAndReady.Run(ctx, c, keys("nonexistent"), args...))
		if code := codeOf(t, arr); code != domain.LuaErrRoomNotFound {
			t.Errorf("expected code %d (RoomNotFound), got %d", domain.LuaErrRoomNotFound, code)
		}
	})

	t.Run("NotInRoom", func(t *testing.T) {
		_, c, ctx := setupMiniRedis(t)
		roomID := "6002"
		userID := "user1"
		createRoom(t, c, ctx, roomID, 1, 5, 10)
		// 不添加观众

		args := []interface{}{userID, time.Now().Unix(), "0", 3600}
		arr := resultArr(t, AutoSeatAndReady.Run(ctx, c, keys(roomID), args...))
		if code := codeOf(t, arr); code != domain.LuaErrNotInRoom {
			t.Errorf("expected code %d (NotInRoom), got %d", domain.LuaErrNotInRoom, code)
		}
	})
}
