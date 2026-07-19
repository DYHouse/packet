package repository

import (
	"context"
	"time"
)

// RobotSchedulerRepository 机器人调度器状态仓储接口，定义房间机器人集合、
// 分配锁与活跃集合的操作，以及基于 SCAN 的房间 ID 扫描。
type RobotSchedulerRepository interface {
	// AddRobotToRoom 添加机器人到房间机器人集合。
	AddRobotToRoom(ctx context.Context, roomID string, userID int64) error
	// RemoveRobotFromRoom 从房间机器人集合移除机器人。
	RemoveRobotFromRoom(ctx context.Context, roomID string, userID int64) error
	// GetRoomRobots 获取房间内所有机器人 userID。
	GetRoomRobots(ctx context.Context, roomID string) ([]int64, error)
	// GetRoomPlayerRobots 获取房间内同时为玩家的机器人 userID（房间机器人集合与房间玩家集合的交集）。
	// 因 RoomPlayersKey 为 HASH 类型，不能用 SINTER，底层使用 SMembers + HKeys 应用层求交集。
	GetRoomPlayerRobots(ctx context.Context, roomID string) ([]int64, error)
	// AcquireAssignLock 获取机器人分配锁。
	// 返回 (locked, token, error)：locked=true 时 token 是本次持有的随机值，释放锁时需传入。
	AcquireAssignLock(ctx context.Context, userID int64, roomID string, ttl time.Duration) (bool, string, error)
	// ReleaseAssignLock 释放机器人分配锁（需校验 token 防止误删）。
	ReleaseAssignLock(ctx context.Context, userID int64, token string) error
	// AcquireRoomAssignLock 获取房间分配限流锁。
	// 返回 (locked, token, error)：locked=true 时 token 是本次持有的随机值，释放锁时需传入。
	AcquireRoomAssignLock(ctx context.Context, roomID string, ttl time.Duration) (bool, string, error)
	// ReleaseRoomAssignLock 释放房间分配限流锁（需校验 token 防止误删）。
	ReleaseRoomAssignLock(ctx context.Context, roomID string, token string) error
	// AddToActiveSet 添加到活跃机器人集合。
	AddToActiveSet(ctx context.Context, userID int64) error
	// RemoveFromActiveSet 从活跃集合移除。
	RemoveFromActiveSet(ctx context.Context, userID int64) error
	// ScanRoomIDs 扫描匹配指定前缀的房间 key，返回房间 ID 列表。
	// prefix 应为带尾随冒号的 key 前缀拼接 "*"（如 "cashparty:room:hash:*"）。
	ScanRoomIDs(ctx context.Context, prefix string, count int64) ([]string, error)
	// GetUserRoom 返回 userRoomKey 指向的 roomID。
	// 返回空串表示 userRoomKey 不存在或值为 "0"；err 仅在 Redis 调用失败时非 nil。
	// 用于幂等检查与跨房间残留清理。
	GetUserRoom(ctx context.Context, userID int64) (string, error)
}
