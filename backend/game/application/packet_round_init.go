package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain/room"
	roundDom "github.com/cashparty/backend/game/domain/round"
	"github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/settlement/domain"
	settlementDto "github.com/cashparty/backend/settlement/dto"
	"gorm.io/gorm"
)

type initRoundResult struct {
	RoundID      int64
	RoundNo      int
	SessionID    int64
	RoomID       int64
	SenderID     int64
	SenderType   string
	TotalAmount  int64
	Commission   int64
	DeductScene  int
	DeductAmount int64
	BatchID      string
}

// roundInitDeductStep 封装不同轮次类型的扣款步骤差异，供 initRoundCore 模板方法注入。
type roundInitDeductStep struct {
	deductScene  int    // 扣款场景
	deductAmount int64  // 扣款金额
	senderID     int64  // 发送者 ID
	senderType   string // 发送者类型
	totalAmount  int64  // 用于计算佣金的总额
	// deductFn 执行扣款并返回 batchID。失败时由 deductFn 内部处理 updateRoundFailed 与日志，
	// 然后返回非 nil error，initRoundCore 据此返回 CodeSystemError。
	deductFn func(ctx context.Context, roomID string, roundID int64) (batchID string, err error)
}

// initRoundCore 是轮次初始化的模板方法，处理公共样板：
// 创建轮次记录 → 执行扣款 → 更新扣款成功信息 → 更新发送者 → 更新金额。
// 扣款步骤以闭包注入，保持三种轮次类型（首轮/后续轮/系统轮）的行为完全一致。
func (p *PacketOrchestrator) initRoundCore(
	ctx context.Context,
	roomID string,
	meta *room.RoomMeta,
	roundNo int,
	step *roundInitDeductStep,
) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	round, err := p.createRoundRecord(ctx, roomIDInt, sessionID, roundNo)
	if err != nil {
		logger.Error("create round record failed", "room_id", roomID, "round_no", roundNo, "error", err)
		return nil, message.NewError(message.CodeSystemError)
	}

	// 写入 round_player_snapshot（失败仅告警，不阻断 round 创建）
	// snapshot 是辅助事实表，不应影响游戏主流程
	if err := p.writeRoundSnapshots(ctx, roomID, sessionID, round.RoundID, roundNo, step.senderID); err != nil {
		logger.Warn("write round snapshots failed",
			"room_id", roomID,
			"round_id", round.RoundID,
			"round_no", roundNo,
			"error", err)
	}

	batchID, err := step.deductFn(ctx, roomID, round.RoundID)
	if err != nil {
		return nil, message.NewError(message.CodeSystemError)
	}

	if err := p.updateRoundDeductSuccess(ctx, round.RoundID, step.deductScene, 1, step.deductAmount, batchID); err != nil {
		logger.Error("update round deduct info failed", "round_id", round.RoundID, "error", err)
	}

	if err := p.dbRepo.RoundDBRepo().UpdateRoundSender(ctx, round.RoundID, step.senderID, step.senderType); err != nil {
		logger.Error("update round sender failed", "round_id", round.RoundID, "error", err)
	}

	commission := p.commissionCfg.Calculate(step.totalAmount)
	if err := p.dbRepo.RoundDBRepo().UpdateRoundAmount(ctx, round.RoundID, step.totalAmount, commission); err != nil {
		logger.Error("update round amount failed", "round_id", round.RoundID, "error", err)
	}

	return &initRoundResult{
		RoundID:      round.RoundID,
		RoundNo:      roundNo,
		SessionID:    sessionID,
		RoomID:       roomIDInt,
		SenderID:     step.senderID,
		SenderType:   step.senderType,
		TotalAmount:  step.totalAmount,
		Commission:   commission,
		DeductScene:  step.deductScene,
		DeductAmount: step.deductAmount,
		BatchID:      batchID,
	}, nil
}

func (p *PacketOrchestrator) createRoundRecord(ctx context.Context, roomID, sessionID int64, roundNo int) (*model.Round, error) {
	// 先查询是否已有 Pending round（罚款时预创建的）- 保留快路径查询
	existing, err := p.dbRepo.RoundDBRepo().GetRoundBySessionAndRoundNo(ctx, sessionID, roundNo)
	if err == nil && existing != nil {
		// 复用预创建的 round，保留原 roundID 和 created_at
		return existing, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("query round failed: %w", err)
	}

	// 不存在则幂等创建（INSERT IGNORE + 查询，并发安全）
	roundID, err := p.idGen.GenerateInt64()
	if err != nil {
		return nil, fmt.Errorf("generate round id: %w", err)
	}
	round := &model.Round{
		RoundID:   roundID,
		SessionID: sessionID,
		RoomID:    roomID,
		RoundNo:   roundNo,
		Status:    model.RoundStatusPending,
	}
	return p.dbRepo.RoundDBRepo().CreateOrGetRound(ctx, round)
}

