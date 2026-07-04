package server

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/utils"
	"github.com/cashparty/backend/gateway/broadcast"
	"github.com/cashparty/backend/gateway/connection"
	"github.com/cashparty/backend/gateway/handler"
	"github.com/cashparty/backend/gateway/health"
	"github.com/cashparty/backend/gateway/middleware"
	"github.com/cashparty/backend/gateway/router"
	"github.com/cashparty/backend/gateway/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type Config struct {
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ReadBufferSize  int
	WriteBufferSize int
	SendQueueSize   int
	AllowedOrigins  []string
	TestEnabled     bool
}

type Server struct {
	config              *Config
	connMgr             *connection.Manager
	auth                *middleware.AuthMiddleware
	router              *router.MessageRouter
	broadcast           *broadcast.BroadcastService
	rateLimiter         *middleware.RateLimiter
	healthChecker       *health.HealthChecker
	signatureMiddleware *middleware.SignatureMiddleware
	gameHandler         *handler.GameHandler
	testService         *service.TestService
	upgrader            websocket.Upgrader
	engine              *gin.Engine
	httpServer          *http.Server
	wg                  sync.WaitGroup
}

func NewServer(
	config *Config,
	connMgr *connection.Manager,
	auth *middleware.AuthMiddleware,
	router *router.MessageRouter,
	broadcast *broadcast.BroadcastService,
	rateLimiter *middleware.RateLimiter,
	healthChecker *health.HealthChecker,
	signatureMiddleware *middleware.SignatureMiddleware,
	gameHandler *handler.GameHandler,
	testService *service.TestService,
) *Server {
	gin.SetMode(gin.ReleaseMode)

	s := &Server{
		config:              config,
		connMgr:             connMgr,
		auth:                auth,
		router:              router,
		broadcast:           broadcast,
		rateLimiter:         rateLimiter,
		healthChecker:       healthChecker,
		signatureMiddleware: signatureMiddleware,
		gameHandler:         gameHandler,
		testService:         testService,
		upgrader: websocket.Upgrader{
			ReadBufferSize:   config.ReadBufferSize,
			WriteBufferSize:  config.WriteBufferSize,
			HandshakeTimeout: 10 * time.Second,
			CheckOrigin: func(r *http.Request) bool {
				if len(config.AllowedOrigins) == 0 {
					return true
				}
				for _, o := range config.AllowedOrigins {
					if o == "*" {
						return true
					}
				}
				origin := r.Header.Get("Origin")
				return utils.ContainsString(config.AllowedOrigins, origin)
			},
		},
		engine: gin.New(),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.engine.Use(gin.Recovery())
	s.engine.Use(requestLogger())

	s.engine.GET("/health", s.healthChecker.CheckHealth)
	s.engine.GET("/ready", s.healthChecker.CheckReady)
	s.engine.GET("/live", s.healthChecker.CheckLive)

	// 测试 token 接口仅在显式开启时注册，生产环境默认关闭
	if s.config.TestEnabled {
		s.engine.POST("/test/token", s.handleTestToken)
	}

	wsGroup := s.engine.Group("")
	wsGroup.Use(middleware.RateLimitMiddleware(s.rateLimiter))
	wsGroup.GET("/ws", s.handleWebSocket)

	game := s.engine.Group("/game")
	game.Use(s.signatureMiddleware.VerifySignature())
	game.GET("/list", s.gameHandler.GetGameList)
	game.POST("/start", s.gameHandler.StartGame)
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	logger.Info("gateway server starting", "addr", addr)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.engine,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
	}

	return s.httpServer.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) {
	logger.Info("gateway server stopping")

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			logger.Error("http server shutdown error", "error", err)
			if closeErr := s.httpServer.Close(); closeErr != nil {
				logger.Error("http server close error", "error", closeErr)
			}
		}
	}

	closeMsg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
	s.connMgr.Range(func(conn *connection.Connection) bool {
		conn.WebSocketConn().WriteMessage(websocket.CloseMessage, closeMsg)
		return true
	})

	done := make(chan struct{})
	go func() {
		s.connMgr.WaitForAllConnectionsClose(30 * time.Second)
		close(done)
	}()

	select {
	case <-done:
		logger.Info("all connections closed gracefully")
	case <-ctx.Done():
		logger.Warn("graceful shutdown timeout, forcing close")
		s.connMgr.CloseAll()
	}

	s.connMgr.Stop()
	s.broadcast.Stop()
	s.auth.Stop()

	// v3 新增：等待 connection goroutines 退出
	waitDone := make(chan struct{})
	go func() { s.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-ctx.Done():
		logger.Warn("server goroutines wait timeout")
	}

	logger.Info("gateway server stopped")
}

