// Package redis 提供 settlement 层的 Redis 数据访问。
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
	KeySettleSendPacketLock   = rediskeys.KeySettleSendPacketLock
	KeySettleRoundLock        = rediskeys.KeySettleRoundLock
	KeySettlePenaltyLock      = rediskeys.KeySettlePenaltyLock
	KeyFirstRoundDeductLock   = rediskeys.KeyFirstRoundDeductLock
	KeyLaterRoundDeductLock   = rediskeys.KeyLaterRoundDeductLock
	KeySystemPacketDeductLock = rediskeys.KeySystemPacketDeductLock
	KeyRefundLock             = rediskeys.KeyRefundLock
	KeyRefundApplyLock        = rediskeys.KeyRefundApplyLock
	// KeyDeductLock 格式已从下划线分隔（%d_%d_%d）改为冒号分隔（%d:%d:%d），规约 SC-10。
	KeyDeductLock          = rediskeys.KeyDeductLock
	KeyBillRetryLock       = rediskeys.KeyBillRetryLock
	KeyPairBillCheckLock   = rediskeys.KeyPairBillCheckLock
	KeyGameSettleLock      = rediskeys.KeyGameSettleLock
	KeyGameSettleRetryLock = rediskeys.KeyGameSettleRetryLock

	// 机器人相关 key（与 game 层共享同一常量）
	KeyRobotVirtualBalance      = rediskeys.KeyRobotVirtualBalance
	KeyRobotVirtualBalanceDirty = rediskeys.KeyRobotVirtualBalanceDirty
	KeyRobotUserIDs             = rediskeys.KeyRobotUserIDs
)

// ============================================================================
// 工厂函数 re-export
// ============================================================================

// SendPacketLockKey 发红包结算锁 key（settlement 层）
// 注意：与 game 层的 SendPacketLockKey(roomID, userID string) 不同，此处参数为 roundID int64。
// 在 common/rediskeys 中已重命名为 SettleSendPacketLockKey 以避免命名冲突。
func SendPacketLockKey(roundID int64) string {
	return rediskeys.SettleSendPacketLockKey(roundID)
}

func SettleRoundLockKey(roundID int64) string {
	return rediskeys.SettleRoundLockKey(roundID)
}

func PenaltyLockKey(roundID int64) string {
	return rediskeys.PenaltyLockKey(roundID)
}

func FirstRoundDeductLockKey(sessionID int64) string {
	return rediskeys.FirstRoundDeductLockKey(sessionID)
}

func LaterRoundDeductLockKey(roundID int64) string {
	return rediskeys.LaterRoundDeductLockKey(roundID)
}

func SystemPacketDeductLockKey(roundID int64) string {
	return rediskeys.SystemPacketDeductLockKey(roundID)
}

func RefundLockKey(refundOrderNo string) string {
	return rediskeys.RefundLockKey(refundOrderNo)
}

func RefundApplyLockKey(billID int64) string {
	return rediskeys.RefundApplyLockKey(billID)
}

// DeductLockKey 扣款锁 key（P1-8 修复：使用 ":" 分隔，禁止下划线 "_"）
func DeductLockKey(roundID int64, billType int, userID int64) string {
	return rediskeys.DeductLockKey(roundID, billType, userID)
}

func BillRetryLockKey(billID int64) string {
	return rediskeys.BillRetryLockKey(billID)
}

func PairBillCheckLockKey(roundTraceID string) string {
	return rediskeys.PairBillCheckLockKey(roundTraceID)
}

func GameSettleLockKey(sessionID int64) string {
	return rediskeys.GameSettleLockKey(sessionID)
}

// GameSettleRetryLockKey 游戏结算重试锁 key（P1-8 修复：使用 ":" 分隔，禁止下划线 "_"）
func GameSettleRetryLockKey(sessionID int64, userID int64) string {
	return rediskeys.GameSettleRetryLockKey(sessionID, userID)
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
