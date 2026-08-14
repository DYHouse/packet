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
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/game"
	"github.com/cashparty/backend/game/domain/push"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/reward"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/scheduler"
)

// RoundSettlementService 负责单局结算：执行结算 Lua、广播结果、发布事件、
// 在游戏结束时通过异步任务回调 GameLifecycleService.EndGameWithOptions。
// 从 GameAppService 拆分（Phase 2.1），保持原有业务逻辑完全不变。
type RoundSettlementService struct {
	repo             repository.RoomRepository
	broadcaster      events.Broadcaster
	eventPublisher   events.GameEventPublisher
	redis            cRedis.RedisClient
	commissionCfg    *game.CommissionConfig
	timeoutCfg       *config.TimeoutConfig
	lockCfg          *config.LockConfig
	scheduler        *scheduler.TimeoutScheduler
	taskRunner       async.TaskRunner
	idGen            idgen.IDGenerator
	gameEnder        GameEnder
	leaderboardSvc   *LeaderboardService
}

// NewRoundSettlementService 创建单局结算服务。
func NewRoundSettlementService(
	repo repository.RoomRepository,
	broadcaster events.Broadcaster,
	eventPublisher events.GameEventPublisher,
	redisClient cRedis.RedisClient,
	timeoutCfg *config.TimeoutConfig,
	lockCfg *config.LockConfig,
	schedulerInst *scheduler.TimeoutScheduler,
	taskRunner async.TaskRunner,
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
		commissionCfg:  game.DefaultCommissionConfig(),
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

// SetLeaderboardService 注入排行榜构建服务，
// 供游戏结束时从 MySQL session_players + bill_record 构建完整排行榜。
func (s *RoundSettlementService) SetLeaderboardService(ls *LeaderboardService) {
	s.leaderboardSvc = ls
}

// SettleRound 执行单局结算。
// 该方法由 GameAppService.GrabPacket/OnRobotGrabbed（通过异步任务）和
// GameLifecycleService.OnGrabTimeout（同步调用）触发。
func (s *RoundSettlementService) SettleRound(ctx context.Context, roomID, roundID string) {
	lockKey := rediskeys.SettleLockKey(roomID, roundID)

	err := lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.GameAppSettleLockTTL.Seconds()), func() error {
		meta, _ := s.repo.GetRoomMeta(ctx, roomID)

		var sessionPlayerTotalsKey string
		if meta != nil && meta.CurrentSessionID != "" {
			sessionPlayerTotalsKey = rediskeys.SessionPlayerTotalsKey(roomID, meta.CurrentSessionID)
		}

		keys := []string{
			rediskeys.RoundStateKey(roomID, roundID),
			rediskeys.RoundGrabbersKey(roomID, roundID),
			rediskeys.RoomPlayersKey(roomID),
			rediskeys.RoomHashKey(roomID),
			rediskeys.RoundAvailablePacketsKey(roomID, roundID),
			sessionPlayerTotalsKey,
		}

		args := []interface{}{
			roundID,
			time.Now().Unix(),
			rediskeys.PacketInfoPrefix(roomID),
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

		var results []push.RoundEndResult
		if len(res) > 6 {
			if arr, ok := res[6].([]interface{}); ok {
				for _, item := range arr {
					if tuple, ok := item.([]interface{}); ok && len(tuple) >= 7 {
						isAutoAssigned := converter.ParseInt(tuple[5]) == 1
						packetID := converter.ParseInt64(tuple[6])
						results = append(results, push.RoundEndResult{
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

		// 游戏结束时从 MySQL 构建完整排行榜（含被踢/替补玩家）。
		// 替代原从 Lua res[9] 读取 finalResults 的方式（原方式会漏掉替补玩家、
		// 被踢玩家 nickname/avatar 为空）。失败时降级为空 finalResults，
		// 前端显示空排行榜，记 Error 日志，不阻塞游戏结束流程。
		var finalResults []push.GameResult
		if isGameEnd && s.leaderboardSvc != nil && meta != nil {
			fr, err := s.leaderboardSvc.BuildFinalLeaderboard(ctx, meta.CurrentSessionID)
			if err != nil {
				logger.Error("build final leaderboard failed",
					"room_id", roomID,
					"session_id", meta.CurrentSessionID,
					"error", err)
			} else {
				finalResults = fr
				s.leaderboardSvc.CleanupRedisTotals(ctx, roomID, meta.CurrentSessionID)
			}
		}

		logger.Info("reward from redis", "rewardType", rewardType, "rewardAmount", rewardAmount)

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushRoundEnd, &push.RoundEndPush{
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
			roundResults := make([]*events.RoundResult, 0, len(results))
			for _, r := range results {
				roundResults = append(roundResults, &events.RoundResult{
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
			event := &events.GameEvent{
				EventHeader: message.NewEventHeader(roundSettleTraceID),
				RoomID:      roomID,
				SessionID:   meta.CurrentSessionID,
				RoundID:     roundID,
				EventType:   events.GameEventRoundSettle,
			}
			_ = event.SetPayload(&events.RoundSettleData{
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
			var finalResultsForEvent []*events.FinalResult
			if meta != nil {
				finalResultsForEvent = make([]*events.FinalResult, 0, len(finalResults))
				for _, r := range finalResults {
					finalResultsForEvent = append(finalResultsForEvent, &events.FinalResult{
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
						AllowedStatus: int(room.RoomStatusPlaying),
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
				if rewardType == reward.RewardTypeStraight {
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
