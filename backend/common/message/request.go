package message

import (
    "encoding/json"
    "time"

    "github.com/google/uuid"
)

type Request struct {
    Cmd       string          `json:"cmd"`
    RequestID string          `json:"request_id"`
    Data      json.RawMessage `json:"data,omitempty"`
    Timestamp int64           `json:"timestamp"`
}

func NewRequest(cmd string, data interface{}) (*Request, error) {
    req := &Request{
        Cmd:       cmd,
        RequestID: generateRequestID(),
        Timestamp: time.Now().UnixMilli(),
    }

    if data != nil {
        dataBytes, err := json.Marshal(data)
        if err != nil {
            return nil, err
        }
        req.Data = dataBytes
    }

    return req, nil
}

func (r *Request) ParseData(v interface{}) error {
    if len(r.Data) == 0 {
        return nil
    }
    return json.Unmarshal(r.Data, v)
}

func (r *Request) ToJSON() ([]byte, error) {
    return json.Marshal(r)
}

func (r *Request) Validate() bool {
    return r.Cmd != ""
}

func generateRequestID() string {
    return "req_" + uuid.New().String()[:16]
}
