package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/cashparty/backend/common/logger"
	statsconfig "github.com/cashparty/backend/stats/config"
	"github.com/gin-gonic/gin"
)

// Application 是 stats 服务的应用入口, 持有 Container、HTTP server 及生命周期协调原语.
type Application struct {
	Container *Container
	cfg       *statsconfig.Config
	server    *http.Server
	cancel    context.CancelFunc
	appCtx    context.Context
	wg        sync.WaitGroup
	errChan   chan error
}

// NewApplication 加载配置、初始化 logger、创建 Container 并组装 HTTP server.
func NewApplication(cfgPath string) (*Application, error) {
	cfg, err := statsconfig.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	logger.Init(&logger.LogConfig{
		Level:    cfg.Log.Level,
		Filename: cfg.Log.Filename,
	})

	logger.Info("stats service starting",
		"name", cfg.Server.Name,
		"port", cfg.Server.Port,
	)

	appCtx, cancel := context.WithCancel(context.Background())
	app := &Application{
		cfg:     cfg,
		cancel:  cancel,
		appCtx:  appCtx,
		errChan: make(chan error, 1),
	}

	container, err := NewContainer(app.cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create container failed: %w", err)
	}
	app.Container = container

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(corsMiddleware())

	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := engine.Group("/api/v1")
	app.Container.StatsHandler.RegisterRoutes(api)

	app.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return app, nil
}

// Start 启动 HTTP server goroutine (v3: wg + recover, 失败发 errChan 而非 logger.Fatal).
func (a *Application) Start(ctx context.Context) error {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("stats http server panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		logger.Info("stats server listening", "addr", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.errChan <- err
		}
	}()
	return nil
}

// Stop 优雅关闭: HTTP server -> 等待 goroutine -> 底层资源.
func (a *Application) Stop() error {
	logger.Info("shutting down stats service...")

	// 1. Shutdown HTTP server (5s 超时)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil {
		logger.Error("stats server shutdown error", "error", err)
	}

	// 2. 等待 goroutine 退出
	if a.cancel != nil {
		a.cancel()
	}
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warn("stats goroutines wait timeout")
	}

	// 3. 关闭底层资源
	if a.Container != nil {
		a.Container.Stop()
	}

	logger.Info("stats service stopped")
	return nil
}

// Wait 阻塞直到 errChan 收到错误 (服务异常退出).
func (a *Application) Wait() error {
	return <-a.errChan
}

// Run 是 stats 服务的入口: 解析配置路径 -> 创建 -> 启动 -> 等待信号/错误 -> 停止.
func Run() {
	cfgPath := "config/stats.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	app, err := NewApplication(cfgPath)
	if err != nil {
		logger.Fatal("failed to create application", "error", err)
	}

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		logger.Fatal("failed to start application", "error", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		logger.Info("received shutdown signal")
	case err := <-app.errChan:
		logger.Error("application error", "error", err)
	}

	if err := app.Stop(); err != nil {
		logger.Error("failed to stop application", "error", err)
	}
}

// corsMiddleware 设置 CORS 响应头并处理 OPTIONS 预检请求.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
