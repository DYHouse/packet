package application

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/scheduler"
)

// GameAppService 作为 facade，转发调用到 3 个职责单一的子 Service：
//   - PacketOrchestrator：发包管线（SendPacket/StartFirstRound/HandleSystemSendTimeout/...）
//   - RoundSettlementService：单局结算（SettleRound）
//   - GameLifecycleService：游戏生命周期管理（StartGame/ResumeGame/超时处理/...）
//
// 外部调用方仍调 appSvc.SendPacket()，内部转发到 packetOrchestrator.SendPacket()，
// 保持 gRPC 接口与外部依赖完全兼容。
// GrabPacket/OnRobotGrabbed 保留在 facade 中，结算调用 roundSettlementService.SettleRound。
type GameAppService struct {
	repo                   domain.RoomRepository
	grabService            *GrabService
	broadcaster            domain.Broadcaster
	scheduler              *scheduler.TimeoutScheduler
	taskRunner             *async.TaskRunner
	packetOrchestrator     *PacketOrchestrator
	roundSettlementService *RoundSettlementService
	gameLifecycleService   *GameLifecycleService
}

// NewGameAppService 创建 GameAppService facade。
// 3 个子 Service 由 bootstrap 创建并注入；跨 Service 依赖（DeductFailureHandler、
// GameEnder、PacketInitiator、RoundSettler）由 bootstrap 通过 Setter 注入。
//
// 与原构造函数相比移除了 3 个死字段对应的参数：publisher、refundSvc、rewardController
// （原 game_app_service.go 中从未引用这些字段，属于历史遗留死代码）。
func NewGameAppService(
	repo domain.RoomRepository,
	grabService *GrabService,
	broadcaster domain.Broadcaster,
	schedulerInst *scheduler.TimeoutScheduler,
	taskRunner *async.TaskRunner,
	packetOrchestrator *PacketOrchestrator,
	roundSettlementService *RoundSettlementService,
	gameLifecycleService *GameLifecycleService,
) *GameAppService {
	return &GameAppService{
		repo:                   repo,
		grabService:            grabService,
		broadcaster:            broadcaster,
		scheduler:              schedulerInst,
		taskRunner:             taskRunner,
		packetOrchestrator:     packetOrchestrator,
		roundSettlementService: roundSettlementService,
		gameLifecycleService:   gameLifecycleService,
	}
}

// SetRoomAppService 注入 RoomAppService（用于踢人后触发自动替补）。
// 转发到 GameLifecycleService.SetRoomAppService。
func (s *GameAppService) SetRoomAppService(svc *RoomAppService) {
	s.gameLifecycleService.SetRoomAppService(svc)
}

// SetGameEndCallback injects the game end callback used to notify the robot
// scheduler when a game ends. 转发到 GameLifecycleService.SetGameEndCallback。
func (s *GameAppService) SetGameEndCallback(cb GameEndCallback) {
	s.gameLifecycleService.SetGameEndCallback(cb)
}

// SendPacket 转发到 PacketOrchestrator.SendPacket。
func (s *GameAppService) SendPacket(ctx context.Context, req *SendPacketRequest) (*SendPacketResult, error) {
	return s.packetOrchestrator.SendPacket(ctx, req)
}

// StartGame 转发到 GameLifecycleService.StartGame。
func (s *GameAppService) StartGame(ctx context.Context, roomID string) {
	s.gameLifecycleService.StartGame(ctx, roomID)
}

// ResumeGame 转发到 GameLifecycleService.ResumeGame。
func (s *GameAppService) ResumeGame(ctx context.Context, req *ResumeGameRequest) error {
	return s.gameLifecycleService.ResumeGame(ctx, req)
}

// OnGrabTimeout 转发到 GameLifecycleService.OnGrabTimeout。
func (s *GameAppService) OnGrabTimeout(ctx context.Context, roomID string, roundID string) {
	s.gameLifecycleService.OnGrabTimeout(ctx, roomID, roundID)
}

// OnSendTimeout 转发到 GameLifecycleService.OnSendTimeout。
func (s *GameAppService) OnSendTimeout(ctx context.Context, roomID string, userID string) {
	s.gameLifecycleService.OnSendTimeout(ctx, roomID, userID)
}

