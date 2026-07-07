package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/settlement/infrastructure/persistence/redis"
)

// RobotChecker 机器人身份识别接口
type RobotChecker interface {
	IsRobot(ctx context.Context, userID int64) bool
}

// redisRobotChecker 基于 Redis SET 的机器人身份识别实现
type redisRobotChecker struct {
	redis *cRedis.Client
}

// NewRobotChecker 创建 RobotChecker 实例
func NewRobotChecker(redis *cRedis.Client) RobotChecker {
	return &redisRobotChecker{redis: redis}
}

// IsRobot 判断 userID 是否为机器人
// 使用 Redis SISMEMBER cashparty:robot:user_ids {userID} 实现 O(1) 查询
// Redis 故障时 fail-closed：保守地返回 true（认为是机器人），避免真实玩家资金误走虚拟钱包路径
func (c *redisRobotChecker) IsRobot(ctx context.Context, userID int64) bool {
	if c.redis == nil {
		return false
	}
	exists, err := c.redis.SIsMember(ctx, redis.RobotUserIDsKey(), fmt.Sprintf("%d", userID)).Result()
	if err != nil {
		logger.Error("check robot failed, fail-closed treat as robot", "user_id", userID, "error", err)
		return true
	}
	return exists
}
