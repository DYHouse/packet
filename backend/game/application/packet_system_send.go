package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain/events"
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/domain/round"
	"github.com/cashparty/backend/game/model"
)

// handleSystemSendTimeout 处理系统发包超时（豹子奖励触发）。
func (p *PacketOrchestrator) HandleSystemSendTimeout(ctx context.Context, roomID string) {
	logger.Info("system send packet triggered by leopard reward", "room_id", roomID)

	meta, err := p.repo.GetRoomMeta(ctx, roomID)
	if err != nil || meta == nil {
		logger.Error("failed to get room meta for system send", "room_id", roomID, "error", err)
		return
	}

	nextRound := int(meta.CurrentRound) + 1

	p.executeSystemSendPacket(ctx, &systemSendPacketParams{
		RoomID:      roomID,
		Meta:        meta,
		RoundNo:     nextRound,
		TotalAmount: meta.RoomFee,
		Reason:      "leopard_reward",
		Scenario:    round.SendScenarioLeopardReward,
		SenderType:  round.SenderTypeSystem,
		Nickname:    "system",
	})
}

// StartFirstRound 启动首轮发包（由 GameLifecycleService.StartGame 调用）。
func (p *PacketOrchestrator) StartFirstRound(ctx context.Context, roomID string, meta *room.RoomMeta) {
	playersMap, err := p.repo.GetPlayers(ctx, roomID)
	if err != nil {
		logger.Error("get players failed", "room_id", roomID, "error", err)
		return
	}

	if len(playersMap) == 0 {
		logger.Error("no players in room", "room_id", roomID)
		return
	}

	players := make([]*room.Player, 0, len(playersMap))
	for _, player := range playersMap {
		players = append(players, player)
	}

	initResult, err := p.initRoundAndDeduct(ctx, roomID, meta, 1, players)
	if err != nil {
		logger.Error("first round init and deduct failed", "room_id", roomID, "error", err)
		if p.deductFailureHandler != nil {
			p.deductFailureHandler.HandleDeductFailure(ctx, roomID, meta, message.ReasonFirstRoundDeductFailed, err)
		}
		return
	}

	result, err := p.sendPacketPipeline(ctx, &round.SendPacketParams{
		RoomID:      roomID,
		Scenario:    round.SendScenarioFirstRound,
		TotalAmount: meta.RoomFee,
		RoundNo:     1,
		RoundID:     initResult.RoundID,
		PlayerCount: meta.MaxPlayers,
	})
	if err != nil {
		logger.Error("first round send failed", "room_id", roomID, "error", err)
		if updateErr := p.updateRoundFailed(ctx, initResult.RoundID, fmt.Sprintf("send packet failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", initResult.RoundID, "error", updateErr)
		}
		return
	}

	if err := p.dbRepo.RoundDBRepo().UpdateRoundStatus(ctx, initResult.RoundID, model.RoundStatusSending); err != nil {
		logger.Error("update round status to sending failed", "round_id", initResult.RoundID, "error", err)
	}

	if err := p.taskRunner.Submit("post_send_packet_first_round", 10*time.Second, func(ctx context.Context) {
		p.postSendPacketAsync(ctx, &postSendPacketParams{
			RoomID:       roomID,
			RoundID:      result.RoundID,
			PacketIDs:    result.PacketIDs,
			UserID:       "0",
			Nickname:     "system",
			Amount:       result.Amount,
			Commission:   result.Commission,
			ActualAmount: result.ActualAmount,
			NextRound:    1,
			PacketCount:  result.PacketCount,
			SenderType:   round.SenderTypeSystem,
		})
	}); err != nil {
		logger.Warn("submit post_send_packet_first_round task failed", "error", err)
	}
}

type systemSendPacketParams struct {
	RoomID      string
	Meta        *room.RoomMeta
	RoundNo     int
	TotalAmount int64
	Reason      string
	Scenario    round.SendScenario
	SenderType  string
	Nickname    string
}

func (p *PacketOrchestrator) executeSystemSendPacket(ctx context.Context, params *systemSendPacketParams) {
	initResult, err := p.initSystemRoundAndDeduct(ctx, params.RoomID, params.Meta, params.RoundNo, params.TotalAmount, params.Reason)
	if err != nil {
		logger.Error("init system round failed",
			"room_id", params.RoomID,
			"round_no", params.RoundNo,
			"reason", params.Reason,
			"error", err)
		return
	}

	result, err := p.sendPacketPipeline(ctx, &round.SendPacketParams{
		RoomID:      params.RoomID,
		SenderID:    "0",
		Scenario:    params.Scenario,
		TotalAmount: params.TotalAmount,
		RoundNo:     params.RoundNo,
		RoundID:     initResult.RoundID,
		PlayerCount: params.Meta.MaxPlayers,
	})
	if err != nil {
		logger.Error("system send packet failed",
			"room_id", params.RoomID,
			"reason", params.Reason,
			"error", err)
		if updateErr := p.updateRoundFailed(ctx, initResult.RoundID, fmt.Sprintf("send packet failed: %v", err)); updateErr != nil {
			logger.Error("update round failed status error", "round_id", initResult.RoundID, "error", updateErr)
		}
		return
	}

	if err := p.dbRepo.RoundDBRepo().UpdateRoundStatus(ctx, initResult.RoundID, model.RoundStatusSending); err != nil {
		logger.Error("update round status to sending failed", "round_id", initResult.RoundID, "error", err)
	}

	result.Nickname = params.Nickname

	if err := p.taskRunner.Submit("post_send_packet_system", 10*time.Second, func(ctx context.Context) {
		p.postSendPacketAsync(ctx, &postSendPacketParams{
			RoomID:       params.RoomID,
			RoundID:      result.RoundID,
			PacketIDs:    result.PacketIDs,
			UserID:       "0",
			Nickname:     params.Nickname,
			Amount:       result.Amount,
			Commission:   result.Commission,
			ActualAmount: result.ActualAmount,
			NextRound:    params.RoundNo,
			PacketCount:  result.PacketCount,
			SenderType:   params.SenderType,
		})
	}); err != nil {
		logger.Warn("submit post_send_packet_system task failed", "error", err)
	}
}

