package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/broadcast"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

type GameBroadcaster struct {
	broadcaster broadcast.Broadcaster
}

func NewGameBroadcaster(cfg *config.BroadcastConfig, producer *kafka.Producer, redis *cRedis.Client) *GameBroadcaster {
	factory := broadcast.NewBroadcastFactory(cfg, producer, redis)

	return &GameBroadcaster{
		broadcaster: factory.CreateBroadcaster(),
	}
}

// Broadcast 房间广播。失败时记 Warn 日志（含业务上下文 roomID/event），不阻塞主流程。
// 选择 Warn 而非 Error：广播失败是预期内可恢复故障，玩家可通过重连拉状态恢复；
// 底层 Kafka/RedisBroadcaster 已在 Error 级别记录发送失败，本层只补业务上下文。
func (b *GameBroadcaster) Broadcast(ctx context.Context, roomID string, event string, data interface{}, excludeUserID string) error {
	if err := b.broadcaster.Broadcast(ctx, roomID, event, data, excludeUserID); err != nil {
		logger.Warn("game broadcast failed",
			"room_id", roomID,
			"event", event,
			"exclude_user_id", excludeUserID,
			"error", err)
		return err
	}
	return nil
}

// BroadcastToUser 单用户推送。失败时记 Warn 日志，不阻塞主流程。
func (b *GameBroadcaster) BroadcastToUser(ctx context.Context, userID string, event string, data interface{}) error {
	if err := b.broadcaster.BroadcastToUser(ctx, userID, event, data); err != nil {
		logger.Warn("game broadcast to user failed",
			"user_id", userID,
			"event", event,
			"error", err)
		return err
	}
	return nil
}
