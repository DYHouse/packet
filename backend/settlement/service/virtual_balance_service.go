package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/settlement/infrastructure/persistence/redis"
	goredis "github.com/redis/go-redis/v9"
)

// VirtualBalanceService 结算层虚拟余额服务
// 与 game 层的 VirtualBalanceService 共享相同的 Redis key（各自维护 keys.go），
// 独立实现以避免循环依赖（game → settlement 已存在，反向不允许）
type VirtualBalanceService struct {
	redis *cRedis.Client
}

// NewVirtualBalanceService 创建结算层 VirtualBalanceService 实例
func NewVirtualBalanceService(redis *cRedis.Client) *VirtualBalanceService {
	return &VirtualBalanceService{redis: redis}
}

// Deduct 虚拟扣款（Lua 原子操作）
// 通过 luaDeductBalance 脚本原子执行 "扣减 → 余额检查 → 回滚 → 标记 dirty"，
// 消除原 Go 代码三步之间的竞态。
func (s *VirtualBalanceService) Deduct(ctx context.Context, userID int64, amount int64) error {
	key := redis.RobotVirtualBalanceKey(userID)
	dirtyKey := redis.RobotVirtualBalanceDirtyKey()
	result, err := s.redis.Eval(ctx, luaDeductBalance, []string{key, dirtyKey}, amount, fmt.Sprintf("%d", userID)).Int64()
	if err != nil {
		return fmt.Errorf("deduct virtual balance failed: %w", err)
	}
	if result == 0 {
		return fmt.Errorf("virtual balance is not enough: user_id=%d, amount=%d", userID, amount)
	}
	return nil
}

// Credit 虚拟入账（原子操作）
func (s *VirtualBalanceService) Credit(ctx context.Context, userID int64, amount int64) error {
	key := redis.RobotVirtualBalanceKey(userID)
	_, err := s.redis.IncrBy(ctx, key, amount).Result()
	if err != nil {
		return fmt.Errorf("credit virtual balance failed: %w", err)
	}
	// 标记需要同步到DB
	s.redis.SAdd(ctx, redis.RobotVirtualBalanceDirtyKey(), fmt.Sprintf("%d", userID))
	return nil
}

// GetBalance 查询虚拟余额
func (s *VirtualBalanceService) GetBalance(ctx context.Context, userID int64) (int64, error) {
	key := redis.RobotVirtualBalanceKey(userID)
	val, err := s.redis.Get(ctx, key).Result()
	if err == goredis.Nil {
		// 缓存未命中，返回0（机器人余额应该已由 game 层初始化到 Redis）
		logger.Warn("virtual balance not in cache, returning 0", "user_id", userID)
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get virtual balance failed: %w", err)
	}
	var balance int64
	fmt.Sscanf(val, "%d", &balance)
	return balance, nil
}
