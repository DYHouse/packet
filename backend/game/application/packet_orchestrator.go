package application

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/async"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/algorithm"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/game/scheduler"
	settlementService "github.com/cashparty/backend/settlement/service"
)

// PacketOrchestrator 负责发包管线：玩家发包、系统发包、轮次初始化与扣款。
// 从 GameAppService 拆分（Phase 2.1），保持原有业务逻辑完全不变。
type PacketOrchestrator struct {
	repo                 domain.RoomRepository
	dbRepo               domain.DBRepository
	broadcaster          domain.Broadcaster
	eventPublisher       domain.GameEventPublisher
	grabService          *GrabService
	packetGenerator      *algorithm.PacketGenerator
	rewardSettler        *settlementService.RewardSettler
	redis                *cRedis.Client
	commissionCfg        *domain.CommissionConfig
	deductSvc            *settlementService.DeductService
	taskRunner           *async.TaskRunner
	idGen                idgen.IDGenerator
	lockCfg              *config.LockConfig
	timeoutCfg           *config.TimeoutConfig
	scheduler            *scheduler.TimeoutScheduler
	deductFailureHandler DeductFailureHandler
}

// NewPacketOrchestrator 创建发包编排器。
func NewPacketOrchestrator(
	repo domain.RoomRepository,
	dbRepo domain.DBRepository,
	broadcaster domain.Broadcaster,
	eventPublisher domain.GameEventPublisher,
	grabService *GrabService,
	packetGenerator *algorithm.PacketGenerator,
	rewardSettler *settlementService.RewardSettler,
	redisClient *cRedis.Client,
	deductSvc *settlementService.DeductService,
	taskRunner *async.TaskRunner,
	idGen idgen.IDGenerator,
	lockCfg *config.LockConfig,
	timeoutCfg *config.TimeoutConfig,
	schedulerInst *scheduler.TimeoutScheduler,
) *PacketOrchestrator {
	if lockCfg == nil {
		lockCfg = &config.LockConfig{}
		config.SetLockDefaults(lockCfg)
	}
	return &PacketOrchestrator{
		repo:            repo,
		dbRepo:          dbRepo,
		broadcaster:     broadcaster,
		eventPublisher:  eventPublisher,
		grabService:     grabService,
		packetGenerator: packetGenerator,
		rewardSettler:   rewardSettler,
		redis:           redisClient,
		commissionCfg:   domain.DefaultCommissionConfig(),
		deductSvc:       deductSvc,
		taskRunner:      taskRunner,
		idGen:           idGen,
		lockCfg:         lockCfg,
		timeoutCfg:      timeoutCfg,
		scheduler:       schedulerInst,
	}
}

// SetDeductFailureHandler 注入扣款失败处理器（GameLifecycleService），
// 用于跨 Service 通知扣款失败并结束游戏。
func (p *PacketOrchestrator) SetDeductFailureHandler(h DeductFailureHandler) {
	p.deductFailureHandler = h
}

type SendPacketRequest struct {
	RoomID string
	UserID string
}

type SendPacketResult struct {
	PacketCount int
}

type sendPacketResult struct {
	PacketCount  int
	RoundID      string
	PacketIDs    []string
	Amount       int64
	Commission   int64
	ActualAmount int64
	NextRound    int
	Nickname     string
	SenderType   string
}

