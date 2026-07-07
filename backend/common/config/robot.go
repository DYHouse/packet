package config

import "time"

// RobotConfig 机器人配置
type RobotConfig struct {
	Enabled   bool                 `mapstructure:"enabled" yaml:"enabled"`
	Scheduler RobotSchedulerConfig `mapstructure:"scheduler" yaml:"scheduler"`
	Behavior  BehaviorConfig       `mapstructure:"behavior" yaml:"behavior"`
	Account   AccountConfig        `mapstructure:"account" yaml:"account"`
}

type RobotSchedulerConfig struct {
	ScanInterval       time.Duration `mapstructure:"scan_interval" yaml:"scan_interval"`
	MinRealPlayers     int           `mapstructure:"min_real_players" yaml:"min_real_players"`
	MaxRobotsPerRoom   int           `mapstructure:"max_robots_per_room" yaml:"max_robots_per_room"`
	RobotAssignLockTTL time.Duration `mapstructure:"robot_assign_lock_ttl" yaml:"robot_assign_lock_ttl"`
	RoomAssignLockTTL  time.Duration `mapstructure:"room_assign_lock_ttl" yaml:"room_assign_lock_ttl"`
	RecycleCooldown    time.Duration `mapstructure:"recycle_cooldown" yaml:"recycle_cooldown"`
	ReserveCount       int           `mapstructure:"reserve_count" yaml:"reserve_count"`
	ReserveRatioMax    float64       `mapstructure:"reserve_ratio_max" yaml:"reserve_ratio_max"`
}

type BehaviorConfig struct {
	SeatDelayMin      time.Duration `mapstructure:"seat_delay_min" yaml:"seat_delay_min"`
	SeatDelayMax      time.Duration `mapstructure:"seat_delay_max" yaml:"seat_delay_max"`
	ReadyDelayMin     time.Duration `mapstructure:"ready_delay_min" yaml:"ready_delay_min"`
	ReadyDelayMax     time.Duration `mapstructure:"ready_delay_max" yaml:"ready_delay_max"`
	GrabDelayMin      time.Duration `mapstructure:"grab_delay_min" yaml:"grab_delay_min"`
	GrabDelayMax      time.Duration `mapstructure:"grab_delay_max" yaml:"grab_delay_max"`
	GrabSkipProb      float64       `mapstructure:"grab_skip_prob" yaml:"grab_skip_prob"`
	SendDelayMin      time.Duration `mapstructure:"send_delay_min" yaml:"send_delay_min"`
	SendDelayMax      time.Duration `mapstructure:"send_delay_max" yaml:"send_delay_max"`
	LeaveAfterGameMin time.Duration `mapstructure:"leave_after_game_min" yaml:"leave_after_game_min"`
	LeaveAfterGameMax time.Duration `mapstructure:"leave_after_game_max" yaml:"leave_after_game_max"`
	ActionRetryMax    int           `mapstructure:"action_retry_max" yaml:"action_retry_max"`
	ActionRetryDelay  time.Duration `mapstructure:"action_retry_delay" yaml:"action_retry_delay"`
}

type AccountConfig struct {
	InitialBalanceMulti float64       `mapstructure:"initial_balance_multi" yaml:"initial_balance_multi"`
	LowBalanceThreshold float64       `mapstructure:"low_balance_threshold" yaml:"low_balance_threshold"`
	SyncInterval        time.Duration `mapstructure:"sync_interval" yaml:"sync_interval"`
}

func SetRobotDefaults(cfg *RobotConfig) {
	if cfg.Scheduler.ScanInterval == 0 {
		cfg.Scheduler.ScanInterval = 5 * time.Second
	}
	if cfg.Scheduler.MinRealPlayers == 0 {
		cfg.Scheduler.MinRealPlayers = 2
	}
	if cfg.Scheduler.MaxRobotsPerRoom == 0 {
		cfg.Scheduler.MaxRobotsPerRoom = 3
	}
	if cfg.Scheduler.RobotAssignLockTTL == 0 {
		cfg.Scheduler.RobotAssignLockTTL = 10 * time.Second
	}
	if cfg.Scheduler.RoomAssignLockTTL == 0 {
		cfg.Scheduler.RoomAssignLockTTL = 30 * time.Second
	}
	if cfg.Scheduler.RecycleCooldown == 0 {
		cfg.Scheduler.RecycleCooldown = 60 * time.Second
	}
	if cfg.Scheduler.ReserveCount == 0 {
		cfg.Scheduler.ReserveCount = 5
	}
	if cfg.Scheduler.ReserveRatioMax == 0 {
		cfg.Scheduler.ReserveRatioMax = 0.3
	}

	if cfg.Behavior.SeatDelayMin == 0 {
		cfg.Behavior.SeatDelayMin = 2 * time.Second
	}
	if cfg.Behavior.SeatDelayMax == 0 {
		cfg.Behavior.SeatDelayMax = 5 * time.Second
	}
	if cfg.Behavior.ReadyDelayMin == 0 {
		cfg.Behavior.ReadyDelayMin = 1 * time.Second
	}
	if cfg.Behavior.ReadyDelayMax == 0 {
		cfg.Behavior.ReadyDelayMax = 3 * time.Second
	}
	if cfg.Behavior.GrabDelayMin == 0 {
		cfg.Behavior.GrabDelayMin = 1 * time.Second
	}
	if cfg.Behavior.GrabDelayMax == 0 {
		cfg.Behavior.GrabDelayMax = 8 * time.Second
	}
	if cfg.Behavior.SendDelayMin == 0 {
		cfg.Behavior.SendDelayMin = 2 * time.Second
	}
	if cfg.Behavior.SendDelayMax == 0 {
		cfg.Behavior.SendDelayMax = 5 * time.Second
	}
	if cfg.Behavior.LeaveAfterGameMin == 0 {
		cfg.Behavior.LeaveAfterGameMin = 3 * time.Second
	}
	if cfg.Behavior.LeaveAfterGameMax == 0 {
		cfg.Behavior.LeaveAfterGameMax = 10 * time.Second
	}

	if cfg.Account.InitialBalanceMulti == 0 {
		cfg.Account.InitialBalanceMulti = 1.5
	}
	if cfg.Account.LowBalanceThreshold == 0 {
		cfg.Account.LowBalanceThreshold = 0.5
	}
	if cfg.Account.SyncInterval == 0 {
		cfg.Account.SyncInterval = 30 * time.Second
	}
}
