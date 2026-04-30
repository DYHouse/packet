package message

import "encoding/json"

type BroadcastMessage struct {
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id,omitempty"`
	UserIDs    []string        `json:"user_ids,omitempty"`
	ExcludeID  string          `json:"exclude_id,omitempty"`
	Event      string          `json:"event"`
	Data       json.RawMessage `json:"data"`
}

func NewRoomBroadcastMessage(roomID string, event string, data interface{}) (*BroadcastMessage, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &BroadcastMessage{
		TargetType: TargetTypeRoom,
		TargetID:   roomID,
		Event:      event,
		Data:       dataBytes,
	}, nil
}

func NewUserBroadcastMessage(userIDs []string, event string, data interface{}) (*BroadcastMessage, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &BroadcastMessage{
		TargetType: TargetTypeUser,
		UserIDs:    userIDs,
		Event:      event,
		Data:       dataBytes,
	}, nil
}

func (m *BroadcastMessage) WithExcludeID(excludeID string) *BroadcastMessage {
	m.ExcludeID = excludeID
	return m
}

func (m *BroadcastMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func ParseBroadcastMessage(data []byte) (*BroadcastMessage, error) {
	var msg BroadcastMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
