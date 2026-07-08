package redis

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
)

// userCacheTTL 用户缓存 TTL，与原 user_service.go 中的 30*time.Minute 保持一致。
const userCacheTTL = 30 * time.Minute

// userCacheRepository 用户缓存仓储实现，实现 repository.UserCacheRepository 接口。
// 负责 Redis 缓存的读写与失效，TTL 由本实现管理。
type userCacheRepository struct {
	redis cRedis.RedisClient
}

// NewUserCacheRepository 创建用户缓存仓储实例，返回 repository.UserCacheRepository 接口。
func NewUserCacheRepository(redis cRedis.RedisClient) repository.UserCacheRepository {
	return &userCacheRepository{redis: redis}
}

// GetUser 从缓存获取用户（按 userID）。未命中或反序列化失败返回 nil, nil。
func (r *userCacheRepository) GetUser(ctx context.Context, userID string) (*model.User, error) {
	data := r.redis.Get(ctx, rediskeys.UserByUserIDKey(userID)).Val()
	if data == "" {
		return nil, nil
	}
	var user model.User
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		return nil, nil
	}
	return &user, nil
}

// SetUser 写入用户缓存（按 userID），TTL 为 userCacheTTL。
func (r *userCacheRepository) SetUser(ctx context.Context, userID string, user *model.User) error {
	data, _ := json.Marshal(user)
	return r.redis.Set(ctx, rediskeys.UserByUserIDKey(userID), string(data), userCacheTTL).Err()
}

// DeleteUser 删除用户缓存（按 userID）。
func (r *userCacheRepository) DeleteUser(ctx context.Context, userID string) error {
	return r.redis.Del(ctx, rediskeys.UserByUserIDKey(userID)).Err()
}

// GetUserById 从缓存获取用户（按主键 id）。未命中或反序列化失败返回 nil, nil。
func (r *userCacheRepository) GetUserById(ctx context.Context, id string) (*model.User, error) {
	data := r.redis.Get(ctx, rediskeys.UserByIdKey(id)).Val()
	if data == "" {
		return nil, nil
	}
	var user model.User
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		return nil, nil
	}
	return &user, nil
}

// SetUserById 写入用户缓存（按主键 id），TTL 为 userCacheTTL。
func (r *userCacheRepository) SetUserById(ctx context.Context, id string, user *model.User) error {
	data, _ := json.Marshal(user)
	return r.redis.Set(ctx, rediskeys.UserByIdKey(id), string(data), userCacheTTL).Err()
}

// DeleteUserById 删除用户缓存（按主键 id）。
func (r *userCacheRepository) DeleteUserById(ctx context.Context, id string) error {
	return r.redis.Del(ctx, rediskeys.UserByIdKey(id)).Err()
}

// GetPendingCredit 获取玩家当前游戏的待入账金额（累计抢红包+奖励）。
// 读取流程：PlayerRoomKey → RoomHashKey(HGetAll) → SessionPlayerTotalsKey(HGet)。
// 任何 Redis 错误或数据缺失时返回 0，与原 user_service.go 行为一致。
func (r *userCacheRepository) GetPendingCredit(ctx context.Context, userID string) int64 {
	roomID := r.redis.Get(ctx, rediskeys.PlayerRoomKey(userID)).Val()
	if roomID == "" || roomID == "0" {
		return 0
	}

	roomData := r.redis.HGetAll(ctx, rediskeys.RoomHashKey(roomID)).Val()
	if len(roomData) == 0 {
		return 0
	}

	status, _ := strconv.Atoi(roomData["status"])
	if status != 2 {
		return 0
	}

	sessionID := roomData["current_session_id"]
	if sessionID == "" {
		return 0
	}

	val := r.redis.HGet(ctx, rediskeys.SessionPlayerTotalsKey(sessionID), userID).Val()
	if val == "" {
		return 0
	}
	amount, _ := strconv.ParseInt(val, 10, 64)
	return amount
}
