package broadcast

import (
	"context"

	"github.com/cashparty/backend/common/message"
)

type MessageHandler func(ctx context.Context, msg *message.BroadcastMessage) error

type Consumer interface {
	Start(ctx context.Context) error
	Close() error
}
