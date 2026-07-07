package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
)

// UserIDConvertService 依赖 domain.UserService 接口（由 game 层 adapter 实现），
// 不再直接引用 game 模型层，从而解除 settlement → game 模型层的反向依赖。
type UserIDConvertService struct {
	userSvc domain.UserService
}

func NewUserIDConvertService(userSvc domain.UserService) *UserIDConvertService {
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
