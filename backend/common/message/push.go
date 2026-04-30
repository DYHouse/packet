package message

import (
    "encoding/json"
    "time"
)

type PushMessage struct {
    Type      string      `json:"type"`
    Data      interface{} `json:"data"`
    Timestamp int64       `json:"timestamp"`
}

func NewPushMessage(msgType string, data interface{}) *PushMessage {
    return &PushMessage{
        Type:      msgType,
        Data:      data,
        Timestamp: time.Now().UnixMilli(),
    }
}

func (p *PushMessage) ToJSON() ([]byte, error) {
    return json.Marshal(p)
}

func ParsePushMessage(data []byte) (*PushMessage, error) {
    var msg PushMessage
    if err := json.Unmarshal(data, &msg); err != nil {
        return nil, err
    }
    return &msg, nil
}
