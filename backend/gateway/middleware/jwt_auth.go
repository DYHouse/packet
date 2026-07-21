package middleware

import (
	"net/http"
	"strings"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/gateway/service"
	"github.com/gin-gonic/gin"
)

// JWTAuthMiddleware 校验 Authorization: Bearer <token>，
// 从 TokenService 验证 JWT，将 InternalUserID / Nickname / Avatar 写入 gin.Context。
// 用于玩家级 HTTP 端点（如头像上传），与 merchant 签名中间件互斥使用。
type JWTAuthMiddleware struct {
	tokenService *service.TokenService
}

func NewJWTAuthMiddleware(tokenService *service.TokenService) *JWTAuthMiddleware {
	return &JWTAuthMiddleware{tokenService: tokenService}
}

const (
	ContextKeyInternalUserID = "internal_user_id"
	ContextKeyNickname       = "nickname"
	ContextKeyAvatar         = "avatar"
)

// VerifyJWT 返回 gin 中间件，校验 Bearer JWT 并注入用户信息到 context。
func (m *JWTAuthMiddleware) VerifyJWT() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "missing bearer token"})
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		claims, err := m.tokenService.VerifyToken(token)
		if err != nil {
			logger.Warn("jwt verify failed", "error", err, "path", c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "invalid token"})
			return
		}
		c.Set(ContextKeyInternalUserID, claims.InternalUserID)
		c.Set(ContextKeyNickname, claims.Nickname)
		c.Set(ContextKeyAvatar, claims.Avatar)
		c.Next()
	}
}
