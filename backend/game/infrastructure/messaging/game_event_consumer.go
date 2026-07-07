package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/cashparty/backend/common/kafka"
	lockScripts "github.com/cashparty/backend/common/lock/scripts"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/trace"
	"github.com/cashparty/backend/game/domain"
	redisKeys "github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/google/uuid"
)

// GameEventHandlerInterface 抽象游戏事件处理逻辑，供 GameEventConsumer 调用。
// 实现方位于 application 层（如 application.GameEventHandler），consumer 仅做消息解析与转发。
type GameEventHandlerInterface interface {
	HandleGameEvent(ctx context.Context, event *domain.GameEvent) error
}

// GameEventConsumer 仅负责消息解析、Redis 幂等抢占与错误回报。
// 业务编排逻辑已迁移至 application.GameEventHandler（通过 handler 字段注入）。
type GameEventConsumer struct {
	redis    *cRedis.Client
	handler  GameEventHandlerInterface
	consumer *kafka.Consumer
}

func NewGameEventConsumer(
	redis *cRedis.Client,
	handler GameEventHandlerInterface,
	cfg kafka.ConsumerConfig,
) (*GameEventConsumer, error) {
	c := &GameEventConsumer{
		redis:   redis,
		handler: handler,
	}
	consumer, err := kafka.NewConsumer(cfg, c.HandleEvent, nil)
	if err != nil {
		return nil, fmt.Errorf("create game event kafka consumer failed: %w", err)
	}
	c.consumer = consumer
	return c, nil
}

func (c *GameEventConsumer) Start(ctx context.Context) error {
	logger.Info("game event consumer started")
	return c.consumer.Start(ctx)
}

// Close 委托给内部 kafka.Consumer，由 bootstrap 统一管理生命周期。
func (c *GameEventConsumer) Close() error {
	return c.consumer.Close()
}

// HandleEvent 仅做：解析消息 → 恢复 TraceID → Redis 幂等抢占 → 调 handler → 错误回报。
// 业务编排逻辑全部由 handler 处理。
func (c *GameEventConsumer) HandleEvent(ctx context.Context, msg kafka.Message) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("game event consumer panic",
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()

	var event domain.GameEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("unmarshal game event failed", "error", err)
		return fmt.Errorf("unmarshal event failed: %w", err)
	}

	// 从 event 恢复 TraceID 到 context，使下游日志/DB 操作可关联
	if event.TraceID != "" {
		ctx = trace.WithTraceID(ctx, event.TraceID)
	}

	acquired, token, acquireErr := c.tryAcquire(ctx, event.TraceID)
	if acquireErr != nil {
		// Redis 不可用（fail-closed），返回 error 让 Kafka 重试
		return acquireErr
	}
	if !acquired {
		logger.Warn("event already processed", "trace_id", event.TraceID)
		return nil
	}

	// 仅在处理失败时释放抢占锁，让 Kafka 重试能重新进入；
	// 处理成功时保留锁作为 7 天幂等标记，防止重复消费。
	shouldRelease := false
	defer func() {
		if !shouldRelease {
			return
		}
		if releaseErr := c.releaseAcquire(ctx, event.TraceID, token); releaseErr != nil {
			logger.Warn("failed to release acquire lock",
				"trace_id", event.TraceID,
				"error", releaseErr)
		}
	}()

	// 转发到 application 层 handler 处理业务逻辑
	if err := c.handler.HandleGameEvent(ctx, &event); err != nil {
		shouldRelease = true
		logger.Error("handle game event failed",
			"event_type", event.EventType,
			"trace_id", event.TraceID,
			"error", err)
		return err
	}

	return nil
}

// tryAcquire 用 SetNX 原子抢占事件处理权。
// 返回 (true, token, nil) 表示抢占成功（首次处理），token 为本次持有的随机值，释放锁时需传入。
// 返回 (false, "", nil) 表示已被其他 consumer 处理过（幂等跳过）。
// 返回 (false, "", err) 表示 Redis 不可用（fail-closed），调用方应返回 error 让 Kafka 重试。
// token 用于 releaseAcquire 校验持有者，防止 TTL 过期后误删其他实例的锁（§7.4/§15.5）。
// 业务侧幂等（DB 唯一索引/FirstOrCreate/状态机）仍作为兜底防线。
func (c *GameEventConsumer) tryAcquire(ctx context.Context, traceID string) (bool, string, error) {
	if c.redis == nil {
		return true, "", nil
	}
	token := uuid.New().String()
	key := redisKeys.GameEventProcessedKey(traceID)
	ok, err := c.redis.SetNX(ctx, key, token, 7*24*time.Hour).Result()
	if err != nil {
		logger.Error("tryAcquire SetNX failed, fail-closed to prevent duplicate processing",
			"trace_id", traceID,
			"error", err)
		return false, "", fmt.Errorf("tryAcquire SetNX failed: %w", err)
	}
	return ok, token, nil
}

// releaseAcquire 处理失败时释放抢占，让 Kafka 重试能重新进入。
// 通过 Lua 脚本原子校验 token 后才 DEL，防止 TTL 过期后被其他实例抢占，原持有者误删新持有者的锁（§7.4/§15.5）。
func (c *GameEventConsumer) releaseAcquire(ctx context.Context, traceID string, token string) error {
	if c.redis == nil {
		return nil
	}
	key := redisKeys.GameEventProcessedKey(traceID)
	return lockScripts.ReleaseLockScript.Run(ctx, c.redis, []string{key}, token).Err()
}
