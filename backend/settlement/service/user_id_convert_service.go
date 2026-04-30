package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	gameModel "github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/settlement/dto"
)

type UserService interface {
	GetUserById(ctx context.Context, id string) (*gameModel.User, error)
}

type UserIDConvertService struct {
	userSvc UserService
}

func NewUserIDConvertService(userSvc UserService) *UserIDConvertService {
	return &UserIDConvertService{
		userSvc: userSvc,
	}
}

func (s *UserIDConvertService) GetPlatformUserID(ctx context.Context, internalUserID int64) (string, error) {
	if internalUserID == dto.PlatformAccountID {
		return "", fmt.Errorf("platform account should not call platform api")
	}

	user, err := s.userSvc.GetUserById(ctx, fmt.Sprintf("%d", internalUserID))
	if err != nil {
		logger.Error("get user by id failed", "internal_user_id", internalUserID, "error", err)
		return "", fmt.Errorf("get user by id failed: %w", err)
	}

	if user.UserID == "" {
		return "", fmt.Errorf("user platform id is empty, internal_user_id: %d", internalUserID)
	}

	return user.UserID, nil
}
