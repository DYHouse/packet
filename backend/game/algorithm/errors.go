package algorithm

import "fmt"

const (
	ErrCodeInvalidTotalAmount  = 6001
	ErrCodeInvalidPacketCount  = 6002
	ErrCodeAmountTooSmall      = 6003
	ErrCodePacketCountMismatch = 6004
	ErrCodeSumMismatch         = 6005
	ErrCodeConfigError         = 6007
	ErrCodeInternalError       = 6008
)

type Error struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("algorithm error [%d]: %s", e.Code, e.Msg)
}

func NewError(code int, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}
