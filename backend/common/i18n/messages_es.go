package i18n

import "fmt"

// 本文件从 common/message/errors.go 迁移而来（P1-4 领域特定结构迁移）。
// 仅包含西班牙语（es）国际化消息映射与查询函数。
//
// 为避免循环依赖（common/message 依赖本包构造 Error 的默认消息），
// 本包不 import common/message，map 的 key 使用字面量 int/string，
// 与 common/message 中 CodeXXX / ReasonXXX 常量值一一对应（见行尾注释）。

// ==================== 统一消息映射 ====================
var codeMessages = map[int]string{
	0:    "Éxito",                                                     // CodeSuccess
	400:  "Error de parámetro",                                        // CodeInvalidParams
	401:  "No autorizado",                                             // CodeUnauthorized
	403:  "Acceso prohibido",                                          // CodeForbidden
	404:  "Recurso no encontrado",                                     // CodeNotFound
	5000: "Error interno del servidor",                                // CodeSystemError
	1001: "Tipo de sala inválido",                                     // CodeInvalidRoomType
	1002: "El usuario ya está en la sala",                             // CodeUserAlreadyInRoom
	1003: "Saldo insuficiente",                                        // CodeInsufficientBalance
	1004: "La sala está llena",                                        // CodeRoomFull
	1005: "La sala no existe",                                         // CodeRoomNotFound
	1006: "La sala no está en estado de espera",                       // CodeRoomNotWaiting
	1007: "Límite de jugadores con la misma IP excedido",              // CodeSameIPLimit
	1008: "Límite de jugadores con el mismo dispositivo excedido",     // CodeSameDeviceLimit
	1009: "El usuario no existe",                                      // CodeUserNotFound
	1011: "Cuenta de usuario deshabilitada",                           // CodeUserDisabled
	1012: "Cuenta de usuario congelada",                               // CodeUserFrozen
	1013: "Sistema ocupado",                                           // CodeSystemBusy
	1014: "Operación demasiado frecuente",                             // CodeOperationTooFrequent
	1015: "No hay salas disponibles",                                  // CodeNoIdleRoom
	1016: "Límite diario de salas alcanzado",                          // CodeDailyRoomLimit
	1017: "Operación en progreso",                                     // CodeOperationInProgress
	1018: "Juego en progreso",                                         // CodeGameInProgress
	1019: "Usuario en lista negra",                                    // CodeUserBlacklisted
	1021: "Penalización por salida aplicada, tarifa de sala deducida", // CodeLeavePenaltyApplied
	2001: "Formato de mensaje inválido",                               // CodeInvalidMessage
	2002: "Comando faltante",                                          // CodeMissingCommand
	2003: "Comando desconocido",                                       // CodeUnknownCommand
	2004: "Límite de conexiones excedido",                             // CodeConnectionLimit
	2005: "Autenticación fallida",                                     // CodeAuthFailed
	2006: "El usuario ya está conectado",                              // CodeUserAlreadyConnected
	2007: "El usuario no está en la sala",                             // CodeNotInRoom
	2008: "Frecuencia de solicitudes excedida",                        // CodeRateLimitExceeded
	3001: "Sobre rojo no encontrado",                                  // CodePacketNotFound
	3002: "El sobre rojo ya fue reclamado",                            // CodePacketAlreadyGrabbed
	3003: "No es tu turno para enviar sobre rojo",                     // CodeNotYourTurn
	3004: "El juego no ha comenzado",                                  // CodeGameNotStarted
	3005: "El juego ha terminado",                                     // CodeGameAlreadyEnded
	3006: "Estado de juego inválido",                                  // CodeInvalidGameState
	3007: "Tiempo para reclamar sobre rojo agotado",                   // CodeGrabTimeout
	3008: "Tiempo para enviar sobre rojo agotado",                     // CodeSendTimeout
	3009: "Número de asiento inválido",                                // CodeInvalidSeatNo
	3010: "El asiento ya está ocupado",                                // CodeSeatOccupied
	3011: "El jugador no está en la sala",                             // CodePlayerNotInRoom
	1022: "El jugador está listo",                                     // CodePlayerAlreadyReady
	1023: "El jugador no puede salir de la sala",                      // CodePlayerCannotLeave
	1024: "Solo los jugadores pueden operar",                          // CodeNotPlayer
	3013: "Ya es jugador",                                             // CodeAlreadyPlayer
	3014: "Ya ha seleccionado asiento",                                // CodeAlreadySeated
	3015: "No ha seleccionado asiento",                                // CodeNotSeated
	3016: "Debe seleccionar asiento primero",                          // CodeNeedSeatFirst
	3017: "Sobre rojo ya creado",                                      // CodePacketExists
	3018: "Ya reclamó el sobre rojo",                                  // CodeAlreadyGrabbed
	3019: "No hay sobres rojos para reclamar",                         // CodeNoPacket
	3020: "El jugador no está desconectado",                           // CodePlayerNotOffline
	3021: "No está en fase de reclamar sobres rojos",                  // CodeNotInGrabbingPhase
	3022: "Ronda no encontrada",                                       // CodeRoundNotFound
	3023: "Número de ronda inválido",                                  // CodeInvalidRoundNumber
	3024: "No hay jugadores",                                          // CodeNoPlayers
	3025: "Los sobres rojos ya existen",                               // CodePacketsAlreadyExist
	3026: "Penalización aplicada",                                     // CodePenaltyApplied
	3027: "El jugador ha sido expulsado",                              // CodePlayerKicked
	3028: "Reemplazo fallido",                                         // CodeReplacementFailed
	3029: "No todos los jugadores están listos",                       // CodeNotAllPlayersReady
	3031: "Juego reanudado, el sistema envía sobre rojo",              // CodeGameResumed
	3032: "El jugador ya envió un sobre rojo, no se puede expulsar",   // CodePlayerAlreadySent
	3040: "Ya está en la cola de espera",                              // CodeAlreadyQueued
	3041: "No está en la cola de espera",                              // CodeNotQueued
	3042: "Los robots no pueden entrar en la cola",                    // CodeRobotNotAllowed
	3043: "Fallo en la sustitución automática",                        // CodeSubstituteFailed
	5001: "Operación Redis fallida",                                   // CodeRedisError
	5002: "Operación de base de datos fallida",                        // CodeMySQLError
	5003: "Error de cola de mensajes",                                 // CodeKafkaError
	5004: "Error de API de plataforma",                                // CodePlatformAPIError
	5005: "Fallo al adquirir bloqueo",                                 // CodeLockFailed
	6001: "Error al consultar el historial",                           // CodeHistoryQueryFailed
	6002: "La sesión no existe",                                       // CodeSessionNotFound
	6003: "El jugador no está en la sesión",                           // CodePlayerNotInSession
	6004: "Parámetros del historial inválidos",                         // CodeHistoryParamInvalid
}

