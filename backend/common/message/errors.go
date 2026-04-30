package message

import "fmt"

// ==================== 通用错误码 (0-999) ====================
const (
	CodeSuccess       = 0
	CodeInvalidParams = 400
	CodeUnauthorized  = 401
	CodeForbidden     = 403
	CodeNotFound      = 404
	CodeInternalError = 500
)

// ==================== 房间相关错误码 (1000-1999) ====================
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
	CodePlayerCannotLeave   = 1023
	CodeNotPlayer          = 1024
)

// ==================== 连接相关错误码 (2000-2999) ====================
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

// ==================== 游戏相关错误码 (3000-3999) ====================
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
)

// ==================== 系统错误码 (5000-5999) ====================
const (
	CodeSystemError      = 5000
	CodeRedisError       = 5001
	CodeMySQLError       = 5002
	CodeKafkaError       = 5003
	CodePlatformAPIError = 5004
	CodeLockFailed       = 5005
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
)

// ==================== 游戏中断原因 ====================
const (
	ReasonNormalEnd               = "normal"
	ReasonFirstRoundDeductFailed  = "first_round_deduct_failed"
	ReasonLaterRoundDeductFailed  = "later_round_deduct_failed"
	ReasonPartialDeductFailed     = "partial_deduct_failed"
	ReasonReplacementTimeout      = "replacement_timeout"
	ReasonSystemError             = "system_error"
)

// ==================== 统一消息映射 ====================
var codeMessages = map[int]string{
	CodeSuccess:              "成功",
	CodeInvalidParams:        "参数错误",
	CodeUnauthorized:         "未授权",
	CodeForbidden:            "禁止访问",
	CodeNotFound:             "资源不存在",
	CodeSystemError:          "内部服务器错误",
	CodeInvalidRoomType:      "无效的房间类型",
	CodeUserAlreadyInRoom:    "用户已在房间中",
	CodeInsufficientBalance:  "余额不足",
	CodeRoomFull:             "房间已满",
	CodeRoomNotFound:         "房间不存在",
	CodeRoomNotWaiting:       "房间不在等待状态",
	CodeSameIPLimit:          "同IP玩家数量超限",
	CodeSameDeviceLimit:      "同设备玩家数量超限",
	CodeUserNotFound:         "用户不存在",
	CodeUserDisabled:         "用户账号已禁用",
	CodeUserFrozen:           "用户账号已冻结",
	CodeSystemBusy:           "系统繁忙",
	CodeOperationTooFrequent: "操作过于频繁",
	CodeNoIdleRoom:           "无空闲房间",
	CodeDailyRoomLimit:       "达到每日房间限制",
	CodeOperationInProgress:  "操作进行中",
	CodeGameInProgress:       "游戏进行中",
	CodeUserBlacklisted:      "用户被列入黑名单",
	CodeLeavePenaltyApplied:  "离开惩罚已应用，房费已扣除",
	CodeInvalidMessage:       "无效的消息格式",
	CodeMissingCommand:       "缺少命令",
	CodeUnknownCommand:       "未知的命令",
	CodeConnectionLimit:      "连接数超限",
	CodeAuthFailed:           "认证失败",
	CodeUserAlreadyConnected: "用户已连接",
	CodeNotInRoom:            "用户不在房间中",
	CodeRateLimitExceeded:    "请求频率超限",
	CodePacketNotFound:       "红包不存在",
	CodePacketAlreadyGrabbed: "红包已被抢过",
	CodeNotYourTurn:          "未轮到你发红包",
	CodeGameNotStarted:       "游戏未开始",
	CodeGameAlreadyEnded:     "游戏已结束",
	CodeInvalidGameState:     "无效的游戏状态",
	CodeGrabTimeout:          "抢红包超时",
	CodeSendTimeout:          "发红包超时",
	CodeInvalidSeatNo:        "无效的座位号",
	CodeSeatOccupied:         "座位已被占用",
	CodePlayerNotInRoom:      "玩家不在房间中",
	CodePlayerAlreadyReady:   "玩家已准备",
	CodePlayerCannotLeave:   "玩家无法离开房间",
	CodeNotPlayer:          "只有玩家才能操作",
	CodeAlreadyPlayer:        "已经是玩家",
	CodeAlreadySeated:        "已经选座",
	CodeNotSeated:            "未选座",
	CodeNeedSeatFirst:        "需要先选座",
	CodePacketExists:         "红包已创建",
	CodeAlreadyGrabbed:       "已抢过红包",
	CodeNoPacket:             "没有可抢的红包",
	CodePlayerNotOffline:     "玩家未离线",
	CodeNotInGrabbingPhase:   "不在抢红包阶段",
	CodeRoundNotFound:        "回合不存在",
	CodeInvalidRoundNumber:   "无效的回合编号",
	CodeNoPlayers:            "没有玩家",
	CodePacketsAlreadyExist:  "红包已存在",
	CodePenaltyApplied:       "惩罚已应用",
	CodePlayerKicked:         "玩家已被踢出",
	CodeReplacementFailed:    "补位失败",
	CodeNotAllPlayersReady:   "不是所有玩家都已准备",
	CodeGameResumed:          "游戏已恢复，系统发送红包",
	CodePlayerAlreadySent:    "玩家已发送红包，不能踢出",
	CodeRedisError:           "Redis操作失败",
	CodeMySQLError:           "数据库操作失败",
	CodeKafkaError:           "消息队列错误",
	CodePlatformAPIError:     "平台API错误",
	CodeLockFailed:           "获取锁失败",
}

// ==================== 踢出原因消息映射 ====================
var kickMessages = map[string]string{
	ReasonSeatTimeout:       "选座超时，已被移出房间",
	ReasonReadyTimeout:      "准备超时，已被移出房间",
	ReasonDisconnectTimeout: "断线超时，已被移出房间",
	ReasonSystemKick:        "系统踢出",
	ReasonUserRequest:       "主动离开房间",
	ReasonPlayerLeave:       "玩家离开房间",
	ReasonLoginElsewhere:    "您的账号在其他设备登录",
}

// ==================== 游戏中断原因消息映射 ====================
var interruptMessages = map[string]string{
	ReasonNormalEnd:              "游戏正常结束",
	ReasonFirstRoundDeductFailed: "余额不足，扣款失败，游戏结束",
	ReasonLaterRoundDeductFailed: "余额不足，扣款失败，游戏结束",
	ReasonPartialDeductFailed:    "部分玩家扣款失败，已自动申请退款，游戏结束",
	ReasonReplacementTimeout:     "补位超时，游戏结束",
	ReasonSystemError:            "系统错误，游戏结束",
}

// ==================== 错误类型 ====================
type Error struct {
	Code int
	Msg  string
}

func NewError(code int) *Error {
	msg, ok := codeMessages[code]
	if !ok {
		msg = "未知错误"
	}
	return &Error{Code: code, Msg: msg}
}

func NewErrorWithMsg(code int, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Msg)
}

// ==================== 工具函数 ====================
func GetErrorMsg(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}

func GetKickMessage(reason string) string {
	if msg, ok := kickMessages[reason]; ok {
		return msg
	}
	return "已被移出房间"
}

func GetInterruptMessage(reason string) string {
	if msg, ok := interruptMessages[reason]; ok {
		return msg
	}
	return "游戏中断"
}

func IsGameError(err error) (*Error, bool) {
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return nil, false
}

func IsErrorCode(err error, code int) bool {
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}

func GetInsufficientBalanceMsg(requiredFee, balance int64) string {
	return fmt.Sprintf("余额不足，需要%d，当前余额%d", requiredFee, balance)
}
