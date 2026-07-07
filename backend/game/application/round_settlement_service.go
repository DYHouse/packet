package application

import (
	"context"
	"sort"
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
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/scheduler"
)

// RoundSettlementService 负责单局结算：执行结算 Lua、广播结果、发布事件、
// 在游戏结束时通过异步任务回调 GameLifecycleService.EndGameWithOptions。
// 从 GameAppService 拆分（Phase 2.1），保持原有业务逻辑完全不变。
type RoundSettlementService struct {
	repo           domain.RoomRepository
	broadcaster    domain.Broadcaster
	eventPublisher domain.GameEventPublisher
	redis          *cRedis.Client
	commissionCfg  *domain.CommissionConfig
	timeoutCfg     *config.TimeoutConfig
	lockCfg        *config.LockConfig
	scheduler      *scheduler.TimeoutScheduler
	taskRunner     *async.TaskRunner
	idGen          idgen.IDGenerator
	gameEnder      GameEnder
}

// NewRoundSettlementService 创建单局结算服务。
func NewRoundSettlementService(
	repo domain.RoomRepository,
	broadcaster domain.Broadcaster,
	eventPublisher domain.GameEventPublisher,
	redisClient *cRedis.Client,
	timeoutCfg *config.TimeoutConfig,
	lockCfg *config.LockConfig,
	schedulerInst *scheduler.TimeoutScheduler,
	taskRunner *async.TaskRunner,
	idGen idgen.IDGenerator,
) *RoundSettlementService {
	if lockCfg == nil {
		lockCfg = &config.LockConfig{}
		config.SetLockDefaults(lockCfg)
	}
	return &RoundSettlementService{
		repo:           repo,
		broadcaster:    broadcaster,
		eventPublisher: eventPublisher,
		redis:          redisClient,
		commissionCfg:  domain.DefaultCommissionConfig(),
		timeoutCfg:     timeoutCfg,
		lockCfg:        lockCfg,
		scheduler:      schedulerInst,
		taskRunner:     taskRunner,
		idGen:          idGen,
	}
}

// SetGameEnder 注入游戏结束处理器（GameLifecycleService），
// 供结算完成且游戏结束时通过异步任务回调结束游戏。
func (s *RoundSettlementService) SetGameEnder(e GameEnder) {
	s.gameEnder = e
}

