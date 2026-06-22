package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
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
// 使用 Redis SISMEMBER robot:user_ids {userID} 实现 O(1) 查询
func (c *redisRobotChecker) IsRobot(ctx context.Context, userID int64) bool {
	if c.redis == nil {
		return false
	}
	key := "robot:user_ids"
	exists, err := c.redis.SIsMember(ctx, key, fmt.Sprintf("%d", userID)).Result()
	if err != nil {
		logger.Error("check robot failed", "user_id", userID, "error", err)
		return false
	}
	return exists
}
