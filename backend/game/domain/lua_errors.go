package domain

const (
	LuaSuccess = 0

	// 房间相关错误 (1-9)
	LuaErrRoomNotFound   = 1
	LuaErrRoomFull       = 2
	LuaErrSpectatorFull  = 3
	LuaErrAlreadyInRoom  = 4
	LuaErrNoCandidate    = 5
	LuaErrGameStarted    = 6
	LuaErrSeatOccupied   = 7
	LuaErrInvalidSeat    = 8
	LuaErrAlreadyPlayer  = 9

	// 座位相关错误 (10-19)
	LuaErrAlreadySeated  = 10
	LuaErrNotSeated      = 11
	LuaErrNeedSeatFirst  = 12
	LuaErrAlreadyReady   = 13
	LuaErrUserNotInRoom  = 14
	LuaErrTotalFull      = 15
	LuaErrPlayerCannotLeave = 16

	// 红包相关错误 (20-29)
	LuaErrPacketExists     = 20
	LuaErrAlreadyGrabbed   = 21
	LuaErrNoPacket         = 22
	LuaErrPacketNotFound   = 23

	// 玩家状态错误 (30-39)
	LuaErrPlayerNotFound   = 30
	LuaErrPlayerNotOffline = 31

	// 抢红包错误 (40-49)
	LuaErrNotGrabbingPhase = 40
	LuaErrGrabTimeout      = 41

	// 发红包错误 (50-59)
	LuaErrInvalidRoundNumber = 50
	LuaErrNotYourTurn        = 51
	LuaErrNoPlayers          = 52

	// 权限错误 (60-69)
	LuaErrNotPlayer = 60

	// 排队相关错误 (70-79)
	LuaErrAlreadyQueued   = 70
	LuaErrNotQueued       = 71
	LuaErrRobotNotAllowed = 72
	LuaErrNoEmptySeat     = 73
	LuaErrSubstituteFail  = 74
	LuaErrQueueEmpty      = 75
)
