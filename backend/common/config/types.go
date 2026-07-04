package config

import "time"

type GatewayConfig struct {
	MaxConnections    int           `mapstructure:"max_connections" yaml:"max_connections"`
	SendQueueSize     int           `mapstructure:"send_queue_size" yaml:"send_queue_size"`
	ReadBufferSize    int           `mapstructure:"read_buffer_size" yaml:"read_buffer_size"`
	WriteBufferSize   int           `mapstructure:"write_buffer_size" yaml:"write_buffer_size"`
	MaxMessageSize    int64         `mapstructure:"max_message_size" yaml:"max_message_size"`
	HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval" yaml:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `mapstructure:"heartbeat_timeout" yaml:"heartbeat_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	ReconnectTimeout  time.Duration `mapstructure:"reconnect_timeout" yaml:"reconnect_timeout"`
	KickOldConnection bool          `mapstructure:"kick_old_connection" yaml:"kick_old_connection"`
	AllowedOrigins    []string      `mapstructure:"allowed_origins" yaml:"allowed_origins"`
	AdminAPIKey       string        `mapstructure:"admin_api_key" yaml:"admin_api_key"`
}

func SetGatewayDefaults(cfg *GatewayConfig) {
	if cfg.MaxConnections == 0 {
		cfg.MaxConnections = 50000
	}
	if cfg.SendQueueSize == 0 {
		cfg.SendQueueSize = 256
	}
	if cfg.ReadBufferSize == 0 {
		cfg.ReadBufferSize = 4096
	}
	if cfg.WriteBufferSize == 0 {
		cfg.WriteBufferSize = 4096
	}
	if cfg.MaxMessageSize == 0 {
		cfg.MaxMessageSize = 65536
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 90 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 60 * time.Second
	}
	if cfg.ReconnectTimeout == 0 {
		cfg.ReconnectTimeout = 60 * time.Second
	}
}

type TimeoutConfig struct {
	Seat          time.Duration `mapstructure:"seat" yaml:"seat"`
	Ready         time.Duration `mapstructure:"ready" yaml:"ready"`
	Grab          time.Duration `mapstructure:"grab" yaml:"grab"`
	Send          time.Duration `mapstructure:"send" yaml:"send"`
	Replace       time.Duration `mapstructure:"replace" yaml:"replace"`
	Robot         time.Duration `mapstructure:"robot" yaml:"robot"`
	CheckInterval time.Duration `mapstructure:"check_interval" yaml:"check_interval"`
}

func SetTimeoutDefaults(cfg *TimeoutConfig) {
	if cfg.Seat == 0 {
		cfg.Seat = 30 * time.Second
	}
	if cfg.Ready == 0 {
		cfg.Ready = 3 * time.Second
	}
	if cfg.Grab == 0 {
		cfg.Grab = 20 * time.Second
	}
	if cfg.Send == 0 {
		cfg.Send = 30 * time.Second
	}
	if cfg.Replace == 0 {
		cfg.Replace = 30 * time.Second
	}
	if cfg.Robot == 0 {
		cfg.Robot = 5 * time.Second
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 1 * time.Second
	}
}

type KafkaConfig struct {
	Enabled bool     `mapstructure:"enabled" yaml:"enabled"`
	Brokers []string `mapstructure:"brokers" yaml:"brokers"`
}

type BroadcastConfig struct {
	Mode     string               `mapstructure:"mode" yaml:"mode"`
	Kafka    BroadcastKafkaConfig `mapstructure:"kafka" yaml:"kafka"`
	RedisPub BroadcastRedisConfig `mapstructure:"redis_pubsub" yaml:"redis_pubsub"`
}

type BroadcastKafkaConfig struct {
	Topic string `mapstructure:"topic" yaml:"topic"`
}

type BroadcastRedisConfig struct {
	Channel string `mapstructure:"channel" yaml:"channel"`
}

type PlatformConfig struct {
	Provider       string        `mapstructure:"provider" yaml:"provider"`
	BaseURL        string        `mapstructure:"base_url" yaml:"base_url"`
	MerchantID     string        `mapstructure:"merchant_id" yaml:"merchant_id"`
	MerchantSecret string        `mapstructure:"merchant_secret" yaml:"merchant_secret"`
	GameID         int           `mapstructure:"game_id" yaml:"game_id"`
	GameCode       string        `mapstructure:"game_code" yaml:"game_code"`
	GameName       string        `mapstructure:"game_name" yaml:"game_name"`
	Currency       string        `mapstructure:"currency" yaml:"currency"`
	Timeout        time.Duration `mapstructure:"timeout" yaml:"timeout"`
	MaxRetries     int           `mapstructure:"max_retries" yaml:"max_retries"`
}

