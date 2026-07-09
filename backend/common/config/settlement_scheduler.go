package config

import "time"

// SettlementSchedulerConfig configures the 6 settlement schedulers.
type SettlementSchedulerConfig struct {
	CreditRetry        SettlementSchedulerSubConfig `mapstructure:"credit_retry" yaml:"credit_retry"`
	SettlementCheck    SettlementSchedulerSubConfig `mapstructure:"settlement_check" yaml:"settlement_check"`
	RefundProcess      SettlementSchedulerSubConfig `mapstructure:"refund_process" yaml:"refund_process"`
	GameSettleRetry    SettlementSchedulerSubConfig `mapstructure:"game_settle_retry" yaml:"game_settle_retry"`
	GameSettleTimeout  SettlementSchedulerSubConfig `mapstructure:"game_settle_timeout" yaml:"game_settle_timeout"`
	VirtualBalanceSync SettlementSchedulerSubConfig `mapstructure:"virtual_balance_sync" yaml:"virtual_balance_sync"`
}

// SettlementSchedulerSubConfig 是每个调度器的子配置。
// 不同调度器按需使用 Interval/InitialDelay/LockTTL 之外的扩展字段，
// 未使用的字段保持零值即可。
type SettlementSchedulerSubConfig struct {
	Interval     time.Duration `mapstructure:"interval" yaml:"interval"`
	InitialDelay time.Duration `mapstructure:"initial_delay" yaml:"initial_delay"`
	LockTTL      int           `mapstructure:"lock_ttl" yaml:"lock_ttl"`
	// Limit 批量查询上限，用于 credit_retry / refund_process / game_settle_timeout。
	Limit int `mapstructure:"limit" yaml:"limit"`
	// TimeoutDuration 超时判定时长，用于 game_settle_timeout（默认 1h）。
	TimeoutDuration time.Duration `mapstructure:"timeout_duration" yaml:"timeout_duration"`
	// DeductedNotSettledLookback 已扣款未结算的回溯时长，用于 settlement_check（默认 5min）。
	DeductedNotSettledLookback time.Duration `mapstructure:"deducted_not_settled_lookback" yaml:"deducted_not_settled_lookback"`
	// FailedFirstRoundLookback 首回合扣款失败的回溯时长，用于 settlement_check（默认 10min）。
	FailedFirstRoundLookback time.Duration `mapstructure:"failed_first_round_lookback" yaml:"failed_first_round_lookback"`
	// Offset 分页偏移量，用于 refund_process（默认 0）。
	Offset int `mapstructure:"offset" yaml:"offset"`
}

func SetSettlementSchedulerDefaults(cfg *SettlementSchedulerConfig) {
	// CreditRetry: Interval=30s, InitialDelay=10s, LockTTL=60, Limit=100
	if cfg.CreditRetry.Interval == 0 {
		cfg.CreditRetry.Interval = 30 * time.Second
	}
	if cfg.CreditRetry.InitialDelay == 0 {
		cfg.CreditRetry.InitialDelay = 10 * time.Second
	}
	if cfg.CreditRetry.LockTTL == 0 {
		cfg.CreditRetry.LockTTL = 60
	}
	if cfg.CreditRetry.Limit == 0 {
		cfg.CreditRetry.Limit = 100
	}
	// SettlementCheck: Interval=5m, InitialDelay=1m, LockTTL=300, DeductedNotSettledLookback=5min, FailedFirstRoundLookback=10min
	if cfg.SettlementCheck.Interval == 0 {
		cfg.SettlementCheck.Interval = 5 * time.Minute
	}
	if cfg.SettlementCheck.InitialDelay == 0 {
		cfg.SettlementCheck.InitialDelay = time.Minute
	}
	if cfg.SettlementCheck.LockTTL == 0 {
		cfg.SettlementCheck.LockTTL = 300
	}
	if cfg.SettlementCheck.DeductedNotSettledLookback == 0 {
		cfg.SettlementCheck.DeductedNotSettledLookback = 5 * time.Minute
	}
	if cfg.SettlementCheck.FailedFirstRoundLookback == 0 {
		cfg.SettlementCheck.FailedFirstRoundLookback = 10 * time.Minute
	}
	// RefundProcess: Interval=1m, InitialDelay=30s, LockTTL=120, Limit=100, Offset=0
	if cfg.RefundProcess.Interval == 0 {
		cfg.RefundProcess.Interval = time.Minute
	}
	if cfg.RefundProcess.InitialDelay == 0 {
		cfg.RefundProcess.InitialDelay = 30 * time.Second
	}
	if cfg.RefundProcess.LockTTL == 0 {
		cfg.RefundProcess.LockTTL = 120
	}
	if cfg.RefundProcess.Limit == 0 {
		cfg.RefundProcess.Limit = 100
	}
	// Offset 默认 0，零值即正确值，无需额外设置
	// GameSettleRetry: Interval=30s, InitialDelay=15s, LockTTL=60
	if cfg.GameSettleRetry.Interval == 0 {
		cfg.GameSettleRetry.Interval = 30 * time.Second
	}
	if cfg.GameSettleRetry.InitialDelay == 0 {
		cfg.GameSettleRetry.InitialDelay = 15 * time.Second
	}
	if cfg.GameSettleRetry.LockTTL == 0 {
		cfg.GameSettleRetry.LockTTL = 60
	}
	// GameSettleTimeout: Interval=5m, InitialDelay=1m, LockTTL=300, TimeoutDuration=1h, Limit=100
	if cfg.GameSettleTimeout.Interval == 0 {
		cfg.GameSettleTimeout.Interval = 5 * time.Minute
	}
	if cfg.GameSettleTimeout.InitialDelay == 0 {
		cfg.GameSettleTimeout.InitialDelay = time.Minute
	}
	if cfg.GameSettleTimeout.LockTTL == 0 {
		cfg.GameSettleTimeout.LockTTL = 300
	}
	if cfg.GameSettleTimeout.TimeoutDuration == 0 {
		cfg.GameSettleTimeout.TimeoutDuration = time.Hour
	}
	if cfg.GameSettleTimeout.Limit == 0 {
		cfg.GameSettleTimeout.Limit = 100
	}
	// VirtualBalanceSync: Interval=30s, InitialDelay=0, LockTTL=35（interval.Seconds()+5）
	if cfg.VirtualBalanceSync.Interval == 0 {
		cfg.VirtualBalanceSync.Interval = 30 * time.Second
	}
	// InitialDelay 默认 0，零值即无初始延迟，无需额外设置
	if cfg.VirtualBalanceSync.LockTTL == 0 {
		cfg.VirtualBalanceSync.LockTTL = 35
	}
}

// LuaConfig Lua 脚本调用层配置。
// UseEvalSHA 为 nil 或 true 时走 EVALSHA 路径；显式设为 false 时回退到 EVAL。
type LuaConfig struct {
	UseEvalSHA *bool `mapstructure:"use_evalsha" yaml:"use_evalsha"`
}
