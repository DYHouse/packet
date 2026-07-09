package redis

import (
	"context"
	"errors"
	"fmt"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/infrastructure/persistence/redis/scripts"
	goredis "github.com/redis/go-redis/v9"
)

// VirtualBalanceRepository 虚拟余额仓储实现
// 从 game/infrastructure/persistence/redis/virtual_balance.go 迁移而来，
// 收敛所有虚拟余额 Redis 操作到 settlement 层。
//
// 行为约束：
//   - Redis Key 引用 common/rediskeys 常量
//   - dirty 标志读写次序与原 game 层实现一致
//   - Deduct 通过 luaDeductBalance 脚本原子执行 INCRBY + SADD（消除竞态）
//   - Credit 通过 luaCreditBalance 脚本原子执行 INCRBY + SADD（消除竞态）
type VirtualBalanceRepository struct {
	redis cRedis.RedisClient
	repo  domain.RobotAccountStore
}

// NewVirtualBalanceRepository 创建虚拟余额仓储实例
func NewVirtualBalanceRepository(redis cRedis.RedisClient, repo domain.RobotAccountStore) *VirtualBalanceRepository {
	return &VirtualBalanceRepository{
		redis: redis,
		repo:  repo,
	}
}

// Deduct 虚拟扣款（Lua 原子操作）
// 通过 luaDeductBalance 脚本原子执行 "扣减 → 余额检查 → 回滚 → 标记 dirty"，
// 消除原 Go 代码三步之间的竞态。
func (s *VirtualBalanceRepository) Deduct(ctx context.Context, userID int64, amount int64) error {
	key := rediskeys.RobotVirtualBalanceKey(userID)
	dirtyKey := rediskeys.RobotVirtualBalanceDirtyKey()
	result, err := scripts.DeductBalance.Run(ctx, s.redis, []string{key, dirtyKey}, amount, converter.FormatID(userID)).Int64()
	if err != nil {
		return fmt.Errorf("deduct virtual balance failed: %w", err)
	}
	if result == 0 {
		return fmt.Errorf("virtual balance is not enough: user_id=%d, amount=%d", userID, amount)
	}
	return nil
}

// Credit 虚拟入账（Lua 原子操作）
// 通过 luaCreditBalance 脚本原子执行 "INCRBY + SADD"，消除原 Go 代码两步之间的竞态
// （INCRBY 成功但 SADD 失败时 dirty 标志丢失，导致 SyncToDB 漏同步该用户余额）。
func (s *VirtualBalanceRepository) Credit(ctx context.Context, userID int64, amount int64) error {
	key := rediskeys.RobotVirtualBalanceKey(userID)
	dirtyKey := rediskeys.RobotVirtualBalanceDirtyKey()
	if _, err := scripts.CreditBalance.Run(ctx, s.redis, []string{key, dirtyKey}, amount, converter.FormatID(userID)).Result(); err != nil {
		return fmt.Errorf("credit virtual balance failed: %w", err)
	}
	return nil
}

// GetBalance 查询虚拟余额
func (s *VirtualBalanceRepository) GetBalance(ctx context.Context, userID int64) (int64, error) {
	key := rediskeys.RobotVirtualBalanceKey(userID)
	balance, err := s.redis.Get(ctx, key).Int64()
	if err == nil {
		return balance, nil
	}
	if !errors.Is(err, goredis.Nil) {
		return 0, err
	}
	// 缓存未命中，从DB加载
	balance, err = s.repo.GetVirtualBalance(ctx, userID)
	if err != nil {
		return 0, err
	}
	if err := s.redis.Set(ctx, key, balance, 0).Err(); err != nil {
		return 0, err
	}
	return balance, nil
}

// SyncToDB 批量同步脏数据到DB（由定时任务调用）
//
// 使用 SPOP 逐个原子弹出成员，避免原 SMembers+Del 两步操作的竞态：
//  1. Del 会误删循环期间其他 goroutine 通过 Credit 新 SAdd 的成员
//  2. Del 会丢失循环中 DB 更新失败被 continue 跳过的成员
//
// SPOP 保证每个成员只被消费一次；处理失败时重新 SAdd 回 dirtyKey 等下次重试。
func (s *VirtualBalanceRepository) SyncToDB(ctx context.Context) error {
	dirtyKey := rediskeys.RobotVirtualBalanceDirtyKey()

	for {
		// SPOP 原子弹出成员：弹出并删除一步完成，无竞态窗口
		member, err := s.redis.SPop(ctx, dirtyKey).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				return nil // 集合为空，同步完成
			}
			return err
		}

		userID, err := converter.ParseIDStrict(member)
		if err != nil {
			// 无效成员（数据格式错误），丢弃不重试
			logger.Error("parse dirty userID failed, discard", "member", member, "error", err)
			continue
		}

		balance, err := s.redis.Get(ctx, rediskeys.RobotVirtualBalanceKey(userID)).Int64()
		if err != nil {
			// 读取余额失败，重新加回 dirtyKey 等下次重试
			if err := s.redis.SAdd(ctx, dirtyKey, member).Err(); err != nil {
				logger.Error("sadd dirty key failed",
					"key", dirtyKey, "member", member, "error", err)
			}
			logger.Error("get virtual balance failed, re-add to dirty", "user_id", userID, "error", err)
			continue
		}

		if err := s.repo.UpdateBalance(ctx, userID, balance); err != nil {
			// DB 更新失败，重新加回 dirtyKey 等下次重试
			if err := s.redis.SAdd(ctx, dirtyKey, member).Err(); err != nil {
				logger.Error("sadd dirty key failed",
					"key", dirtyKey, "member", member, "error", err)
			}
			logger.Error("update balance to DB failed, re-add to dirty", "user_id", userID, "error", err)
			continue
		}
	}
}

// AddToRobotSet 添加到机器人ID集合（供RobotChecker使用）
func (s *VirtualBalanceRepository) AddToRobotSet(ctx context.Context, userID int64) error {
	return s.redis.SAdd(ctx, rediskeys.RobotUserIDsKey(), converter.FormatID(userID)).Err()
}

// IsRobot 判断是否为机器人
func (s *VirtualBalanceRepository) IsRobot(ctx context.Context, userID int64) (bool, error) {
	return s.redis.SIsMember(ctx, rediskeys.RobotUserIDsKey(), converter.FormatID(userID)).Result()
}

// SetBalance 设置虚拟余额（初始化时使用）
func (s *VirtualBalanceRepository) SetBalance(ctx context.Context, userID int64, balance int64) error {
	return s.redis.Set(ctx, rediskeys.RobotVirtualBalanceKey(userID), balance, 0).Err()
}