// ==================== 踢出原因消息映射 ====================
var kickMessages = map[string]string{
	"seat_timeout":       "Tiempo de selección de asiento agotado, eliminado de la sala", // ReasonSeatTimeout
	"ready_timeout":      "Tiempo de preparación agotado, eliminado de la sala",          // ReasonReadyTimeout
	"disconnect_timeout": "Tiempo de desconexión agotado, eliminado de la sala",          // ReasonDisconnectTimeout
	"system_kick":        "Expulsado por el sistema",                                     // ReasonSystemKick
	"user_request":       "Salió de la sala voluntariamente",                             // ReasonUserRequest
	"player_leave":       "El jugador salió de la sala",                                  // ReasonPlayerLeave
	"login_elsewhere":    "Su cuenta ha iniciado sesión en otro dispositivo",             // ReasonLoginElsewhere
	"penalty_kick":       "No realizaste el envío manual durante dos rondas consecutivas. Has sido expulsado de la sala", // ReasonPenaltyKick
}

// ==================== 游戏中断原因消息映射 ====================
var interruptMessages = map[string]string{
	"normal":                    "Juego terminado normalmente",                                                                 // ReasonNormalEnd
	"first_round_deduct_failed": "Saldo insuficiente, cargo fallido, juego terminado",                                          // ReasonFirstRoundDeductFailed
	"later_round_deduct_failed": "Saldo insuficiente, cargo fallido, juego terminado",                                          // ReasonLaterRoundDeductFailed
	"partial_deduct_failed":     "Cargo fallido para algunos jugadores, reembolso solicitado automáticamente, juego terminado", // ReasonPartialDeductFailed
	"replacement_timeout":       "Tiempo de reemplazo agotado, juego terminado",                                                // ReasonReplacementTimeout
	"system_error":              "Error del sistema, juego terminado",                                                          // ReasonSystemError
	"penalty_deduct_failed":     "Cargo de penalización fallido, juego terminado",                                              // ReasonPenaltyDeductFailed
}

// ==================== 惩罚原因消息映射 ====================
var penaltyMessages = map[string]string{
	"send_timeout":       "No enviaste las Cashbox a tiempo. El sistema ha descontado el monto correspondiente de tu saldo y realizó el envío en tu nombre", // ReasonPenaltySendTimeout
	"leave_during_game":  "Saliste durante la partida. Se ha descontado el monto correspondiente de tu saldo",                                            // ReasonPenaltyLeaveDuringGame
	"disconnect_timeout": "Tiempo de desconexión agotado. Se ha descontado el monto correspondiente de tu saldo",                                        // ReasonPenaltyDisconnectTimeout
}

// GetErrorMsg 根据错误码返回西班牙语消息，未知码返回默认消息。
func GetErrorMsg(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "Error desconocido"
}

// GetKickMessage 根据踢出原因返回西班牙语消息，未知原因返回默认消息。
func GetKickMessage(reason string) string {
	if msg, ok := kickMessages[reason]; ok {
		return msg
	}
	return "Eliminado de la sala"
}

// GetInterruptMessage 根据中断原因返回西班牙语消息，未知原因返回默认消息。
func GetInterruptMessage(reason string) string {
	if msg, ok := interruptMessages[reason]; ok {
		return msg
	}
	return "Juego interrumpido"
}

// GetPenaltyMessage 根据惩罚原因返回西班牙语消息，未知原因返回默认消息。
func GetPenaltyMessage(reason string) string {
	if msg, ok := penaltyMessages[reason]; ok {
		return msg
	}
	return "Penalización aplicada"
}

// GetInsufficientBalanceMsg 构造余额不足的西班牙语消息。
func GetInsufficientBalanceMsg(requiredFee, balance int64) string {
	return fmt.Sprintf("Saldo insuficiente, se necesita %d, saldo actual %d", requiredFee, balance)
}
