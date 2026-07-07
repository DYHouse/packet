package domain

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

// EventPublisher 是统一的事件发布接口，组合 RoomEventPublisher 与 GameEventPublisher。
//
// Deprecated: 使用 RoomEventPublisher 或 GameEventPublisher 替代。
// 保留此接口仅为向后兼容，新代码应按需依赖单一职责接口。
type EventPublisher interface {
	RoomEventPublisher
	GameEventPublisher
}

// Broadcaster 是 common/broadcast.Broadcaster 的类型别名，确保全项目接口唯一。
type Broadcaster = broadcast.Broadcaster
