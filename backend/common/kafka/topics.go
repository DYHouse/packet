package kafka

// Topic常量定义
const (
	// 游戏事件
	TopicGameEvents      = "cashparty.game.events"
	TopicGameResult      = "cashparty.game.result"
	TopicRoundResult     = "cashparty.round.result"
	TopicSettlement      = "cashparty.settlement"
	TopicSettlementReply = "cashparty.settlement.reply"

	// 玩家事件
	TopicPlayerAction = "cashparty.player.action"
	TopicPlayerNotify = "cashparty.player.notify"

	// 风控事件
	TopicRiskControl = "cashparty.risk.control"
	TopicRiskResult  = "cashparty.risk.result"

	// 算法服务
	TopicAlgorithmRequest = "cashparty.algorithm.request"
	TopicAlgorithmResult  = "cashparty.algorithm.result"

	// 运营事件
	TopicAuditLog = "cashparty.audit.log"
	TopicMetrics  = "cashparty.metrics"

	// Gateway 广播
	TopicGatewayBroadcast = "cashparty.gateway.broadcast"

	// 房间事件
	TopicRoomEvents = "cashparty.room.events"
)
