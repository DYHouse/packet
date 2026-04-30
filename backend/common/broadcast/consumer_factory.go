package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
)

type ConsumerFactory struct {
	config       *config.BroadcastConfig
	kafkaBrokers []string
	kafkaGroupID string
	redis        *cRedis.Client
}

func NewConsumerFactory(cfg *config.BroadcastConfig, kafkaBrokers []string, kafkaGroupID string, redis *cRedis.Client) *ConsumerFactory {
	return &ConsumerFactory{
		config:       cfg,
		kafkaBrokers: kafkaBrokers,
		kafkaGroupID: kafkaGroupID,
		redis:        redis,
	}
}

func (f *ConsumerFactory) CreateConsumer(handler MessageHandler) Consumer {
	mode := BroadcastMode(f.config.Mode)

	switch mode {
	case ModeKafka:
		return f.createKafkaConsumer(handler)
	case ModeRedisPubSub:
		return f.createRedisPubSubConsumer(handler)
	default:
		logger.Warn("unknown broadcast mode, using redis_pubsub as default", "mode", mode)
		return f.createRedisPubSubConsumer(handler)
	}
}

func (f *ConsumerFactory) createKafkaConsumer(handler MessageHandler) Consumer {
	topic := f.config.Kafka.Topic
	if topic == "" {
		topic = BroadcastTopicKafka
	}

	logger.Info("creating kafka consumer", "topic", topic, "group_id", f.kafkaGroupID)

	wrapper := func(ctx context.Context, msg kafka.Message) error {
		broadcastMsg, err := message.ParseBroadcastMessage(msg.Value)
		if err != nil {
			logger.Error("failed to parse broadcast message", "error", err)
			return err
		}

		return handler(ctx, broadcastMsg)
	}

	consumer := kafka.NewConsumer(f.kafkaBrokers, topic, f.kafkaGroupID, wrapper)
	return NewKafkaConsumer(consumer)
}

func (f *ConsumerFactory) createRedisPubSubConsumer(handler MessageHandler) Consumer {
	channel := f.config.RedisPub.Channel
	if channel == "" {
		channel = BroadcastChannelGateway
	}

	logger.Info("creating redis pubsub consumer", "channel", channel)
	return NewRedisPubSubConsumer(f.redis, channel, handler)
}
