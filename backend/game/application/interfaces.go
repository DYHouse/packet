package application

import (
	"context"

	"github.com/cashparty/backend/game/domain/room"
)

// DeductFailureHandler 抽象扣款失败处理，供 PacketOrchestrator 跨 Service 调用 GameLifecycleService。
type DeductFailureHandler interface {
	HandleDeductFailure(ctx context.Context, roomID string, meta *room.RoomMeta, reason string, err error)
}

// RobotChecker 抽象机器人身份识别（与 settlement/service.RobotChecker 方法签名一致）。
// 由 settlement/service.redisRobotChecker 实现，通过 container 注入。
// fail-closed：Redis 不可用时返回 error，调用方必须中止资金操作。
type RobotChecker interface {
	IsRobot(ctx context.Context, userID int64) (bool, error)
}

// GameEnder 抽象游戏结束逻辑，供 RoundSettlementService 通过异步任务回调 GameLifecycleService。
type GameEnder interface {
	EndGameWithOptions(ctx context.Context, roomID string, opts *EndGameOptions) error
}

// PacketInitiator 抽象发包启动入口，供 GameLifecycleService 跨 Service 调用 PacketOrchestrator。
type PacketInitiator interface {
	StartFirstRound(ctx context.Context, roomID string, meta *room.RoomMeta)
	HandleSystemSendTimeout(ctx context.Context, roomID string)
	ForceSendPacketForPlayer(ctx context.Context, roomID, userID string, penaltyAmount int64)
	SystemSendRound(ctx context.Context, roomID string, meta *room.RoomMeta, roundNo int)
}

// RoundSettler 抽象单局结算，供 GameAppService 和 GameLifecycleService 跨 Service 调用 RoundSettlementService。
type RoundSettler interface {
	SettleRound(ctx context.Context, roomID, roundID string)
}

// RoundEnsurer 抽象"确保下一轮 round 存在"逻辑，供 SeatAppService 和 RoomAppService
// 跨 Service 调用 GameLifecycleService.EnsureNextRound（与 PacketInitiator 模式一致）。
// 用于补位扣款场景：替补费本质为"下一局准备玩家"，应关联到下一轮 roundID，
// 与 OnSendTimeout/OnReplaceTimeout 罚款场景语义一致。
type RoundEnsurer interface {
	EnsureNextRound(ctx context.Context, roomID, sessionID int64, roundNo int) (int64, error)
}

// RobotBehaviorNotifier 抽象机器人行为通知，供 GameEventHandler 调用 robot 子包的行为引擎，
// 避免 application → robot 的循环依赖。
type RobotBehaviorNotifier interface {
	OnPacketCreated(ctx context.Context, roomID string, roundID string)
	OnRoundSettle(ctx context.Context, roomID string, minPlayerID int64, isGameEnd bool)
}
