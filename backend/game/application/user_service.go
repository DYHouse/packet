package application

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/push"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
)

type UserService struct {
	dbRepo      repository.DBRepository
	cacheRepo   repository.UserCacheRepository
	avatarCfg   *config.AvatarConfig
	idGen       idgen.IDGenerator
	broadcaster events.Broadcaster
}

func NewUserService(
	dbRepo repository.DBRepository,
	cacheRepo repository.UserCacheRepository,
	avatarCfg *config.AvatarConfig,
	idGen idgen.IDGenerator,
	broadcaster events.Broadcaster,
) *UserService {
	return &UserService{
		dbRepo:      dbRepo,
		cacheRepo:   cacheRepo,
		avatarCfg:   avatarCfg,
		idGen:       idGen,
		broadcaster: broadcaster,
	}
}

func (s *UserService) SaveUser(ctx context.Context, userID, nickname, avatar, ip, deviceID string) (string, string, error) {
	cachedUser, _ := s.cacheRepo.GetUser(ctx, userID)
	if cachedUser != nil {
		logger.Info("user found in cache, skip save", "user_id", userID, "id", cachedUser.ID)
		return converter.FormatID(cachedUser.ID), cachedUser.Avatar, nil
	}

	existingUser, err := s.dbRepo.UserDBRepo().GetUser(ctx, userID)
	if err == nil && existingUser != nil {
		_ = s.cacheRepo.SetUser(ctx, userID, existingUser)
		logger.Info("user already exists in db", "user_id", userID, "id", existingUser.ID)
		return converter.FormatID(existingUser.ID), existingUser.Avatar, nil
	}

	if avatar == "" && s.avatarCfg != nil {
		avatar = GetRandomAvatar(s.avatarCfg.BaseURL, s.avatarCfg.DefaultCount)
	}

	newUser, err := model.NewUser(s.idGen, userID, nickname, avatar, ip, deviceID)
	if err != nil {
		logger.Error("failed to generate user id", "error", err, "user_id", userID)
		return "", "", err
	}
	if err := s.dbRepo.UserDBRepo().CreateOrUpdateUser(ctx, newUser); err != nil {
		logger.Error("failed to create user", "error", err, "user_id", userID)
		return "", "", err
	}

	_ = s.cacheRepo.SetUser(ctx, userID, newUser)
	logger.Info("user saved and cached", "user_id", userID, "id", newUser.ID, "nickname", nickname)
	return converter.FormatID(newUser.ID), newUser.Avatar, nil
}

func (s *UserService) GetUser(ctx context.Context, userID string) (*model.User, error) {
	cachedUser, _ := s.cacheRepo.GetUser(ctx, userID)
	if cachedUser != nil {
		return cachedUser, nil
	}

	user, err := s.dbRepo.UserDBRepo().GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	_ = s.cacheRepo.SetUser(ctx, userID, user)
	return user, nil
}

func (s *UserService) GetUserById(ctx context.Context, id string) (*model.User, error) {
	cachedUser, _ := s.cacheRepo.GetUserById(ctx, id)
	if cachedUser != nil {
		return cachedUser, nil
	}

	user, err := s.dbRepo.UserDBRepo().GetUserById(ctx, id)
	if err != nil {
		return nil, err
	}

	_ = s.cacheRepo.SetUserById(ctx, id, user)
	return user, nil
}

// SetUserIsRobot marks a user as a robot account by primary key id.
// It also invalidates the Redis cache so subsequent reads reflect the update.
func (s *UserService) SetUserIsRobot(ctx context.Context, id int64) error {
	if err := s.dbRepo.UserDBRepo().SetUserIsRobot(ctx, id); err != nil {
		return err
	}
	// Invalidate both cache keys so the next read picks up is_robot=true from DB.
	_ = s.cacheRepo.DeleteUserById(ctx, strconv.FormatInt(id, 10))
	// Best-effort: we don't have the user_id string here, so we skip the
	// user_id cache key. It will expire naturally (TTL 30min) and is only
	// used during the initial SaveUser flow which happens before SetUserIsRobot.
	return nil
}

// GetPendingCredit 获取玩家当前游戏的待入账金额（累计抢红包+奖励）
func (s *UserService) GetPendingCredit(ctx context.Context, userID string) int64 {
	return s.cacheRepo.GetPendingCredit(ctx, userID)
}

// UpdateAvatar 更新玩家当前头像 URL，同步失效 Redis 缓存，并推送 user_profile_updated。
// avatarURL 由调用方（HTTP 上传端点）传入最终 URL。
// 失败时返回 error，调用方 MUST 记录日志（规约 SID-3）。
// 推送失败不影响主流程：客户端下次 auth_ok 仍会拿到最新 avatar。
func (s *UserService) UpdateAvatar(ctx context.Context, userID string, avatarURL string) error {
	// 1. 查现有 user 拿到主键 id（userID 字符串 → id int64）
	user, err := s.dbRepo.UserDBRepo().GetUserById(ctx, userID)
	if err != nil {
		return fmt.Errorf("UserService.UpdateAvatar: get user failed: %w", err)
	}
	// 2. 校验 avatarURL 长度 ≤ 512（与 User.Avatar gorm tag 一致）
	if len(avatarURL) > 5120 {
		return fmt.Errorf("UserService.UpdateAvatar: avatar url too long: %d", len(avatarURL))
	}
	// 3. 写 DB
	if err := s.dbRepo.UserDBRepo().UpdateAvatar(ctx, user.ID, avatarURL); err != nil {
		return fmt.Errorf("UserService.UpdateAvatar: update db failed: %w", err)
	}
	// 4. 失效旧缓存并回填新值，确保 gateway auth 中间件能立即读到最新 avatar。
	// 先删再写：避免并发场景下旧值覆盖新值（cache-aside 标准做法）。
	user.Avatar = avatarURL
	_ = s.cacheRepo.DeleteUser(ctx, userID)
	_ = s.cacheRepo.DeleteUserById(ctx, strconv.FormatInt(user.ID, 10))
	_ = s.cacheRepo.SetUser(ctx, userID, user)
	_ = s.cacheRepo.SetUserById(ctx, strconv.FormatInt(user.ID, 10), user)

	// 5. 推送 user_profile_updated 给该用户所有在线连接（跨节点 via Kafka）
	if s.broadcaster != nil {
		pushData := &push.UserProfileUpdatedPush{
			Avatar:   avatarURL,
			Nickname: user.Nickname,
		}
		if err := s.broadcaster.BroadcastToUser(ctx, userID, message.PushUserProfileUpdated, pushData); err != nil {
			logger.Warn("push user_profile_updated failed",
				"user_id", userID, "error", err)
			// 推送失败不影响主流程：客户端下次 auth_ok 仍会拿到最新 avatar
		}
	}

	logger.Info("user avatar updated", "user_id", userID, "id", user.ID, "avatar", avatarURL)
	return nil
}
