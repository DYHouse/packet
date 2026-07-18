package message

import (
	"fmt"

	"github.com/cashparty/backend/common/i18n"
)

// 本文件经 P1-4 领域特定结构迁移清理：
// - 西语消息映射与 Get* 函数已迁移至 common/i18n/messages_es.go
// - 游戏推送 payload 已迁移至 game/domain/push_payload.go
// - 网关请求/响应结构已迁移至 gateway/protocol/
// 本文件仅保留 Error 类型、错误码常量、原因常量与 Error 构造函数。

// ==================== 通用错误码（0-999） ====================
const (
	CodeSuccess       = 0
	CodeInvalidParams = 400
	CodeUnauthorized  = 401
	CodeForbidden     = 403
	CodeNotFound      = 404
	CodeInternalError = 500
)

// ==================== 房间错误码（1000-1999） ====================
const (
	CodeInvalidRoomType      = 1001
	CodeUserAlreadyInRoom    = 1002
	CodeInsufficientBalance  = 1003
	CodeRoomFull             = 1004
	CodeRoomNotFound         = 1005
	CodeRoomNotWaiting       = 1006
	CodeSameIPLimit          = 1007
	CodeSameDeviceLimit      = 1008
	CodeUserNotFound         = 1009
	CodeUserDisabled         = 1011
	CodeUserFrozen           = 1012
	CodeSystemBusy           = 1013
	CodeOperationTooFrequent = 1014
	CodeNoIdleRoom           = 1015
	CodeDailyRoomLimit       = 1016
	CodeOperationInProgress  = 1017
	CodeGameInProgress       = 1018
	CodeUserBlacklisted      = 1019
	CodeLeavePenaltyApplied  = 1021
	CodePlayerAlreadyReady   = 1022
	CodePlayerCannotLeave    = 1023
	CodeNotPlayer            = 1024
)

// ==================== 连接错误码（2000-2999） ====================
const (
	CodeInvalidMessage       = 2001
	CodeMissingCommand       = 2002
	CodeUnknownCommand       = 2003
	CodeConnectionLimit      = 2004
	CodeAuthFailed           = 2005
	CodeUserAlreadyConnected = 2006
	CodeNotInRoom            = 2007
	CodeRateLimitExceeded    = 2008
)

// ==================== 游戏错误码（3000-3999） ====================
const (
	CodePacketNotFound       = 3001
	CodePacketAlreadyGrabbed = 3002
	CodeNotYourTurn          = 3003
	CodeGameNotStarted       = 3004
	CodeGameAlreadyEnded     = 3005
	CodeInvalidGameState     = 3006
	CodeGrabTimeout          = 3007
	CodeSendTimeout          = 3008
	CodeInvalidSeatNo        = 3009
	CodeSeatOccupied         = 3010
	CodePlayerNotInRoom      = 3011
	CodeAlreadyPlayer        = 3013
	CodeAlreadySeated        = 3014
	CodeNotSeated            = 3015
	CodeNeedSeatFirst        = 3016
	CodePacketExists         = 3017
	CodeAlreadyGrabbed       = 3018
	CodeNoPacket             = 3019
	CodePlayerNotOffline     = 3020
	CodeNotInGrabbingPhase   = 3021
	CodeRoundNotFound        = 3022
	CodeInvalidRoundNumber   = 3023
	CodeNoPlayers            = 3024
	CodePacketsAlreadyExist  = 3025
	CodePenaltyApplied       = 3026
	CodePlayerKicked         = 3027
	CodeReplacementFailed    = 3028
	CodeNotAllPlayersReady   = 3029
	CodeReconnectExpired     = 3030
	CodeGameResumed          = 3031
	CodePlayerAlreadySent    = 3032

	CodeAlreadyQueued    = 3040
	CodeNotQueued        = 3041
	CodeRobotNotAllowed  = 3042
	CodeSubstituteFailed = 3043
)

// ==================== 系统错误码（5000-5999） ====================
const (
	CodeSystemError      = 5000
	CodeRedisError       = 5001
	CodeMySQLError       = 5002
	CodeKafkaError       = 5003
	CodePlatformAPIError = 5004
	CodeLockFailed       = 5005
)

// ==================== 玩家历史错误码（6000-6999） ====================
const (
	CodeHistoryQueryFailed  = 6001 // 历史查询失败
	CodeSessionNotFound     = 6002 // 会话不存在
	CodePlayerNotInSession  = 6003 // 玩家不在该会话中
	CodeHistoryParamInvalid = 6004 // 参数校验失败
)

// ==================== 踢出原因 ====================
const (
	ReasonSeatTimeout       = "seat_timeout"
	ReasonReadyTimeout      = "ready_timeout"
	ReasonDisconnectTimeout = "disconnect_timeout"
	ReasonSystemKick        = "system_kick"
	ReasonUserRequest       = "user_request"
	ReasonPlayerLeave       = "player_leave"
	ReasonLoginElsewhere    = "login_elsewhere"
	ReasonPenaltyKick       = "penalty_kick"
)

// ==================== 惩罚原因 ====================
const (
	ReasonPenaltySendTimeout       = "send_timeout"
	ReasonPenaltyLeaveDuringGame   = "leave_during_game"
	ReasonPenaltyDisconnectTimeout = "disconnect_timeout"
)

// ==================== 游戏中断原因 ====================
const (
	ReasonNormalEnd                 = "normal"
	ReasonFirstRoundDeductFailed    = "first_round_deduct_failed"
	ReasonLaterRoundDeductFailed    = "later_round_deduct_failed"
	ReasonPartialDeductFailed       = "partial_deduct_failed"
	ReasonReplacementTimeout        = "replacement_timeout"
	ReasonSystemError               = "system_error"
	ReasonPenaltyDeductFailed       = "penalty_deduct_failed"
	ReasonSubstituteFeeDeductFailed = "substitute_fee_deduct_failed"
)

// ==================== Error 类型 ====================
type Error struct {
	Code int
	Msg  string
}

// NewError 根据错误码构造 Error，消息从 common/i18n 查询西班牙语默认消息。
func NewError(code int) *Error {
	return &Error{Code: code, Msg: i18n.GetErrorMsg(code)}
}

// NewErrorWithMsg 根据错误码与自定义消息构造 Error。
func NewErrorWithMsg(code int, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Msg)
}

// IsGameError 判断 err 是否为 *Error 类型，返回断言结果。
func IsGameError(err error) (*Error, bool) {
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return nil, false
}

// IsErrorCode 判断 err 是否为指定错误码的 *Error。
func IsErrorCode(err error, code int) bool {
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}
