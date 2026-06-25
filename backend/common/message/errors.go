package message

import "fmt"

// ==================== Códigos de error generales (0-999) ====================
const (
	CodeSuccess       = 0
	CodeInvalidParams = 400
	CodeUnauthorized  = 401
	CodeForbidden     = 403
	CodeNotFound      = 404
	CodeInternalError = 500
)

// ==================== Códigos de error de sala (1000-1999) ====================
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

// ==================== Códigos de error de conexión (2000-2999) ====================
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

// ==================== Códigos de error de juego (3000-3999) ====================
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
	CodeNotQueued         = 3041
	CodeRobotNotAllowed  = 3042
	CodeSubstituteFailed = 3043
)

// ==================== Códigos de error del sistema (5000-5999) ====================
const (
	CodeSystemError      = 5000
	CodeRedisError       = 5001
	CodeMySQLError       = 5002
	CodeKafkaError       = 5003
	CodePlatformAPIError = 5004
	CodeLockFailed       = 5005
)

// ==================== Códigos de error de historial de jugador (6000-6999) ====================
const (
	CodeHistoryQueryFailed  = 6001 // 历史查询失败
	CodeSessionNotFound     = 6002 // 会话不存在
	CodePlayerNotInSession  = 6003 // 玩家不在该会话中
	CodeHistoryParamInvalid = 6004 // 参数校验失败
)

// ==================== Razones de expulsión ====================
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

// ==================== Razones de penalización ====================
const (
	ReasonPenaltySendTimeout       = "send_timeout"
	ReasonPenaltyLeaveDuringGame   = "leave_during_game"
	ReasonPenaltyDisconnectTimeout = "disconnect_timeout"
)

// ==================== Razones de interrupción de juego ====================
const (
	ReasonNormalEnd               = "normal"
	ReasonFirstRoundDeductFailed  = "first_round_deduct_failed"
	ReasonLaterRoundDeductFailed  = "later_round_deduct_failed"
	ReasonPartialDeductFailed     = "partial_deduct_failed"
	ReasonReplacementTimeout      = "replacement_timeout"
	ReasonSystemError             = "system_error"
	ReasonPenaltyDeductFailed     = "penalty_deduct_failed"
)

// ==================== Mapeo de mensajes unificado ====================
var codeMessages = map[int]string{
	CodeSuccess:              "Éxito",
	CodeInvalidParams:        "Error de parámetro",
	CodeUnauthorized:         "No autorizado",
	CodeForbidden:            "Acceso prohibido",
	CodeNotFound:             "Recurso no encontrado",
	CodeSystemError:          "Error interno del servidor",
	CodeInvalidRoomType:      "Tipo de sala inválido",
	CodeUserAlreadyInRoom:    "El usuario ya está en la sala",
	CodeInsufficientBalance:  "Saldo insuficiente",
	CodeRoomFull:             "La sala está llena",
	CodeRoomNotFound:         "La sala no existe",
	CodeRoomNotWaiting:       "La sala no está en estado de espera",
	CodeSameIPLimit:          "Límite de jugadores con la misma IP excedido",
	CodeSameDeviceLimit:      "Límite de jugadores con el mismo dispositivo excedido",
	CodeUserNotFound:         "El usuario no existe",
	CodeUserDisabled:         "Cuenta de usuario deshabilitada",
	CodeUserFrozen:           "Cuenta de usuario congelada",
	CodeSystemBusy:           "Sistema ocupado",
	CodeOperationTooFrequent: "Operación demasiado frecuente",
	CodeNoIdleRoom:           "No hay salas disponibles",
	CodeDailyRoomLimit:       "Límite diario de salas alcanzado",
	CodeOperationInProgress:  "Operación en progreso",
	CodeGameInProgress:       "Juego en progreso",
	CodeUserBlacklisted:      "Usuario en lista negra",
	CodeLeavePenaltyApplied:  "Penalización por salida aplicada, tarifa de sala deducida",
	CodeInvalidMessage:       "Formato de mensaje inválido",
	CodeMissingCommand:       "Comando faltante",
	CodeUnknownCommand:       "Comando desconocido",
	CodeConnectionLimit:      "Límite de conexiones excedido",
	CodeAuthFailed:           "Autenticación fallida",
	CodeUserAlreadyConnected: "El usuario ya está conectado",
	CodeNotInRoom:            "El usuario no está en la sala",
	CodeRateLimitExceeded:    "Frecuencia de solicitudes excedida",
	CodePacketNotFound:       "Sobre rojo no encontrado",
	CodePacketAlreadyGrabbed: "El sobre rojo ya fue reclamado",
	CodeNotYourTurn:          "No es tu turno para enviar sobre rojo",
	CodeGameNotStarted:       "El juego no ha comenzado",
	CodeGameAlreadyEnded:     "El juego ha terminado",
	CodeInvalidGameState:     "Estado de juego inválido",
	CodeGrabTimeout:          "Tiempo para reclamar sobre rojo agotado",
	CodeSendTimeout:          "Tiempo para enviar sobre rojo agotado",
	CodeInvalidSeatNo:        "Número de asiento inválido",
	CodeSeatOccupied:         "El asiento ya está ocupado",
	CodePlayerNotInRoom:      "El jugador no está en la sala",
	CodePlayerAlreadyReady:   "El jugador está listo",
	CodePlayerCannotLeave:   "El jugador no puede salir de la sala",
	CodeNotPlayer:          "Solo los jugadores pueden operar",
	CodeAlreadyPlayer:        "Ya es jugador",
	CodeAlreadySeated:        "Ya ha seleccionado asiento",
	CodeNotSeated:            "No ha seleccionado asiento",
	CodeNeedSeatFirst:        "Debe seleccionar asiento primero",
	CodePacketExists:         "Sobre rojo ya creado",
	CodeAlreadyGrabbed:       "Ya reclamó el sobre rojo",
	CodeNoPacket:             "No hay sobres rojos para reclamar",
	CodePlayerNotOffline:     "El jugador no está desconectado",
	CodeNotInGrabbingPhase:   "No está en fase de reclamar sobres rojos",
	CodeRoundNotFound:        "Ronda no encontrada",
	CodeInvalidRoundNumber:   "Número de ronda inválido",
	CodeNoPlayers:            "No hay jugadores",
	CodePacketsAlreadyExist:  "Los sobres rojos ya existen",
	CodePenaltyApplied:       "Penalización aplicada",
	CodePlayerKicked:         "El jugador ha sido expulsado",
	CodeReplacementFailed:    "Reemplazo fallido",
	CodeNotAllPlayersReady:   "No todos los jugadores están listos",
	CodeGameResumed:          "Juego reanudado, el sistema envía sobre rojo",
	CodePlayerAlreadySent:    "El jugador ya envió un sobre rojo, no se puede expulsar",
	CodeAlreadyQueued:        "Ya está en la cola de espera",
	CodeNotQueued:            "No está en la cola de espera",
	CodeRobotNotAllowed:     "Los robots no pueden entrar en la cola",
	CodeSubstituteFailed:    "Fallo en la sustitución automática",
	CodeRedisError:           "Operación Redis fallida",
	CodeMySQLError:           "Operación de base de datos fallida",
	CodeKafkaError:           "Error de cola de mensajes",
	CodePlatformAPIError:     "Error de API de plataforma",
	CodeLockFailed:           "Fallo al adquirir bloqueo",
}

