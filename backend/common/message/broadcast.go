package message

import (
	"encoding/json"

	"github.com/google/uuid"
)

type BroadcastMessage struct {
	EventHeader
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id,omitempty"`
	UserIDs    []string        `json:"user_ids,omitempty"`
	ExcludeID  string          `json:"exclude_id,omitempty"`
	Event      string          `json:"event"`
	Data       json.RawMessage `json:"data"`
}

// NewBroadcastMessage 创建 BroadcastMessage 并自动填充 EventID/Timestamp/Version。
// traceID 为空时自动生成一个。
func NewBroadcastMessage(event string, data interface{}, targetType, targetID string, excludeID string) (*BroadcastMessage, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &BroadcastMessage{
		EventHeader: NewEventHeader(uuid.New().String()), // 调用方未传 traceID 时自动生成
		TargetType:  targetType,
		TargetID:    targetID,
		ExcludeID:   excludeID,
		Event:       event,
		Data:        dataBytes,
	}, nil
}

// NewRoomBroadcastMessage 创建面向房间的广播消息。
func NewRoomBroadcastMessage(roomID string, event string, data interface{}) (*BroadcastMessage, error) {
	return NewBroadcastMessage(event, data, TargetTypeRoom, roomID, "")
}

// NewUserBroadcastMessage 创建面向指定用户的广播消息。
func NewUserBroadcastMessage(userIDs []string, event string, data interface{}) (*BroadcastMessage, error) {
	msg, err := NewBroadcastMessage(event, data, TargetTypeUser, "", "")
	if err != nil {
		return nil, err
	}
	msg.UserIDs = userIDs
	return msg, nil
}

// WithExcludeID 设置排除的用户 ID（链式调用）。
func (m *BroadcastMessage) WithExcludeID(excludeID string) *BroadcastMessage {
	m.ExcludeID = excludeID
	return m
}

// Marshal 将消息序列化为 JSON 字节。
func (m *BroadcastMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// ParseBroadcastMessage 从 JSON 字节解析 BroadcastMessage。
// 向前兼容：旧消息无 EventID/TraceID/Timestamp/Version 字段时用零值。
func ParseBroadcastMessage(data []byte) (*BroadcastMessage, error) {
	var msg BroadcastMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
