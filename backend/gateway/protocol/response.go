package protocol

import (
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/i18n"
	"github.com/cashparty/backend/common/message"
)

// 本文件从 common/message/response.go 迁移而来（P1-4 领域特定结构迁移）。
// 仅包含网关层 WS 响应协议结构。
// PingResponse 一并从 common/message/payload.go 迁移至此（属于网关响应 payload）。

type Response struct {
	Cmd       string      `json:"cmd"`
	RequestID string      `json:"request_id"`
	Code      int         `json:"code"`
	Msg       string      `json:"msg"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

type PingResponse struct {
	ServerTime int64 `json:"server_time"`
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
	return NewResponse(cmd, requestID, message.CodeSuccess, i18n.GetErrorMsg(message.CodeSuccess), data)
}

func NewErrorResponse(cmd, requestID string, code int) *Response {
	return NewResponse(cmd, requestID, code, i18n.GetErrorMsg(code), nil)
}

func NewErrorResponseWithMsg(cmd, requestID string, code int, msg string) *Response {
	return NewResponse(cmd, requestID, code, msg, nil)
}

func (r *Response) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

func (r *Response) IsSuccess() bool {
	return r.Code == message.CodeSuccess
}

func ParseResponse(data []byte) (*Response, error) {
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
