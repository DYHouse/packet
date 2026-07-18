package events

import (
	"encoding/json"

	"github.com/cashparty/backend/common/message"
)

// RoomEventType 房间事件类型，统一使用字符串字面量（动词原形）。
type RoomEventType string

const (
	RoomEventSpectatorJoin   RoomEventType = "spectator_join"
	RoomEventSpectatorLeave  RoomEventType = "spectator_leave"
	RoomEventSeatCancel      RoomEventType = "seat_cancel"
	RoomEventPlayerReady     RoomEventType = "player_ready"
	RoomEventSpectatorKick   RoomEventType = "spectator_kick"
	RoomEventPlayerReconnect RoomEventType = "player_reconnect"
	RoomEventQueueJoin       RoomEventType = "queue_join"
	RoomEventQueueLeave      RoomEventType = "queue_leave"
	RoomEventSubstitute      RoomEventType = "substitute"
)

type RoomEvent struct {
	message.EventHeader
	EventType RoomEventType   `json:"event_type"`
	RoomID    string          `json:"room_id"`
	UserID    string          `json:"user_id,omitempty"`
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
	// Source 标识替补来源："queue"=排队队列自动替补，"spectator"=观众手动选座补位。
	// 空值向前兼容旧消息（视为 "substitute"）。
	Source string `json:"source,omitempty"`
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
	event.EventHeader.FillIfEmpty()
	return &event, nil
}

func NewSpectatorJoinEvent(roomID, userID string, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventSpectatorJoin,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(SpectatorJoinPayload{
		Nickname: nickname,
		Avatar:   avatar,
	})
	return event
}

func NewSpectatorLeaveEvent(roomID, userID string, reason string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventSpectatorLeave,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(SpectatorLeavePayload{
		Reason: reason,
	})
	return event
}

func NewSeatCancelEvent(roomID, userID string, seatNo int, nickname string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventSeatCancel,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(SeatCancelPayload{
		SeatNo:   seatNo,
		Nickname: nickname,
	})
	return event
}

func NewPlayerReadyEvent(roomID, userID string, seatNo int, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventPlayerReady,
		RoomID:      roomID,
		UserID:      userID,
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
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventSpectatorKick,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(SpectatorKickPayload{
		SeatNo: seatNo,
		Reason: reason,
	})
	return event
}

func NewPlayerReconnectEvent(roomID, userID string, seatNo int) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventPlayerReconnect,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(PlayerReconnectPayload{
		SeatNo: seatNo,
	})
	return event
}

func NewQueueJoinEvent(roomID, userID string, queuePosition int, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventQueueJoin,
		RoomID:      roomID,
		UserID:      userID,
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
		EventHeader: message.NewEventHeader(""),
		EventType:   RoomEventQueueLeave,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(QueueLeavePayload{
		Reason: reason,
	})
	return event
}

// NewSubstituteEvent 构造替补事件。
// source 标识替补来源："queue"=排队队列自动替补，"spectator"=观众手动选座补位。
func NewSubstituteEvent(roomID, userID string, seatNo int, nickname, avatar, traceID, source string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(traceID),
		EventType:   RoomEventSubstitute,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(SubstitutePayload{
		SeatNo:   seatNo,
		Nickname: nickname,
		Avatar:   avatar,
		Source:   source,
	})
	return event
}
