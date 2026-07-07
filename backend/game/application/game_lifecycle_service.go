package application

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/scheduler"
	settlementService "github.com/cashparty/backend/settlement/service"
)

// GameEndCallback is invoked from endGameWithOptions after a game ends. It is
// used by the robot scheduler to recycle robots without introducing a circular
// dependency between GameAppService and RobotSchedulerService.
type GameEndCallback func(ctx context.Context, roomID string)

// EndGameOptions 控制游戏结束行为。
type EndGameOptions struct {
	AllowedStatus int
	EndReason     string
	SessionID     string
	ActualRounds  int
	FinalResults  []*domain.FinalResult
}

type ResumeGameRequest struct {
	RoomID       string
	CurrentRound int
}

// GameLifecycleService 负责游戏生命周期管理：开始/恢复/结束游戏、超时处理、玩家进出。
// 从 GameAppService 拆分（Phase 2.1），保持原有业务逻辑完全不变。
// 方法分布在 3 个文件中：
//   - game_lifecycle_service.go：核心生命周期（startGameCore/StartGame/ResumeGame）+ 构造与 Setter
//   - game_lifecycle_timeout.go：超时处理（OnGrabTimeout/OnSendTimeout/OnReplaceTimeout）
//   - game_lifecycle_endgame.go：游戏结束（HandleDeductFailure/EndGameWithOptions/handleKickAndReplace）
type GameLifecycleService struct {
	repo              domain.RoomRepository
	broadcaster       domain.Broadcaster
	eventPublisher    domain.GameEventPublisher
	scheduler         *scheduler.TimeoutScheduler
	redis             *cRedis.Client
	lockCfg           *config.LockConfig
	timeoutCfg        *config.TimeoutConfig
	redisTTL          config.RedisTTLConfig
	penaltyService    *PenaltyService
	settlementService *settlementService.PenaltySettlementService
	grabService       *GrabService
	taskRunner        *async.TaskRunner
	idGen             idgen.IDGenerator
	gameEndCallback   GameEndCallback
	roomAppService    *RoomAppService
	packetInitiator   PacketInitiator
	roundSettler      RoundSettler
}

// NewGameLifecycleService 创建游戏生命周期服务。
func NewGameLifecycleService(
	repo domain.RoomRepository,
	broadcaster domain.Broadcaster,
	eventPublisher domain.GameEventPublisher,
	schedulerInst *scheduler.TimeoutScheduler,
	redisClient *cRedis.Client,
	lockCfg *config.LockConfig,
	timeoutCfg *config.TimeoutConfig,
	redisTTL config.RedisTTLConfig,
	penaltyService *PenaltyService,
	settlementSvc *settlementService.PenaltySettlementService,
	grabService *GrabService,
	taskRunner *async.TaskRunner,
	idGen idgen.IDGenerator,
) *GameLifecycleService {
	if lockCfg == nil {
		lockCfg = &config.LockConfig{}
		config.SetLockDefaults(lockCfg)
	}
	return &GameLifecycleService{
		repo:              repo,
		broadcaster:       broadcaster,
		eventPublisher:    eventPublisher,
		scheduler:         schedulerInst,
		redis:             redisClient,
		lockCfg:           lockCfg,
		timeoutCfg:        timeoutCfg,
		redisTTL:          redisTTL,
		penaltyService:    penaltyService,
		settlementService: settlementSvc,
		grabService:       grabService,
		taskRunner:        taskRunner,
		idGen:             idGen,
	}
}

// SetRoomAppService 注入 RoomAppService（用于踢人后触发自动替补）。
func (s *GameLifecycleService) SetRoomAppService(svc *RoomAppService) {
	s.roomAppService = svc
}

// SetGameEndCallback injects the game end callback used to notify the robot
// scheduler when a game ends.
func (s *GameLifecycleService) SetGameEndCallback(cb GameEndCallback) {
	s.gameEndCallback = cb
}

// SetPacketInitiator 注入发包启动器（PacketOrchestrator）。
func (s *GameLifecycleService) SetPacketInitiator(p PacketInitiator) {
	s.packetInitiator = p
}

// SetRoundSettler 注入结算器（RoundSettlementService）。
func (s *GameLifecycleService) SetRoundSettler(r RoundSettler) {
	s.roundSettler = r
}

