package repository

import "context"

// RobotPoolRepository 机器人账号池仓储接口，定义可用机器人池的集合操作。
type RobotPoolRepository interface {
	// AddToAvailablePool 添加到可用账号池。
	AddToAvailablePool(ctx context.Context, userID int64) error
	// RemoveFromAvailablePool 从可用账号池移除。
	RemoveFromAvailablePool(ctx context.Context, userID int64) error
	// GetAvailableCount 获取可用账号池数量。
	GetAvailableCount(ctx context.Context) (int64, error)
}
