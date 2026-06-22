package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	goredis "github.com/redis/go-redis/v9"
)

// VirtualBalanceService 结算层虚拟余额服务
// 与 game 层的 VirtualBalanceService 共享相同的 Redis key，但独立实现以避免循环依赖
type VirtualBalanceService struct {
	redis *cRedis.Client
}

// NewVirtualBalanceService 创建结算层 VirtualBalanceService 实例
func NewVirtualBalanceService(redis *cRedis.Client) *VirtualBalanceService {
	return &VirtualBalanceService{redis: redis}
}

// Deduct 虚拟扣款（原子操作）
func (s *VirtualBalanceService) Deduct(ctx context.Context, userID int64, amount int64) error {
	key := fmt.Sprintf("robot:virtual_balance:%d", userID)
	newBalance, err := s.redis.IncrBy(ctx, key, -amount).Result()
	if err != nil {
		return fmt.Errorf("deduct virtual balance failed: %w", err)
	}
	if newBalance < 0 {
		// 余额为负，回滚并返回错误
		s.redis.IncrBy(ctx, key, amount)
		return fmt.Errorf("virtual balance is negative after deduct: user_id=%d, balance=%d", userID, newBalance)
	}
	// 标记需要同步到DB
	s.redis.SAdd(ctx, "robot:virtual_balance:dirty", fmt.Sprintf("%d", userID))
	return nil
}

// Credit 虚拟入账（原子操作）
func (s *VirtualBalanceService) Credit(ctx context.Context, userID int64, amount int64) error {
	key := fmt.Sprintf("robot:virtual_balance:%d", userID)
	_, err := s.redis.IncrBy(ctx, key, amount).Result()
	if err != nil {
		return fmt.Errorf("credit virtual balance failed: %w", err)
	}
	// 标记需要同步到DB
	s.redis.SAdd(ctx, "robot:virtual_balance:dirty", fmt.Sprintf("%d", userID))
	return nil
}

// GetBalance 查询虚拟余额
func (s *VirtualBalanceService) GetBalance(ctx context.Context, userID int64) (int64, error) {
	key := fmt.Sprintf("robot:virtual_balance:%d", userID)
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
