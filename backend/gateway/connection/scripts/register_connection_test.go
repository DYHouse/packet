package scripts

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/redis/go-redis/v9"
)

// newMiniRedisClient 启动一个 miniredis 实例并包装为 cRedis.Client。
// 测试结束自动清理连接与 miniredis 实例。
func newMiniRedisClient(t *testing.T) cRedis.RedisClient {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run failed: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return cRedis.NewClientFromRaw(rdb)
}

// parseRegisterResult 解析 RegisterConnectionScript 的返回数组。
// 期望返回 {code, oldConnID, oldNodeID}。
func parseRegisterResult(t *testing.T, res interface{}) (int64, string, string) {
	t.Helper()
	arr, ok := res.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T: %v", res, res)
	}
	if len(arr) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(arr), arr)
	}
	code, ok := arr[0].(int64)
	if !ok {
		t.Fatalf("expected code int64, got %T: %v", arr[0], arr[0])
	}
	oldConnID, _ := arr[1].(string)
	oldNodeID, _ := arr[2].(string)
	return code, oldConnID, oldNodeID
}

// TestRegisterConnection_FirstRegister 验证用户无连接记录时首次注册返回 {0,”,”}，
// 且 hash 字段被正确写入。
func TestRegisterConnection_FirstRegister(t *testing.T) {
	client := newMiniRedisClient(t)
	ctx := context.Background()

	userConnKey := "gateway:conn:user:1001"
	keys := []string{userConnKey}
	args := []interface{}{"conn-aaa", "node-1", "web", "device-1", int64(1700000000), int64(86400)}

	res, err := RegisterConnectionScript.Run(ctx, client, keys, args...).Result()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	code, oldConnID, oldNodeID := parseRegisterResult(t, res)
	if code != 0 || oldConnID != "" || oldNodeID != "" {
		t.Fatalf("expected {0,'',''}, got {%d,%q,%q}", code, oldConnID, oldNodeID)
	}

	connID, err := client.HGet(ctx, userConnKey, "conn_id").Result()
	if err != nil || connID != "conn-aaa" {
		t.Fatalf("hash conn_id mismatch: got %q, err=%v", connID, err)
	}
	nodeID, err := client.HGet(ctx, userConnKey, "node_id").Result()
	if err != nil || nodeID != "node-1" {
		t.Fatalf("hash node_id mismatch: got %q, err=%v", nodeID, err)
	}
}

// TestRegisterConnection_SameConnID 验证同一 connID 重复注册视为幂等成功，
// 返回 {0,”,”}，不触发踢旧逻辑。
func TestRegisterConnection_SameConnID(t *testing.T) {
	client := newMiniRedisClient(t)
	ctx := context.Background()

	userConnKey := "gateway:conn:user:1002"
	keys := []string{userConnKey}
	args := []interface{}{"conn-bbb", "node-1", "web", "device-1", int64(1700000000), int64(86400)}

	// 第一次注册
	if _, err := RegisterConnectionScript.Run(ctx, client, keys, args...).Result(); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}

	// 同 connID 再次注册
	res, err := RegisterConnectionScript.Run(ctx, client, keys, args...).Result()
	if err != nil {
		t.Fatalf("second Run failed: %v", err)
	}

	code, oldConnID, oldNodeID := parseRegisterResult(t, res)
	if code != 0 || oldConnID != "" || oldNodeID != "" {
		t.Fatalf("expected {0,'',''} for same connID, got {%d,%q,%q}", code, oldConnID, oldNodeID)
	}
}

// TestRegisterConnection_DifferentConnID 验证不同 connID 注册触发踢旧，
// 返回 {1, oldConnID, oldNodeID}，且 hash 被更新为新连接信息。
func TestRegisterConnection_DifferentConnID(t *testing.T) {
	client := newMiniRedisClient(t)
	ctx := context.Background()

	userConnKey := "gateway:conn:user:1003"
	keys := []string{userConnKey}

	// 第一次注册 conn-old / node-old
	firstArgs := []interface{}{"conn-old", "node-old", "web", "device-1", int64(1700000000), int64(86400)}
	if _, err := RegisterConnectionScript.Run(ctx, client, keys, firstArgs...).Result(); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}

	// 不同 connID 再次注册 conn-new / node-new
	newArgs := []interface{}{"conn-new", "node-new", "web", "device-2", int64(1700000001), int64(86400)}
	res, err := RegisterConnectionScript.Run(ctx, client, keys, newArgs...).Result()
	if err != nil {
		t.Fatalf("second Run failed: %v", err)
	}

	code, oldConnID, oldNodeID := parseRegisterResult(t, res)
	if code != 1 || oldConnID != "conn-old" || oldNodeID != "node-old" {
		t.Fatalf("expected {1,'conn-old','node-old'}, got {%d,%q,%q}", code, oldConnID, oldNodeID)
	}

	// hash 应被更新为新连接信息
	connID, err := client.HGet(ctx, userConnKey, "conn_id").Result()
	if err != nil || connID != "conn-new" {
		t.Fatalf("hash conn_id should be updated to conn-new, got %q, err=%v", connID, err)
	}
	nodeID, err := client.HGet(ctx, userConnKey, "node_id").Result()
	if err != nil || nodeID != "node-new" {
		t.Fatalf("hash node_id should be updated to node-new, got %q, err=%v", nodeID, err)
	}
}
