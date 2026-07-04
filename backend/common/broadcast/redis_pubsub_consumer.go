package broadcast

import (
	"context"
	"fmt"
	"math/rand"
	"runtime/debug"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/redis/go-redis/v9"
)

type RedisPubSubConsumer struct {
	redis   *cRedis.Client
	channel string
	handler MessageHandler
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewRedisPubSubConsumer(redis *cRedis.Client, channel string, handler MessageHandler) *RedisPubSubConsumer {
	if channel == "" {
		channel = BroadcastChannelGateway
	}

	return &RedisPubSubConsumer{
		redis:   redis,
		channel: channel,
		handler: handler,
		done:    make(chan struct{}),
	}
}

// Start runs the reconnect loop until ctx is cancelled. On disconnect it
// reconnects with exponential backoff (initial 1s, cap 30s) plus jitter.
func (c *RedisPubSubConsumer) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	defer close(c.done)

	backoff := time.Second
	attempt := 0

	for {
		if err := c.ctx.Err(); err != nil {
			return nil
		}

		pubsub := c.redis.Subscribe(c.ctx, c.channel)

		if _, err := pubsub.Receive(ctx); err != nil {
			_ = pubsub.Close()
			attempt++
			logger.Warn("redis pubsub reconnecting",
				"channel", c.channel,
				"attempt", attempt,
				"backoff", backoff,
				"error", err)
			select {
			case <-c.ctx.Done():
				return nil
			case <-time.After(backoff + time.Duration(rand.Intn(100))*time.Millisecond):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}

		if attempt > 0 {
			logger.Info("redis pubsub reconnected", "channel", c.channel, "attempt", attempt)
		} else {
			logger.Info("redis pubsub consumer started", "channel", c.channel)
		}
		backoff = time.Second
		attempt = 0

		err := c.consumeMessages(pubsub)
		_ = pubsub.Close()
		if err == nil {
			return nil
		}
		if c.ctx.Err() != nil {
			return nil
		}
		attempt++
		logger.Warn("redis pubsub reconnecting",
			"channel", c.channel,
			"attempt", attempt,
			"backoff", backoff,
			"error", err)
		select {
		case <-c.ctx.Done():
			return nil
		case <-time.After(backoff + time.Duration(rand.Intn(100))*time.Millisecond):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// consumeMessages blocks on the pubsub channel until either ctx is cancelled
// (returns nil) or the channel closes (returns error to trigger reconnect).
func (c *RedisPubSubConsumer) consumeMessages(pubsub *redis.PubSub) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("consume messages panic",
				"panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("consume messages panic: %v", r)
		}
	}()

	ch := pubsub.Channel()

	for {
		select {
		case <-c.ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("redis pubsub channel closed: %s", c.channel)
			}

			broadcastMsg, err := message.ParseBroadcastMessage([]byte(msg.Payload))
			if err != nil {
				logger.Error("failed to parse broadcast message", "error", err)
				continue
			}

			if err := c.handler(c.ctx, broadcastMsg); err != nil {
				logger.Error("failed to handle broadcast message", "error", err)
			}
		}
	}
}

func (c *RedisPubSubConsumer) Close() error {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	return nil
}
