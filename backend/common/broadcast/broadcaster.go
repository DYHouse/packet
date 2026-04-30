package broadcast

import (
	"context"
)

type Broadcaster interface {
	Broadcast(ctx context.Context, roomID string, event string, data interface{}, excludeUserID string) error
	BroadcastToUser(ctx context.Context, userID string, event string, data interface{}) error
}
