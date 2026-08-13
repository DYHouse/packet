package bootstrap

import (
	"fmt"

	"github.com/cashparty/backend/common/kafka"
	cRedis "github.com/cashparty/backend/common/redis"
	gameconfig "github.com/cashparty/backend/game/config"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/infrastructure/broadcast"
	"github.com/cashparty/backend/game/infrastructure/messaging"
)

// createKafkaProducer 构造 game 服务的 Kafka Producer。
// brokers 为空时返回 error（配置守卫，PR-2 / CF-4）。
func createKafkaProducer(cfg *gameconfig.Config) (kafka.KafkaProducer, error) {
	if len(cfg.Kafka.Brokers) == 0 {
		return nil, fmt.Errorf("kafka producer config: brokers must not be empty")
	}
	return kafka.NewProducer(kafka.ProducerConfig{
		Brokers: cfg.Kafka.Brokers,
		SASL:    cfg.Kafka.SASL,
		TLS:     cfg.Kafka.TLS,
	})
}

// GroupID 策略（PLAN §5 Phase 7 任务 5 / §6 规约 CR-12）：
// GameEvent/RoomEvent 使用 per-node group（`{base}-{nodeID}`），每个节点消费全量消息，
// 靠 Redis SetNX 互斥保证恰好一次处理。这是有意设计，因为业务需要每个节点都感知事件
// （如 robot scheduler、connection manager）；若未来需要分区级负载均衡，可改为固定
// groupID 并移除 SetNX 互斥。

// createRoomEventConsumer 构造 RoomEventConsumer。
// GroupID 使用 per-node 策略：`game-room-events-{nodeID}`。
// 扣款已在 game 层同步完成（tryAutoSubstitute / SetReady），消费者侧不再需要 settleAppService 和 robotChecker。
func createRoomEventConsumer(
	cfg *gameconfig.Config,
	dbRepo repository.DBRepository,
	roomRepo repository.RoomRepository,
	redis cRedis.RedisClient,
	nodeID string,
) (*messaging.RoomEventConsumer, error) {
	consumerCfg := kafka.NewConsumerConfig(
		cfg.Kafka.Brokers,
		kafka.TopicRoomEvents,
		fmt.Sprintf("game-room-events-%s", nodeID),
	)
	consumerCfg.SASL = cfg.Kafka.SASL
	consumerCfg.TLS = cfg.Kafka.TLS
	return messaging.NewRoomEventConsumer(dbRepo, roomRepo, redis, consumerCfg)
}

// createGameEventConsumer 构造 GameEventConsumer。
// GroupID 使用 per-node 策略：`game-events-{nodeID}`。
// 业务编排逻辑通过 handler 注入，consumer 仅做消息解析与转发。
func createGameEventConsumer(
	cfg *gameconfig.Config,
	redis cRedis.RedisClient,
	handler messaging.GameEventHandlerInterface,
	nodeID string,
) (*messaging.GameEventConsumer, error) {
	consumerCfg := kafka.NewConsumerConfig(
		cfg.Kafka.Brokers,
		kafka.TopicGameEvents,
		fmt.Sprintf("game-events-%s", nodeID),
	)
	consumerCfg.SASL = cfg.Kafka.SASL
	consumerCfg.TLS = cfg.Kafka.TLS
	return messaging.NewGameEventConsumer(redis, handler, consumerCfg)
}

// createBroadcaster 构造 GameBroadcaster。
func createBroadcaster(
	cfg *gameconfig.Config,
	producer kafka.KafkaProducer,
	redis cRedis.RedisClient,
) (*broadcast.GameBroadcaster, error) {
	return broadcast.NewGameBroadcaster(&cfg.Broadcast, producer, redis), nil
}
