package config

import "time"

// LockConfig holds TTL configurations for the 14 business distributed locks.
// All durations default to the historically hardcoded values via
// SetLockDefaults to preserve backward compatibility.
type LockConfig struct {
	// FirstRoundDeductLockTTL guards DeductForFirstRound settlement lock.
	FirstRoundDeductLockTTL time.Duration `mapstructure:"first_round_deduct_lock_ttl" yaml:"first_round_deduct_lock_ttl"`
	// LaterRoundDeductLockTTL guards per-user later round deduction.
	LaterRoundDeductLockTTL time.Duration `mapstructure:"later_round_deduct_lock_ttl" yaml:"later_round_deduct_lock_ttl"`
	// SystemPacketDeductLockTTL guards system packet deduction.
	SystemPacketDeductLockTTL time.Duration `mapstructure:"system_packet_deduct_lock_ttl" yaml:"system_packet_deduct_lock_ttl"`
	// SettleRoundLockTTL guards SettleRound credit posting.
	SettleRoundLockTTL time.Duration `mapstructure:"settle_round_lock_ttl" yaml:"settle_round_lock_ttl"`
	// GameSettleLockTTL guards SettleGame aggregate settlement.
	GameSettleLockTTL time.Duration `mapstructure:"game_settle_lock_ttl" yaml:"game_settle_lock_ttl"`
	// GameSettleRetryLockTTL guards RetryPlayerSettle per-player retry.
	GameSettleRetryLockTTL time.Duration `mapstructure:"game_settle_retry_lock_ttl" yaml:"game_settle_retry_lock_ttl"`
	// CreditRetryLockTTL guards RetryCredit bill retry.
	CreditRetryLockTTL time.Duration `mapstructure:"credit_retry_lock_ttl" yaml:"credit_retry_lock_ttl"`
	// RefundApplyLockTTL guards ApplyForRefund creation.
	RefundApplyLockTTL time.Duration `mapstructure:"refund_apply_lock_ttl" yaml:"refund_apply_lock_ttl"`
	// RefundApproveLockTTL guards ApproveRefund execution.
	RefundApproveLockTTL time.Duration `mapstructure:"refund_approve_lock_ttl" yaml:"refund_approve_lock_ttl"`
	// RefundRejectLockTTL guards RejectRefund status update.
	RefundRejectLockTTL time.Duration `mapstructure:"refund_reject_lock_ttl" yaml:"refund_reject_lock_ttl"`
	// SendPacketLockTTL guards SendPacket round creation.
	SendPacketLockTTL time.Duration `mapstructure:"send_packet_lock_ttl" yaml:"send_packet_lock_ttl"`
	// SendTimeoutLockTTL guards OnSendTimeout fallback handling.
	SendTimeoutLockTTL time.Duration `mapstructure:"send_timeout_lock_ttl" yaml:"send_timeout_lock_ttl"`
	// ReplaceTimeoutLockTTL guards OnReplaceTimeout penalty distribution.
	ReplaceTimeoutLockTTL time.Duration `mapstructure:"replace_timeout_lock_ttl" yaml:"replace_timeout_lock_ttl"`
	// GameAppSettleLockTTL guards game-layer settleRound invocation.
	GameAppSettleLockTTL time.Duration `mapstructure:"game_app_settle_lock_ttl" yaml:"game_app_settle_lock_ttl"`
}

// SetLockDefaults populates zero-valued LockConfig fields with the
// historically hardcoded TTLs to preserve backward compatibility.
func SetLockDefaults(cfg *LockConfig) {
	if cfg == nil {
		return
	}
	if cfg.FirstRoundDeductLockTTL == 0 {
		cfg.FirstRoundDeductLockTTL = 60 * time.Second
	}
	if cfg.LaterRoundDeductLockTTL == 0 {
		cfg.LaterRoundDeductLockTTL = 30 * time.Second
	}
	if cfg.SystemPacketDeductLockTTL == 0 {
		cfg.SystemPacketDeductLockTTL = 30 * time.Second
	}
	if cfg.SettleRoundLockTTL == 0 {
		cfg.SettleRoundLockTTL = 30 * time.Second
	}
	if cfg.GameSettleLockTTL == 0 {
		cfg.GameSettleLockTTL = 60 * time.Second
	}
	if cfg.GameSettleRetryLockTTL == 0 {
		cfg.GameSettleRetryLockTTL = 30 * time.Second
	}
	if cfg.CreditRetryLockTTL == 0 {
		cfg.CreditRetryLockTTL = 30 * time.Second
	}
	if cfg.RefundApplyLockTTL == 0 {
		cfg.RefundApplyLockTTL = 30 * time.Second
	}
	if cfg.RefundApproveLockTTL == 0 {
		cfg.RefundApproveLockTTL = 30 * time.Second
	}
	if cfg.RefundRejectLockTTL == 0 {
		cfg.RefundRejectLockTTL = 30 * time.Second
	}
	if cfg.SendPacketLockTTL == 0 {
		cfg.SendPacketLockTTL = 10 * time.Second
	}
	if cfg.SendTimeoutLockTTL == 0 {
		cfg.SendTimeoutLockTTL = 10 * time.Second
	}
	if cfg.ReplaceTimeoutLockTTL == 0 {
		cfg.ReplaceTimeoutLockTTL = 30 * time.Second
	}
	if cfg.GameAppSettleLockTTL == 0 {
		cfg.GameAppSettleLockTTL = 30 * time.Second
	}
}
