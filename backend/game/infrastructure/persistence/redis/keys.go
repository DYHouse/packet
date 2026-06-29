package redis

import "fmt"

const (
	keyPrefix               = "cashparty"
	keyRoomHashPrefix       = keyPrefix + ":room:hash"
	keyRoomSpectatorsPrefix = keyPrefix + ":room:spectators"
	keyRoomPlayersPrefix    = keyPrefix + ":room:players"

	KeyRoomHash          = keyRoomHashPrefix + ":%s"
	KeyRoomSpectators    = keyRoomSpectatorsPrefix + ":%s"
	KeyRoomPlayers       = keyRoomPlayersPrefix + ":%s"
	KeyPlayerRoom        = keyPrefix + ":player:room:%s"
	KeyRoomNode          = keyPrefix + ":room:node:%s"
	KeyRoomSeq           = keyPrefix + ":room:seq:%s"
	KeyDeadLetterQueue   = keyPrefix + ":db:dead_letter"
	KeyReconcileLock     = keyPrefix + ":lock:reconcile:rooms"
	KeyRoomSeats         = keyPrefix + ":room:seats:%s"
	KeyRoomSeatOwner     = keyPrefix + ":room:seat:owner:%s"
	KeyRoomQueue         = keyPrefix + ":room:queue:%s"
	KeySeatTimeout       = keyPrefix + ":room:seat:timeout"
	KeyReadyTimeout      = keyPrefix + ":room:ready:timeout"
	KeyDisconnectTimeout = keyPrefix + ":room:disconnect:timeout"

	KeyRoundPackets          = keyPrefix + ":round:packets:%s"
	KeyRoundAvailablePackets = keyPrefix + ":round:available_packets:%s"
	KeyRoundGrabbers         = keyPrefix + ":round:grabbers:%s"
	KeyRoundReward           = keyPrefix + ":round:reward:%s"
	KeyUserGrabbed           = keyPrefix + ":round:grabbed:%s:%s"
	KeyPacketInfo            = keyPrefix + ":packet:info:%s"
	KeyGrabRecord            = keyPrefix + ":grab:record:%s"
	KeyRoundGrabRecord       = keyPrefix + ":round:grab_record:%s:%s"
	KeyRoomPlayer            = keyPrefix + ":room:player:%s:%s"
	KeyRoomNoToID            = keyPrefix + ":room:no_to_id:%s"
	KeyUserByUserID          = keyPrefix + ":user:user_id:%s"
	KeyUserById              = keyPrefix + ":user:id:%s"

	KeyGameStartLock      = keyPrefix + ":lock:game_start:%s"
	KeySettleLock         = keyPrefix + ":lock:settle:%s:%s"
	KeyGameEndLock        = keyPrefix + ":lock:game_end:%s"
	KeySendPacketLock     = keyPrefix + ":lock:send_packet:%s:%s"
	KeyReplaceTimeoutLock = keyPrefix + ":lock:replace_timeout:%s:%s"

	KeyRoundState       = keyPrefix + ":round:state:%s"
	KeyRoundStatePrefix = keyPrefix + ":round:state:"
	KeyPenaltyCount     = keyPrefix + ":penalty:count:%s:%s"
	KeyPenaltyRecord    = keyPrefix + ":penalty:record:%s:%s"
	KeyTimeout          = keyPrefix + ":timeout:%s"
	KeySettlementDone   = keyPrefix + ":settle:done:%d"

	KeyRoomEventProcessed = keyPrefix + ":room:event:processed:%s"
	KeyGameEventProcessed = keyPrefix + ":game:event:processed:%s"

	KeyRewardCycleStraight = keyPrefix + ":reward:cycle:%s:%s:straight"
	KeyRewardCycleLeopard  = keyPrefix + ":reward:cycle:%s:%s:leopard"
	KeyProfitDaily         = keyPrefix + ":profit:daily:%s"
	KeySessionPlayerTotals = keyPrefix + ":session:%s:player:totals"

	// 机器人相关 key（统一 cashparty: 前缀，遵循本文件规范）
	KeyRobotPoolAvailable       = keyPrefix + ":robot:pool:available"
	KeyRobotRoom                = keyPrefix + ":robot:room:%s"
	KeyRobotAssignLock          = keyPrefix + ":robot:assign:%d"
	KeyRobotRoomAssignLock      = keyPrefix + ":robot:room_assign:%s"
	KeyRobotRecycleCooldown     = keyPrefix + ":robot:recycle_cooldown:%d"
	KeyRobotSchedulerActive     = keyPrefix + ":robot:scheduler:active"
	KeyRobotVirtualBalance      = keyPrefix + ":robot:virtual_balance:%d"
	KeyRobotVirtualBalanceDirty = keyPrefix + ":robot:virtual_balance:dirty"
	KeyRobotUserIDs             = keyPrefix + ":robot:user_ids"
)

func RoomHashKey(roomID string) string {
	return fmt.Sprintf(KeyRoomHash, roomID)
}

func RoomSpectatorsKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSpectators, roomID)
}

func RoomPlayersKey(roomID string) string {
	return fmt.Sprintf(KeyRoomPlayers, roomID)
}

func PlayerRoomKey(userID string) string {
	return fmt.Sprintf(KeyPlayerRoom, userID)
}

func RoomNodeKey(roomID string) string {
	return fmt.Sprintf(KeyRoomNode, roomID)
}

func RoomSeqKey(date string) string {
	return fmt.Sprintf(KeyRoomSeq, date)
}

func DeadLetterQueueKey() string {
	return KeyDeadLetterQueue
}

func ReconcileLockKey() string {
	return KeyReconcileLock
}

func RoomSeatsKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSeats, roomID)
}

func RoomSeatOwnerKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSeatOwner, roomID)
}

func RoomQueueKey(roomID string) string {
	return fmt.Sprintf(KeyRoomQueue, roomID)
}

func SeatTimeoutKey() string {
	return KeySeatTimeout
}

func ReadyTimeoutKey() string {
	return KeyReadyTimeout
}

func DisconnectTimeoutKey() string {
	return KeyDisconnectTimeout
}

func RoundPacketsKey(roundID string) string {
	return fmt.Sprintf(KeyRoundPackets, roundID)
}

func RoundAvailablePacketsKey(roundID string) string {
	return fmt.Sprintf(KeyRoundAvailablePackets, roundID)
}

func RoundGrabbersKey(roundID string) string {
	return fmt.Sprintf(KeyRoundGrabbers, roundID)
}

func RoundRewardKey(roundID string) string {
	return fmt.Sprintf(KeyRoundReward, roundID)
}

func UserGrabbedKey(roundID, userID string) string {
	return fmt.Sprintf(KeyUserGrabbed, roundID, userID)
}

func PacketInfoKey(packetID string) string {
	return fmt.Sprintf(KeyPacketInfo, packetID)
}

func GrabRecordKey(packetID string) string {
	return fmt.Sprintf(KeyGrabRecord, packetID)
}

func RoundGrabRecordKey(roundID, userID string) string {
	return fmt.Sprintf(KeyRoundGrabRecord, roundID, userID)
}

func RoomPlayerKey(roomID, userID string) string {
	return fmt.Sprintf(KeyRoomPlayer, roomID, userID)
}

func RoomNoToIDKey(roomNo string) string {
	return fmt.Sprintf(KeyRoomNoToID, roomNo)
}

func UserByUserIDKey(userID string) string {
	return fmt.Sprintf(KeyUserByUserID, userID)
}

func UserByIdKey(id string) string {
	return fmt.Sprintf(KeyUserById, id)
}

func GameStartLockKey(roomID string) string {
	return fmt.Sprintf(KeyGameStartLock, roomID)
}

func RoundStateKey(roundID string) string {
	return fmt.Sprintf(KeyRoundState, roundID)
}

func PenaltyCountKey(roomID, userID string) string {
	return fmt.Sprintf(KeyPenaltyCount, roomID, userID)
}

func PenaltyRecordKey(roomID, userID string) string {
	return fmt.Sprintf(KeyPenaltyRecord, roomID, userID)
}

func TimeoutKey(timeoutType string) string {
	return fmt.Sprintf(KeyTimeout, timeoutType)
}

func SettlementDoneKey(roundID int64) string {
	return fmt.Sprintf(KeySettlementDone, roundID)
}

func SettleLockKey(roomID, roundID string) string {
	return fmt.Sprintf(KeySettleLock, roomID, roundID)
}

func GameEndLockKey(roomID string) string {
	return fmt.Sprintf(KeyGameEndLock, roomID)
}

func SendPacketLockKey(roomID, userID string) string {
	return fmt.Sprintf(KeySendPacketLock, roomID, userID)
}

func ReplaceTimeoutLockKey(roomID, userID string) string {
	return fmt.Sprintf(KeyReplaceTimeoutLock, roomID, userID)
}

func RoomEventProcessedKey(eventID string) string {
	return fmt.Sprintf(KeyRoomEventProcessed, eventID)
}

func GameEventProcessedKey(traceID string) string {
	return fmt.Sprintf(KeyGameEventProcessed, traceID)
}

func RewardCycleStraightKey(roomID, sessionID string) string {
	return fmt.Sprintf(KeyRewardCycleStraight, roomID, sessionID)
}

func RewardCycleLeopardKey(roomID, sessionID string) string {
	return fmt.Sprintf(KeyRewardCycleLeopard, roomID, sessionID)
}

func ProfitDailyKey(date string) string {
	return fmt.Sprintf(KeyProfitDaily, date)
}

func SessionPlayerTotalsKey(sessionID string) string {
	return fmt.Sprintf(KeySessionPlayerTotals, sessionID)
}

// 机器人 key 工厂函数

// RobotPoolAvailableKey 可用机器人账号池 key
func RobotPoolAvailableKey() string {
	return KeyRobotPoolAvailable
}

// RobotRoomKey 房间机器人集合 key
func RobotRoomKey(roomID string) string {
	return fmt.Sprintf(KeyRobotRoom, roomID)
}

// RobotAssignLockKey 机器人分配锁 key
func RobotAssignLockKey(robotUserID int64) string {
	return fmt.Sprintf(KeyRobotAssignLock, robotUserID)
}

// RobotRoomAssignLockKey 房间分配限流锁 key
func RobotRoomAssignLockKey(roomID string) string {
	return fmt.Sprintf(KeyRobotRoomAssignLock, roomID)
}

// RobotRecycleCooldownKey 机器人回收冷却 key
func RobotRecycleCooldownKey(userID int64) string {
	return fmt.Sprintf(KeyRobotRecycleCooldown, userID)
}

// RobotSchedulerActiveKey 调度器活跃机器人集合 key
func RobotSchedulerActiveKey() string {
	return KeyRobotSchedulerActive
}

// RobotVirtualBalanceKey 机器人虚拟余额 key
func RobotVirtualBalanceKey(userID int64) string {
	return fmt.Sprintf(KeyRobotVirtualBalance, userID)
}

// RobotVirtualBalanceDirtyKey 虚拟余额脏数据集合 key
func RobotVirtualBalanceDirtyKey() string {
	return KeyRobotVirtualBalanceDirty
}

// RobotUserIDsKey 机器人用户ID集合 key
func RobotUserIDsKey() string {
	return KeyRobotUserIDs
}
