package domain

import (
	"context"

	"github.com/cashparty/backend/common/broadcast"
)

// EventPublisher 是统一的事件发布接口，覆盖 RoomEvent 和 GameEvent。
// 任何实现此接口的类型必须支持两种事件的发布。
type EventPublisher interface {
	PublishRoomEvent(ctx context.Context, event *RoomEvent) error
	PublishGameEvent(ctx context.Context, event *GameEvent) error
}

// Broadcaster 是 common/broadcast.Broadcaster 的类型别名，确保全项目接口唯一。
type Broadcaster = broadcast.Broadcaster
