package bootstrap

import (
	"fmt"

	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway/broadcast"
	gatewayconfig "github.com/cashparty/backend/gateway/config"
	"github.com/cashparty/backend/gateway/connection"
)

// 注意：gateway 不需要 createKafkaProducer。
// Phase 4（P1-22）已删除 gateway 的 Kafka Producer：BroadcastService 内部通过
// ConsumerFactory 创建自己的 consumer，不依赖外部 Producer。

// GroupID 策略（PLAN §5 Phase 7 任务 5 / §6 规约 CR-12）：
// gateway 广播使用 per-node group（`gateway-broadcast-{nodeID}`），每个节点消费全量
// 广播消息，再通过 IsUserConnectedLocally 过滤本地连接。这是广播语义所必需的：
// 每个节点都必须看到每条消息，以判断目标用户是否连接在本节点。

// createBroadcastService 构造 gateway 的 BroadcastService。
// GroupID 使用 per-node 策略：`gateway-broadcast-{nodeID}`。
func createBroadcastService(
	cfg *gatewayconfig.Config,
	redis cRedis.RedisClient,
	manager *connection.Manager,
	nodeID string,
) (*broadcast.BroadcastService, error) {
	kafkaGroupID := fmt.Sprintf("gateway-broadcast-%s", nodeID)
	return broadcast.NewBroadcastService(manager, redis, &cfg.Broadcast, cfg.Kafka.Brokers, kafkaGroupID), nil
}