func (s *GameLifecycleService) startGameCore(ctx context.Context, roomID string, meta *domain.RoomMeta) string {
	sessionID, err := s.idGen.GenerateString()
	if err != nil {
		logger.Error("generate session id failed",
			"room_id", roomID,
			"error", err,
		)
		return ""
	}

	if err := s.repo.UpdateRoomSessionID(ctx, roomID, sessionID); err != nil {
		logger.Error("failed to update session_id", "room_id", roomID, "error", err)
	}
	meta.CurrentSessionID = sessionID

	if s.scheduler != nil {
		s.scheduler.ClearAllRoomTimeouts(ctx, roomID)
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, roomID, message.PushGameStart, &message.GameStartPush{
			RoomID:       roomID,
			CurrentRound: 1,
			MaxRounds:    int32(meta.MaxRounds),
		}, "")
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
	if stateData != nil && s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, roomID, message.PushRoomState, BuildFullRoomState(stateData), "")
	}

	if s.eventPublisher != nil && stateData != nil {
		players := make([]*domain.PlayerInfo, 0, len(stateData.Players))
		for _, p := range stateData.Players {
			players = append(players, &domain.PlayerInfo{
				UserID:   p.UserID,
				Nickname: p.Nickname,
				Avatar:   p.Avatar,
				SeatNo:   p.SeatNo,
			})
		}

		traceID, err := s.idGen.GenerateString()
		if err != nil {
			logger.Warn("generate trace id failed for session start event",
				"room_id", roomID,
				"error", err,
			)
		}
		event := &domain.GameEvent{
			EventHeader: message.NewEventHeader(traceID),
			RoomID:      roomID,
			SessionID:   sessionID,
			EventType:   domain.GameEventSessionStart,
		}
		_ = event.SetPayload(&domain.SessionStartData{
			RoomNo:     meta.RoomNo,
			ConfigID:   meta.ConfigID,
			ConfigName: meta.ConfigName,
			RoomFee:    meta.RoomFee,
			MaxRounds:  meta.MaxRounds,
			Players:    players,
		})
		if err := s.taskRunner.Submit("publish_session_start", 5*time.Second, func(ctx context.Context) {
			if err := s.eventPublisher.PublishGameEvent(ctx, event); err != nil {
				logger.Error("publish session start event failed", "error", err)
			}
		}); err != nil {
			logger.Warn("submit publish_session_start task failed", "error", err)
		}
	}

	logger.Info("game started",
		"room_id", roomID,
		"session_id", sessionID,
		"max_rounds", meta.MaxRounds,
	)

	return sessionID
}

// StartGame 启动游戏：执行 TryStartGame Lua 脚本抢占启动权，成功后启动首轮。
func (s *GameLifecycleService) StartGame(ctx context.Context, roomID string) {
	roomHashKey := redis.RoomHashKey(roomID)
	now := time.Now().Unix()

	result, err := scripts.TryStartGame.Run(ctx, s.redis, []string{roomHashKey}, now).Result()
	if err != nil {
		logger.Error("failed to execute start game lua script",
			"room_id", roomID,
			"error", err,
		)
		return
	}

	if resultArray, ok := result.([]interface{}); ok && len(resultArray) >= 1 {
		if success, ok := resultArray[0].(int64); !ok || success != 1 {
			logger.Info("game start skipped, already started by another instance",
				"room_id", roomID,
				"reason", resultArray[1],
			)
			return
		}
	} else {
		logger.Warn("unexpected result from start game lua script",
			"room_id", roomID,
			"result", result,
		)
		return
	}

	meta, err := s.repo.GetRoomMeta(ctx, roomID)
	if err != nil {
		logger.Error("failed to get room meta", "room_id", roomID, "error", err)
		return
	}

	s.startGameCore(ctx, roomID, meta)
	if s.packetInitiator != nil {
		s.packetInitiator.StartFirstRound(ctx, roomID, meta)
	}
}

// ResumeGame 恢复中断的游戏：清除替补超时、广播恢复事件、系统发包。
func (s *GameLifecycleService) ResumeGame(ctx context.Context, req *ResumeGameRequest) error {
	logger.Info("resuming game from interrupt",
		"room_id", req.RoomID,
		"current_round", req.CurrentRound,
	)

	if s.scheduler != nil {
		s.scheduler.ClearRoomTimeouts(ctx, scheduler.TimeoutTypeReplace, req.RoomID)
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, req.RoomID, message.PushGameResumed, &message.GameResumedPush{
			RoomID:       req.RoomID,
			CurrentRound: int32(req.CurrentRound),
			NextSenderID: "0",
			Message:      message.GetErrorMsg(message.CodeGameResumed),
		}, "")
	}

	stateData, _ := s.repo.GetRoomStateData(ctx, req.RoomID)
	if stateData != nil && s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, req.RoomID, message.PushRoomState, BuildFullRoomState(stateData), "")
	}

	meta, _ := s.repo.GetRoomMeta(ctx, req.RoomID)
	if meta != nil && s.packetInitiator != nil {
		s.packetInitiator.SystemSendRound(ctx, req.RoomID, meta, req.CurrentRound)
	}

	return nil
}
