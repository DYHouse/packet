package message

// ==================== 命令类型 ====================
const (
	CmdPing            = "ping"
	CmdJoinRoom        = "join_room"
	CmdAutoMatch       = "auto_match"
	CmdLeaveRoom       = "leave_room"
	CmdRoomState       = "room_state"
	CmdSelectSeat      = "select_seat"
	CmdCancelSeat      = "cancel_seat"
	CmdPlayerReady     = "player_ready"
	CmdSendPacket      = "send_packet"
	CmdGrabPacket      = "grab_packet"
	CmdGetRoomList     = "get_room_list"
	CmdGetRoomTypeList = "get_room_type_list"
	CmdReconnect       = "reconnect"
	CmdDisconnect      = "disconnect"
	CmdGetUserBalance  = "get_user_balance"
	CmdEnqueue         = "enqueue"
	CmdDequeue         = "dequeue"
)

// ==================== 推送类型 ====================
const (
	PushRoomState = "room_state"

	PushSpectatorJoined    = "spectator_joined"
	PushSpectatorLeft      = "spectator_left"
	PushPlayerJoined       = "player_joined"
	PushPlayerLeft         = "player_left"
	PushSeatSelected       = "seat_selected"
	PushSeatCancelled      = "seat_cancelled"
	PushPlayerReady        = "player_ready"
	PushPlayerDisconnected = "player_disconnected"
	PushPlayerReconnected  = "player_reconnected"

	PushGameStart       = "game_start"
	PushGameResumed     = "game_resumed"
	PushCountdownStart  = "countdown_start"
	PushCountdown       = "countdown"
	PushRoundStart      = "round_start"
	PushPacketGrabbed   = "packet_grabbed"
	PushRoundEnd        = "round_end"
	PushAutoDistribute  = "auto_distribute"
	PushPenalty         = "penalty"
	PushWaitReplacement = "wait_replacement"
	PushGameInterrupted = "game_interrupted"
	PushSubstitute      = "substitute"

	PushKicked           = "kicked"
	PushReconnectSuccess = "reconnect_success"
	PushError            = "error"
	PushDequeued         = "dequeued"
)

// ==================== 房间状态常量 ====================
const (
	RoomStatusWaiting     = 1
	RoomStatusPlaying     = 2
	RoomStatusInterrupted = 4
)

// ==================== 广播目标类型 ====================
const (
	TargetTypeRoom = "room"
	TargetTypeUser = "user"
)

// ==================== 游戏阶段常量 ====================
const (
	GamePhaseWaiting    = "WAITING"
	GamePhaseCountdown  = "COUNTDOWN"
	GamePhaseRoundStart = "ROUND_START"
	GamePhaseGrabbing   = "GRABBING"
	GamePhaseSettling   = "SETTLING"
	GamePhaseWaitSend   = "WAIT_SEND"
	GamePhaseGameEnd    = "GAME_END"
)

// ==================== 惩罚类型常量 ====================
const (
	PenaltyTypeSendTimeout       = "send_timeout"
	PenaltyTypeLeaveDuringGame   = "leave_during_game"
	PenaltyTypeDisconnectTimeout = "disconnect_timeout"
)
