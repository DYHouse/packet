package scripts

import (
	cRedis "github.com/cashparty/backend/common/redis"
)

var (
	// 房间/座位管理（9）
	JoinAsSpectator   = cRedis.NewScript("join_spectator", luaJoinAsSpectator)
	SelectSeat        = cRedis.NewScript("select_seat", luaSelectSeat)
	CancelSeat        = cRedis.NewScript("cancel_seat", luaCancelSeat)
	LeaveRoom         = cRedis.NewScript("leave_room", luaLeaveRoom)
	KickPlayer        = cRedis.NewScript("kick_player", luaKickPlayerAndInterrupt)
	PlayerReady       = cRedis.NewScript("player_ready", luaPlayerReady)
	HandleSeatTimeout = cRedis.NewScript("handle_seat_timeout", luaHandleSeatTimeout)
	AutoSeatAndReady  = cRedis.NewScript("auto_seat_ready", luaAutoSeatAndReady)
	TryStartGame      = cRedis.NewScript("try_start_game", luaTryStartGame)

	// 队列与替补（3）
	Enqueue        = cRedis.NewScript("enqueue", luaEnqueue)
	Dequeue        = cRedis.NewScript("dequeue", luaDequeue)
	AutoSubstitute = cRedis.NewScript("auto_substitute", luaAutoSubstitute)

	// 红包流程（4）
	GrabPacket            = cRedis.NewScript("grab_packet", luaGrabPacket)
	RobotGrabPacket       = cRedis.NewScript("robot_grab_packet", luaRobotGrabPacket)
	AutoDistributePackets = cRedis.NewScript("auto_distribute", luaAutoDistributePackets)
	SendPacket            = cRedis.NewScript("send_packet", luaSendPacket)

	// 回合结算（2）
	EndGame     = cRedis.NewScript("end_game", luaEndGame)
	SettleRound = cRedis.NewScript("settle_round", luaSettleRound)

	// 惩罚（2）
	HandlePenalty     = cRedis.NewScript("handle_penalty", luaHandlePenalty)
	DistributePenalty = cRedis.NewScript("distribute_penalty", luaDistributePenalty)
)
