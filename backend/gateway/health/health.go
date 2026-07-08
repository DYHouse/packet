package health

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway/connection"
	"github.com/gin-gonic/gin"
)

type HealthChecker struct {
	redis   cRedis.RedisClient
	connMgr *connection.Manager
}

func NewHealthChecker(redis cRedis.RedisClient, connMgr *connection.Manager) *HealthChecker {
	return &HealthChecker{
		redis:   redis,
		connMgr: connMgr,
	}
}

type HealthStatus struct {
	Status          string                 `json:"status"`
	Service         string                 `json:"service"`
	Timestamp       int64                  `json:"timestamp"`
	Connections     int64                  `json:"connections"`
	MaxConnections  int                    `json:"max_connections"`
	Dependencies    map[string]interface{} `json:"dependencies"`
	SystemResources map[string]interface{} `json:"system_resources"`
	Uptime          int64                  `json:"uptime_seconds"`
}

func (h *HealthChecker) CheckHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	status := &HealthStatus{
		Status:       "ok",
		Service:      "gateway",
		Timestamp:    time.Now().UnixMilli(),
		Dependencies: make(map[string]interface{}),
	}

	status.Connections = h.connMgr.GetConnectionCount()

	redisHealth := h.checkRedis(ctx)
	status.Dependencies["redis"] = redisHealth
	if redisHealth["status"] != "ok" {
		status.Status = "degraded"
	}

	status.SystemResources = h.getSystemResources()

	c.JSON(http.StatusOK, status)
}

func (h *HealthChecker) checkRedis(ctx context.Context) map[string]interface{} {
	result := make(map[string]interface{})

	start := time.Now()
	err := h.redis.Raw().Ping(ctx).Err()
	latency := time.Since(start)

	if err != nil {
		result["status"] = "error"
		result["error"] = err.Error()
		logger.Error("redis health check failed", "error", err)
	} else {
		result["status"] = "ok"
		result["latency_ms"] = latency.Milliseconds()
	}

	return result
}

func (h *HealthChecker) getSystemResources() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]interface{}{
		"goroutines":  runtime.NumGoroutine(),
		"cpu_cores":   runtime.NumCPU(),
		"memory_mb":   m.Alloc / 1024 / 1024,
		"heap_mb":     m.HeapAlloc / 1024 / 1024,
		"stack_mb":    m.StackInuse / 1024 / 1024,
		"gc_pause_ns": m.PauseTotalNs,
	}
}

func (h *HealthChecker) CheckReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	err := h.redis.Raw().Ping(ctx).Err()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not_ready",
			"error":  "redis not available",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}

func (h *HealthChecker) CheckLive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "alive",
	})
}
