package events

import (
	"encoding/json"

	"github.com/cashparty/backend/common/message"
)

type GameEventType string

const (
	GameEventSessionStart  GameEventType = "session_start"
	GameEventPacketCreated GameEventType = "packet_created"
	GameEventRoundSettle   GameEventType = "round_settle"
	GameEventSessionEnd    GameEventType = "session_end"
)

type GameEvent struct {
	message.EventHeader
	EventType GameEventType   `json:"event_type"`
	RoomID    string          `json:"room_id"`
	SessionID string          `json:"session_id"`
	RoundID   string          `json:"round_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

// SetPayload 序列化 v 并设置到 Payload。
func (e *GameEvent) SetPayload(v interface{}) error {
	bytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.Payload = bytes
	return nil
}

// GetPayload 将 Payload 反序列化到 v。
func (e *GameEvent) GetPayload(v interface{}) error {
	return json.Unmarshal(e.Payload, v)
}

type SessionStartData struct {
	RoomNo     string        `json:"room_no"`
	ConfigID   int64         `json:"config_id"`
	ConfigName string        `json:"config_name"`
	RoomFee    int64         `json:"room_fee"`
	MaxRounds  int           `json:"max_rounds"`
	Players    []*PlayerInfo `json:"players"`
}

type PlayerInfo struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	SeatNo   int    `json:"seat_no"`
}

type PacketCreatedData struct {
	RoomID      string        `json:"room_id"`
	SessionID   string        `json:"session_id"`
	RoundID     string        `json:"round_id"`
	RoundNo     int           `json:"round_no"`
	SenderID    string        `json:"sender_id"`
	SenderType  string        `json:"sender_type"`
	TotalAmount int64         `json:"total_amount"`
	Commission  int64         `json:"commission"`
	Packets     []*PacketData `json:"packets"`
}

type PacketData struct {
	PacketID string `json:"packet_id"`
	RoomID   string `json:"room_id"`
	RoundID  string `json:"round_id"`
	Amount   int64  `json:"amount"`
	Position int    `json:"position"`
}

type RoundSettleData struct {
	RoundNo          int            `json:"round_no"`
	SenderID         string         `json:"sender_id"`
	SenderType       string         `json:"sender_type"`
	TotalAmount      int64          `json:"total_amount"`
	Commission       int64          `json:"commission"`
	RoomFeePerPlayer int64          `json:"room_fee_per_player"`
	PacketCount      int            `json:"packet_count"`
	Results          []*RoundResult `json:"results"`
	MinPlayerID      string         `json:"min_player_id"`
	IsGameEnd        bool           `json:"is_game_end"`
	RewardType       int            `json:"reward_type"`
	RewardAmount     int64          `json:"reward_amount"`
	TriggerType      int            `json:"trigger_type"`
}

type RoundResult struct {
	UserID         string `json:"user_id"`
	PacketID       string `json:"packet_id"`
	Position       int    `json:"position"`
	Amount         int64  `json:"amount"`
	IsAutoAssigned bool   `json:"is_auto_assigned"`
}

type SessionEndData struct {
	ActualRounds int            `json:"actual_rounds"`
	EndReason    string         `json:"end_reason"`
	FinalResults []*FinalResult `json:"final_results"`
}

type FinalResult struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	TotalProfit int64  `json:"total_profit"`
	Rank        int    `json:"rank"`
}
