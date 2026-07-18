package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/i18n"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/push"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/game/scheduler"
	settlementApplication "github.com/cashparty/backend/settlement/application"
	"gorm.io/gorm"
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
	FinalResults  []*events.FinalResult
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
	repo             repository.RoomRepository
	dbRepo           repository.DBRepository
	broadcaster      events.Broadcaster
	eventPublisher   events.GameEventPublisher
	scheduler        *scheduler.TimeoutScheduler
	redis            cRedis.RedisClient
	lockCfg          *config.LockConfig
	timeoutCfg       *config.TimeoutConfig
	redisTTL         config.RedisTTLConfig
	penaltyService   *PenaltyService
	settleAppService *settlementApplication.SettleAppService
	grabService      *GrabService
	taskRunner       async.TaskRunner
	idGen            idgen.IDGenerator
	gameEndCallback  GameEndCallback
	roomAppService   *RoomAppService
	packetInitiator  PacketInitiator
	roundSettler     RoundSettler
}

// NewGameLifecycleService 创建游戏生命周期服务。
func NewGameLifecycleService(
	repo repository.RoomRepository,
	dbRepo repository.DBRepository,
	broadcaster events.Broadcaster,
	eventPublisher events.GameEventPublisher,
	schedulerInst *scheduler.TimeoutScheduler,
	redisClient cRedis.RedisClient,
	lockCfg *config.LockConfig,
	timeoutCfg *config.TimeoutConfig,
	redisTTL config.RedisTTLConfig,
	penaltyService *PenaltyService,
	settleAppSvc *settlementApplication.SettleAppService,
	grabService *GrabService,
	taskRunner async.TaskRunner,
	idGen idgen.IDGenerator,
) *GameLifecycleService {
	if lockCfg == nil {
		lockCfg = &config.LockConfig{}
		config.SetLockDefaults(lockCfg)
	}
	return &GameLifecycleService{
		repo:             repo,
		dbRepo:           dbRepo,
		broadcaster:      broadcaster,
		eventPublisher:   eventPublisher,
		scheduler:        schedulerInst,
		redis:            redisClient,
		lockCfg:          lockCfg,
		timeoutCfg:       timeoutCfg,
		redisTTL:         redisTTL,
		penaltyService:   penaltyService,
		settleAppService: settleAppSvc,
		grabService:      grabService,
		taskRunner:       taskRunner,
		idGen:            idGen,
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

func (s *GameLifecycleService) startGameCore(ctx context.Context, roomID string, meta *room.RoomMeta) string {
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
		s.broadcaster.Broadcast(ctx, roomID, message.PushGameStart, &push.GameStartPush{
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
		players := make([]*events.PlayerInfo, 0, len(stateData.Players))
		for _, p := range stateData.Players {
			players = append(players, &events.PlayerInfo{
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
		event := &events.GameEvent{
			EventHeader: message.NewEventHeader(traceID),
			RoomID:      roomID,
			SessionID:   sessionID,
			EventType:   events.GameEventSessionStart,
		}
		_ = event.SetPayload(&events.SessionStartData{
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
	roomHashKey := rediskeys.RoomHashKey(roomID)
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
		if success, ok := resultArray[0].(int64); !ok || success != 0 {
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
		s.broadcaster.Broadcast(ctx, req.RoomID, message.PushGameResumed, &push.GameResumedPush{
			RoomID:       req.RoomID,
			CurrentRound: int32(req.CurrentRound),
			NextSenderID: "0",
			Message:      i18n.GetErrorMsg(message.CodeGameResumed),
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

// EnsureNextRound 确保下一轮 round 存在，用于 inter-round 罚款场景及补位扣款场景。
// 罚款/替补费根因是下一轮未发包，故应关联到下一轮 roundID。
// 如果下一轮 round 已存在（SendPacket 已创建），则复用；
// 如果不存在，则预创建 Pending round，后续 SendPacket 时复用。
// 查询/创建失败时返回 error，调用方降级为 roundID=0，不阻塞主流程。
// 实现 RoundEnsurer 接口，供 SeatAppService 和 RoomAppService 跨 Service 调用。
func (s *GameLifecycleService) EnsureNextRound(ctx context.Context, roomID, sessionID int64, roundNo int) (int64, error) {
	// 先查询是否已有 round（快路径）
	existing, err := s.dbRepo.RoundDBRepo().GetRoundBySessionAndRoundNo(ctx, sessionID, roundNo)
	if err == nil && existing != nil {
		return existing.RoundID, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("query round failed: %w", err)
	}

	// 预创建 Pending round（INSERT IGNORE + 查询，并发安全）
	roundID, err := s.idGen.GenerateInt64()
	if err != nil {
		return 0, fmt.Errorf("generate round id: %w", err)
	}
	round := &model.Round{
		RoundID:   roundID,
		SessionID: sessionID,
		RoomID:    roomID,
		RoundNo:   roundNo,
		Status:    model.RoundStatusPending,
	}
	created, err := s.dbRepo.RoundDBRepo().CreateOrGetRound(ctx, round)
	if err != nil {
		return 0, fmt.Errorf("create round failed: %w", err)
	}
	return created.RoundID, nil
}
