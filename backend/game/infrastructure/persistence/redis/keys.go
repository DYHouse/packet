// Package redis 提供 game 层的 Redis 数据访问。
// 本文件为 key 定义的兼容层（re-export），所有 key 常量与工厂函数的真正定义
// 已迁移到 common/rediskeys/keys.go（单一真相源），此处仅做 re-export 以兼容现有调用方。
// 规约参考 CODING_STANDARD.md §6.2 与 §16 SC-5。
package redis

import (
	"github.com/cashparty/backend/common/rediskeys"
)

// ============================================================================
// 常量 re-export
// ============================================================================

const (
	KeyRoomHash          = rediskeys.KeyRoomHash
	KeyRoomHashPrefix    = rediskeys.KeyRoomHashPrefix
	KeyRoomSpectators    = rediskeys.KeyRoomSpectators
	KeyRoomPlayers       = rediskeys.KeyRoomPlayers
	KeyPlayerRoom        = rediskeys.KeyPlayerRoom
	KeyRoomNode          = rediskeys.KeyRoomNode
	KeyRoomSeq           = rediskeys.KeyRoomSeq
	KeyDeadLetterQueue   = rediskeys.KeyDeadLetterQueue
	KeyRoomSeats         = rediskeys.KeyRoomSeats
	KeyRoomSeatOwner     = rediskeys.KeyRoomSeatOwner
	KeyRoomQueue         = rediskeys.KeyRoomQueue
	KeySeatTimeout       = rediskeys.KeySeatTimeout
	KeyReadyTimeout      = rediskeys.KeyReadyTimeout
	KeyDisconnectTimeout = rediskeys.KeyDisconnectTimeout

	KeyRoundPackets          = rediskeys.KeyRoundPackets
	KeyRoundAvailablePackets = rediskeys.KeyRoundAvailablePackets
	KeyRoundGrabbers         = rediskeys.KeyRoundGrabbers
	KeyRoundReward           = rediskeys.KeyRoundReward
	KeyUserGrabbed           = rediskeys.KeyUserGrabbed
	KeyPacketInfo            = rediskeys.KeyPacketInfo
	KeyGrabRecord            = rediskeys.KeyGrabRecord
	KeyRoundGrabRecord       = rediskeys.KeyRoundGrabRecord
	KeyRoomPlayer            = rediskeys.KeyRoomPlayer
	KeyRoomNoToID            = rediskeys.KeyRoomNoToID
	KeyUserByUserID          = rediskeys.KeyUserByUserID
	KeyUserById              = rediskeys.KeyUserById

	KeyGameStartLock      = rediskeys.KeyGameStartLock
	KeySettleLock         = rediskeys.KeySettleLock
	KeyGameEndLock        = rediskeys.KeyGameEndLock
	KeySendPacketLock     = rediskeys.KeySendPacketLock
	KeyReplaceTimeoutLock = rediskeys.KeyReplaceTimeoutLock

	KeyRoundState       = rediskeys.KeyRoundState
	KeyRoundStatePrefix = rediskeys.KeyRoundStatePrefix
	KeyPenaltyCount     = rediskeys.KeyPenaltyCount
	KeyPenaltyRecord    = rediskeys.KeyPenaltyRecord
	KeyTimeout          = rediskeys.KeyTimeout
	KeySettlementDone   = rediskeys.KeySettlementDone

	KeyRoomEventProcessed = rediskeys.KeyRoomEventProcessed
	KeyGameEventProcessed = rediskeys.KeyGameEventProcessed

	KeyRewardCycleStraight = rediskeys.KeyRewardCycleStraight
	KeyRewardCycleLeopard  = rediskeys.KeyRewardCycleLeopard
	KeyProfitDaily         = rediskeys.KeyProfitDaily
	KeySessionPlayerTotals = rediskeys.KeySessionPlayerTotals

	// 机器人相关 key
	KeyRobotPoolAvailable       = rediskeys.KeyRobotPoolAvailable
	KeyRobotRoom                = rediskeys.KeyRobotRoom
	KeyRobotRoomPrefix          = rediskeys.KeyRobotRoomPrefix
	KeyRobotAssignLock          = rediskeys.KeyRobotAssignLock
	KeyRobotRoomAssignLock      = rediskeys.KeyRobotRoomAssignLock
	KeyRobotRecycleCooldown     = rediskeys.KeyRobotRecycleCooldown
	KeyRobotSchedulerActive     = rediskeys.KeyRobotSchedulerActive
	KeyRobotVirtualBalance      = rediskeys.KeyRobotVirtualBalance
	KeyRobotVirtualBalanceDirty = rediskeys.KeyRobotVirtualBalanceDirty
	KeyRobotUserIDs             = rediskeys.KeyRobotUserIDs
)

// ============================================================================
// 工厂函数 re-export
// ============================================================================

func RoomHashKey(roomID string) string {
	return rediskeys.RoomHashKey(roomID)
}

func RoomSpectatorsKey(roomID string) string {
	return rediskeys.RoomSpectatorsKey(roomID)
}

func RoomPlayersKey(roomID string) string {
	return rediskeys.RoomPlayersKey(roomID)
}

func PlayerRoomKey(userID string) string {
	return rediskeys.PlayerRoomKey(userID)
}

func RoomNodeKey(roomID string) string {
	return rediskeys.RoomNodeKey(roomID)
}

func RoomSeqKey(date string) string {
	return rediskeys.RoomSeqKey(date)
}

func DeadLetterQueueKey() string {
	return rediskeys.DeadLetterQueueKey()
}

func RoomSeatsKey(roomID string) string {
	return rediskeys.RoomSeatsKey(roomID)
}