func SetPlatformDefaults(cfg *PlatformConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
}

type GameServiceConfig struct {
	Address        string        `mapstructure:"address" yaml:"address"`
	Timeout        time.Duration `mapstructure:"timeout" yaml:"timeout"`
	MaxRecvMsgSize int           `mapstructure:"max_recv_msg_size" yaml:"max_recv_msg_size"`
	MaxSendMsgSize int           `mapstructure:"max_send_msg_size" yaml:"max_send_msg_size"`
}

type IDGeneratorConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
}

type AvatarConfig struct {
	BaseURL      string `mapstructure:"base_url" yaml:"base_url"`
	DefaultCount int    `mapstructure:"default_count" yaml:"default_count"`
}

// SettlementSchedulerConfig configures the 5 settlement schedulers.
type SettlementSchedulerConfig struct {
	CreditRetry       SettlementSchedulerSubConfig `mapstructure:"credit_retry" yaml:"credit_retry"`
	SettlementCheck   SettlementSchedulerSubConfig `mapstructure:"settlement_check" yaml:"settlement_check"`
	RefundProcess     SettlementSchedulerSubConfig `mapstructure:"refund_process" yaml:"refund_process"`
	GameSettleRetry   SettlementSchedulerSubConfig `mapstructure:"game_settle_retry" yaml:"game_settle_retry"`
	GameSettleTimeout SettlementSchedulerSubConfig `mapstructure:"game_settle_timeout" yaml:"game_settle_timeout"`
}

// SettlementSchedulerSubConfig is the per-scheduler sub-config.
type SettlementSchedulerSubConfig struct {
	Interval     time.Duration `mapstructure:"interval" yaml:"interval"`
	InitialDelay time.Duration `mapstructure:"initial_delay" yaml:"initial_delay"`
	LockTTL      int           `mapstructure:"lock_ttl" yaml:"lock_ttl"`
}

func SetSettlementSchedulerDefaults(cfg *SettlementSchedulerConfig) {
	// CreditRetry: Interval=30s, InitialDelay=10s, LockTTL=60
	if cfg.CreditRetry.Interval == 0 {
		cfg.CreditRetry.Interval = 30 * time.Second
	}
	if cfg.CreditRetry.InitialDelay == 0 {
		cfg.CreditRetry.InitialDelay = 10 * time.Second
	}
	if cfg.CreditRetry.LockTTL == 0 {
		cfg.CreditRetry.LockTTL = 60
	}
	// SettlementCheck: Interval=5m, InitialDelay=1m, LockTTL=300
	if cfg.SettlementCheck.Interval == 0 {
		cfg.SettlementCheck.Interval = 5 * time.Minute
	}
	if cfg.SettlementCheck.InitialDelay == 0 {
		cfg.SettlementCheck.InitialDelay = time.Minute
	}
	if cfg.SettlementCheck.LockTTL == 0 {
		cfg.SettlementCheck.LockTTL = 300
	}
	// RefundProcess: Interval=1m, InitialDelay=30s, LockTTL=120
	if cfg.RefundProcess.Interval == 0 {
		cfg.RefundProcess.Interval = time.Minute
	}
	if cfg.RefundProcess.InitialDelay == 0 {
		cfg.RefundProcess.InitialDelay = 30 * time.Second
	}
	if cfg.RefundProcess.LockTTL == 0 {
		cfg.RefundProcess.LockTTL = 120
	}
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
	// GameSettleTimeout: Interval=5m, InitialDelay=1m, LockTTL=300
	if cfg.GameSettleTimeout.Interval == 0 {
		cfg.GameSettleTimeout.Interval = 5 * time.Minute
	}
	if cfg.GameSettleTimeout.InitialDelay == 0 {
		cfg.GameSettleTimeout.InitialDelay = time.Minute
	}
	if cfg.GameSettleTimeout.LockTTL == 0 {
		cfg.GameSettleTimeout.LockTTL = 300
	}
}

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

// LuaConfig Lua 脚本调用层配置。
// UseEvalSHA 为 nil 或 true 时走 EVALSHA 路径；显式设为 false 时回退到 EVAL。
type LuaConfig struct {
	UseEvalSHA *bool `mapstructure:"use_evalsha" yaml:"use_evalsha"`
}
