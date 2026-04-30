package application

import (
	"github.com/cashparty/backend/game/domain"
)

type RoomState struct {
	RoomID         string           `json:"room_id"`
	RoomNo         string           `json:"room_no"`
	RoomFee        int64            `json:"room_fee"`
	Status         int              `json:"status"`
	CurrentRound   int              `json:"current_round"`
	MaxRounds      int              `json:"max_rounds"`
	PlayerCount    int              `json:"player_count"`
	SpectatorCount int              `json:"spectator_count"`
	MaxPlayers     int              `json:"max_players"`
	MaxSpectators  int              `json:"max_spectators"`
	Players        []*PlayerInfo    `json:"players"`
	Spectators     []*SpectatorInfo `json:"spectators"`
	Seats          []*SeatInfo      `json:"seats"`
}

type PlayerInfo struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	SeatNo   int    `json:"seat_no"`
	IsOnline bool   `json:"is_online"`
}

type SpectatorInfo struct {
	UserID         string `json:"user_id"`
	Nickname       string `json:"nickname"`
	Avatar         string `json:"avatar"`
	SeatNo         int    `json:"seat_no"`
	SeatSelectedAt int64  `json:"seat_selected_at"`
}

type SeatInfo struct {
	SeatNo   int    `json:"seat_no"`
	Occupied bool   `json:"occupied"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	Ready    bool   `json:"ready"`
}

func BuildRoomState(meta *domain.RoomMeta) *RoomState {
	if meta == nil {
		return nil
	}
	return &RoomState{
		RoomID:         meta.RoomID,
		RoomNo:         meta.RoomNo,
		RoomFee:        meta.RoomFee,
		Status:         int(meta.Status),
		CurrentRound:   meta.CurrentRound,
		MaxRounds:      meta.MaxRounds,
		PlayerCount:    meta.PlayerCount,
		SpectatorCount: meta.SpectatorCount,
		MaxPlayers:     meta.MaxPlayers,
		MaxSpectators:  meta.MaxSpectators,
	}
}

func BuildPlayerInfo(p *domain.Player) *PlayerInfo {
	if p == nil {
		return nil
	}
	return &PlayerInfo{
		UserID:   p.UserID,
		Nickname: p.Nickname,
		Avatar:   p.Avatar,
		SeatNo:   p.SeatNo,
		IsOnline: p.IsOnline(),
	}
}

func BuildSpectatorInfo(s *domain.Spectator) *SpectatorInfo {
	if s == nil {
		return nil
	}
	return &SpectatorInfo{
		UserID:   s.UserID,
		Nickname: s.Nickname,
		Avatar:   s.Avatar,
		SeatNo:   s.SeatNo,
	}
}

func BuildFullRoomState(stateData *domain.RoomStateData) *RoomState {
	if stateData == nil {
		return nil
	}

	players := make([]*PlayerInfo, 0, len(stateData.Players))
	playerSeatMap := make(map[int]*domain.Player, len(stateData.Players))
	for _, p := range stateData.Players {
		players = append(players, &PlayerInfo{
			UserID:   p.UserID,
			Nickname: p.Nickname,
			Avatar:   p.Avatar,
			SeatNo:   p.SeatNo,
			IsOnline: p.IsOnline(),
		})
		if p.SeatNo > 0 {
			playerSeatMap[p.SeatNo] = p
		}
	}

	spectators := make([]*SpectatorInfo, 0, len(stateData.Spectators))
	spectatorMap := make(map[string]*domain.Spectator, len(stateData.Spectators))
	for _, s := range stateData.Spectators {
		spectators = append(spectators, &SpectatorInfo{
			UserID:   s.UserID,
			Nickname: s.Nickname,
			Avatar:   s.Avatar,
			SeatNo:   s.SeatNo,
		})
		spectatorMap[s.UserID] = s
	}

	maxPlayers := stateData.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = 5
	}

	seats := make([]*SeatInfo, 0, maxPlayers)
	for i := 1; i <= maxPlayers; i++ {
		seat := &SeatInfo{
			SeatNo:   i,
			Occupied: false,
		}
		if p, ok := playerSeatMap[i]; ok {
			seat.Occupied = true
			seat.UserID = p.UserID
			seat.Nickname = p.Nickname
			seat.Avatar = p.Avatar
			seat.Ready = true
		} else if ownerID, ok := stateData.SeatOwners[i]; ok {
			if spectator, ok := spectatorMap[ownerID]; ok {
				seat.Occupied = true
				seat.UserID = spectator.UserID
				seat.Nickname = spectator.Nickname
				seat.Avatar = spectator.Avatar
				seat.Ready = false
			}
		}
		seats = append(seats, seat)
	}

	return &RoomState{
		RoomID:         stateData.RoomID,
		RoomNo:         stateData.RoomNo,
		RoomFee:        stateData.RoomFee,
		Status:         int(stateData.Status),
		CurrentRound:   stateData.CurrentRound,
		MaxRounds:      stateData.MaxRounds,
		PlayerCount:    stateData.PlayerCount,
		SpectatorCount: stateData.SpectatorCount,
		MaxPlayers:     stateData.MaxPlayers,
		MaxSpectators:  stateData.MaxSpectators,
		Players:        players,
		Spectators:     spectators,
		Seats:          seats,
	}
}
