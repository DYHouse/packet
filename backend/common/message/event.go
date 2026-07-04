package message

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// EventEnvelopeVersion 是当前 EventEnvelope 的 schema 版本。
const EventEnvelopeVersion = 1

// EventEnvelope 是统一的业务事件信封，覆盖 RoomEvent 和 GameEvent。
type EventEnvelope struct {
	EventID   string          `json:"event_id"`
	EventType string          `json:"event_type"`
	TraceID   string          `json:"trace_id"`
	Timestamp int64           `json:"timestamp"` // Unix 毫秒
	Version   int             `json:"version"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload"`
}

// NewEventEnvelope 创建 EventEnvelope 并自动填充 EventID/Timestamp/Version。
// eventType 为事件类型字符串，source 为发送方标识，traceID 为贯穿日志的追踪 ID（空则自动生成）。
func NewEventEnvelope(eventType, source, traceID string, payload interface{}) (*EventEnvelope, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if traceID == "" {
		traceID = uuid.New().String()
	}
	return &EventEnvelope{
		EventID:   uuid.New().String(),
		EventType: eventType,
		TraceID:   traceID,
		Timestamp: time.Now().UnixMilli(),
		Version:   EventEnvelopeVersion,
		Source:    source,
		Payload:   payloadBytes,
	}, nil
}

// GetPayload 将 Payload 反序列化到 v。
func (e *EventEnvelope) GetPayload(v interface{}) error {
	return json.Unmarshal(e.Payload, v)
}

// SetPayload 序列化 v 并设置到 Payload。
func (e *EventEnvelope) SetPayload(v interface{}) error {
	bytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.Payload = bytes
	return nil
}