// ForceSendPacketForPlayer 强制为玩家发包（超时罚扣后由 GameLifecycleService 调用）。
func (p *PacketOrchestrator) ForceSendPacketForPlayer(ctx context.Context, roomID, userID string, penaltyAmount int64) {
	meta, err := p.repo.GetRoomMeta(ctx, roomID)
	if err != nil || meta == nil {
		logger.Error("failed to get room meta for force send", "room_id", roomID, "user_id", userID, "error", err)
		return
	}

	nextRound := int(meta.CurrentRound) + 1

	player, _ := p.repo.GetPlayer(ctx, roomID, userID)
	nickname := ""
	if player != nil {
		nickname = player.Nickname
	}

	p.executeSystemSendPacket(ctx, &systemSendPacketParams{
		RoomID:      roomID,
		Meta:        meta,
		RoundNo:     nextRound,
		TotalAmount: penaltyAmount,
		Reason:      "send_timeout_forced",
		Scenario:    round.SendScenarioTimeoutForced,
		SenderType:  round.SenderTypeSystemForced,
		Nickname:    nickname,
	})
}

// SystemSendRound 系统发包轮次（恢复中断游戏时由 GameLifecycleService.ResumeGame 调用）。
func (p *PacketOrchestrator) SystemSendRound(ctx context.Context, roomID string, meta *room.RoomMeta, roundNo int) {
	nextRound := roundNo + 1

	p.executeSystemSendPacket(ctx, &systemSendPacketParams{
		RoomID:      roomID,
		Meta:        meta,
		RoundNo:     nextRound,
		TotalAmount: meta.RoomFee,
		Reason:      "resume_interrupt",
		Scenario:    round.SendScenarioResumeInterrupt,
		SenderType:  round.SenderTypeSystemResume,
		Nickname:    "system",
	})
}

func (p *PacketOrchestrator) publishPacketCreatedEvent(ctx context.Context, roomID, roundID string, packetIDs []string, senderID string, totalAmount, commission int64, roundNo int) {
	if p.eventPublisher == nil {
		return
	}

	meta, _ := p.repo.GetRoomMeta(ctx, roomID)
	if meta == nil {
		logger.Error("failed to get room meta for packet created event", "room_id", roomID)
		return
	}

	packets := make([]*events.PacketData, 0, len(packetIDs))
	for _, packetIDStr := range packetIDs {
		packetData, err := p.packetCache.GetPacketInfo(ctx, roomID, packetIDStr)
		if err != nil {
			logger.Error("failed to get packet info", "packet_id", packetIDStr, "error", err)
			continue
		}

		var packet struct {
			PacketID string `json:"packet_id"`
			RoomID   string `json:"room_id"`
			RoundID  string `json:"round_id"`
			Amount   int64  `json:"amount"`
			Position int    `json:"position"`
		}
		if err := json.Unmarshal([]byte(packetData), &packet); err != nil {
			logger.Error("failed to unmarshal packet info", "packet_id", packetIDStr, "error", err)
			continue
		}

		packets = append(packets, &events.PacketData{
			PacketID: packet.PacketID,
			RoomID:   packet.RoomID,
			RoundID:  packet.RoundID,
			Amount:   packet.Amount,
			Position: packet.Position,
		})
	}

	senderType := "player"
	if senderID == "0" {
		senderType = "system"
	}

	packetCreatedTraceID, err := p.idGen.GenerateString()
	if err != nil {
		logger.Warn("generate trace id failed for packet created event",
			"room_id", roomID,
			"round_id", roundID,
			"error", err,
		)
	}
	event := &events.GameEvent{
		EventHeader: message.NewEventHeader(packetCreatedTraceID),
		RoomID:      roomID,
		SessionID:   meta.CurrentSessionID,
		RoundID:     roundID,
		EventType:   events.GameEventPacketCreated,
	}
	_ = event.SetPayload(&events.PacketCreatedData{
		RoomID:      roomID,
		SessionID:   meta.CurrentSessionID,
		RoundID:     roundID,
		RoundNo:     roundNo,
		SenderID:    senderID,
		SenderType:  senderType,
		TotalAmount: totalAmount,
		Commission:  commission,
		Packets:     packets,
	})

	if err := p.taskRunner.Submit("publish_packet_created", 5*time.Second, func(ctx context.Context) {
		if err := p.eventPublisher.PublishGameEvent(ctx, event); err != nil {
			logger.Error("publish packet created event failed", "error", err)
		}
	}); err != nil {
		logger.Warn("submit publish_packet_created task failed", "error", err)
	}
}
