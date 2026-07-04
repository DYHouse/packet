package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/mysql"
	statsconfig "github.com/cashparty/backend/stats/config"
	"github.com/cashparty/backend/stats/handler"
	"github.com/cashparty/backend/stats/repository"
	"github.com/cashparty/backend/stats/service"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Container 持有 stats 服务运行时所需的所有依赖.
type Container struct {
	Config       *statsconfig.Config
	DB           *gorm.DB
	Redis        *redis.Client
	StatsRepo    *repository.StatsRepository
	StatsService *service.StatsService
	StatsHandler *handler.StatsHandler
}

// NewContainer 初始化 DB、Redis (可选)、repository、service、handler.
func NewContainer(cfg *statsconfig.Config) (*Container, error) {
	db, err := mysql.NewDB(&cfg.MySQL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect mysql: %w", err)
	}

	statsRepo := repository.NewStatsRepository(db, cfg.AmountRanges)

	// Redis 缓存 (可选, 连接失败则降级为无缓存)
	var rdb *redis.Client
	if cfg.Redis.Addr != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			PoolSize: cfg.Redis.PoolSize,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := rdb.Ping(ctx).Err(); err != nil {
			logger.Warn("redis connection failed, running without cache", "error", err)
			rdb = nil
		} else {
			logger.Info("redis connected", "addr", cfg.Redis.Addr)
		}
		cancel()
	}

	statsService := service.NewStatsService(statsRepo, rdb)
	statsHandler := handler.NewStatsHandler(statsService)

	return &Container{
		Config:       cfg,
		DB:           db,
		Redis:        rdb,
		StatsRepo:    statsRepo,
		StatsService: statsService,
		StatsHandler: statsHandler,
	}, nil
}

// Stop 关闭底层资源 (Redis 等).
func (c *Container) Stop() {
	if c.Redis != nil {
		c.Redis.Close()
	}
}
