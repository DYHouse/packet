package service

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type GameTokenClaims struct {
	SessionID      string `json:"sid"`
	InternalUserID string `json:"uid"`
	PlatformUserID string `json:"puid"`
	Nickname       string `json:"nick"`
	Avatar         string `json:"avatar"`
	jwt.RegisteredClaims
}

type TokenConfig struct {
	SecretKey string
	TokenTTL  time.Duration
	Issuer    string
}

type TokenService struct {
	config *TokenConfig
}

func NewTokenService(config *TokenConfig) *TokenService {
	if config.TokenTTL == 0 {
		config.TokenTTL = 2 * time.Hour
	}
	if config.Issuer == "" {
		config.Issuer = "gateway-service"
	}
	return &TokenService{config: config}
}

type TokenData struct {
	InternalUserID string
	PlatformUserID string
	Nickname       string
	Avatar         string
}

func (s *TokenService) GenerateToken(data *TokenData) (string, error) {
	if len(s.config.SecretKey) < 32 {
		return "", fmt.Errorf("secret key must be at least 32 characters")
	}

	now := time.Now()
	sessionID := uuid.New().String()

	claims := &GameTokenClaims{
		SessionID:      sessionID,
		InternalUserID: data.InternalUserID,
		PlatformUserID: data.PlatformUserID,
		Nickname:       data.Nickname,
		Avatar:         data.Avatar,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.config.Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.config.TokenTTL)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.config.SecretKey))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

func (s *TokenService) VerifyToken(tokenString string) (*GameTokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &GameTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.config.SecretKey), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*GameTokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}
