package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type RoomEventType int

const (
	RoomEventSpectatorJoin RoomEventType = iota + 1
	RoomEventSpectatorLeave
	RoomEventPlayerJoin
	RoomEventPlayerLeave
	RoomEventStatusChange
	RoomEventSeatSelect
	RoomEventSeatCancel
	RoomEventPlayerReady
	RoomEventSpectatorKick
	RoomEventPlayerDisconnect
	RoomEventPlayerReconnect
	RoomEventQueueJoin
	RoomEventQueueLeave
	RoomEventSubstitute
)

type RoomEvent struct {
	EventID    string        `json:"event_id"`
	EventType  RoomEventType `json:"event_type"`
	RoomID     string        `json:"room_id"`
	UserID     string        `json:"user_id,omitempty"`
	Payload    interface{}   `json:"payload,omitempty"`
	OccurredAt time.Time     `json:"occurred_at"`
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

type GameEvent struct {
	EventType GameEventType `json:"event_type"`
	RoomID    string        `json:"room_id"`
	SessionID string        `json:"session_id"`
	RoundID   string        `json:"round_id,omitempty"`
	Timestamp int64         `json:"timestamp"`
	Data      interface{}   `json:"data"`
	TraceID   string        `json:"trace_id"`
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
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorJoin,
		RoomID:    roomID,
		UserID:    userID,
		Payload: SpectatorJoinPayload{
			Nickname: nickname,
			Avatar:   avatar,
		},
		OccurredAt: time.Now(),
	}
}

func NewSpectatorLeaveEvent(roomID, userID string, reason string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorLeave,
		RoomID:    roomID,
		UserID:    userID,
		Payload: SpectatorLeavePayload{
			Reason: reason,
		},
		OccurredAt: time.Now(),
	}
}

func NewSeatSelectEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSeatSelect,
		RoomID:    roomID,
		UserID:    userID,
		Payload: SeatSelectPayload{
			SeatNo:   seatNo,
			Nickname: nickname,
			Avatar:   avatar,
		},
		OccurredAt: time.Now(),
	}
}

func NewSeatCancelEvent(roomID, userID string, seatNo int, nickname string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSeatCancel,
		RoomID:    roomID,
		UserID:    userID,
		Payload: SeatCancelPayload{
			SeatNo:   seatNo,
			Nickname: nickname,
		},
		OccurredAt: time.Now(),
	}
}

func NewPlayerReadyEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventPlayerReady,
		RoomID:    roomID,
		UserID:    userID,
		Payload: PlayerReadyPayload{
			SeatNo:   seatNo,
			Nickname: nickname,
			Avatar:   avatar,
		},
		OccurredAt: time.Now(),
	}
}

func NewSpectatorKickEvent(roomID, userID string, seatNo int, reason string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorKick,
		RoomID:    roomID,
		UserID:    userID,
		Payload: SpectatorKickPayload{
			SeatNo: seatNo,
			Reason: reason,
		},
		OccurredAt: time.Now(),
	}
}

func NewPlayerReconnectEvent(roomID, userID string, seatNo int) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventPlayerReconnect,
		RoomID:    roomID,
		UserID:    userID,
		Payload: PlayerReconnectPayload{
			SeatNo: seatNo,
		},
		OccurredAt: time.Now(),
	}
}

func NewQueueJoinEvent(roomID, userID string, queuePosition int, nickname, avatar string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventQueueJoin,
		RoomID:    roomID,
		UserID:    userID,
		Payload: QueueJoinPayload{
			QueuePosition: queuePosition,
			Nickname:      nickname,
			Avatar:        avatar,
		},
		OccurredAt: time.Now(),
	}
}

func NewQueueLeaveEvent(roomID, userID string, reason string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventQueueLeave,
		RoomID:    roomID,
		UserID:    userID,
		Payload: QueueLeavePayload{
			Reason: reason,
		},
		OccurredAt: time.Now(),
	}
}

func NewSubstituteEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	return &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSubstitute,
		RoomID:    roomID,
		UserID:    userID,
		Payload: SubstitutePayload{
			SeatNo:   seatNo,
			Nickname: nickname,
			Avatar:   avatar,
		},
		OccurredAt: time.Now(),
	}
}