// ==================== Mapeo de mensajes de razones de expulsión ====================
var kickMessages = map[string]string{
	ReasonSeatTimeout:       "Tiempo de selección de asiento agotado, eliminado de la sala",
	ReasonReadyTimeout:      "Tiempo de preparación agotado, eliminado de la sala",
	ReasonDisconnectTimeout: "Tiempo de desconexión agotado, eliminado de la sala",
	ReasonSystemKick:        "Expulsado por el sistema",
	ReasonUserRequest:       "Salió de la sala voluntariamente",
	ReasonPlayerLeave:       "El jugador salió de la sala",
	ReasonLoginElsewhere:    "Su cuenta ha iniciado sesión en otro dispositivo",
	ReasonPenaltyKick:       "Expulsado por penalización, eliminado de la sala",
}

// ==================== Mapeo de mensajes de razones de interrupción de juego ====================
var interruptMessages = map[string]string{
	ReasonNormalEnd:              "Juego terminado normalmente",
	ReasonFirstRoundDeductFailed: "Saldo insuficiente, cargo fallido, juego terminado",
	ReasonLaterRoundDeductFailed: "Saldo insuficiente, cargo fallido, juego terminado",
	ReasonPartialDeductFailed:    "Cargo fallido para algunos jugadores, reembolso solicitado automáticamente, juego terminado",
	ReasonReplacementTimeout:     "Tiempo de reemplazo agotado, juego terminado",
	ReasonSystemError:            "Error del sistema, juego terminado",
	ReasonPenaltyDeductFailed:    "Cargo de penalización fallido, juego terminado",
}

// ==================== Mapeo de mensajes de razones de penalización ====================
var penaltyMessages = map[string]string{
	ReasonPenaltySendTimeout:       "Tiempo para enviar sobre rojo agotado, tarifa de sala deducida",
	ReasonPenaltyLeaveDuringGame:   "Salió durante el juego, tarifa de sala deducida",
	ReasonPenaltyDisconnectTimeout: "Tiempo de desconexión agotado, tarifa de sala deducida",
}

// ==================== Tipo de error ====================
type Error struct {
	Code int
	Msg  string
}

func NewError(code int) *Error {
	msg, ok := codeMessages[code]
	if !ok {
		msg = "Error desconocido"
	}
	return &Error{Code: code, Msg: msg}
}

func NewErrorWithMsg(code int, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Msg)
}

// ==================== Funciones utilitarias ====================
func GetErrorMsg(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "Error desconocido"
}

func GetKickMessage(reason string) string {
	if msg, ok := kickMessages[reason]; ok {
		return msg
	}
	return "Eliminado de la sala"
}

func GetInterruptMessage(reason string) string {
	if msg, ok := interruptMessages[reason]; ok {
		return msg
	}
	return "Juego interrumpido"
}

func GetPenaltyMessage(reason string) string {
	if msg, ok := penaltyMessages[reason]; ok {
		return msg
	}
	return "Penalización aplicada"
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
	return fmt.Sprintf("Saldo insuficiente, se necesita %d, saldo actual %d", requiredFee, balance)
}