func (s *Server) handleWebSocket(c *gin.Context) {
	token := c.Query("token")

	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": message.CodeInvalidParams,
			"msg":  "token is required",
		})
		return
	}

	wsConn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("websocket upgrade failed", "error", err)
		return
	}

	connID := utils.GenerateConnID()
	conn := connection.NewConnection(connID, wsConn, s.config.SendQueueSize)
	conn.IP = utils.GetClientIP(c.Request)
	conn.UserAgent = c.Request.UserAgent()

	ctx := c.Request.Context()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("handle connection panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		s.handleConnection(ctx, conn, token)
	}()
}

func (s *Server) handleConnection(ctx context.Context, conn *connection.Connection, token string) {
	defer s.cleanupConnection(conn)

	authReq := fmt.Sprintf(`{"cmd":"auth","request_id":"auth_%s","data":{"token":"%s"},"timestamp":%d}`,
		conn.ConnID, token, time.Now().UnixMilli())

	if err := s.auth.OnConnect(ctx, conn, []byte(authReq)); err != nil {
		logger.Warn("authentication failed", "conn_id", conn.ConnID, "error", err)
		return
	}

	roomID := s.connMgr.GetPlayerRoom(conn.UserID)

	if roomID != "" {
		s.handleReconnect(ctx, conn, roomID)
	} else {
		if err := s.connMgr.Register(conn); err != nil {
			logger.Error("failed to register connection", "conn_id", conn.ConnID, "error", err)
			s.sendError(conn, "", "", message.CodeConnectionLimit)
			return
		}
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("write and heartbeat pump panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		s.writeAndHeartbeatPump(conn)
	}()
	s.readPump(ctx, conn)
}

func (s *Server) handleReconnect(ctx context.Context, conn *connection.Connection, roomID string) {
	s.connMgr.CleanupOldConnection(conn.UserID)

	if err := s.connMgr.Register(conn); err != nil {
		s.sendError(conn, "", "", message.CodeConnectionLimit)
		return
	}

	reconnectReq := fmt.Sprintf(
		`{"cmd":"reconnect","request_id":"rc_%s","data":{"room_id":"%s","user_id":"%s"}}`,
		conn.ConnID, roomID, conn.UserID,
	)

	s.router.Route(ctx, conn, []byte(reconnectReq))
}

func (s *Server) readPump(ctx context.Context, conn *connection.Connection) {
	defer conn.Close()

	conn.WebSocketConn().SetReadLimit(512 * 1024)
	conn.WebSocketConn().SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.WebSocketConn().SetPongHandler(func(string) error {
		conn.WebSocketConn().SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.UpdateHeartbeat()
		logger.Info("received pong, heartbeat updated", "conn_id", conn.ConnID)
		return nil
	})

	for {
		_, messageData, err := conn.WebSocketConn().ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Error("websocket read error", "conn_id", conn.ConnID, "error", err)
			}
			break
		}

		conn.WebSocketConn().SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.UpdateHeartbeat()
		logger.Debug("received message, heartbeat updated", "conn_id", conn.ConnID)
		s.router.Route(ctx, conn, messageData)
	}
}

func (s *Server) writeAndHeartbeatPump(conn *connection.Connection) {
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer func() {
		heartbeatTicker.Stop()
		conn.Close()
	}()

	for {
		select {
		case <-conn.CloseChan():
			conn.WebSocketConn().WriteMessage(websocket.CloseMessage, []byte{})
			return
		case msgData, ok := <-conn.SendChan():
			if !ok {
				conn.WebSocketConn().WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			conn.WebSocketConn().SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WebSocketConn().WriteMessage(websocket.TextMessage, msgData); err != nil {
				return
			}
		case <-heartbeatTicker.C:
			if conn.IsHeartbeatTimeout() {
				logger.Warn("connection heartbeat timeout", "conn_id", conn.ConnID)
				return
			}
			conn.WebSocketConn().SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WebSocketConn().WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) cleanupConnection(conn *connection.Connection) {
	switch conn.GetStatus() {
	case connection.StatusAuthed:
		s.connMgr.MarkDisconnected(conn)
	case connection.StatusDisconnected:
		s.connMgr.Unregister(conn.ConnID)
	}
	conn.Close()
}

func (s *Server) sendError(conn *connection.Connection, cmd, requestID string, code int) {
	resp := message.NewErrorResponse(cmd, requestID, code)
	data, _ := resp.ToJSON()
	conn.Send(data)
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logger.Info("http request",
			"method", c.Request.Method,
			"path", path,
			"query", query,
			"status", status,
			"latency", latency,
			"ip", c.ClientIP(),
		)
	}
}

func (s *Server) handleTestToken(c *gin.Context) {
	var req service.TestTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "Invalid request: " + err.Error(),
			"data": nil,
		})
		return
	}

	resp, err := s.testService.GenerateTestToken(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  "Failed to generate token: " + err.Error(),
			"data": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{
			"token":    resp.Token,
			"user_id":  resp.UserID,
			"nickname": resp.Nickname,
		},
	})
}