func RoomSeatOwnerKey(roomID string) string {
	return rediskeys.RoomSeatOwnerKey(roomID)
}

func RoomQueueKey(roomID string) string {
	return rediskeys.RoomQueueKey(roomID)
}

func SeatTimeoutKey() string {
	return rediskeys.SeatTimeoutKey()
}

func ReadyTimeoutKey() string {
	return rediskeys.ReadyTimeoutKey()
}

func DisconnectTimeoutKey() string {
	return rediskeys.DisconnectTimeoutKey()
}

func RoundPacketsKey(roundID string) string {
	return rediskeys.RoundPacketsKey(roundID)
}

func RoundAvailablePacketsKey(roundID string) string {
	return rediskeys.RoundAvailablePacketsKey(roundID)
}

func RoundGrabbersKey(roundID string) string {
	return rediskeys.RoundGrabbersKey(roundID)
}

func RoundRewardKey(roundID string) string {
	return rediskeys.RoundRewardKey(roundID)
}

func UserGrabbedKey(roundID, userID string) string {
	return rediskeys.UserGrabbedKey(roundID, userID)
}

func PacketInfoKey(packetID string) string {
	return rediskeys.PacketInfoKey(packetID)
}

func PacketAvailableKey(packetID string) string {
	return rediskeys.PacketAvailableKey(packetID)
}

func GrabRecordKey(packetID string) string {
	return rediskeys.GrabRecordKey(packetID)
}

func RoundGrabRecordKey(roundID, userID string) string {
	return rediskeys.RoundGrabRecordKey(roundID, userID)
}

func RoomPlayerKey(roomID, userID string) string {
	return rediskeys.RoomPlayerKey(roomID, userID)
}

func RoomNoToIDKey(roomNo string) string {
	return rediskeys.RoomNoToIDKey(roomNo)
}

func UserByUserIDKey(userID string) string {
	return rediskeys.UserByUserIDKey(userID)
}

func UserByIdKey(id string) string {
	return rediskeys.UserByIdKey(id)
}

func GameStartLockKey(roomID string) string {
	return rediskeys.GameStartLockKey(roomID)
}

func RoundStateKey(roundID string) string {
	return rediskeys.RoundStateKey(roundID)
}

func PenaltyCountKey(roomID, userID string) string {
	return rediskeys.PenaltyCountKey(roomID, userID)
}

func PenaltyRecordKey(roomID, userID string) string {
	return rediskeys.PenaltyRecordKey(roomID, userID)
}

func TimeoutKey(timeoutType string) string {
	return rediskeys.TimeoutKey(timeoutType)
}

func SettlementDoneKey(roundID int64) string {
	return rediskeys.SettlementDoneKey(roundID)
}

func SettleLockKey(roomID, roundID string) string {
	return rediskeys.SettleLockKey(roomID, roundID)
}

func GameEndLockKey(roomID string) string {
	return rediskeys.GameEndLockKey(roomID)
}

func SendPacketLockKey(roomID, userID string) string {
	return rediskeys.SendPacketLockKey(roomID, userID)
}

func ReplaceTimeoutLockKey(roomID, userID string) string {
	return rediskeys.ReplaceTimeoutLockKey(roomID, userID)
}

func RoomEventProcessedKey(eventID string) string {
	return rediskeys.RoomEventProcessedKey(eventID)
}

func GameEventProcessedKey(traceID string) string {
	return rediskeys.GameEventProcessedKey(traceID)
}

func RewardCycleStraightKey(roomID, sessionID string) string {
	return rediskeys.RewardCycleStraightKey(roomID, sessionID)
}

func RewardCycleLeopardKey(roomID, sessionID string) string {
	return rediskeys.RewardCycleLeopardKey(roomID, sessionID)
}

func ProfitDailyKey(date string) string {
	return rediskeys.ProfitDailyKey(date)
}

func SessionPlayerTotalsKey(sessionID string) string {
	return rediskeys.SessionPlayerTotalsKey(sessionID)
}

// 机器人 key 工厂函数

// RobotPoolAvailableKey 可用机器人账号池 key
func RobotPoolAvailableKey() string {
	return rediskeys.RobotPoolAvailableKey()
}

// RobotRoomKey 房间机器人集合 key
func RobotRoomKey(roomID string) string {
	return rediskeys.RobotRoomKey(roomID)
}

// RobotAssignLockKey 机器人分配锁 key
func RobotAssignLockKey(robotUserID int64) string {
	return rediskeys.RobotAssignLockKey(robotUserID)
}

// RobotRoomAssignLockKey 房间分配限流锁 key
func RobotRoomAssignLockKey(roomID string) string {
	return rediskeys.RobotRoomAssignLockKey(roomID)
}

// RobotRecycleCooldownKey 机器人回收冷却 key
func RobotRecycleCooldownKey(userID int64) string {
	return rediskeys.RobotRecycleCooldownKey(userID)
}

// RobotSchedulerActiveKey 调度器活跃机器人集合 key
func RobotSchedulerActiveKey() string {
	return rediskeys.RobotSchedulerActiveKey()
}

// RobotVirtualBalanceKey 机器人虚拟余额 key
func RobotVirtualBalanceKey(userID int64) string {
	return rediskeys.RobotVirtualBalanceKey(userID)
}

// RobotVirtualBalanceDirtyKey 虚拟余额脏数据集合 key
func RobotVirtualBalanceDirtyKey() string {
	return rediskeys.RobotVirtualBalanceDirtyKey()
}

// RobotUserIDsKey 机器人用户ID集合 key
func RobotUserIDsKey() string {
	return rediskeys.RobotUserIDsKey()
}
