package events

import (
	"context"

	"github.com/cashparty/backend/common/broadcast"
)

// RoomEventPublisher 负责发布房间事件（RoomEvent）的单一职责接口。
type RoomEventPublisher interface {
	PublishRoomEvent(ctx context.Context, event *RoomEvent) error
}

// GameEventPublisher 负责发布游戏事件（GameEvent）的单一职责接口。
type GameEventPublisher interface {
	PublishGameEvent(ctx context.Context, event *GameEvent) error
}

// Broadcaster 是 common/broadcast.Broadcaster 的类型别名，确保全项目接口唯一。
type Broadcaster = broadcast.Broadcaster
