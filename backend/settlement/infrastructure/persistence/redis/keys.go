package redis

import "fmt"

const (
	keyPrefix = "cashparty"

	KeySettleSendPacketLock     = keyPrefix + ":settle:lock:send_packet:%d"
	KeySettleRoundLock          = keyPrefix + ":settle:lock:round:%d"
	KeySettlePenaltyLock        = keyPrefix + ":settle:lock:penalty:%d"
	KeyFirstRoundDeductLock     = keyPrefix + ":settle:lock:first_round:%d"
	KeyLaterRoundDeductLock     = keyPrefix + ":settle:lock:later_round:%d"
	KeySystemPacketDeductLock   = keyPrefix + ":settle:lock:system_packet:%d"
	KeyRefundLock               = keyPrefix + ":settle:lock:refund:%s"
	KeyRefundApplyLock          = keyPrefix + ":settle:lock:refund_apply:%d"
	KeyDeductLock               = keyPrefix + ":settle:lock:deduct:%d_%d_%d"
	KeyBillRetryLock            = keyPrefix + ":settle:lock:bill_retry:%d"
	KeyPairBillCheckLock        = keyPrefix + ":settle:lock:pair_bill_check:%s"
	KeyGameSettleLock           = keyPrefix + ":settle:lock:game:%d"
	KeyGameSettleRetryLock      = keyPrefix + ":settle:lock:game_retry:%d_%d"
)

func SendPacketLockKey(roundID int64) string {
	return fmt.Sprintf(KeySettleSendPacketLock, roundID)
}

func SettleRoundLockKey(roundID int64) string {
	return fmt.Sprintf(KeySettleRoundLock, roundID)
}

func PenaltyLockKey(roundID int64) string {
	return fmt.Sprintf(KeySettlePenaltyLock, roundID)
}

func FirstRoundDeductLockKey(sessionID int64) string {
	return fmt.Sprintf(KeyFirstRoundDeductLock, sessionID)
}

func LaterRoundDeductLockKey(roundID int64) string {
	return fmt.Sprintf(KeyLaterRoundDeductLock, roundID)
}

func SystemPacketDeductLockKey(roundID int64) string {
	return fmt.Sprintf(KeySystemPacketDeductLock, roundID)
}

func RefundLockKey(refundOrderNo string) string {
	return fmt.Sprintf(KeyRefundLock, refundOrderNo)
}

func RefundApplyLockKey(billID int64) string {
	return fmt.Sprintf(KeyRefundApplyLock, billID)
}

func DeductLockKey(roundID int64, billType int, userID int64) string {
	return fmt.Sprintf(KeyDeductLock, roundID, billType, userID)
}

func BillRetryLockKey(billID int64) string {
	return fmt.Sprintf(KeyBillRetryLock, billID)
}

func PairBillCheckLockKey(roundTraceID string) string {
	return fmt.Sprintf(KeyPairBillCheckLock, roundTraceID)
}

func GameSettleLockKey(sessionID int64) string {
	return fmt.Sprintf(KeyGameSettleLock, sessionID)
}

func GameSettleRetryLockKey(sessionID int64, userID int64) string {
	return fmt.Sprintf(KeyGameSettleRetryLock, sessionID, userID)
}
