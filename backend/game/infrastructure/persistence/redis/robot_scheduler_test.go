package redis

import (
	"context"
	"sort"
	"testing"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/redis/go-redis/v9"
)

// setupMiniRedis 创建 miniredis 实例并包装为 RedisClient。
// 复用 scripts/round_test.go 中同名函数的思路（独立定义避免跨包依赖）。
func setupMiniRedis(t *testing.T) (*miniredis.Miniredis, cRedis.RedisClient, context.Context) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis not available: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cRedis.SetUseEvalSHA(false)
	client := cRedis.NewClientFromRaw(rdb)
	return mr, client, context.Background()
}

// toInt64Set 将 []int64 转为 map[int64]bool 便于无序比对。
func toInt64Set(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// expectedToSortedInt64 将期望集合转为排序后的 []int64，用于断言。
func expectedToSortedInt64(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// sortInt64 对 []int64 升序排序（返回新切片）。
func sortInt64(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestGetRoomPlayerRobots 覆盖三种场景：纯玩家机器人、含观众机器人、Redis 错误。
func TestGetRoomPlayerRobots(t *testing.T) {
	ctx := context.Background()

	t.Run("纯玩家机器人", func(t *testing.T) {
		_, client, _ := setupMiniRedis(t)
		roomID := "room_pure_player_robots"

		// RobotRoomKey = {100001, 100002}
		if err := client.SAdd(ctx, rediskeys.RobotRoomKey(roomID), "100001", "100002").Err(); err != nil {
			t.Fatalf("SAdd robot room failed: %v", err)
		}
		// RoomPlayersKey = {100001, 100002, 200001}
		if err := client.HSet(ctx, rediskeys.RoomPlayersKey(roomID), "100001", "1", "100002", "1", "200001", "1").Err(); err != nil {
			t.Fatalf("HSet room players failed: %v", err)
		}

		repo := NewRobotSchedulerRedis(client)
		got, err := repo.GetRoomPlayerRobots(ctx, roomID)
		if err != nil {
			t.Fatalf("GetRoomPlayerRobots failed: %v", err)
		}

		want := map[int64]bool{100001: true, 100002: true}
		if len(got) != len(want) {
			t.Fatalf("expected %d ids, got %d (%v)", len(want), len(got), got)
		}
		for _, id := range got {
			if !want[id] {
				t.Errorf("unexpected id %d in result", id)
			}
		}
	})

	t.Run("含观众机器人", func(t *testing.T) {
		_, client, _ := setupMiniRedis(t)
		roomID := "room_with_spectator_robots"

		// RobotRoomKey = {100001, 100002, 100003}（100002/100003 为观众机器人）
		if err := client.SAdd(ctx, rediskeys.RobotRoomKey(roomID), "100001", "100002", "100003").Err(); err != nil {
			t.Fatalf("SAdd robot room failed: %v", err)
		}
		// RoomPlayersKey = {100001, 200001, 200002}
		if err := client.HSet(ctx, rediskeys.RoomPlayersKey(roomID), "100001", "1", "200001", "1", "200002", "1").Err(); err != nil {
			t.Fatalf("HSet room players failed: %v", err)
		}

		repo := NewRobotSchedulerRedis(client)
		got, err := repo.GetRoomPlayerRobots(ctx, roomID)
		if err != nil {
			t.Fatalf("GetRoomPlayerRobots failed: %v", err)
		}

		want := map[int64]bool{100001: true}
		gotSet := toInt64Set(got)
		if len(gotSet) != len(want) {
			t.Fatalf("expected %d ids, got %d (%v)", len(want), len(gotSet), got)
		}
		for id := range want {
			if !gotSet[id] {
				t.Errorf("expected id %d missing in result %v", id, got)
			}
		}
		// 明确断言观众机器人不应出现
		for _, id := range got {
			if id == 100002 || id == 100003 {
				t.Errorf("spectator robot %d should not be in intersection result", id)
			}
		}
	})

	t.Run("Redis错误场景", func(t *testing.T) {
		mr, client, _ := setupMiniRedis(t)
		roomID := "room_redis_error"

		// 预置数据后关闭 miniredis，触发后续命令返回错误
		if err := client.SAdd(ctx, rediskeys.RobotRoomKey(roomID), "100001").Err(); err != nil {
			t.Fatalf("SAdd robot room failed: %v", err)
		}
		if err := client.HSet(ctx, rediskeys.RoomPlayersKey(roomID), "100001", "1").Err(); err != nil {
			t.Fatalf("HSet room players failed: %v", err)
		}
		mr.Close()

		repo := NewRobotSchedulerRedis(client)
		got, err := repo.GetRoomPlayerRobots(ctx, roomID)
		if err == nil {
			t.Fatalf("expected non-nil error after miniredis close, got nil (result=%v)", got)
		}
		if got != nil {
			t.Errorf("expected nil result on error, got %v", got)
		}
	})
}

// TestGetRoomPlayerRobotsSortedSnapshot 用排序后 DeepEqual 等价校验纯玩家机器人场景，
// 确保返回顺序不影响断言结论。
func TestGetRoomPlayerRobotsSortedSnapshot(t *testing.T) {
	_, client, ctx := setupMiniRedis(t)
	roomID := "room_sorted_snapshot"

	if err := client.SAdd(ctx, rediskeys.RobotRoomKey(roomID), "100001", "100002").Err(); err != nil {
		t.Fatalf("SAdd robot room failed: %v", err)
	}
	if err := client.HSet(ctx, rediskeys.RoomPlayersKey(roomID), "100001", "1", "100002", "1", "200001", "1").Err(); err != nil {
		t.Fatalf("HSet room players failed: %v", err)
	}

	repo := NewRobotSchedulerRedis(client)
	got, err := repo.GetRoomPlayerRobots(ctx, roomID)
	if err != nil {
		t.Fatalf("GetRoomPlayerRobots failed: %v", err)
	}

	want := expectedToSortedInt64(map[int64]bool{100001: true, 100002: true})
	gotSorted := sortInt64(got)
	if len(gotSorted) != len(want) {
		t.Fatalf("expected %v, got %v", want, gotSorted)
	}
	for i := range want {
		if want[i] != gotSorted[i] {
			t.Fatalf("expected %v, got %v", want, gotSorted)
		}
	}
}
