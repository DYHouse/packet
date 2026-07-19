package application

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/i18n"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/push"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/cashparty/backend/game/scheduler"
)

// HandleDeductFailure 处理扣款失败：广播中断事件并异步结束游戏。
// 实现 DeductFailureHandler 接口，供 PacketOrchestrator 跨 Service 调用。
func (s *GameLifecycleService) HandleDeductFailure(ctx context.Context, roomID string, meta *room.RoomMeta, reason string, err error) {
	if s.broadcaster != nil {
		s.broadcaster.Broadcast(ctx, roomID, message.PushGameInterrupted, &push.GameInterruptedPush{
			RoomID: roomID,
			Reason: reason,
		}, "")
	}

	logger.Error("deduct failed, game ended", "room_id", roomID, "reason", reason, "error", err)

	var sessionID string
	var actualRounds int
	if meta != nil {
		sessionID = meta.CurrentSessionID
		actualRounds = int(meta.CurrentRound)
	}

	if err := s.taskRunner.Submit("end_game_on_deduct_failure", 15*time.Second, func(ctx context.Context) {
		if err := s.EndGameWithOptions(ctx, roomID, &EndGameOptions{
			AllowedStatus: int(room.RoomStatusPlaying),
			EndReason:     reason,
			SessionID:     sessionID,
			ActualRounds:  actualRounds,
		}); err != nil {
			logger.Error("endGameWithOptions failed (deduct failure)",
				"room_id", roomID,
				"session_id", sessionID,
				"reason", reason,
				"error", err)
		}
	}); err != nil {
		logger.Warn("submit end_game_on_deduct_failure task failed", "error", err)
	}
}

// EndGameWithOptions 结束游戏并发布 SessionEnd 事件。
// 实现 GameEnder 接口，供 RoundSettlementService 通过异步任务回调。
func (s *GameLifecycleService) EndGameWithOptions(ctx context.Context, roomID string, opts *EndGameOptions) error {
	if opts == nil {
		opts = &EndGameOptions{
			AllowedStatus: int(room.RoomStatusPlaying),
			EndReason:     message.ReasonNormalEnd,
		}
	}

	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
	}

	args := []interface{}{
		time.Now().Unix(),
		opts.AllowedStatus,
		int64(s.redisTTL.RoomDataTTL.Seconds()),
	}

	res, err := scripts.EndGame.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("end game lua failed", "room_id", roomID, "error", err)
		return err
	}

	code := converter.ParseInt(res[0])
	if code == 1 {
		logger.Info("game already ended (idempotent)", "room_id", roomID)
		return nil
	}
	if code != 0 {
		logger.Error("end game failed", "room_id", roomID, "lua_code", code)
		return domain.MapLuaError(code)
	}

	var results []push.GameResult
	if arr, ok := res[1].([]interface{}); ok {
		for _, item := range arr {
			if tuple, ok := item.([]interface{}); ok && len(tuple) >= 2 {
				results = append(results, push.GameResult{
					UserID:   converter.ParseString(tuple[0]),
					Nickname: converter.ParseString(tuple[1]),
				})
			}
		}
	}

	if s.scheduler != nil {
		for _, r := range results {
			s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeSeat, roomID, r.UserID)
		}
	}

	if s.broadcaster != nil {
		stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
		if stateData != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushRoomState, BuildFullRoomState(stateData), "")
		}
	}

	// 统一发布 SessionEnd 事件（所有游戏结束路径都经过这里）
	if s.eventPublisher != nil && opts.SessionID != "" {
		sessionEndTraceID, err := s.idGen.GenerateString()
		if err != nil {
			logger.Warn("generate trace id failed for session end event",
				"room_id", roomID,
				"error", err,
			)
		}
		sessionEndEvent := &events.GameEvent{
			EventHeader: message.NewEventHeader(sessionEndTraceID),
			RoomID:      roomID,
			SessionID:   opts.SessionID,
			EventType:   events.GameEventSessionEnd,
		}
		_ = sessionEndEvent.SetPayload(&events.SessionEndData{
			ActualRounds: opts.ActualRounds,
			EndReason:    opts.EndReason,
			FinalResults: opts.FinalResults,
		})
		if err := s.taskRunner.Submit("publish_session_end", 5*time.Second, func(ctx context.Context) {
			if err := s.eventPublisher.PublishGameEvent(ctx, sessionEndEvent); err != nil {
				logger.Error("publish session end event failed", "room_id", roomID, "error", err)
			}
		}); err != nil {
			logger.Warn("submit publish_session_end task failed", "error", err)
		}
	}

	logger.Info("game ended", "room_id", roomID, "player_count", len(results), "reason", opts.EndReason)

	// Notify the robot scheduler so it can schedule delayed robot leaves.
	// 本函数的所有调用方（HandleDeductFailure / RoundSettlementService / OnReplaceTimeout）
	// 均已在 taskRunner 异步任务或调度器回调中执行，无需再嵌套 Submit，
	// 否则 Submit 失败会导致 callback 永远不执行，产生僵尸机器人。
	// 同步调用附带 panic recovery，防止 callback 异常影响 EndGame 主流程。
	if s.gameEndCallback != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("game_end_callback panic",
						"room_id", roomID,
						"panic", r,
					)
				}
			}()
			s.gameEndCallback(ctx, roomID)
		}()
	}

	return nil
}

// handleKickAndReplace 踢出玩家并尝试自动替补，无替补时进入 wait_replacement 流程。
func (s *GameLifecycleService) handleKickAndReplace(ctx context.Context, roomID, userID string, penaltyAmount int64) {
	result, err := s.repo.KickPlayerAndInterrupt(ctx, roomID, userID, "penalty_kick")
	if err != nil {
		logger.Error("kick player and interrupt failed",
			"room_id", roomID,
			"user_id", userID,
			"error", err)
		return
	}

	logger.Info("player kicked due to penalty",
		"room_id", roomID,
		"user_id", userID,
		"seat_no", result.SeatNo,
		"room_status", result.RoomStatus)

	if s.scheduler != nil {
		s.scheduler.ClearAllUserTimeouts(ctx, roomID, userID)
		s.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeReplace, roomID, userID)
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(ctx, userID, message.PushKicked, &push.KickedPush{
			RoomID:  roomID,
			UserID:  userID,
			Reason:  message.ReasonPenaltyKick,
			Message: i18n.GetKickMessage(message.ReasonPenaltyKick),
		})
	}

	// 尝试从排队队列自动替补
	substituted := false
	if s.roomAppService != nil && result.SeatNo > 0 {
		subResult := s.roomAppService.TryAutoSubstitute(ctx, roomID, result.SeatNo)
		substituted = subResult != nil
	}

	// 无替补时走原有 wait_replacement 流程
	if !substituted {
		if s.broadcaster != nil {
			stateData, _ := s.repo.GetRoomStateData(ctx, roomID)
			if stateData != nil {
				s.broadcaster.Broadcast(ctx, roomID, message.PushRoomState, BuildFullRoomState(stateData), userID)
			}

			s.broadcaster.Broadcast(ctx, roomID, message.PushWaitReplacement, &push.WaitReplacementPush{
				RoomID:     roomID,
				VacantSeat: int32(result.SeatNo),
				LeftUserID: userID,
				WaitTime:   int32(s.timeoutCfg.Replace.Seconds()),
			}, "")
		}
	}
}