// OnReplaceTimeout 转发到 GameLifecycleService.OnReplaceTimeout。
func (s *GameAppService) OnReplaceTimeout(ctx context.Context, roomID string, leftUserID string) {
	s.gameLifecycleService.OnReplaceTimeout(ctx, roomID, leftUserID)
}

type GrabPacketRequest struct {
	RoomID   string
	UserID   string
	PacketID string
}

type GrabPacketResult struct {
	PacketID string
	Amount   int64
	Position int
	IsLast   bool
}

// GrabPacket 处理玩家抢红包请求，广播抢红包事件，并在最后一抢时异步触发单局结算。
// 与原 GameAppService.GrabPacket 业务逻辑完全一致，仅将内部 settleRound 调用
// 改为跨 Service 调用 roundSettlementService.SettleRound。
func (s *GameAppService) GrabPacket(ctx context.Context, req *GrabPacketRequest) (*GrabPacketResult, error) {
	meta, err := s.repo.GetRoomMeta(ctx, req.RoomID)
	if err != nil {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	if meta.Status != domain.RoomStatusPlaying {
		return nil, message.NewError(message.CodeGameNotStarted)
	}

	roundID := meta.CurrentRoundID
	if roundID == "" {
		return nil, message.NewError(message.CodeNoPacket)
	}

	result, err := s.grabService.GrabPacket(ctx, req.RoomID, roundID, req.UserID, req.PacketID)
	if err != nil {
		return nil, err
	}

	player, _ := s.repo.GetPlayer(ctx, req.RoomID, req.UserID)
	nickname := ""
	if player != nil {
		nickname = player.Nickname
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, req.RoomID, message.PushPacketGrabbed, &message.PacketGrabbedPush{
			RoomID:   req.RoomID,
			RoundID:  roundID,
			PacketID: result.PacketID,
			Position: int32(result.Position),
			UserID:   req.UserID,
			Nickname: nickname,
			Amount:   currency.NewMoneyFromFen(result.Amount),
			IsLast:   result.IsLast,
		}, req.UserID)
	}

	if result.IsLast {
		if s.scheduler != nil {
			s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeGrab, req.RoomID, roundID)
		}
		if err := s.taskRunner.Submit("settle_round", 15*time.Second, func(ctx context.Context) {
			s.roundSettlementService.SettleRound(ctx, req.RoomID, roundID)
		}); err != nil {
			logger.Warn("submit settle_round task failed", "error", err)
		}
	}

	logger.Info("packet grabbed",
		"room_id", req.RoomID,
		"user_id", req.UserID,
		"round_id", roundID,
		"amount", result.Amount,
		"position", result.Position,
	)

	return &GrabPacketResult{
		PacketID: result.PacketID,
		Amount:   result.Amount,
		Position: result.Position,
		IsLast:   result.IsLast,
	}, nil
}

// OnRobotGrabbed handles post-grab logic for robots: broadcast the grab event
// to all players and trigger round settlement if this was the last packet.
// 与原 GameAppService.OnRobotGrabbed 业务逻辑完全一致，仅将内部 settleRound 调用
// 改为跨 Service 调用 roundSettlementService.SettleRound。
func (s *GameAppService) OnRobotGrabbed(ctx context.Context, roomID, roundID, userID string, result *domain.GrabResult) {
	player, _ := s.repo.GetPlayer(ctx, roomID, userID)
	nickname := ""
	if player != nil {
		nickname = player.Nickname
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, roomID, message.PushPacketGrabbed, &message.PacketGrabbedPush{
			RoomID:   roomID,
			RoundID:  roundID,
			PacketID: result.PacketID,
			Position: int32(result.Position),
			UserID:   userID,
			Nickname: nickname,
			Amount:   currency.NewMoneyFromFen(result.Amount),
			IsLast:   result.IsLast,
		}, userID)
	}

	if result.IsLast {
		if s.scheduler != nil {
			s.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeGrab, roomID, roundID)
		}
		if err := s.taskRunner.Submit("settle_round_robot", 15*time.Second, func(ctx context.Context) {
			s.roundSettlementService.SettleRound(ctx, roomID, roundID)
		}); err != nil {
			logger.Warn("submit settle_round_robot task failed", "error", err)
		}
	}
}
