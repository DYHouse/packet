package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
)

// RobotChecker 机器人身份识别接口
type RobotChecker interface {
	IsRobot(ctx context.Context, userID int64) (bool, error)
}

// redisRobotChecker 基于 Redis SET 的机器人身份识别实现
type redisRobotChecker struct {
	redis cRedis.RedisClient
}

// NewRobotChecker 创建 RobotChecker 实例
func NewRobotChecker(redis cRedis.RedisClient) RobotChecker {
	return &redisRobotChecker{redis: redis}
}

// IsRobot 判断 userID 是否为机器人
// 使用 Redis SISMEMBER cashparty:robot:user_ids {userID} 实现 O(1) 查询
// fail-closed：Redis 不可用或查询失败时返回 error，调用方必须中止资金操作，
// 避免真实玩家资金误走虚拟钱包路径或机器人资金误走真实钱包路径。
func (c *redisRobotChecker) IsRobot(ctx context.Context, userID int64) (bool, error) {
	if c.redis == nil {
		logger.Error("check robot failed, redis client is nil", "user_id", userID)
		return false, fmt.Errorf("check robot failed: redis client is nil")
	}
	exists, err := c.redis.SIsMember(ctx, rediskeys.RobotUserIDsKey(), fmt.Sprintf("%d", userID)).Result()
	if err != nil {
		logger.Error("check robot failed", "user_id", userID, "error", err)
		return false, fmt.Errorf("check robot failed: %w", err)
	}
	return exists, nil
}
