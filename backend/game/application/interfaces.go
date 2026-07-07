package application

import (
	"context"

	"github.com/cashparty/backend/game/domain"
)

// DeductFailureHandler 抽象扣款失败处理，供 PacketOrchestrator 跨 Service 调用 GameLifecycleService。
type DeductFailureHandler interface {
	HandleDeductFailure(ctx context.Context, roomID string, meta *domain.RoomMeta, reason string, err error)
}

// GameEnder 抽象游戏结束逻辑，供 RoundSettlementService 通过异步任务回调 GameLifecycleService。
type GameEnder interface {
	EndGameWithOptions(ctx context.Context, roomID string, opts *EndGameOptions) error
}

// PacketInitiator 抽象发包启动入口，供 GameLifecycleService 跨 Service 调用 PacketOrchestrator。
type PacketInitiator interface {
	StartFirstRound(ctx context.Context, roomID string, meta *domain.RoomMeta)
	HandleSystemSendTimeout(ctx context.Context, roomID string)
	ForceSendPacketForPlayer(ctx context.Context, roomID, userID string, penaltyAmount int64)
	SystemSendRound(ctx context.Context, roomID string, meta *domain.RoomMeta, roundNo int)
}

// RoundSettler 抽象单局结算，供 GameAppService 和 GameLifecycleService 跨 Service 调用 RoundSettlementService。
type RoundSettler interface {
	SettleRound(ctx context.Context, roomID, roundID string)
}
