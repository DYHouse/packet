package application

import (
	"context"
	"strconv"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/utils"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
)

type UserService struct {
	dbRepo    repository.DBRepository
	cacheRepo repository.UserCacheRepository
	avatarCfg *config.AvatarConfig
	idGen     idgen.IDGenerator
}

func NewUserService(dbRepo repository.DBRepository, cacheRepo repository.UserCacheRepository, avatarCfg *config.AvatarConfig, idGen idgen.IDGenerator) *UserService {
	return &UserService{
		dbRepo:    dbRepo,
		cacheRepo: cacheRepo,
		avatarCfg: avatarCfg,
		idGen:     idGen,
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
		avatar = utils.GetRandomAvatar(s.avatarCfg.BaseURL, s.avatarCfg.DefaultCount)
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