func (p *PacketOrchestrator) sendPacketPipeline(ctx context.Context, params *domain.SendPacketParams) (*sendPacketResult, error) {
	commission := p.commissionCfg.Calculate(params.TotalAmount)
	actualAmount := params.TotalAmount - commission

	roundID := converter.FormatID(params.RoundID)

	genResult, err := p.packetGenerator.Generate(ctx, &algorithm.GenerateRequest{
		TotalAmount: actualAmount,
		PacketCount: params.PlayerCount,
		RoomID:      params.RoomID,
		RoundID:     roundID,
	})
	if err != nil {
		return nil, message.NewError(message.CodeSystemError)
	}

	senderID := params.Scenario.SenderID(params.SenderID)
	senderType := params.Scenario.SenderType()

	var rewardAmount int64
	if genResult.RewardType > 0 && p.rewardSettler != nil {
		rewardAmount = p.rewardSettler.CalculateRewardAmount(int(genResult.RewardType), params.TotalAmount)
	}

	_, packetIDs, err := p.grabService.InitRoundPackets(ctx,
		params.RoomID, roundID, senderID, senderType,
		params.TotalAmount, commission, actualAmount,
		genResult.PacketAmounts, params.RoundNo, params.Scenario,
		int(genResult.RewardType), rewardAmount,
	)
	if err != nil {
		return nil, message.NewError(message.CodeSystemError)
	}

	return &sendPacketResult{
		PacketCount:  len(genResult.PacketAmounts),
		RoundID:      roundID,
		PacketIDs:    packetIDs,
		Amount:       params.TotalAmount,
		Commission:   commission,
		ActualAmount: actualAmount,
		NextRound:    params.RoundNo,
		SenderType:   senderType,
	}, nil
}

func (p *PacketOrchestrator) SendPacket(ctx context.Context, req *SendPacketRequest) (*SendPacketResult, error) {
	lockKey := redis.SendPacketLockKey(req.RoomID, req.UserID)

	var result *sendPacketResult

	err := lock.WithRedisLock(ctx, p.redis, lockKey, int(p.lockCfg.SendPacketLockTTL.Seconds()), func() error {
		meta, err := p.repo.GetRoomMeta(ctx, req.RoomID)
		if err != nil {
			return message.NewError(message.CodeRoomNotFound)
		}

		if meta.Status != domain.RoomStatusPlaying {
			return message.NewError(message.CodeGameNotStarted)
		}

		nextRound := int(meta.CurrentRound) + 1

		if meta.CurrentRoundID != "" {
			return message.NewError(message.CodePacketsAlreadyExist)
		}

		player, err := p.repo.GetPlayer(ctx, req.RoomID, req.UserID)
		if err != nil || player == nil {
			return message.NewError(message.CodeNotInRoom)
		}

		if nextRound > 1 {
			roomHashKey := redis.RoomHashKey(req.RoomID)
			nextSenderID, _ := p.redis.HGet(ctx, roomHashKey, "next_sender_id").Result()
			if nextSenderID != "" && nextSenderID != req.UserID {
				return message.NewError(message.CodeNotYourTurn)
			}

			initResult, initErr := p.initLaterRoundAndDeduct(ctx, req.RoomID, meta, nextRound, req.UserID, meta.RoomFee)
			if initErr != nil {
				logger.Error("later round init and deduct failed",
					"room_id", req.RoomID,
					"user_id", req.UserID,
					"round_no", nextRound,
					"error", initErr)
				if p.deductFailureHandler != nil {
					p.deductFailureHandler.HandleDeductFailure(ctx, req.RoomID, meta, message.ReasonLaterRoundDeductFailed, initErr)
				}
				return message.NewErrorWithMsg(message.CodeSystemError, message.GetInterruptMessage(message.ReasonLaterRoundDeductFailed))
			}

			pipelineResult, pipelineErr := p.sendPacketPipeline(ctx, &domain.SendPacketParams{
				RoomID:      req.RoomID,
				SenderID:    req.UserID,
				Scenario:    domain.SendScenarioPlayerManual,
				TotalAmount: meta.RoomFee,
				RoundNo:     nextRound,
				RoundID:     initResult.RoundID,
				PlayerCount: meta.MaxPlayers,
			})
			if pipelineErr != nil {
				if updateErr := p.updateRoundFailed(ctx, initResult.RoundID, fmt.Sprintf("send packet failed: %v", pipelineErr)); updateErr != nil {
					logger.Error("update round failed status error", "round_id", initResult.RoundID, "error", updateErr)
				}
				return pipelineErr
			}

			if err := p.dbRepo.RoundDBRepo().UpdateRoundStatus(ctx, initResult.RoundID, model.RoundStatusSending); err != nil {
				logger.Error("update round status to sending failed", "round_id", initResult.RoundID, "error", err)
			}

			pipelineResult.Nickname = player.Nickname
			result = pipelineResult
			return nil
		}

		pipelineResult, pipelineErr := p.sendPacketPipeline(ctx, &domain.SendPacketParams{
			RoomID:      req.RoomID,
			SenderID:    req.UserID,
			Scenario:    domain.SendScenarioPlayerManual,
			TotalAmount: meta.RoomFee,
			RoundNo:     nextRound,
			PlayerCount: meta.MaxPlayers,
		})
		if pipelineErr != nil {
			return pipelineErr
		}

		pipelineResult.Nickname = player.Nickname
		result = pipelineResult
		return nil
	})

	if err != nil {
		if _, ok := message.IsGameError(err); ok {
			return nil, err
		}
		return nil, message.NewError(message.CodeOperationInProgress)
	}

	if err := p.taskRunner.Submit("post_send_packet", 10*time.Second, func(ctx context.Context) {
		p.postSendPacketAsync(ctx, &postSendPacketParams{
			RoomID:       req.RoomID,
			RoundID:      result.RoundID,
			PacketIDs:    result.PacketIDs,
			UserID:       req.UserID,
			Nickname:     result.Nickname,
			Amount:       result.Amount,
			Commission:   result.Commission,
			ActualAmount: result.ActualAmount,
			NextRound:    result.NextRound,
			PacketCount:  result.PacketCount,
			SenderType:   result.SenderType,
		})
	}); err != nil {
		logger.Warn("submit post_send_packet task failed", "error", err)
	}

	return &SendPacketResult{
		PacketCount: result.PacketCount,
	}, nil
}

