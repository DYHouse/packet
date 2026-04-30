package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/broadcast"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/kafka"
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

func (b *GameBroadcaster) Broadcast(roomID string, event string, data interface{}, excludeUserID string) {
	_ = b.broadcaster.Broadcast(context.Background(), roomID, event, data, excludeUserID)
}

func (b *GameBroadcaster) BroadcastToUser(userID string, event string, data interface{}) {
	_ = b.broadcaster.BroadcastToUser(context.Background(), userID, event, data)
}