// writeRoundSnapshots 从 Redis 读取当前玩家 + 旁观者状态，批量写入 round_player_snapshot。
// 失败仅告警，不阻断 round 创建（snapshot 是辅助事实表，不应影响游戏主流程）。
func (p *PacketOrchestrator) writeRoundSnapshots(ctx context.Context, roomID string, sessionID, roundID int64, roundNo int, senderID int64) error {
	stateData, err := p.repo.GetRoomStateData(ctx, roomID)
	if err != nil {
		return fmt.Errorf("get room state data failed: %w", err)
	}

	now := time.Now()
	snapshots := make([]*model.RoundPlayerSnapshot, 0, len(stateData.Players)+len(stateData.Spectators))

	// 玩家快照
	for _, player := range stateData.Players {
		seatNo := player.SeatNo
		userID := converter.ParseID(player.UserID)
		snapshots = append(snapshots, &model.RoundPlayerSnapshot{
			SessionID:   sessionID,
			RoundID:     roundID,
			RoundNo:     roundNo,
			UserID:      userID,
			Role:        "player",
			SeatNo:      &seatNo,
			IsSender:    userID == senderID,
			JoinedAt:    now,
			ActiveStart: now,
			Source:      "initial",
		})
	}

	// 旁观者快照（seat_no 为 null）
	for _, spectator := range stateData.Spectators {
		snapshots = append(snapshots, &model.RoundPlayerSnapshot{
			SessionID:   sessionID,
			RoundID:     roundID,
			RoundNo:     roundNo,
			UserID:      converter.ParseID(spectator.UserID),
			Role:        "spectator",
			SeatNo:      nil,
			IsSender:    false,
			JoinedAt:    now,
			ActiveStart: now,
			Source:      "initial",
		})
	}

	if len(snapshots) == 0 {
		return nil
	}

	return p.dbRepo.SnapshotDBRepo().BatchCreateOnRoundStart(ctx, snapshots)
}

func (p *PacketOrchestrator) updateRoundDeductSuccess(ctx context.Context, roundID int64, deductScene, deductStatus int, deductAmount int64, batchID string) error {
	return p.dbRepo.RoundDBRepo().UpdateRoundDeductInfo(ctx, roundID, deductScene, deductStatus, deductAmount, batchID)
}

func (p *PacketOrchestrator) updateRoundFailed(ctx context.Context, roundID int64, reason string) error {
	return p.dbRepo.RoundDBRepo().UpdateRoundFailed(ctx, roundID, reason)
}

// initRoundAndDeduct 首轮初始化与扣款（所有玩家均摊房费）。
func (p *PacketOrchestrator) initRoundAndDeduct(ctx context.Context, roomID string, meta *room.RoomMeta, roundNo int, players []*room.Player) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	deductPlayers := make([]*settlementDto.PlayerDeductInfo, 0, len(players))
	for _, player := range players {
		userID := converter.ParseID(player.UserID)
		deductPlayers = append(deductPlayers, &settlementDto.PlayerDeductInfo{
			UserID:   userID,
			Nickname: player.Nickname,
		})
	}

	roomFeePerPlayer := meta.RoomFee / int64(len(players))

	deductFn := func(ctx context.Context, roomID string, roundID int64) (string, error) {
		deductReq := &settlementDto.FirstRoundDeductRequest{
			RoomID:           roomIDInt,
			SessionID:        sessionID,
			RoundID:          roundID,
			RoundNo:          roundNo,
			RoomFeePerPlayer: roomFeePerPlayer,
			Players:          deductPlayers,
		}

		deductResult, err := p.settleAppService.DeductForFirstRound(ctx, deductReq)
		if err != nil {
			if updateErr := p.updateRoundFailed(ctx, roundID, fmt.Sprintf("deduct failed: %v", err)); updateErr != nil {
				logger.Error("update round failed status error", "round_id", roundID, "error", updateErr)
			}
			logger.Error("first round deduct failed", "room_id", roomID, "round_id", roundID, "error", err)
			return "", err
		}

		if !deductResult.AllSuccess {
			if updateErr := p.updateRoundFailed(ctx, roundID, fmt.Sprintf("partial deduct failed, success: %d, failed: %d",
				deductResult.SuccessCount, deductResult.FailedCount)); updateErr != nil {
				logger.Error("update round failed status error", "round_id", roundID, "error", updateErr)
			}
			logger.Error("partial deduct failed", "room_id", roomID, "round_id", roundID, "success", deductResult.SuccessCount, "failed", deductResult.FailedCount)
			return "", fmt.Errorf("partial deduct failed")
		}

		return deductResult.BatchID, nil
	}

	return p.initRoundCore(ctx, roomID, meta, roundNo, &roundInitDeductStep{
		deductScene:  domain.DeductSceneFirstRoundShare,
		deductAmount: meta.RoomFee,
		senderID:     0,
		senderType:   roundDom.SenderTypeSystem,
		totalAmount:  meta.RoomFee,
		deductFn:     deductFn,
	})
}