type postSendPacketParams struct {
	RoomID       string
	RoundID      string
	PacketIDs    []string
	UserID       string
	Nickname     string
	Amount       int64
	Commission   int64
	ActualAmount int64
	NextRound    int
	PacketCount  int
	SenderType   string
}

func (p *PacketOrchestrator) postSendPacketAsync(ctx context.Context, params *postSendPacketParams) {
	p.publishPacketCreatedEvent(ctx, params.RoomID, params.RoundID, params.PacketIDs, params.UserID, params.Amount, params.Commission, params.NextRound)

	if p.scheduler != nil {
		if params.UserID != "0" {
			p.scheduler.ClearTimeout(ctx, scheduler.TimeoutTypeSend, params.RoomID, params.UserID)
		}
		p.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeGrab, params.RoomID, params.RoundID)
	}

	packets := make([]message.PacketInfo, len(params.PacketIDs))
	for i, packetID := range params.PacketIDs {
		packets[i] = message.PacketInfo{
			PacketID: packetID,
			Position: int32(i + 1),
		}
	}

	if p.broadcaster != nil {
		p.broadcaster.Broadcast(ctx, params.RoomID, message.PushRoundStart, &message.RoundStartPush{
			RoomID:         params.RoomID,
			RoundID:        params.RoundID,
			CurrentRound:   int32(params.NextRound),
			SenderID:       params.UserID,
			SenderNickname: params.Nickname,
			SenderType:     params.SenderType,
			TotalAmount:    currency.NewMoneyFromFen(params.ActualAmount),
			Commission:     currency.NewMoneyFromFen(params.Commission),
			PacketCount:    int32(params.PacketCount),
			GrabTimeout:    int32(p.timeoutCfg.Grab.Seconds()),
			Packets:        packets,
		}, "")
	}

	logger.Info("packet sent",
		"room_id", params.RoomID,
		"user_id", params.UserID,
		"sender_type", params.SenderType,
		"amount", params.Amount,
		"round_id", params.RoundID,
		"packet_count", params.PacketCount,
	)
}
