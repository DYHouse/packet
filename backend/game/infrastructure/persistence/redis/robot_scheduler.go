package redis

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/google/uuid"
)

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
// 返回 (locked, token)：locked=true 时 token 是本次持有的随机值，释放锁时需传入
// token 用于 ReleaseAssignLock 校验持有者，防止 TTL 过期后误删其他持有者的锁
func (s *RobotSchedulerRedis) AcquireAssignLock(ctx context.Context, userID int64, roomID string, ttl time.Duration) (bool, string, error) {
	token := uuid.New().String()
	ok, err := s.redis.SetNX(ctx, RobotAssignLockKey(userID), token, ttl).Result()
	if err != nil {
		return false, "", err
	}
	return ok, token, nil
}

// ReleaseAssignLock 释放分配锁（需校验 token）
// 若 TTL 已过期被他人抢占，GET != token，不会 del，保护新持有者
func (s *RobotSchedulerRedis) ReleaseAssignLock(ctx context.Context, userID int64, token string) error {
	return scripts.ReleaseAssignLock.Run(ctx, s.redis, []string{RobotAssignLockKey(userID)}, token).Err()
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
