package domain

// Lua 错误码常量表（单一真相源）
// Lua 脚本注释和 Go 侧 MapLuaError 共用
// 业务脚本返回值第一项必须是这些常量之一
// 通用脚本（锁释放/限流/连接注册/余额扣减）不走 MapLuaError，返回 0/1
const (
	// 成功 / 幂等
	LuaErrSuccess    = 0
	LuaErrIdempotent = 2 // 幂等成功（已处理过，非错误）

	// 房间相关错误 (1-9)
	LuaErrRoomNotFound     = 1
	LuaErrRoomFullTotal    = 3 // 房间总人数已满
	LuaErrAlreadyInRoom    = 4
	LuaErrNoCandidate      = 5
	LuaErrGameNotInPlaying = 6
	LuaErrSeatOccupied     = 7
	LuaErrInvalidSeatNo    = 8
	LuaErrAlreadyPlayer    = 9

	// 座位相关错误 (10-19)
	LuaErrAlreadySeated     = 10
	LuaErrNotSeated         = 11
	LuaErrNoSeatSelected    = 12
	LuaErrNotInRoom         = 14
	LuaErrRoomFull          = 15 // 观众席已满
	LuaErrPlayerCannotLeave = 16

	// 红包相关错误 (20-29)
	LuaErrPacketsAlreadyExist = 20
	LuaErrAlreadyGrabbed      = 21
	LuaErrPacketNotAvailable  = 22
	LuaErrPacketInfoNotFound  = 23

	// 玩家状态错误 (30-39)
	LuaErrPlayerNotFound    = 30
	LuaErrPlayerNotOffline  = 31
	LuaErrPlayerAlreadySent = 32

	// 抢红包错误 (40-49)
	LuaErrNotInGrabbingPhase = 40
	LuaErrGrabTimeout        = 41

	// 发红包错误 (50-59)
	LuaErrNotFirstRound = 50
	LuaErrNotYourTurn   = 51
	LuaErrNoPlayers     = 52

	// 权限错误 (60-69)
	LuaErrOnlyPlayerCanGrab = 60

	// 排队相关错误 (70-79)
	LuaErrAlreadyQueued   = 70
	LuaErrNotQueued       = 71
	LuaErrRobotNotAllowed = 72
	LuaErrNoEmptySeat     = 73
	LuaErrSubstituteFail  = 74
	LuaErrQueueEmpty      = 75
)
