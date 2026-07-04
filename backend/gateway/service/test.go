package service

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/logger"
)

type TestService struct {
	tokenService *TokenService
	userSaver    UserSaver
}

func NewTestService(tokenService *TokenService, userSaver UserSaver) *TestService {
	return &TestService{
		tokenService: tokenService,
		userSaver:    userSaver,
	}
}

type TestTokenRequest struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

type TestTokenResponse struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
}

func (s *TestService) GenerateTestToken(ctx context.Context, req *TestTokenRequest) (*TestTokenResponse, error) {
	if req.Nickname == "" {
		req.Nickname = "TestPlayer"
	}

	internalUserID, avatarURL, err := s.saveUserAndGetInternalID(ctx, req.UserID, req.Nickname, req.Avatar)
	if err != nil {
		return nil, err
	}

	if avatarURL != "" {
		req.Avatar = avatarURL
	}

	token, err := s.tokenService.GenerateToken(&TokenData{
		InternalUserID: internalUserID,
		PlatformUserID: req.UserID,
		Nickname:       req.Nickname,
		Avatar:         req.Avatar,
	})
	if err != nil {
		return nil, err
	}

	logger.Info("test token generated", "user_id", req.UserID, "internal_user_id", internalUserID)

	return &TestTokenResponse{
		Token:    token,
		UserID:   req.UserID,
		Nickname: req.Nickname,
	}, nil
}

func (s *TestService) saveUserAndGetInternalID(ctx context.Context, userID, nickname, avatar string) (string, string, error) {
	if s.userSaver == nil {
		logger.Error("user saver not configured", "user_id", userID)
		return "", "", fmt.Errorf("%w: user saver not configured", ErrUserSaveFailed)
	}

	internalUserID, avatarURL, err := s.userSaver.SaveUser(ctx, userID, nickname, avatar, "127.0.0.1", "test-device")
	if err != nil {
		logger.Error("failed to save test user", "error", err, "user_id", userID)
		return "", "", fmt.Errorf("%w: %w", ErrUserSaveFailed, err)
	}

	return internalUserID, avatarURL, nil
}