// SettleRound 执行单局结算。
// 该方法由 GameAppService.GrabPacket/OnRobotGrabbed（通过异步任务）和
// GameLifecycleService.OnGrabTimeout（同步调用）触发。
func (s *RoundSettlementService) SettleRound(ctx context.Context, roomID, roundID string) {
	lockKey := redis.SettleLockKey(roomID, roundID)

	err := lock.WithRedisLock(ctx, s.redis, lockKey, int(s.lockCfg.GameAppSettleLockTTL.Seconds()), func() error {
		meta, _ := s.repo.GetRoomMeta(ctx, roomID)

		var sessionPlayerTotalsKey string
		if meta != nil && meta.CurrentSessionID != "" {
			sessionPlayerTotalsKey = redis.SessionPlayerTotalsKey(meta.CurrentSessionID)
		}

		keys := []string{
			redis.RoundStateKey(roundID),
			redis.RoundGrabbersKey(roundID),
			redis.RoomPlayersKey(roomID),
			redis.RoomHashKey(roomID),
			redis.RoundAvailablePacketsKey(roundID),
			sessionPlayerTotalsKey,
		}

		args := []interface{}{
			roundID,
			time.Now().Unix(),
			rediskeys.KeyPacketInfoPrefix,
		}

		res, err := scripts.SettleRound.Run(ctx, s.redis, keys, args...).Slice()
		if err != nil {
			logger.Error("settle round failed", "room_id", roomID, "round_id", roundID, "error", err)
			return err
		}

		code := converter.ParseInt(res[0])
		if code == 2 {
			logger.Info("round already settled (idempotent)", "room_id", roomID, "round_id", roundID)
			return nil
		}
		if code != 0 {
			logger.Error("settle round lua failed", "room_id", roomID, "round_id", roundID, "lua_code", code)
			return domain.MapLuaError(code)
		}

		roundNo := converter.ParseInt(res[1])
		senderID := converter.ParseString(res[2])
		totalAmount := converter.ParseInt64(res[3])
		minAmountPlayer := converter.ParseString(res[4])
		isGameEnd := converter.ParseInt(res[5]) == 1

		var results []message.RoundResult
		if len(res) > 6 {
			if arr, ok := res[6].([]interface{}); ok {
				for _, item := range arr {
					if tuple, ok := item.([]interface{}); ok && len(tuple) >= 7 {
						isAutoAssigned := converter.ParseInt(tuple[5]) == 1
						packetID := converter.ParseInt64(tuple[6])
						results = append(results, message.RoundResult{
							UserID:         converter.ParseString(tuple[0]),
							Amount:         currency.NewMoneyFromFen(converter.ParseInt64(tuple[1])),
							Nickname:       converter.ParseString(tuple[2]),
							Position:       int32(converter.ParseInt(tuple[3])),
							Avatar:         converter.ParseString(tuple[4]),
							IsAutoAssigned: isAutoAssigned,
							PacketID:       converter.FormatID(packetID),
						})
					}
				}
			}
		}

		sort.Slice(results, func(i, j int) bool {
			return results[i].Position < results[j].Position
		})

		commission := int64(0)
		if meta != nil {
			commission = s.commissionCfg.Calculate(totalAmount)
		}

		var rewardType int
		var rewardAmount int64
		if len(res) > 8 {
			rewardType = converter.ParseInt(res[7])
			rewardAmount = converter.ParseInt64(res[8])
		}

		var finalResults []message.GameResult
		if isGameEnd && len(res) > 9 {
			if arr, ok := res[9].([]interface{}); ok {
				for _, item := range arr {
					if tuple, ok := item.([]interface{}); ok && len(tuple) >= 5 {
						finalResults = append(finalResults, message.GameResult{
							UserID:      converter.ParseString(tuple[0]),
							Nickname:    converter.ParseString(tuple[1]),
							Avatar:      converter.ParseString(tuple[2]),
							TotalProfit: currency.NewMoneyFromFen(converter.ParseInt64(tuple[3])),
							Rank:        int32(converter.ParseInt(tuple[4])),
						})
					}
				}
			}
		}

		logger.Info("reward from redis", "rewardType", rewardType, "rewardAmount", rewardAmount)

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushRoundEnd, &message.RoundEndPush{
				RoomID:          roomID,
				RoundID:         roundID,
				CurrentRound:    int32(roundNo),
				SenderID:        senderID,
				TotalAmount:     currency.NewMoneyFromFen(totalAmount),
				Commission:      currency.NewMoneyFromFen(commission),
				Results:         results,
				MinAmountPlayer: minAmountPlayer,
				NextSenderID:    minAmountPlayer,
				IsGameEnd:       isGameEnd,
				RewardType:      rewardType,
				RewardAmount:    currency.NewMoneyFromFen(rewardAmount),
				FinalResults:    finalResults,
			}, "")
		}

		if s.eventPublisher != nil && meta != nil {
			roundResults := make([]*domain.RoundResult, 0, len(results))
			for _, r := range results {
				roundResults = append(roundResults, &domain.RoundResult{
					UserID:         r.UserID,
					PacketID:       r.PacketID,
					Position:       int(r.Position),
					Amount:         r.Amount.Fen(),
					IsAutoAssigned: r.IsAutoAssigned,
				})
			}

			senderType := "player"
			if senderID == "0" {
				senderType = "system"
			}

			var roomFeePerPlayer int64
			if roundNo == 1 && senderType == "system" && len(results) > 0 {
				roomFeePerPlayer = meta.RoomFee / int64(len(results))
			}

			roundSettleTraceID, err := s.idGen.GenerateString()
			if err != nil {
				logger.Warn("generate trace id failed for round settle event",
					"room_id", roomID,
					"round_id", roundID,
					"error", err,
				)
			}
			event := &domain.GameEvent{
				EventHeader: message.NewEventHeader(roundSettleTraceID),
				RoomID:      roomID,
				SessionID:   meta.CurrentSessionID,
				RoundID:     roundID,
				EventType:   domain.GameEventRoundSettle,
			}
			_ = event.SetPayload(&domain.RoundSettleData{
				RoundNo:          roundNo,
				SenderID:         senderID,
				SenderType:       senderType,
				TotalAmount:      totalAmount,
				Commission:       commission,
				RoomFeePerPlayer: roomFeePerPlayer,
				PacketCount:      len(results),
				Results:          roundResults,
				MinPlayerID:      minAmountPlayer,
				IsGameEnd:        isGameEnd,
				RewardType:       rewardType,
				RewardAmount:     rewardAmount,
				TriggerType:      0,
			})
			if err := s.taskRunner.Submit("publish_round_settle", 5*time.Second, func(ctx context.Context) {
				if err := s.eventPublisher.PublishGameEvent(ctx, event); err != nil {
					logger.Error("publish round settle event failed", "error", err)
				}
			}); err != nil {
				logger.Warn("submit publish_round_settle task failed", "error", err)
			}
		}

		if isGameEnd {
			var finalResultsForEvent []*domain.FinalResult
			if meta != nil {
				finalResultsForEvent = make([]*domain.FinalResult, 0, len(finalResults))
				for _, r := range finalResults {
					finalResultsForEvent = append(finalResultsForEvent, &domain.FinalResult{
						UserID:      r.UserID,
						Nickname:    r.Nickname,
						TotalProfit: r.TotalProfit.Fen(),
						Rank:        int(r.Rank),
					})
				}
			}

			var sessionID string
			if meta != nil {
				sessionID = meta.CurrentSessionID
			}

			if err := s.taskRunner.Submit("end_game_on_settle", 15*time.Second, func(ctx context.Context) {
				if s.gameEnder != nil {
					if err := s.gameEnder.EndGameWithOptions(ctx, roomID, &EndGameOptions{
						AllowedStatus: int(domain.RoomStatusPlaying),
						EndReason:     message.ReasonNormalEnd,
						SessionID:     sessionID,
						ActualRounds:  roundNo,
						FinalResults:  finalResultsForEvent,
					}); err != nil {
						logger.Error("endGameWithOptions failed (normal end)",
							"room_id", roomID,
							"session_id", sessionID,
							"error", err)
					}
				}
			}); err != nil {
				logger.Warn("submit end_game_on_settle task failed", "error", err)
			}
		} else {
			if s.scheduler != nil {
				var sendDuration time.Duration
				if minAmountPlayer == "0" {
					sendDuration = 8 * time.Second
				} else {
					sendDuration = s.timeoutCfg.Send
					if sendDuration == 0 {
						sendDuration = 30 * time.Second
					}
				}
				if rewardType == domain.RewardTypeStraight {
					sendDuration += 5 * time.Second
				}
				s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSend, roomID, minAmountPlayer, sendDuration)
			}
		}

		logger.Info("round settled",
			"room_id", roomID,
			"round_id", roundID,
			"round_no", roundNo,
			"is_game_end", isGameEnd,
			"reward_type", rewardType,
			"reward_amount", rewardAmount,
		)

		return nil
	})

	if err != nil {
		logger.Error("settle round with lock failed", "room_id", roomID, "round_id", roundID, "error", err)
	}
}
