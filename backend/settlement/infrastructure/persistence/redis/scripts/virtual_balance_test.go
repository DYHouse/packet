package scripts

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/redis/go-redis/v9"
)

const (
	testBalanceKey = "cashparty:robot:virtual_balance:user1"
	testDirtyKey   = "cashparty:robot:virtual_balance:dirty"
	testUserID     = "user1"
)

// newTestClient 启动一个 miniredis 实例并返回包装好的 cRedis.Client。
func newTestClient(t *testing.T) (cRedis.RedisClient, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis start failed: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	return cRedis.NewClientFromRaw(rdb), mr
}

// TestLuaDeductBalance_Sufficient 余额充足：1000 扣减 100，返回 1，余额变为 900，
// 并将 userID 加入 dirty 集合。
func TestLuaDeductBalance_Sufficient(t *testing.T) {
	cRedis.SetUseEvalSHA(false)
	ctx := context.Background()
	client, mr := newTestClient(t)

	mr.Set(testBalanceKey, "1000")

	keys := []string{testBalanceKey, testDirtyKey}
	res, err := DeductBalance.Run(ctx, client, keys, 100, testUserID).Result()
	if err != nil {
		t.Fatalf("DeductBalance run failed: %v", err)
	}
	if got := res.(int64); got != 1 {
		t.Fatalf("expect result 1 (success), got %d", got)
	}

	if balance, _ := mr.Get(testBalanceKey); balance != "900" {
		t.Fatalf("expect balance 900, got %s", balance)
	}

	if ok, _ := mr.IsMember(testDirtyKey, testUserID); !ok {
		t.Fatalf("expect dirty set contains %s", testUserID)
	}
}

// TestLuaDeductBalance_Insufficient 余额不足：50 扣减 100，返回 0，余额回滚保持不变。
func TestLuaDeductBalance_Insufficient(t *testing.T) {
	cRedis.SetUseEvalSHA(false)
	ctx := context.Background()
	client, mr := newTestClient(t)

	mr.Set(testBalanceKey, "50")

	keys := []string{testBalanceKey, testDirtyKey}
	res, err := DeductBalance.Run(ctx, client, keys, 100, testUserID).Result()
	if err != nil {
		t.Fatalf("DeductBalance run failed: %v", err)
	}
	if got := res.(int64); got != 0 {
		t.Fatalf("expect result 0 (insufficient), got %d", got)
	}

	if balance, _ := mr.Get(testBalanceKey); balance != "50" {
		t.Fatalf("expect balance 50 (unchanged after rollback), got %s", balance)
	}
}

// TestLuaDeductBalance_Concurrent 并发安全：余额 1000，10 个 goroutine 各扣减 100，
// 验证总扣减不超过 1000 且余额不为负。
func TestLuaDeductBalance_Concurrent(t *testing.T) {
	cRedis.SetUseEvalSHA(false)
	ctx := context.Background()
	client, mr := newTestClient(t)

	mr.Set(testBalanceKey, "1000")

	keys := []string{testBalanceKey, testDirtyKey}

	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := DeductBalance.Run(ctx, client, keys, 100, testUserID).Result()
			if err != nil {
				return
			}
			if res.(int64) == 1 {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 总扣减不超过 1000
	totalDeducted := successCount * 100
	if totalDeducted > 1000 {
		t.Fatalf("total deduction %d exceeds balance 1000", totalDeducted)
	}

	// 余额应等于 1000 - totalDeducted
	balance, _ := mr.Get(testBalanceKey)
	expect := 1000 - totalDeducted
	if balance != strconv.Itoa(expect) {
		t.Fatalf("expect balance %d, got %s", expect, balance)
	}
}
