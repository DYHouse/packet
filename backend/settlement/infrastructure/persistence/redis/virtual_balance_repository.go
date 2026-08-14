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
//   - Deduct 通过 luaDeductBalance 脚本原子执行 INCRBY + SET dirty（消除竞态）
//   - Credit 通过 luaCreditBalance 脚本原子执行 INCRBY + SET dirty（消除竞态）
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
	// per-user 脏标记（{userID} hash tag），与余额 key 同 slot，Cluster 兼容。
	// Lua 脚本内部用 SET KEYS[2] '1' 写入该 per-user STRING 标记。
	dirtyKey := rediskeys.RobotDirtyKey(userID)
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
// 通过 luaCreditBalance 脚本原子执行 "INCRBY + SET dirty"，消除原 Go 代码两步之间的竞态
// （INCRBY 成功但 dirty 标记丢失会导致 SyncToDB 漏同步该用户余额）。
func (s *VirtualBalanceRepository) Credit(ctx context.Context, userID int64, amount int64) error {
	key := rediskeys.RobotVirtualBalanceKey(userID)
	// per-user 脏标记（{userID} hash tag），与余额 key 同 slot，Cluster 兼容。
	// Lua 脚本内部用 SET KEYS[2] '1' 写入该 per-user STRING 标记。
	dirtyKey := rediskeys.RobotDirtyKey(userID)
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
// 改造为 per-user dirty 标记（Cluster 兼容）：
//   - 遍历机器人 userID 集合 RobotUserIDsKey()（全局 SET，单 key 操作）
//   - 对每个 userID 检查 RobotDirtyKey(userID) 是否为 '1'
//   - 脏数据同步到 DB 成功后 DEL dirty 标记；处理失败时保留标记等下次重试
func (s *VirtualBalanceRepository) SyncToDB(ctx context.Context) error {
	// 全局机器人 userID 集合（单 key，不随用户分片，无跨 slot 问题）
	userIDs, err := s.redis.SMembers(ctx, rediskeys.RobotUserIDsKey()).Result()
	if err != nil {
		return err
	}

	for _, member := range userIDs {
		userID, err := converter.ParseIDStrict(member)
		if err != nil {
			// 无效成员（数据格式错误），跳过不处理
			logger.Error("parse robot userID failed, skip", "member", member, "error", err)
			continue
		}

		// per-user 脏标记（{userID} hash tag），与余额 key 同 slot
		dirtyKey := rediskeys.RobotDirtyKey(userID)
		val, err := s.redis.Get(ctx, dirtyKey).Result()
		if err != nil {
			// dirty 标记不存在或读取失败，该用户无脏数据，跳过
			continue
		}
		if val != "1" {
			continue
		}

		balance, err := s.redis.Get(ctx, rediskeys.RobotVirtualBalanceKey(userID)).Int64()
		if err != nil {
			// 读取余额失败，保留 dirty 标记等下次重试
			logger.Error("get virtual balance failed, keep dirty", "user_id", userID, "error", err)
			continue
		}

		if err := s.repo.UpdateBalance(ctx, userID, balance); err != nil {
			// DB 更新失败，保留 dirty 标记等下次重试
			logger.Error("update balance to DB failed, keep dirty", "user_id", userID, "error", err)
			continue
		}

		// 同步成功，清除 dirty 标记
		if err := s.redis.Del(ctx, dirtyKey).Err(); err != nil {
			logger.Error("del dirty key failed", "key", dirtyKey, "user_id", userID, "error", err)
		}
	}
	return nil
}

// AddToRobotSet 添加到机器人ID集合（供RobotChecker使用）
func (s *VirtualBalanceRepository) AddToRobotSet(ctx context.Context, userID int64) error {
	return s.redis.SAdd(ctx, rediskeys.RobotUserIDsKey(), converter.FormatID(userID)).Err()
}

// SetBalance 设置虚拟余额（初始化时使用）
func (s *VirtualBalanceRepository) SetBalance(ctx context.Context, userID int64, balance int64) error {
	return s.redis.Set(ctx, rediskeys.RobotVirtualBalanceKey(userID), balance, 0).Err()
}
