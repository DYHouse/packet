package application

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	settlementDomain "github.com/cashparty/backend/settlement/domain"
)

// HistoryService 玩家历史记录应用服务
type HistoryService struct {
	dbRepo   domain.DBRepository
	billRepo settlementDomain.BillRepository
}

// NewHistoryService 创建 HistoryService 实例
func NewHistoryService(dbRepo domain.DBRepository, billRepo settlementDomain.BillRepository) *HistoryService {
	return &HistoryService{
		dbRepo:   dbRepo,
		billRepo: billRepo,
	}
}

// GetPlayerHistory 获取玩家历史对局列表（按时间倒序分页）
func (s *HistoryService) GetPlayerHistory(ctx context.Context, userID int64, req PlayerHistoryReq) (*PlayerHistoryResp, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	rows, total, err := s.dbRepo.HistoryDBRepo().ListPlayerSessionsWithBill(userID, req.StartDate, req.EndDate, req.ConfigName, pageSize, offset)
	if err != nil {
		logger.Error("failed to list player sessions with bill", "user_id", userID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}

	items := make([]PlayerHistoryItem, 0, len(rows))
	for i := range rows {
		items = append(items, playerSessionBillRowToItem(&rows[i]))
	}

	return &PlayerHistoryResp{
		List:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// GetPlayerSessionDetail 获取玩家某局对局详情（含回合明细与个人抢包结果）
func (s *HistoryService) GetPlayerSessionDetail(ctx context.Context, userID int64, sessionID int64) (*PlayerSessionDetailResp, error) {
	// 1. 校验玩家是否属于该会话
	player, err := s.dbRepo.HistoryDBRepo().GetPlayerSession(userID, sessionID)
	if err != nil {
		logger.Error("failed to get player session", "user_id", userID, "session_id", sessionID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}
	if player == nil {
		return nil, message.NewError(message.CodePlayerNotInSession)
	}

	// 2. 查询会话基本信息
	session, err := s.dbRepo.HistoryDBRepo().GetSession(sessionID)
	if err != nil {
		logger.Error("failed to get session", "session_id", sessionID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}
	if session == nil {
		return nil, message.NewError(message.CodeSessionNotFound)
	}

	// 3. 查询会话所有回合与玩家抢包记录
	rounds, err := s.dbRepo.HistoryDBRepo().GetSessionRounds(sessionID)
	if err != nil {
		logger.Error("failed to get session rounds", "session_id", sessionID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}

	grabRecords, err := s.dbRepo.HistoryDBRepo().ListPlayerGrabRecords(sessionID, userID)
	if err != nil {
		logger.Error("failed to list player grab records", "session_id", sessionID, "user_id", userID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}

	// 4. 查询玩家个人结果卡片（基于 bill_record 聚合）
	billSummary, err := s.dbRepo.HistoryDBRepo().GetPlayerSessionBillSummary(userID, sessionID)
	if err != nil {
		logger.Error("failed to get player session bill summary", "user_id", userID, "session_id", sessionID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}

	// 5. 查询玩家发包回合列表
	sendRounds, err := s.dbRepo.HistoryDBRepo().GetPlayerSendRounds(sessionID, userID)
	if err != nil {
		logger.Error("failed to get player send rounds", "user_id", userID, "session_id", sessionID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}

	// 6. 构建 round_id -> grabRecord 索引
	grabByRound := make(map[int64]model.RoundGrabRecord, len(grabRecords))
	for i := range grabRecords {
		grabByRound[grabRecords[i].RoundID] = grabRecords[i]
	}

	// 7. 构建 round_id -> sendRound 索引
	sendByRound := make(map[int64]model.Round, len(sendRounds))
	for i := range sendRounds {
		sendByRound[sendRounds[i].RoundID] = sendRounds[i]
	}

	// 8. 组装回合明细
	roundDetails := make([]RoundDetail, 0, len(rounds))
	for i := range rounds {
		rd := RoundDetail{
			RoundID:     converter.FormatID(rounds[i].RoundID),
			RoundNo:     rounds[i].RoundNo,
			SenderID:    converter.FormatID(rounds[i].SenderID),
			SenderType:  rounds[i].SenderType,
			TotalAmount: currency.NewMoneyFromFen(rounds[i].TotalAmount),
			StartedAt:   timeToMs(rounds[i].StartedAt),
			EndedAt:     timeToMs(rounds[i].EndedAt),
			Status:      int(rounds[i].Status),
		}
		if rec, ok := grabByRound[rounds[i].RoundID]; ok {
			rd.MyGrab = &GrabDetail{
				PacketID:       converter.FormatID(rec.PacketID),
				Amount:         currency.NewMoneyFromFen(rec.Amount),
				IsMin:          rec.IsMin == 1,
				IsAutoAssigned: rec.IsAutoAssigned == 1,
				GrabbedAt:      rec.GrabbedAt.UnixMilli(),
			}
		}
		if sr, ok := sendByRound[rounds[i].RoundID]; ok {
			rd.MySend = &SendDetail{
				TotalAmount: currency.NewMoneyFromFen(sr.TotalAmount),
				StartedAt:   timeToMs(sr.StartedAt),
			}
		}
		roundDetails = append(roundDetails, rd)
	}

	// 9. 组装个人结果卡片（基于 bill 聚合 + player 的 seat_no/joined_at/left_at）
	myStats := PlayerHistoryItem{
		SessionID:     converter.FormatID(session.SessionID),
		RoomNo:        session.RoomNo,
		ConfigName:    session.ConfigName,
		RoomFee:       currency.NewMoneyFromFen(session.RoomFee),
		MaxRounds:     session.MaxRounds,
		ActualRounds:  session.ActualRounds,
		Status:        int(session.Status),
		StartedAt:     timeToMs(session.StartedAt),
		EndedAt:       timeToMs(session.EndedAt),
		EndReason:     session.EndReason,
		SeatNo:        player.SeatNo,
		SendCount:     int(billSummary.SendCount),
		GrabCount:     int(billSummary.GrabCount),
		TotalSend:     currency.NewMoneyFromFen(billSummary.TotalSend),
		FirstRoundFee: currency.NewMoneyFromFen(billSummary.FirstRoundFee),
		Penalty:       currency.NewMoneyFromFen(billSummary.Penalty),
		TotalBet:      currency.NewMoneyFromFen(billSummary.TotalBet),
		TotalGrab:     currency.NewMoneyFromFen(billSummary.TotalGrab),
		Profit:        currency.NewMoneyFromFen(billSummary.Profit),
		JoinedAt:      player.JoinedAt.UnixMilli(),
		LeftAt:        timeToMs(player.LeftAt),
	}

	// 10. 组装响应
	return &PlayerSessionDetailResp{
		Session: gameSessionToSessionInfo(session),
		MyStats: myStats,
		Rounds:  roundDetails,
	}, nil
}

// GetPlayerStats 获取玩家累计统计概览
func (s *HistoryService) GetPlayerStats(ctx context.Context, userID int64) (*PlayerStatsResp, error) {
	agg, err := s.dbRepo.HistoryDBRepo().AggregatePlayerStatsFromBill(userID)
	if err != nil {
		logger.Error("failed to aggregate player stats from bill", "user_id", userID, "error", err)
		return nil, message.NewError(message.CodeHistoryQueryFailed)
	}
	if agg == nil {
		return &PlayerStatsResp{}, nil
	}

	loseCount := agg.TotalGames - agg.WinCount

	var winRate float64
	var avgProfit int64
	if agg.TotalGames > 0 {
		winRate = float64(agg.WinCount) / float64(agg.TotalGames)
		avgProfit = int64(float64(agg.TotalProfit) / float64(agg.TotalGames))
	}

	return &PlayerStatsResp{
		TotalGames:     agg.TotalGames,
		WinCount:       agg.WinCount,
		LoseCount:      loseCount,
		WinRate:        winRate,
		TotalProfit:    currency.NewMoneyFromFen(agg.TotalProfit),
		TotalSend:      currency.NewMoneyFromFen(agg.TotalSend),
		FirstRoundFee:  currency.NewMoneyFromFen(agg.FirstRoundFee),
		Penalty:        currency.NewMoneyFromFen(agg.Penalty),
		TotalBet:       currency.NewMoneyFromFen(agg.TotalBet),
		TotalGrab:      currency.NewMoneyFromFen(agg.TotalGrab),
		TotalSendCount: agg.TotalSendCount,
		TotalGrabCount: agg.TotalGrabCount,
		AvgProfit:      currency.NewMoneyFromFen(avgProfit),
	}, nil
}

// playerSessionBillRowToItem 将 PlayerSessionBillRow 转换为 PlayerHistoryItem
// 数据源来自 game_sessions + bill_record 聚合，不含 session_players 的
// SeatNo/JoinedAt/LeftAt/Nickname/Avatar 字段，这些字段填零值。
func playerSessionBillRowToItem(row *domain.PlayerSessionBillRow) PlayerHistoryItem {
	return PlayerHistoryItem{
		SessionID:     converter.FormatID(row.SessionID),
		RoomNo:        row.RoomNo,
		ConfigName:    row.ConfigName,
		RoomFee:       currency.NewMoneyFromFen(row.RoomFee),
		MaxRounds:     row.MaxRounds,
		ActualRounds:  row.ActualRounds,
		Status:        row.Status,
		StartedAt:     timeToMs(row.StartedAt),
		EndedAt:       timeToMs(row.EndedAt),
		EndReason:     row.EndReason,
		SendCount:     int(row.SendCount),
		GrabCount:     int(row.GrabCount),
		TotalSend:     currency.NewMoneyFromFen(row.TotalSend),
		FirstRoundFee: currency.NewMoneyFromFen(row.FirstRoundFee),
		Penalty:       currency.NewMoneyFromFen(row.Penalty),
		TotalBet:      currency.NewMoneyFromFen(row.TotalBet),
		TotalGrab:     currency.NewMoneyFromFen(row.TotalGrab),
		Profit:        currency.NewMoneyFromFen(row.Profit),
	}
}

// gameSessionToSessionInfo 将 GameSession 转换为 SessionInfo
func gameSessionToSessionInfo(session *model.GameSession) SessionInfo {
	return SessionInfo{
		SessionID:    converter.FormatID(session.SessionID),
		RoomNo:       session.RoomNo,
		ConfigName:   session.ConfigName,
		RoomFee:      currency.NewMoneyFromFen(session.RoomFee),
		MaxRounds:    session.MaxRounds,
		ActualRounds: session.ActualRounds,
		Status:       int(session.Status),
		StartedAt:    timeToMs(session.StartedAt),
		EndedAt:      timeToMs(session.EndedAt),
		EndReason:    session.EndReason,
	}
}

// timeToMs 将 *time.Time 转为毫秒时间戳，nil 返回 0
func timeToMs(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}
