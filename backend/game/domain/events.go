package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RoomEventType 房间事件类型，统一使用字符串字面量（动词原形）。
type RoomEventType string

const (
	RoomEventSpectatorJoin   RoomEventType = "spectator_join"
	RoomEventSpectatorLeave  RoomEventType = "spectator_leave"
	RoomEventSeatSelect      RoomEventType = "seat_select"
	RoomEventSeatCancel      RoomEventType = "seat_cancel"
	RoomEventPlayerReady     RoomEventType = "player_ready"
	RoomEventSpectatorKick   RoomEventType = "spectator_kick"
	RoomEventPlayerReconnect RoomEventType = "player_reconnect"
	RoomEventQueueJoin       RoomEventType = "queue_join"
	RoomEventQueueLeave      RoomEventType = "queue_leave"
	RoomEventSubstitute      RoomEventType = "substitute"
)

// RoomEventVersion 是当前 RoomEvent 的 schema 版本。
const RoomEventVersion = 1

type RoomEvent struct {
	EventID   string          `json:"event_id"`
	EventType RoomEventType   `json:"event_type"`
	RoomID    string          `json:"room_id"`
	UserID    string          `json:"user_id,omitempty"`
	TraceID   string          `json:"trace_id,omitempty"`
	Version   int             `json:"version"`
	Timestamp int64           `json:"timestamp"` // Unix 毫秒
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// SetPayload 序列化 v 并设置到 Payload。
func (e *RoomEvent) SetPayload(v interface{}) error {
	bytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.Payload = bytes
	return nil
}

// GetPayload 将 Payload 反序列化到 v。
func (e *RoomEvent) GetPayload(v interface{}) error {
	return json.Unmarshal(e.Payload, v)
}

type SpectatorJoinPayload struct {
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
}

type SpectatorLeavePayload struct {
	Reason string `json:"reason,omitempty"`
}

type SeatSelectPayload struct {
	SeatNo   int    `json:"seat_no"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
}

type SeatCancelPayload struct {
	SeatNo   int    `json:"seat_no"`
	Nickname string `json:"nickname,omitempty"`
}

type PlayerReadyPayload struct {
	SeatNo   int    `json:"seat_no"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
}

type SpectatorKickPayload struct {
	SeatNo int    `json:"seat_no"`
	Reason string `json:"reason"`
}

type PlayerReconnectPayload struct {
	SeatNo int `json:"seat_no"`
}

type QueueJoinPayload struct {
	QueuePosition int    `json:"queue_position"`
	Nickname      string `json:"nickname,omitempty"`
	Avatar        string `json:"avatar,omitempty"`
}

type QueueLeavePayload struct {
	Reason string `json:"reason,omitempty"`
}

type SubstitutePayload struct {
	SeatNo   int    `json:"seat_no"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
}

type GameEventType string

const (
	GameEventSessionStart  GameEventType = "session_start"
	GameEventPacketCreated GameEventType = "packet_created"
	GameEventRoundSettle   GameEventType = "round_settle"
	GameEventSessionEnd    GameEventType = "session_end"
)

// GameEventVersion 是当前 GameEvent 的 schema 版本。
const GameEventVersion = 1

type GameEvent struct {
	EventID   string          `json:"event_id"`
	EventType GameEventType   `json:"event_type"`
	RoomID    string          `json:"room_id"`
	SessionID string          `json:"session_id"`
	RoundID   string          `json:"round_id,omitempty"`
	TraceID   string          `json:"trace_id"`
	Version   int             `json:"version"`
	Timestamp int64           `json:"timestamp"` // Unix 毫秒
	Data      json.RawMessage `json:"data"`
}

// SetPayload 序列化 v 并设置到 Data。
func (e *GameEvent) SetPayload(v interface{}) error {
	bytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.Data = bytes
	return nil
}

// GetPayload 将 Data 反序列化到 v。
func (e *GameEvent) GetPayload(v interface{}) error {
	return json.Unmarshal(e.Data, v)
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

func generateEventID() string {
	return uuid.New().String()
}

func (e *RoomEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// ParseRoomEvent 从 JSON 字节解析 RoomEvent。
// 向前兼容：旧消息无 TraceID/Version/Timestamp 字段时用零值。
func ParseRoomEvent(data []byte) (*RoomEvent, error) {
	var event RoomEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if event.EventID == "" {
		event.EventID = generateEventID()
	}
	return &event, nil
}

func NewSpectatorJoinEvent(roomID, userID string, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorJoin,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SpectatorJoinPayload{
		Nickname: nickname,
		Avatar:   avatar,
	})
	return event
}

func NewSpectatorLeaveEvent(roomID, userID string, reason string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorLeave,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SpectatorLeavePayload{
		Reason: reason,
	})
	return event
}

func NewSeatSelectEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSeatSelect,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SeatSelectPayload{
		SeatNo:   seatNo,
		Nickname: nickname,
		Avatar:   avatar,
	})
	return event
}

func NewSeatCancelEvent(roomID, userID string, seatNo int, nickname string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSeatCancel,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SeatCancelPayload{
		SeatNo:   seatNo,
		Nickname: nickname,
	})
	return event
}

func NewPlayerReadyEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventPlayerReady,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(PlayerReadyPayload{
		SeatNo:   seatNo,
		Nickname: nickname,
		Avatar:   avatar,
	})
	return event
}

func NewSpectatorKickEvent(roomID, userID string, seatNo int, reason string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorKick,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SpectatorKickPayload{
		SeatNo: seatNo,
		Reason: reason,
	})
	return event
}

func NewPlayerReconnectEvent(roomID, userID string, seatNo int) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventPlayerReconnect,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(PlayerReconnectPayload{
		SeatNo: seatNo,
	})
	return event
}

func NewQueueJoinEvent(roomID, userID string, queuePosition int, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventQueueJoin,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(QueueJoinPayload{
		QueuePosition: queuePosition,
		Nickname:      nickname,
		Avatar:        avatar,
	})
	return event
}

func NewQueueLeaveEvent(roomID, userID string, reason string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventQueueLeave,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(QueueLeavePayload{
		Reason: reason,
	})
	return event
}

func NewSubstituteEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSubstitute,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SubstitutePayload{
		SeatNo:   seatNo,
		Nickname: nickname,
		Avatar:   avatar,
	})
	return event
}
