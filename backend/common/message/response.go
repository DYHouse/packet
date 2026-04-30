package message

import (
    "encoding/json"
    "time"
)

type Response struct {
    Cmd       string      `json:"cmd"`
    RequestID string      `json:"request_id"`
    Code      int         `json:"code"`
    Msg       string      `json:"msg"`
    Data      interface{} `json:"data,omitempty"`
    Timestamp int64       `json:"timestamp"`
}

func NewResponse(cmd, requestID string, code int, msg string, data interface{}) *Response {
    return &Response{
        Cmd:       cmd,
        RequestID: requestID,
        Code:      code,
        Msg:       msg,
        Data:      data,
        Timestamp: time.Now().UnixMilli(),
    }
}

func NewSuccessResponse(cmd, requestID string, data interface{}) *Response {
    return NewResponse(cmd, requestID, CodeSuccess, GetErrorMsg(CodeSuccess), data)
}

func NewErrorResponse(cmd, requestID string, code int) *Response {
    return NewResponse(cmd, requestID, code, GetErrorMsg(code), nil)
}

func NewErrorResponseWithMsg(cmd, requestID string, code int, msg string) *Response {
    return NewResponse(cmd, requestID, code, msg, nil)
}

func (r *Response) ToJSON() ([]byte, error) {
    return json.Marshal(r)
}

func (r *Response) IsSuccess() bool {
    return r.Code == CodeSuccess
}

func ParseResponse(data []byte) (*Response, error) {
    var resp Response
    if err := json.Unmarshal(data, &resp); err != nil {
        return nil, err
    }
    return &resp, nil
}
