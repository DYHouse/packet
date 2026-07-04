package broadcast

import (
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

type BroadcastMode string

const (
	ModeKafka       BroadcastMode = "kafka"
	ModeRedisPubSub BroadcastMode = "redis_pubsub"
)

// BroadcastChannelGateway 是 gateway 广播的 Redis Pub/Sub 频道名。
const BroadcastChannelGateway = "cashparty:gateway:broadcast"

type BroadcastFactory struct {
	config   *config.BroadcastConfig
	producer *kafka.Producer
	redis    *cRedis.Client
}

func NewBroadcastFactory(cfg *config.BroadcastConfig, producer *kafka.Producer, redis *cRedis.Client) *BroadcastFactory {
	return &BroadcastFactory{
		config:   cfg,
		producer: producer,
		redis:    redis,
	}
}

func (f *BroadcastFactory) CreateBroadcaster() Broadcaster {
	mode := BroadcastMode(f.config.Mode)

	switch mode {
	case ModeKafka:
		return f.createKafkaBroadcaster()
	case ModeRedisPubSub:
		return f.createRedisPubSubBroadcaster()
	default:
		logger.Warn("unknown broadcast mode, using redis_pubsub as default",
			"mode", mode,
			"supported_modes", []string{string(ModeKafka), string(ModeRedisPubSub)})
		return f.createRedisPubSubBroadcaster()
	}
}

func (f *BroadcastFactory) createKafkaBroadcaster() Broadcaster {
	topic := f.config.Kafka.Topic
	if topic == "" {
		topic = kafka.TopicGatewayBroadcast
	}

	logger.Info("creating kafka broadcaster", "topic", topic)
	return NewKafkaBroadcaster(f.producer, topic)
}

func (f *BroadcastFactory) createRedisPubSubBroadcaster() Broadcaster {
	channel := f.config.RedisPub.Channel
	if channel == "" {
		channel = BroadcastChannelGateway
	}

	logger.Info("creating redis pubsub broadcaster", "channel", channel)
	return NewRedisPubSubBroadcaster(f.redis, channel)
}

func (f *BroadcastFactory) GetMode() BroadcastMode {
	return BroadcastMode(f.config.Mode)
}