// initLaterRoundAndDeduct 后续轮初始化与扣款（最小金额玩家发包）。
func (p *PacketOrchestrator) initLaterRoundAndDeduct(ctx context.Context, roomID string, meta *room.RoomMeta, roundNo int, senderID string, roomFee int64) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	senderIDInt := converter.ParseID(senderID)
	senderType := "player"

	roundTraceID := fmt.Sprintf("RT_%d_%d", sessionID, roundNo)

	deductFn := func(ctx context.Context, roomID string, roundID int64) (string, error) {
		deductReq := &settlementDto.LaterRoundDeductRequest{
			RoomID:       roomIDInt,
			SessionID:    sessionID,
			RoundID:      roundID,
			RoundNo:      roundNo,
			RoomFee:      roomFee,
			MinPlayerID:  senderIDInt,
			RoundTraceID: roundTraceID,
		}

		err := p.settleAppService.DeductForLaterRound(ctx, deductReq)
		if err != nil {
			if updateErr := p.updateRoundFailed(ctx, roundID, fmt.Sprintf("deduct failed: %v", err)); updateErr != nil {
				logger.Error("update round failed status error", "round_id", roundID, "error", updateErr)
			}
			logger.Error("later round deduct failed", "room_id", roomID, "round_id", roundID, "error", err)
			return "", err
		}

		return roundTraceID, nil
	}

	return p.initRoundCore(ctx, roomID, meta, roundNo, &roundInitDeductStep{
		deductScene:  domain.DeductSceneLaterRoundMin,
		deductAmount: roomFee,
		senderID:     senderIDInt,
		senderType:   senderType,
		totalAmount:  roomFee,
		deductFn:     deductFn,
	})
}

// initSystemRoundAndDeduct 系统发包轮次初始化与扣款。
func (p *PacketOrchestrator) initSystemRoundAndDeduct(ctx context.Context, roomID string, meta *room.RoomMeta, roundNo int, totalAmount int64, reason string) (*initRoundResult, error) {
	roomIDInt := converter.ParseID(roomID)
	sessionID := roomIDInt
	if meta.CurrentSessionID != "" {
		sessionID = converter.ParseID(meta.CurrentSessionID)
	}

	roundTraceID := fmt.Sprintf("RT_%d_%d", sessionID, roundNo)

	deductFn := func(ctx context.Context, roomID string, roundID int64) (string, error) {
		deductReq := &settlementDto.SystemPacketDeductRequest{
			RoomID:       roomIDInt,
			SessionID:    sessionID,
			RoundID:      roundID,
			RoundNo:      roundNo,
			TotalAmount:  totalAmount,
			RoundTraceID: roundTraceID,
			Reason:       reason,
		}

		err := p.settleAppService.DeductForSystemPacket(ctx, deductReq)
		if err != nil {
			if updateErr := p.updateRoundFailed(ctx, roundID, fmt.Sprintf("deduct failed: %v", err)); updateErr != nil {
				logger.Error("update round failed status error", "round_id", roundID, "error", updateErr)
			}
			logger.Error("system packet deduct failed", "room_id", roomID, "round_id", roundID, "error", err)
			return "", err
		}

		return roundTraceID, nil
	}

	return p.initRoundCore(ctx, roomID, meta, roundNo, &roundInitDeductStep{
		deductScene:  domain.DeductSceneSystemPacket,
		deductAmount: totalAmount,
		senderID:     0,
		senderType:   "system",
		totalAmount:  totalAmount,
		deductFn:     deductFn,
	})
}
