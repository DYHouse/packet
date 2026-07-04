package kafka

// Topic 常量定义
const (
	// TopicGameEvents 游戏事件 topic
	TopicGameEvents = "cashparty.game.events"

	// TopicRoomEvents 房间事件 topic
	TopicRoomEvents = "cashparty.room.events"

	// TopicGatewayBroadcast 用于广播消息（原 common/broadcast/constants.go 中的
	// Kafka topic 常量合并至此）。
	TopicGatewayBroadcast = "cashparty.gateway.broadcast"
)
