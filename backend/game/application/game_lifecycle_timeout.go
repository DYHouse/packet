package application

import (
	"context"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/i18n"
	"github.com/cashparty/backend/common/lock"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain/push"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/domain/round"
	settlementDto "github.com/cashparty/backend/settlement/dto"
)

// OnGrabTimeout 处理抢红包超时：自动分配剩余红包并触发单局结算。
func (s *GameLifecycleService) OnGrabTimeout(ctx context.Context, roomID string, roundID string) {
	logger.Warn("grab timeout, auto distributing", "room_id", roomID, "round_id", roundID)

	count, results, err := s.grabService.AutoDistribute(ctx, roomID, roundID)
	if err != nil {
		logger.Error("auto distribute failed", "room_id", roomID, "round_id", roundID, "error", err)
		return
	}

	if s.broadcaster != nil {
		msgResults := make([]push.DistributeResultPush, len(results))
		for i, r := range results {
			msgResults[i] = push.DistributeResultPush{
				UserID:   r.UserID,
				Amount:   currency.NewMoneyFromFen(r.Amount),
				Position: r.Position,
			}
		}
		s.broadcaster.Broadcast(ctx, roomID, message.PushAutoDistribute, &push.AutoDistributePush{
			RoomID:           roomID,
			RoundID:          roundID,
			DistributedCount: int32(count),
			Results:          msgResults,
		}, "")
	}

	if s.roundSettler != nil {
		s.roundSettler.SettleRound(ctx, roomID, roundID)
	}
}

// OnSendTimeout 处理发红包超时：罚扣并强制发包或踢人替补。
func (s *GameLifecycleService) OnSendTimeout(ctx context.Context, roomID string, userID string) {
	logger.Warn("send timeout triggered", "room_id", roomID, "user_id", userID)

	if userID == "0" {
		if s.packetInitiator != nil {
			s.packetInitiator.HandleSystemSendTimeout(ctx, roomID)
		}
		return
	}

	lockKey := rediskeys.SendPacketLockKey(roomID, userID)

	err := lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.SendTimeoutLockTTL.Seconds()), func() error {
		meta, err := s.repo.GetRoomMeta(ctx, roomID)
		if err != nil {
			logger.Error("failed to get room meta for timeout", "room_id", roomID, "error", err)
			return nil
		}

		if meta.CurrentRoundID != "" {
			logger.Info("packet already sent, skip timeout handling",
				"room_id", roomID,
				"user_id", userID,
				"current_round_id", meta.CurrentRoundID,
			)
			return nil
		}

		nextSenderID, _ := s.repo.GetNextSenderID(ctx, roomID)
		if nextSenderID != "" && nextSenderID != userID {
			logger.Info("not this player's turn, skip timeout handling",
				"room_id", roomID,
				"user_id", userID,
				"next_sender_id", nextSenderID,
			)
			return nil
		}

		roomIDInt := converter.ParseID(roomID)
		sessionID := roomIDInt
		if meta.CurrentSessionID != "" {
			sessionID = converter.ParseID(meta.CurrentSessionID)
		}

		// 预创建下一轮 Pending round（罚款根因 = 下一轮未发包）
		nextRoundNo := int(meta.CurrentRound) + 1
		roundID, err := s.EnsureNextRound(ctx, roomIDInt, sessionID, nextRoundNo)
		if err != nil {
			logger.Error("ensure next round failed", "room_id", roomID, "session_id", meta.CurrentSessionID, "round_no", nextRoundNo, "error", err)
			roundID = 0
		}

		result, err := s.penaltyService.ApplyPenalty(ctx, roomID, userID, round.PenaltyTypeSendTimeout, meta.RoomFee, sessionID, nextRoundNo, roundID)
		if err != nil {
			logger.Error("apply penalty failed", "room_id", roomID, "user_id", userID, "error", err)
			return nil
		}

		if result.DeductFailed {
			s.HandleDeductFailure(ctx, roomID, meta, message.ReasonPenaltyDeductFailed, result.DeductError)
			return nil
		}

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushPenalty, &push.PenaltyPush{
				RoomID:        roomID,
				UserID:        userID,
				PenaltyType:   message.ReasonPenaltySendTimeout,
				PenaltyAmount: currency.NewMoneyFromFen(result.Amount),
				PenaltyCount:  int32(result.Count),
				KickRequired:  result.KickRequired,
				Reason:        i18n.GetPenaltyMessage(result.Reason),
			}, "")
		}

		if result.KickRequired {
			s.handleKickAndReplace(ctx, roomID, userID, meta.RoomFee)
		} else {
			if s.packetInitiator != nil {
				s.packetInitiator.ForceSendPacketForPlayer(ctx, roomID, userID, result.Amount)
			}
		}

		return nil
	})

	if err != nil {
		logger.Error("failed to acquire lock for send timeout",
			"room_id", roomID,
			"user_id", userID,
			"error", err,
		)
	}
}

