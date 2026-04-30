package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/utils"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/model"
)

type UserService struct {
	dbRepo      domain.DBRepository
	redis       *cRedis.Client
	avatarCfg   *config.AvatarConfig
}

func NewUserService(dbRepo domain.DBRepository, redis *cRedis.Client, avatarCfg *config.AvatarConfig) *UserService {
	return &UserService{
		dbRepo:    dbRepo,
		redis:     redis,
		avatarCfg: avatarCfg,
	}
}

func (s *UserService) SaveUser(ctx context.Context, userID, nickname, avatar, ip, deviceID string) (string, string, error) {
	cacheKey := redis.UserByUserIDKey(userID)

	cachedData := s.redis.Get(ctx, cacheKey).Val()
	if cachedData != "" {
		var user model.User
		if err := json.Unmarshal([]byte(cachedData), &user); err == nil {
			logger.Info("user found in cache, skip save", "user_id", userID, "id", user.ID)
			return converter.FormatID(user.ID), user.Avatar, nil
		}
	}

	existingUser, err := s.dbRepo.UserDBRepo().GetUser(ctx, userID)
	if err == nil && existingUser != nil {
		userData, _ := json.Marshal(existingUser)
		s.redis.Set(ctx, cacheKey, string(userData), 30*time.Minute)
		logger.Info("user already exists in db", "user_id", userID, "id", existingUser.ID)
		return converter.FormatID(existingUser.ID), existingUser.Avatar, nil
	}

	if avatar == "" && s.avatarCfg != nil {
		avatar = utils.GetRandomAvatar(s.avatarCfg.BaseURL, s.avatarCfg.DefaultCount)
	}

	newUser := model.NewUser(userID, nickname, avatar, ip, deviceID)
	if err := s.dbRepo.UserDBRepo().CreateOrUpdateUser(ctx, newUser); err != nil {
		logger.Error("failed to create user", "error", err, "user_id", userID)
		return "", "", err
	}

	userData, _ := json.Marshal(newUser)
	s.redis.Set(ctx, cacheKey, string(userData), 30*time.Minute)
	logger.Info("user saved and cached", "user_id", userID, "id", newUser.ID, "nickname", nickname)
	return converter.FormatID(newUser.ID), newUser.Avatar, nil
}

func (s *UserService) GetUser(ctx context.Context, userID string) (*model.User, error) {
	cacheKey := redis.UserByUserIDKey(userID)

	cachedData := s.redis.Get(ctx, cacheKey).Val()
	if cachedData != "" {
		var user model.User
		if err := json.Unmarshal([]byte(cachedData), &user); err == nil {
			return &user, nil
		}
	}

	user, err := s.dbRepo.UserDBRepo().GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	userData, _ := json.Marshal(user)
	s.redis.Set(ctx, cacheKey, string(userData), 30*time.Minute)
	return user, nil
}

func (s *UserService) GetUserById(ctx context.Context, id string) (*model.User, error) {
	cacheKey := redis.UserByIdKey(id)

	cachedData := s.redis.Get(ctx, cacheKey).Val()
	if cachedData != "" {
		var user model.User
		if err := json.Unmarshal([]byte(cachedData), &user); err == nil {
			return &user, nil
		}
	}

	user, err := s.dbRepo.UserDBRepo().GetUserById(ctx, id)
	if err != nil {
		return nil, err
	}

	userData, _ := json.Marshal(user)
	s.redis.Set(ctx, cacheKey, string(userData), 30*time.Minute)
	return user, nil
}
