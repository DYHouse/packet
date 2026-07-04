package bootstrap

import (
	"fmt"

	"github.com/cashparty/backend/common/kafka"
	cRedis "github.com/cashparty/backend/common/redis"
	gameconfig "github.com/cashparty/backend/game/config"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/broadcast"
	"github.com/cashparty/backend/game/infrastructure/messaging"
	settlementService "github.com/cashparty/backend/settlement/service"
	"gorm.io/gorm"
)

// createKafkaProducer 构造 game 服务的 Kafka Producer。
// brokers 为空时返回 error（配置守卫，PR-2 / CF-4）。
func createKafkaProducer(cfg *gameconfig.Config) (*kafka.Producer, error) {
	if len(cfg.Kafka.Brokers) == 0 {
		return nil, fmt.Errorf("kafka producer config: brokers must not be empty")
	}
	return kafka.NewProducer(kafka.ProducerConfig{Brokers: cfg.Kafka.Brokers})
}

// GroupID 策略（PLAN §5 Phase 7 任务 5 / §6 规约 CR-12）：
// GameEvent/RoomEvent 使用 per-node group（`{base}-{nodeID}`），每个节点消费全量消息，
// 靠 Redis SetNX 互斥保证恰好一次处理。这是有意设计，因为业务需要每个节点都感知事件
// （如 robot scheduler、connection manager）；若未来需要分区级负载均衡，可改为固定
// groupID 并移除 SetNX 互斥。

// createRoomEventConsumer 构造 RoomEventConsumer。
// GroupID 使用 per-node 策略：`game-room-events-{nodeID}`。
func createRoomEventConsumer(
	cfg *gameconfig.Config,
	dbRepo domain.DBRepository,
	redis *cRedis.Client,
	nodeID string,
) (*messaging.RoomEventConsumer, error) {
	consumerCfg := kafka.NewConsumerConfig(
		cfg.Kafka.Brokers,
		kafka.TopicRoomEvents,
		fmt.Sprintf("game-room-events-%s", nodeID),
	)
	return messaging.NewRoomEventConsumer(dbRepo, redis, consumerCfg)
}

// createGameEventConsumer 构造 GameEventConsumer。
// GroupID 使用 per-node 策略：`game-events-{nodeID}`。
func createGameEventConsumer(
	cfg *gameconfig.Config,
	db *gorm.DB,
	redis *cRedis.Client,
	settlementSvc *settlementService.SettlementService,
	robotBehaviorEngine messaging.RobotBehaviorEngineInterface,
	nodeID string,
) (*messaging.GameEventConsumer, error) {
	consumerCfg := kafka.NewConsumerConfig(
		cfg.Kafka.Brokers,
		kafka.TopicGameEvents,
		fmt.Sprintf("game-events-%s", nodeID),
	)
	return messaging.NewGameEventConsumer(db, redis, settlementSvc, robotBehaviorEngine, consumerCfg)
}

// createBroadcaster 构造 GameBroadcaster。
func createBroadcaster(
	cfg *gameconfig.Config,
	producer *kafka.Producer,
	redis *cRedis.Client,
) (*broadcast.GameBroadcaster, error) {
	return broadcast.NewGameBroadcaster(&cfg.Broadcast, producer, redis), nil
}
