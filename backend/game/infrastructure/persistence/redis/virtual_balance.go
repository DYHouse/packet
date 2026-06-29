package redis

import (
	"context"
	"errors"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	goredis "github.com/redis/go-redis/v9"
)

// VirtualBalanceService 虚拟余额服务
type VirtualBalanceService struct {
	redis *cRedis.Client
	repo  *mysql.RobotAccountRepository
}

// NewVirtualBalanceService 创建虚拟余额服务实例
func NewVirtualBalanceService(redis *cRedis.Client, repo *mysql.RobotAccountRepository) *VirtualBalanceService {
	return &VirtualBalanceService{
		redis: redis,
		repo:  repo,
	}
}

// Credit 虚拟入账（原子操作）
func (s *VirtualBalanceService) Credit(ctx context.Context, userID int64, amount int64) error {
	key := RobotVirtualBalanceKey(userID)
	if _, err := s.redis.IncrBy(ctx, key, amount).Result(); err != nil {
		return err
	}
	if err := s.redis.SAdd(ctx, RobotVirtualBalanceDirtyKey(), converter.FormatID(userID)).Err(); err != nil {
		return err
	}
	return nil
}

// GetBalance 查询虚拟余额
func (s *VirtualBalanceService) GetBalance(ctx context.Context, userID int64) (int64, error) {
	key := RobotVirtualBalanceKey(userID)
	balance, err := s.redis.Get(ctx, key).Int64()
	if err == nil {
		return balance, nil
	}
	if !errors.Is(err, goredis.Nil) {
		return 0, err
	}
	// 缓存未命中，从DB加载
	account, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	if err := s.redis.Set(ctx, key, account.VirtualBalance, 0).Err(); err != nil {
		return 0, err
	}
	return account.VirtualBalance, nil
}

// SyncToDB 批量同步脏数据到DB（由定时任务调用）
func (s *VirtualBalanceService) SyncToDB(ctx context.Context) error {
	dirtyKey := RobotVirtualBalanceDirtyKey()
	members, err := s.redis.SMembers(ctx, dirtyKey).Result()
	if err != nil {
		return err
	}
	for _, member := range members {
		userID, err := converter.ParseIDStrict(member)
		if err != nil {
			logger.Error("parse dirty userID failed", "member", member, "error", err)
			continue
		}
		balance, err := s.redis.Get(ctx, RobotVirtualBalanceKey(userID)).Int64()
		if err != nil {
			logger.Error("get virtual balance failed", "user_id", userID, "error", err)
			continue
		}
		if err := s.repo.UpdateBalance(ctx, userID, balance); err != nil {
			logger.Error("update balance to DB failed", "user_id", userID, "error", err)
			continue
		}
	}
	// 清空脏数据集合
	if err := s.redis.Del(ctx, dirtyKey).Err(); err != nil {
		return err
	}
	return nil
}

// AddToRobotSet 添加到机器人ID集合（供RobotChecker使用）
func (s *VirtualBalanceService) AddToRobotSet(ctx context.Context, userID int64) error {
	return s.redis.SAdd(ctx, RobotUserIDsKey(), converter.FormatID(userID)).Err()
}

// IsRobot 判断是否为机器人
func (s *VirtualBalanceService) IsRobot(ctx context.Context, userID int64) (bool, error) {
	return s.redis.SIsMember(ctx, RobotUserIDsKey(), converter.FormatID(userID)).Result()
}

// SetBalance 设置虚拟余额（初始化时使用）
func (s *VirtualBalanceService) SetBalance(ctx context.Context, userID int64, balance int64) error {
	return s.redis.Set(ctx, RobotVirtualBalanceKey(userID), balance, 0).Err()
}
