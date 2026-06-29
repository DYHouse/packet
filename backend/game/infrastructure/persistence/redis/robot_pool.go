package redis

import (
	"context"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

// RobotPoolAvailableKey 可用机器人账号池 Redis key
func RobotPoolAvailableKey() string {
	return "robot:pool:available"
}

// RobotPoolService 机器人账号池服务
type RobotPoolService struct {
	redis *cRedis.Client
}

// NewRobotPoolService 创建机器人账号池服务实例
func NewRobotPoolService(redis *cRedis.Client) *RobotPoolService {
	return &RobotPoolService{redis: redis}
}

// AddToAvailablePool 添加到可用账号池
func (s *RobotPoolService) AddToAvailablePool(ctx context.Context, userID int64) error {
	return s.redis.SAdd(ctx, RobotPoolAvailableKey(), converter.FormatID(userID)).Err()
}

// RemoveFromAvailablePool 从可用账号池移除
func (s *RobotPoolService) RemoveFromAvailablePool(ctx context.Context, userID int64) error {
	return s.redis.SRem(ctx, RobotPoolAvailableKey(), converter.FormatID(userID)).Err()
}

// GetAvailableCount 获取可用账号池数量
func (s *RobotPoolService) GetAvailableCount(ctx context.Context) (int64, error) {
	return s.redis.SCard(ctx, RobotPoolAvailableKey()).Result()
}

// IsAvailable 检查是否在可用池中
func (s *RobotPoolService) IsAvailable(ctx context.Context, userID int64) (bool, error) {
	return s.redis.SIsMember(ctx, RobotPoolAvailableKey(), converter.FormatID(userID)).Result()
}

// GetAvailableRobots 获取所有可用机器人ID
func (s *RobotPoolService) GetAvailableRobots(ctx context.Context) ([]int64, error) {
	members, err := s.redis.SMembers(ctx, RobotPoolAvailableKey()).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		userID, err := converter.ParseIDStrict(member)
		if err != nil {
			logger.Warn("parse robot userID failed", "member", member, "error", err)
			continue
		}
		ids = append(ids, userID)
	}
	return ids, nil
}
