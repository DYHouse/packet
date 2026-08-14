package redis

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cashparty/backend/common/converter"
	lockScripts "github.com/cashparty/backend/common/lock/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// RobotSchedulerRedis 机器人调度器状态 Redis 服务
type RobotSchedulerRedis struct {
	redis cRedis.RedisClient
}

// NewRobotSchedulerRedis 创建机器人调度器状态 Redis 服务实例
func NewRobotSchedulerRedis(redis cRedis.RedisClient) *RobotSchedulerRedis {
	return &RobotSchedulerRedis{redis: redis}
}

// AddRobotToRoom 添加机器人到房间
func (s *RobotSchedulerRedis) AddRobotToRoom(ctx context.Context, roomID string, userID int64) error {
	return s.redis.SAdd(ctx, rediskeys.RobotRoomKey(roomID), converter.FormatID(userID)).Err()
}

// RemoveRobotFromRoom 从房间移除机器人
func (s *RobotSchedulerRedis) RemoveRobotFromRoom(ctx context.Context, roomID string, userID int64) error {
	return s.redis.SRem(ctx, rediskeys.RobotRoomKey(roomID), converter.FormatID(userID)).Err()
}

// GetRoomRobots 获取房间内所有机器人
func (s *RobotSchedulerRedis) GetRoomRobots(ctx context.Context, roomID string) ([]int64, error) {
	members, err := s.redis.SMembers(ctx, rediskeys.RobotRoomKey(roomID)).Result()
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

// GetRoomPlayerRobots 获取房间内同时为玩家的机器人 userID 集合。
// 因 RoomPlayersKey 为 HASH 类型，不能用 SINTER（SINTER 要求双方均为 SET），
// 改用 SMembers 读取 RobotRoomKey（SET）+ HKeys 读取 RoomPlayersKey（HASH），应用层用 map 求交集。
func (s *RobotSchedulerRedis) GetRoomPlayerRobots(ctx context.Context, roomID string) ([]int64, error) {
	robotMembers, err := s.redis.SMembers(ctx, rediskeys.RobotRoomKey(roomID)).Result()
	if err != nil {
		return nil, err
	}
	playerMembers, err := s.redis.HKeys(ctx, rediskeys.RoomPlayersKey(roomID)).Result()
	if err != nil {
		return nil, err
	}
	// 以玩家集合构建 map，遍历机器人集合取交集
	playerSet := make(map[string]struct{}, len(playerMembers))
	for _, member := range playerMembers {
		playerSet[member] = struct{}{}
	}
	ids := make([]int64, 0, len(robotMembers))
	for _, member := range robotMembers {
		if _, ok := playerSet[member]; !ok {
			continue
		}
		userID, err := converter.ParseIDStrict(member)
		if err != nil {
			logger.Warn("parse room player robot userID failed", "member", member, "error", err)
			continue
		}
		ids = append(ids, userID)
	}
	return ids, nil
}

// ClearRoomRobots 清空房间机器人
func (s *RobotSchedulerRedis) ClearRoomRobots(ctx context.Context, roomID string) error {
	return s.redis.Del(ctx, rediskeys.RobotRoomKey(roomID)).Err()
}

// AcquireAssignLock 获取分配锁
// 返回 (locked, token)：locked=true 时 token 是本次持有的随机值，释放锁时需传入
// token 用于 ReleaseAssignLock 校验持有者，防止 TTL 过期后误删其他持有者的锁
func (s *RobotSchedulerRedis) AcquireAssignLock(ctx context.Context, userID int64, roomID string, ttl time.Duration) (bool, string, error) {
	token := uuid.New().String()
	ok, err := s.redis.SetNX(ctx, rediskeys.RobotAssignLockKey(userID), token, ttl).Result()
	if err != nil {
		return false, "", err
	}
	return ok, token, nil
}

// ReleaseAssignLock 释放分配锁（需校验 token）
// 若 TTL 已过期被他人抢占，GET != token，不会 del，保护新持有者
func (s *RobotSchedulerRedis) ReleaseAssignLock(ctx context.Context, userID int64, token string) error {
	return lockScripts.ReleaseLockScript.Run(ctx, s.redis, []string{rediskeys.RobotAssignLockKey(userID)}, token).Err()
}

// AcquireRoomAssignLock 获取房间分配限流锁
// 返回 (locked, token, error)：locked=true 时 token 是本次持有的随机值，释放锁时需传入
// token 用于 ReleaseRoomAssignLock 校验持有者，防止 TTL 过期后误删其他持有者的锁
func (s *RobotSchedulerRedis) AcquireRoomAssignLock(ctx context.Context, roomID string, ttl time.Duration) (bool, string, error) {
	token := uuid.New().String()
	ok, err := s.redis.SetNX(ctx, rediskeys.RobotRoomAssignLockKey(roomID), token, ttl).Result()
	if err != nil {
		return false, "", err
	}
	return ok, token, nil
}

// ReleaseRoomAssignLock 释放房间分配限流锁（需校验 token）
// 若 TTL 已过期被他人抢占，GET != token，不会 del，保护新持有者
func (s *RobotSchedulerRedis) ReleaseRoomAssignLock(ctx context.Context, roomID string, token string) error {
	return lockScripts.ReleaseLockScript.Run(ctx, s.redis, []string{rediskeys.RobotRoomAssignLockKey(roomID)}, token).Err()
}

// SetRecycleCooldown 设置回收冷却
func (s *RobotSchedulerRedis) SetRecycleCooldown(ctx context.Context, userID int64, ttl time.Duration) error {
	return s.redis.Set(ctx, rediskeys.RobotRecycleCooldownKey(userID), 1, ttl).Err()
}

// IsInRecycleCooldown 检查是否在回收冷却中
func (s *RobotSchedulerRedis) IsInRecycleCooldown(ctx context.Context, userID int64) (bool, error) {
	n, err := s.redis.Exists(ctx, rediskeys.RobotRecycleCooldownKey(userID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// AddToActiveSet 添加到活跃集合
func (s *RobotSchedulerRedis) AddToActiveSet(ctx context.Context, userID int64) error {
	return s.redis.SAdd(ctx, rediskeys.RobotSchedulerActiveKey(), converter.FormatID(userID)).Err()
}

// RemoveFromActiveSet 从活跃集合移除
func (s *RobotSchedulerRedis) RemoveFromActiveSet(ctx context.Context, userID int64) error {
	return s.redis.SRem(ctx, rediskeys.RobotSchedulerActiveKey(), converter.FormatID(userID)).Err()
}

// IsActiveRobot 检查是否为活跃机器人
func (s *RobotSchedulerRedis) IsActiveRobot(ctx context.Context, userID int64) (bool, error) {
	return s.redis.SIsMember(ctx, rediskeys.RobotSchedulerActiveKey(), converter.FormatID(userID)).Result()
}

// GetActiveCount 获取活跃机器人数量
func (s *RobotSchedulerRedis) GetActiveCount(ctx context.Context) (int64, error) {
	return s.redis.SCard(ctx, rediskeys.RobotSchedulerActiveKey()).Result()
}

// ScanRoomIDs 扫描匹配指定前缀的房间 key，返回房间 ID 列表。
// prefix 应为完整 SCAN 匹配模式（如 "cashparty:*:room:hash"）。
// 房间 ID 取 key 中最后一个冒号后的段。
// Cluster 模式下通过 ScanAll 自动跨所有 master 节点扫描。
func (s *RobotSchedulerRedis) ScanRoomIDs(ctx context.Context, prefix string, count int64) ([]string, error) {
	keys, err := s.redis.ScanAll(ctx, prefix, count)
	if err != nil {
		return nil, err
	}
	roomIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		idx := strings.LastIndex(key, ":")
		if idx < 0 || idx == len(key)-1 {
			continue
		}
		roomID := key[idx+1:]
		if roomID == "" {
			continue
		}
		roomIDs = append(roomIDs, roomID)
	}
	return roomIDs, nil
}

// GetUserRoom 返回 userRoomKey 指向的 roomID。
// 返回空串表示 userRoomKey 不存在或值为 "0"；err 仅在 Redis 调用失败时非 nil。
func (s *RobotSchedulerRedis) GetUserRoom(ctx context.Context, userID int64) (string, error) {
	// 跨房间反查用户当前所在房间，使用 CurrentRoomKey（{userID} hash tag）
	val, err := s.redis.Get(ctx, rediskeys.CurrentRoomKey(converter.FormatID(userID))).Result()
	if err != nil {
		// Redis Nil 表示 key 不存在，返回空串而非 err
		if errors.Is(err, goredis.Nil) {
			return "", nil
		}
		return "", err
	}
	if val == "" || val == "0" {
		return "", nil
	}
	return val, nil
}
