package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/gateway/config"
	"github.com/cashparty/backend/gateway/model"
)

var ErrUserSaveFailed = errors.New("failed to save user")

type UserSaver interface {
	SaveUser(ctx context.Context, userID, nickname, avatar, ip, deviceID string) (internalUserID, avatarURL string, err error)
}

type GameService struct {
	cfg          *config.Config
	tokenService *TokenService
	userSaver    UserSaver
}

type GameStartRequest struct {
	GameID    int    `json:"game_id"`
	GameCode  string `json:"game_code"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Avatar    string `json:"avatar"`
	Currency  string `json:"currency"`
	Lang      string `json:"lang"`
	ClientIP  string `json:"client_ip"`
	Version   string `json:"version"`
	VariantID int    `json:"variant_id,omitempty"`
}

type GameStartResponse struct {
	UserToken  string `json:"user_token"`
	GameURL    string `json:"game_url"`
	GameConfig string `json:"game_config"`
}

func NewGameService(cfg *config.Config, tokenService *TokenService, userSaver UserSaver) *GameService {
	return &GameService{
		cfg:          cfg,
		tokenService: tokenService,
		userSaver:    userSaver,
	}
}

func (s *GameService) GetGameList(ctx context.Context, games []*model.Game) []*model.Game {
	logger.Info("getting game list", "count", len(games))
	return games
}

func (s *GameService) StartGame(ctx context.Context, req *GameStartRequest, game *model.Game) (*GameStartResponse, error) {
	logger.Info("starting game", "game_code", req.GameCode, "user_id", req.UserID)

	if !game.IsActive() {
		return nil, fmt.Errorf("game is not active")
	}

	internalUserID, avatarURL, err := s.saveUserAndGetInternalID(ctx, req.UserID, req.Username, req.Avatar, req.ClientIP)
	if err != nil {
		return nil, err
	}

	if avatarURL != "" {
		req.Avatar = avatarURL
	}

	token, err := s.tokenService.GenerateToken(&TokenData{
		InternalUserID: internalUserID,
		PlatformUserID: req.UserID,
		Nickname:       req.Username,
		Avatar:         req.Avatar,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	params := url.Values{}
	params.Set("currency", req.Currency)
	params.Set("gameId", fmt.Sprintf("%d", game.ID))
	params.Set("gameName", game.Name)
	params.Set("lang", req.Lang)
	params.Set("mid", s.cfg.Merchant.ID)
	params.Set("resource_id", fmt.Sprintf("%d", game.ResourceID))
	params.Set("token", token)
	params.Set("userId", req.UserID)
	params.Set("version", req.Version)
	params.Set("wsUrl", s.cfg.Merchant.WsURL)

	gameURL := fmt.Sprintf("%s/%s?%s", s.cfg.Merchant.GameEntryURL, game.GameCode, params.Encode())

	gameConfig := map[string]interface{}{
		"user_token": token,
		"currency":   req.Currency,
		"lang":       req.Lang,
		"ws_url":     s.cfg.Merchant.WsURL,
	}

	gameConfigJSON, err := json.Marshal(gameConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal game config: %w", err)
	}

	response := &GameStartResponse{
		UserToken:  token,
		GameURL:    gameURL,
		GameConfig: string(gameConfigJSON),
	}

	logger.Info("game started successfully", "user_id", req.UserID, "game_code", game.GameCode, "internal_user_id", internalUserID)

	return response, nil
}

func (s *GameService) saveUserAndGetInternalID(ctx context.Context, userID, nickname, avatar, clientIP string) (string, string, error) {
	if s.userSaver == nil {
		logger.Error("user saver not configured", "user_id", userID)
		return "", "", fmt.Errorf("%w: user saver not configured", ErrUserSaveFailed)
	}

	internalUserID, avatarURL, err := s.userSaver.SaveUser(ctx, userID, nickname, avatar, clientIP, "")
	if err != nil {
		logger.Error("failed to save user", "error", err, "user_id", userID)
		return "", "", fmt.Errorf("%w: %v", ErrUserSaveFailed, err)
	}

	return internalUserID, avatarURL, nil
}
