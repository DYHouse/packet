package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

// RobotRoomKey 房间机器人集合 Redis key
func RobotRoomKey(roomID string) string {
	return fmt.Sprintf("robot:room:%s", roomID)
}

// RobotAssignLockKey 机器人分配锁 Redis key
func RobotAssignLockKey(robotUserID int64) string {
	return fmt.Sprintf("robot:assign:%d", robotUserID)
}

// RobotRoomAssignLockKey 房间分配限流锁 Redis key
func RobotRoomAssignLockKey(roomID string) string {
	return fmt.Sprintf("robot:room_assign:%s", roomID)
}

// RobotRecycleCooldownKey 机器人回收冷却 Redis key
func RobotRecycleCooldownKey(userID int64) string {
	return fmt.Sprintf("robot:recycle_cooldown:%d", userID)
}

// RobotSchedulerActiveKey 调度器活跃机器人集合 Redis key
func RobotSchedulerActiveKey() string {
	return "robot:scheduler:active"
}

// RobotSchedulerRedis 机器人调度器状态 Redis 服务
type RobotSchedulerRedis struct {
	redis *cRedis.Client
}

// NewRobotSchedulerRedis 创建机器人调度器状态 Redis 服务实例
func NewRobotSchedulerRedis(redis *cRedis.Client) *RobotSchedulerRedis {
	return &RobotSchedulerRedis{redis: redis}
}

// AddRobotToRoom 添加机器人到房间
func (s *RobotSchedulerRedis) AddRobotToRoom(ctx context.Context, roomID string, userID int64) error {
	return s.redis.SAdd(ctx, RobotRoomKey(roomID), converter.FormatID(userID)).Err()
}

// RemoveRobotFromRoom 从房间移除机器人
func (s *RobotSchedulerRedis) RemoveRobotFromRoom(ctx context.Context, roomID string, userID int64) error {
	return s.redis.SRem(ctx, RobotRoomKey(roomID), converter.FormatID(userID)).Err()
}

// GetRoomRobots 获取房间内所有机器人
func (s *RobotSchedulerRedis) GetRoomRobots(ctx context.Context, roomID string) ([]int64, error) {
	members, err := s.redis.SMembers(ctx, RobotRoomKey(roomID)).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		userID, err := converter.ParseIDStrict(member)
		if err != nil {
			logger.Warn("parse room robot userID failed", "member", member, "error", err)
			continue
		}
		ids = append(ids, userID)
	}
	return ids, nil
}

// ClearRoomRobots 清空房间机器人
func (s *RobotSchedulerRedis) ClearRoomRobots(ctx context.Context, roomID string) error {
	return s.redis.Del(ctx, RobotRoomKey(roomID)).Err()
}

// AcquireAssignLock 获取分配锁
// 返回 true 表示获取成功
func (s *RobotSchedulerRedis) AcquireAssignLock(ctx context.Context, userID int64, roomID string, ttl time.Duration) (bool, error) {
	return s.redis.SetNX(ctx, RobotAssignLockKey(userID), roomID, ttl).Result()
}

// ReleaseAssignLock 释放分配锁
func (s *RobotSchedulerRedis) ReleaseAssignLock(ctx context.Context, userID int64) error {
	return s.redis.Del(ctx, RobotAssignLockKey(userID)).Err()
}

// AcquireRoomAssignLock 获取房间分配限流锁
// 返回 true 表示获取成功
func (s *RobotSchedulerRedis) AcquireRoomAssignLock(ctx context.Context, roomID string, ttl time.Duration) (bool, error) {
	return s.redis.SetNX(ctx, RobotRoomAssignLockKey(roomID), 1, ttl).Result()
}

// SetRecycleCooldown 设置回收冷却
func (s *RobotSchedulerRedis) SetRecycleCooldown(ctx context.Context, userID int64, ttl time.Duration) error {
	return s.redis.Set(ctx, RobotRecycleCooldownKey(userID), 1, ttl).Err()
}

// IsInRecycleCooldown 检查是否在回收冷却中
func (s *RobotSchedulerRedis) IsInRecycleCooldown(ctx context.Context, userID int64) (bool, error) {
	n, err := s.redis.Exists(ctx, RobotRecycleCooldownKey(userID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// AddToActiveSet 添加到活跃集合
func (s *RobotSchedulerRedis) AddToActiveSet(ctx context.Context, userID int64) error {
	return s.redis.SAdd(ctx, RobotSchedulerActiveKey(), converter.FormatID(userID)).Err()
}

// RemoveFromActiveSet 从活跃集合移除
func (s *RobotSchedulerRedis) RemoveFromActiveSet(ctx context.Context, userID int64) error {
	return s.redis.SRem(ctx, RobotSchedulerActiveKey(), converter.FormatID(userID)).Err()
}

// IsActiveRobot 检查是否为活跃机器人
func (s *RobotSchedulerRedis) IsActiveRobot(ctx context.Context, userID int64) (bool, error) {
	return s.redis.SIsMember(ctx, RobotSchedulerActiveKey(), converter.FormatID(userID)).Result()
}

// GetActiveCount 获取活跃机器人数量
func (s *RobotSchedulerRedis) GetActiveCount(ctx context.Context) (int64, error) {
	return s.redis.SCard(ctx, RobotSchedulerActiveKey()).Result()
}