// OnReplaceTimeout 处理替补超时：分配罚扣并结束游戏。
func (s *GameLifecycleService) OnReplaceTimeout(ctx context.Context, roomID string, leftUserID string) {
	logger.Warn("replacement timeout", "room_id", roomID, "left_user_id", leftUserID)

	lockKey := rediskeys.ReplaceTimeoutLockKey(roomID, leftUserID)

	err := lock.WithRedisLock(ctx, lockKey, int(s.lockCfg.ReplaceTimeoutLockTTL.Seconds()), func() error {
		meta, err := s.repo.GetRoomMeta(ctx, roomID)
		if err != nil || meta == nil {
			logger.Error("failed to get room meta for replacement timeout", "room_id", roomID, "error", err)
			return nil
		}

		if meta.Status != room.RoomStatusInterrupted {
			logger.Info("room status changed, skip replacement timeout", "room_id", roomID, "status", meta.Status)
			return nil
		}

		dist, err := s.penaltyService.DistributePenalty(ctx, roomID, meta.RoomFee, []string{leftUserID})
		if err != nil {
			logger.Error("distribute penalty failed", "room_id", roomID, "error", err)
			return err
		}

		roomIDInt := converter.ParseID(roomID)
		sessionID := roomIDInt
		if meta.CurrentSessionID != "" {
			sessionID = converter.ParseID(meta.CurrentSessionID)
		}

		recipientIDs := make([]int64, 0, len(dist.Recipients))
		for _, r := range dist.Recipients {
			recipientIDs = append(recipientIDs, converter.ParseID(r))
		}

		// 预创建下一轮 Pending round（罚款根因 = 下一轮未发包）
		nextRoundNo := int(meta.CurrentRound) + 1
		roundID, err := s.EnsureNextRound(ctx, roomIDInt, sessionID, nextRoundNo)
		if err != nil {
			logger.Error("ensure next round failed", "room_id", roomID, "session_id", meta.CurrentSessionID, "round_no", nextRoundNo, "error", err)
			roundID = 0
		}

		distReq := &settlementDto.PenaltyDistributeRequest{
			RoomID:       roomIDInt,
			SessionID:    sessionID,
			RoundID:      roundID,
			RoundNo:      nextRoundNo,
			Amount:       meta.RoomFee,
			Recipients:   recipientIDs,
			Reason:       "replacement_timeout",
			TriggerPhase: "inter_round",
		}

		if err := s.settleAppService.DistributePenaltyFromPlatform(ctx, distReq); err != nil {
			logger.Error("distribute penalty from platform failed", "room_id", roomID, "error", err)
		}

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(ctx, roomID, message.PushGameInterrupted, &push.GameInterruptedPush{
				RoomID:       roomID,
				Reason:       message.ReasonReplacementTimeout,
				PenaltyShare: currency.NewMoneyFromFen(dist.ShareAmount),
				Recipients:   dist.Recipients,
			}, "")
		}

		if err := s.EndGameWithOptions(ctx, roomID, &EndGameOptions{
			AllowedStatus: int(room.RoomStatusInterrupted),
			EndReason:     message.ReasonReplacementTimeout,
			SessionID:     meta.CurrentSessionID,
			ActualRounds:  int(meta.CurrentRound),
		}); err != nil {
			logger.Error("endGameWithOptions failed (replace timeout)",
				"room_id", roomID,
				"session_id", meta.CurrentSessionID,
				"error", err)
		}
		return nil
	})

	if err != nil {
		logger.Error("replace timeout handling failed", "room_id", roomID, "left_user_id", leftUserID, "error", err)
	}
}
