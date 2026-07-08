package repository

import (
	"context"

	"github.com/cashparty/backend/game/model"
)

// UserCacheRepository 用户缓存仓储接口，定义用户数据的 cache-aside 操作。
// 实现方负责 Redis 缓存的读写与失效，应用层负责协调缓存与 DB 的 cache-aside 流程。
type UserCacheRepository interface {
	// GetUser 从缓存获取用户（按 userID）。未命中返回 nil, nil。
	GetUser(ctx context.Context, userID string) (*model.User, error)
	// SetUser 写入用户缓存（按 userID），TTL 由实现方管理。
	SetUser(ctx context.Context, userID string, user *model.User) error
	// DeleteUser 删除用户缓存（按 userID）。
	DeleteUser(ctx context.Context, userID string) error
	// GetUserById 从缓存获取用户（按主键 id）。未命中返回 nil, nil。
	GetUserById(ctx context.Context, id string) (*model.User, error)
	// SetUserById 写入用户缓存（按主键 id），TTL 由实现方管理。
	SetUserById(ctx context.Context, id string, user *model.User) error
	// DeleteUserById 删除用户缓存（按主键 id）。
	DeleteUserById(ctx context.Context, id string) error
	// GetPendingCredit 获取玩家当前游戏的待入账金额（累计抢红包+奖励）。
	GetPendingCredit(ctx context.Context, userID string) int64
}
