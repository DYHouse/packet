package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/gateway/connection"
	"github.com/cashparty/backend/gateway/protocol"
	"github.com/cashparty/backend/gateway/service"
)

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrInvalidToken          = errors.New("invalid token")
	ErrTokenExpired          = errors.New("token expired")
	ErrAuthFailed            = errors.New("authentication failed")
	ErrTooManyFailedAttempts = errors.New("too many failed authentication attempts")
)

// AuthLockConfig Auth 锁定配置
type AuthLockConfig struct {
	MaxAttempts   int
	LockDuration  time.Duration
	CounterWindow time.Duration
}

type AuthMiddleware struct {
	tokenService  *service.TokenService
	redis         cRedis.RedisClient
	maxAttempts   int
	lockDuration  time.Duration
	counterWindow time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewAuthMiddleware 创建 Auth 中间件
func NewAuthMiddleware(tokenService *service.TokenService, redis cRedis.RedisClient, cfg AuthLockConfig) *AuthMiddleware {
	ctx, cancel := context.WithCancel(context.Background())
	return &AuthMiddleware{
		tokenService:  tokenService,
		redis:         redis,
		maxAttempts:   cfg.MaxAttempts,
		lockDuration:  cfg.LockDuration,
		counterWindow: cfg.CounterWindow,
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (m *AuthMiddleware) OnConnect(ctx context.Context, conn *connection.Connection, firstMessage []byte) error {
	var req protocol.Request
	if err := json.Unmarshal(firstMessage, &req); err != nil {
		logger.Warn("failed to parse first message", "conn_id", conn.ConnID, "error", err)
		m.sendError(conn, "", "", message.CodeInvalidMessage)
		return ErrInvalidToken
	}

	if req.Cmd != "auth" {
		logger.Warn("first message is not auth command", "conn_id", conn.ConnID, "cmd", req.Cmd)
		m.sendError(conn, req.Cmd, req.RequestID, message.CodeUnauthorized)
		return ErrUnauthorized
	}

	var authData struct {
		Token string `json:"token"`
	}
	if err := req.ParseData(&authData); err != nil {
		logger.Warn("failed to parse auth data", "conn_id", conn.ConnID, "error", err)
		m.sendError(conn, req.Cmd, req.RequestID, message.CodeInvalidParams)
		return ErrInvalidToken
	}

	if authData.Token == "" {
		logger.Warn("empty token", "conn_id", conn.ConnID)
		m.sendError(conn, req.Cmd, req.RequestID, message.CodeUnauthorized)
		return ErrInvalidToken
	}

	if m.isLocked(ctx, conn.IP) {
		logger.Warn("IP is locked due to too many failed attempts", "conn_id", conn.ConnID, "ip", conn.IP)
		m.sendError(conn, req.Cmd, req.RequestID, message.CodeForbidden)
		return ErrTooManyFailedAttempts
	}

	claims, err := m.tokenService.VerifyToken(authData.Token)
	if err != nil {
		m.recordFailedAttempt(ctx, conn.IP)
		logger.Warn("token verification failed", "conn_id", conn.ConnID, "error", err)
		m.sendError(conn, req.Cmd, req.RequestID, message.CodeAuthFailed)
		return ErrAuthFailed
	}

	m.clearFailedAttempts(ctx, conn.IP)

	// 从 Redis 缓存读取最新用户资料（由 game service 写入），避免 JWT 中的 avatar 过时。
	// 缓存未命中时降级为 JWT claims 中的值，不影响 auth 主流程。
	nickname := claims.Nickname
	avatar := claims.Avatar
	if cached, err := m.fetchCachedUser(ctx, claims.InternalUserID); err == nil && cached != nil {
		if cached.Nickname != "" {
			nickname = cached.Nickname
		}
		if cached.Avatar != "" {
			avatar = cached.Avatar
		}
	}

	conn.SetUserInfo(claims.InternalUserID, nickname, avatar)
	conn.SetStatus(connection.StatusAuthed)

	logger.Info("user authenticated successfully",
		"conn_id", conn.ConnID,
		"platform_user_id", claims.PlatformUserID,
		"internal_user_id", claims.InternalUserID,
		"nickname", nickname)

	m.sendSuccess(conn, req.Cmd, req.RequestID, map[string]interface{}{
		"user_id":  claims.InternalUserID,
		"nickname": nickname,
		"avatar":   avatar,
	})

	return nil
}

// cachedUser 仅反序列化所需字段，避免耦合 game/model 完整定义。
type cachedUser struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// fetchCachedUser 从 Redis 读取 game service 写入的用户缓存（key: cashparty:user:id:{id}）。
// 未命中返回 (nil, nil)；Redis 故障返回 (nil, error)，调用方降级为 JWT claims。
func (m *AuthMiddleware) fetchCachedUser(ctx context.Context, internalUserID string) (*cachedUser, error) {
	key := rediskeys.UserByIdKey(internalUserID)
	data, err := m.redis.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if data == "" {
		return nil, nil
	}
	var u cachedUser
	if err := json.Unmarshal([]byte(data), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (m *AuthMiddleware) sendError(conn *connection.Connection, cmd, requestID string, code int) {
	resp := protocol.NewErrorResponse(cmd, requestID, code)
	data, _ := resp.ToJSON()
	conn.Send(data)
}

func (m *AuthMiddleware) sendSuccess(conn *connection.Connection, cmd, requestID string, data interface{}) {
	resp := protocol.NewSuccessResponse(cmd, requestID, data)
	respData, _ := resp.ToJSON()
	conn.Send(respData)
}

// isLocked 检查 IP 是否被锁定（查 Redis）
func (m *AuthMiddleware) isLocked(ctx context.Context, ip string) bool {
	key := rediskeys.GatewayLockedIPKey(ip)
	exists, err := m.redis.Exists(ctx, key).Result()
	if err != nil {
		logger.Error("failed to check ip lock in redis",
			"ip", ip, "error", err)
		return false // Redis 故障时 fail-open，避免锁死所有用户
	}
	return exists > 0
}

// recordFailedAttempt 记录失败尝试（Redis INCR 持久化）
func (m *AuthMiddleware) recordFailedAttempt(ctx context.Context, ip string) {
	counterKey := rediskeys.GatewayAuthFailKey(ip)
	count, err := m.redis.Incr(ctx, counterKey).Result()
	if err != nil {
		logger.Error("failed to record failed attempt",
			"ip", ip, "error", err)
		return
	}
	if count == 1 {
		if err := m.redis.Expire(ctx, counterKey, m.counterWindow).Err(); err != nil {
			logger.Warn("failed to set expire on auth fail counter",
				"ip", ip, "error", err)
		}
	}

	if count >= int64(m.maxAttempts) {
		lockKey := rediskeys.GatewayLockedIPKey(ip)
		if err := m.redis.Set(ctx, lockKey, "1", m.lockDuration).Err(); err != nil {
			logger.Error("failed to set ip lock",
				"ip", ip, "error", err)
		}
		logger.Warn("IP locked due to too many failed attempts",
			"ip", ip, "attempts", count, "lock_duration", m.lockDuration)
	}
}

// clearFailedAttempts 清理失败计数（成功登录后调用）。
// Redis Cluster 模式下，单次 Del 多 key 要求所有 key 落在同一 slot，
// 而 auth_fail 与 locked_ip 前缀不同会跨 slot，触发 CROSSSLOT 错误，
// 因此改为逐个删除以保证 Cluster 兼容性。
func (m *AuthMiddleware) clearFailedAttempts(ctx context.Context, ip string) {
	counterKey := rediskeys.GatewayAuthFailKey(ip)
	lockKey := rediskeys.GatewayLockedIPKey(ip)
	if err := m.redis.Del(ctx, counterKey).Err(); err != nil {
		logger.Warn("failed to clear failed attempt counter",
			"ip", ip, "key", counterKey, "error", err)
	}
	if err := m.redis.Del(ctx, lockKey).Err(); err != nil {
		logger.Warn("failed to clear locked ip",
			"ip", ip, "key", lockKey, "error", err)
	}
}

// Stop 停止 Auth 中间件
func (m *AuthMiddleware) Stop() {
	m.cancel()
}
