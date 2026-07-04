package broadcast

import (
	"context"
	"runtime/debug"
	"sync"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/redis/go-redis/v9"
)

type RedisPubSubConsumer struct {
	redis   *cRedis.Client
	channel string
	handler MessageHandler
	pubsub  *redis.PubSub
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewRedisPubSubConsumer(redis *cRedis.Client, channel string, handler MessageHandler) *RedisPubSubConsumer {
	if channel == "" {
		channel = BroadcastChannelGateway
	}

	return &RedisPubSubConsumer{
		redis:   redis,
		channel: channel,
		handler: handler,
	}
}

func (c *RedisPubSubConsumer) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx) // 派生自参数 ctx

	c.pubsub = c.redis.Subscribe(c.ctx, c.channel)

	_, err := c.pubsub.Receive(c.ctx)
	if err != nil {
		logger.Error("failed to subscribe to redis channel", "channel", c.channel, "error", err)
		return err
	}

	c.wg.Add(1)
	go c.consumeMessages()

	logger.Info("redis pubsub consumer started", "channel", c.channel)
	return nil
}

func (c *RedisPubSubConsumer) consumeMessages() {
	defer c.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("consume messages panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	ch := c.pubsub.Channel()

	for {
		select {
		case <-c.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
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
	}

	if c.pubsub != nil {
		c.pubsub.Close()
	}

	c.wg.Wait()
	return nil
}
