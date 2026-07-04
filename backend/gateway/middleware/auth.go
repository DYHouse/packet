package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway"
	"github.com/cashparty/backend/gateway/connection"
	"github.com/cashparty/backend/gateway/service"
)

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrInvalidToken          = errors.New("invalid token")
	ErrTokenExpired          = errors.New("token expired")
	ErrAuthFailed            = errors.New("authentication failed")
	ErrTooManyFailedAttempts = errors.New("too many failed authentication attempts")
)

type AuthMiddleware struct {
	tokenService   *service.TokenService
	redis          *cRedis.Client
	failedAttempts sync.Map
	maxAttempts    int
	lockDuration   time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func NewAuthMiddleware(tokenService *service.TokenService, redis *cRedis.Client) *AuthMiddleware {
	ctx, cancel := context.WithCancel(context.Background())
	m := &AuthMiddleware{
		tokenService: tokenService,
		redis:        redis,
		maxAttempts:  5,
		lockDuration: 15 * time.Minute,
		ctx:          ctx,
		cancel:       cancel,
	}
	m.wg.Add(1)
	go m.cleanupRoutine()
	return m
}

func (m *AuthMiddleware) OnConnect(ctx context.Context, conn *connection.Connection, firstMessage []byte) error {
	var req message.Request
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

	if m.isLocked(conn.IP) {
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

	m.clearFailedAttempts(conn.IP)

	conn.SetUserInfo(claims.InternalUserID, claims.Nickname, claims.Avatar)
	conn.SetStatus(connection.StatusAuthed)

	logger.Info("user authenticated successfully",
		"conn_id", conn.ConnID,
		"platform_user_id", claims.PlatformUserID,
		"internal_user_id", claims.InternalUserID,
		"nickname", claims.Nickname)

	m.sendSuccess(conn, req.Cmd, req.RequestID, map[string]interface{}{
		"user_id":  claims.InternalUserID,
		"nickname": claims.Nickname,
		"avatar":   claims.Avatar,
	})

	return nil
}

func (m *AuthMiddleware) sendError(conn *connection.Connection, cmd, requestID string, code int) {
	resp := message.NewErrorResponse(cmd, requestID, code)
	data, _ := resp.ToJSON()
	conn.Send(data)
}

func (m *AuthMiddleware) sendSuccess(conn *connection.Connection, cmd, requestID string, data interface{}) {
	resp := message.NewSuccessResponse(cmd, requestID, data)
	respData, _ := resp.ToJSON()
	conn.Send(respData)
}

func (m *AuthMiddleware) isLocked(ip string) bool {
	attempts, ok := m.failedAttempts.Load(ip)
	if !ok {
		return false
	}
	return attempts.(int) >= m.maxAttempts
}

func (m *AuthMiddleware) recordFailedAttempt(ctx context.Context, ip string) {
	attempts, _ := m.failedAttempts.LoadOrStore(ip, 0)
	newAttempts := attempts.(int) + 1
	m.failedAttempts.Store(ip, newAttempts)

	if newAttempts >= m.maxAttempts {
		key := gateway.GatewayLockedIPKey(ip)
		m.redis.Set(ctx, key, "1", m.lockDuration)
		logger.Warn("IP locked due to too many failed attempts", "ip", ip, "attempts", newAttempts)
	}
}

func (m *AuthMiddleware) clearFailedAttempts(ip string) {
	m.failedAttempts.Delete(ip)
}

func (m *AuthMiddleware) cleanupRoutine() {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("cleanup routine panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.failedAttempts.Range(func(key, value interface{}) bool {
				m.failedAttempts.Delete(key)
				return true
			})
		}
	}
}

func (m *AuthMiddleware) Stop() {
	m.cancel()
	// v3 新增：等待 cleanupRoutine 退出
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("auth middleware stop timeout")
	}
}
