package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Gateway     GatewayConfig     `mapstructure:"gateway"`
	Timeout     TimeoutConfig     `mapstructure:"timeout"`
	Redis       RedisConfig       `mapstructure:"redis"`
	MySQL       MySQLConfig       `mapstructure:"mysql"`
	Kafka       KafkaConfig       `mapstructure:"kafka"`
	Broadcast   BroadcastConfig   `mapstructure:"broadcast"`
	Platform    PlatformConfig    `mapstructure:"platform"`
	Log         LogConfig         `mapstructure:"log"`
	GameService GameServiceConfig `mapstructure:"game_service"`
	Nacos       NacosConfig       `mapstructure:"nacos"`
	Algorithm   AlgorithmConfig   `mapstructure:"algorithm"`
	IDGenerator IDGeneratorConfig `mapstructure:"id_generator"`
	Avatar      AvatarConfig      `mapstructure:"avatar"`
	Robot       RobotConfig       `mapstructure:"robot"`
	Lua         LuaConfig         `mapstructure:"lua"`
}

// LuaConfig Lua 脚本调用层配置。
// UseEvalSHA 为 nil 或 true 时走 EVALSHA 路径；显式设为 false 时回退到 EVAL。
type LuaConfig struct {
	UseEvalSHA *bool `mapstructure:"use_evalsha"`
}

type RobotConfig struct {
	Enabled   bool            `mapstructure:"enabled"`
	Scheduler SchedulerConfig `mapstructure:"scheduler"`
	Behavior  BehaviorConfig  `mapstructure:"behavior"`
	Account   AccountConfig   `mapstructure:"account"`
}

type SchedulerConfig struct {
	ScanInterval       time.Duration `mapstructure:"scan_interval"`
	MinRealPlayers     int           `mapstructure:"min_real_players"`
	MaxRobotsPerRoom   int           `mapstructure:"max_robots_per_room"`
	RobotAssignLockTTL time.Duration `mapstructure:"robot_assign_lock_ttl"`
	RoomAssignLockTTL  time.Duration `mapstructure:"room_assign_lock_ttl"`
	RecycleCooldown    time.Duration `mapstructure:"recycle_cooldown"`
	ReserveCount       int           `mapstructure:"reserve_count"`
	ReserveRatioMax    float64       `mapstructure:"reserve_ratio_max"`
}

type BehaviorConfig struct {
	SeatDelayMin      time.Duration `mapstructure:"seat_delay_min"`
	SeatDelayMax      time.Duration `mapstructure:"seat_delay_max"`
	ReadyDelayMin     time.Duration `mapstructure:"ready_delay_min"`
	ReadyDelayMax     time.Duration `mapstructure:"ready_delay_max"`
	GrabDelayMin      time.Duration `mapstructure:"grab_delay_min"`
	GrabDelayMax      time.Duration `mapstructure:"grab_delay_max"`
	GrabSkipProb      float64       `mapstructure:"grab_skip_prob"`
	SendDelayMin      time.Duration `mapstructure:"send_delay_min"`
	SendDelayMax      time.Duration `mapstructure:"send_delay_max"`
	LeaveAfterGameMin time.Duration `mapstructure:"leave_after_game_min"`
	LeaveAfterGameMax time.Duration `mapstructure:"leave_after_game_max"`
	ActionRetryMax    int           `mapstructure:"action_retry_max"`
	ActionRetryDelay  time.Duration `mapstructure:"action_retry_delay"`
}

type AccountConfig struct {
	InitialBalanceMulti float64       `mapstructure:"initial_balance_multi"`
	LowBalanceThreshold float64       `mapstructure:"low_balance_threshold"`
	SyncInterval        time.Duration `mapstructure:"sync_interval"`
}

type ServerConfig struct {
	Name     string `mapstructure:"name"`
	GRPCPort int    `mapstructure:"grpc_port"`
	HTTPPort int    `mapstructure:"http_port"`
	WSPort   int    `mapstructure:"ws_port"`
	Mode     string `mapstructure:"mode"` // debug / release
}

func (c *ServerConfig) HTTPAddr() string {
	return fmt.Sprintf(":%d", c.HTTPPort)
}

type GatewayConfig struct {
	MaxConnections    int           `mapstructure:"max_connections"`
	SendQueueSize     int           `mapstructure:"send_queue_size"`
	ReadBufferSize    int           `mapstructure:"read_buffer_size"`
	WriteBufferSize   int           `mapstructure:"write_buffer_size"`
	MaxMessageSize    int64         `mapstructure:"max_message_size"`
	HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `mapstructure:"heartbeat_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout"`
	ReconnectTimeout  time.Duration `mapstructure:"reconnect_timeout"`
	KickOldConnection bool          `mapstructure:"kick_old_connection"`
	AllowedOrigins    []string      `mapstructure:"allowed_origins"`
	AdminAPIKey       string        `mapstructure:"admin_api_key"`
}

type TimeoutConfig struct {
	Seat    time.Duration `mapstructure:"seat"`
	Ready   time.Duration `mapstructure:"ready"`
	Grab    time.Duration `mapstructure:"grab"`
	Send    time.Duration `mapstructure:"send"`
	Replace time.Duration `mapstructure:"replace"`
}

type RedisConfig struct {
	Addr         string        `mapstructure:"addr"`
	Password     string        `mapstructure:"password"`
	DB           int           `mapstructure:"db"`
	PoolSize     int           `mapstructure:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	UseEvalSHA   *bool         `mapstructure:"use_evalsha"`

	Mode          string   `mapstructure:"mode"`
	MasterName    string   `mapstructure:"master_name"`
	SentinelAddrs []string `mapstructure:"sentinel_addrs"`
}

type MySQLConfig struct {
	DSN             string `mapstructure:"dsn"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

type KafkaConfig struct {
	Brokers []string `mapstructure:"brokers"`
}

type BroadcastConfig struct {
	Mode     string               `mapstructure:"mode"`
	Kafka    BroadcastKafkaConfig `mapstructure:"kafka"`
	RedisPub BroadcastRedisConfig `mapstructure:"redis_pubsub"`
}

type BroadcastKafkaConfig struct {
	Topic string `mapstructure:"topic"`
}

type BroadcastRedisConfig struct {
	Channel string `mapstructure:"channel"`
}

type PlatformConfig struct {
	Provider       string        `mapstructure:"provider"`
	BaseURL        string        `mapstructure:"base_url"`
	MerchantID     string        `mapstructure:"merchant_id"`
	MerchantSecret string        `mapstructure:"merchant_secret"`
	GameID         int           `mapstructure:"game_id"`
	GameCode       string        `mapstructure:"game_code"`
	GameName       string        `mapstructure:"game_name"`
	Currency       string        `mapstructure:"currency"`
	Timeout        time.Duration `mapstructure:"timeout"`
	MaxRetries     int           `mapstructure:"max_retries"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}

type GameServiceConfig struct {
	Address        string        `mapstructure:"address"`
	Timeout        time.Duration `mapstructure:"timeout"`
	MaxRecvMsgSize int           `mapstructure:"max_recv_msg_size"`
	MaxSendMsgSize int           `mapstructure:"max_send_msg_size"`
}

type NacosConfig struct {
	ServerAddr      string `mapstructure:"server_addr"`
	Namespace       string `mapstructure:"namespace"`
	Group           string `mapstructure:"group"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	ServiceName     string `mapstructure:"service_name"`
	ServiceAddr     string `mapstructure:"service_addr"`
	ServicePort     uint64 `mapstructure:"service_port"`
	ConfigDataID    string `mapstructure:"config_data_id"`
	ConfigGroup     string `mapstructure:"config_group"`
	AlgorithmDataID string `mapstructure:"algorithm_data_id"`
	AlgorithmGroup  string `mapstructure:"algorithm_group"`
	Enabled         bool   `mapstructure:"enabled"`
}

type AlgorithmConfig struct {
	MinPacketAmount     *int64               `mapstructure:"min_packet_amount"`
	StraightProbability *float64             `mapstructure:"straight_probability"`
	LeopardProbability  *float64             `mapstructure:"leopard_probability"`
	RewardControl       *RewardControlConfig `mapstructure:"reward_control"`
}

type RewardControlConfig struct {
	GlobalSwitchEnabled  bool                        `mapstructure:"global_switch_enabled"`
	ProfitRatioThreshold *float64                    `mapstructure:"profit_ratio_threshold"`
	RoomConfigs          map[int64]*RoomRewardConfig `mapstructure:"room_configs"`
}

type RoomRewardConfig struct {
	GuaranteeEnabled    bool    `mapstructure:"guarantee_enabled"`
	GuaranteeStraight   bool    `mapstructure:"guarantee_straight"`
	GuaranteeLeopard    bool    `mapstructure:"guarantee_leopard"`
	ProbabilityEnabled  bool    `mapstructure:"probability_enabled"`
	StraightProbability float64 `mapstructure:"straight_probability"`
	LeopardProbability  float64 `mapstructure:"leopard_probability"`
}

type IDGeneratorConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

type AvatarConfig struct {
	BaseURL      string `mapstructure:"base_url"`
	DefaultCount int    `mapstructure:"default_count"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	configDir := filepath.Dir(configPath)
	algorithmConfigPath := filepath.Join(configDir, "algorithm.yaml")
	if err := loadAlgorithmConfig(algorithmConfigPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load algorithm config: %w", err)
	}

	setDefaults(&cfg)
	return &cfg, nil
}

func loadAlgorithmConfig(path string, cfg *Config) error {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil
	}

	var algoCfg AlgorithmConfig
	if err := v.Unmarshal(&algoCfg); err != nil {
		return fmt.Errorf("failed to unmarshal algorithm config: %w", err)
	}

	cfg.Algorithm = algoCfg
	return nil
}

func LoadFromContent(content string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.AutomaticEnv()

	if err := v.ReadConfig(strings.NewReader(content)); err != nil {
		return nil, fmt.Errorf("failed to read config content: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	setDefaults(&cfg)
	return &cfg, nil
}

func LoadAlgorithmFromContent(content string) (*AlgorithmConfig, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	if err := v.ReadConfig(strings.NewReader(content)); err != nil {
		return nil, fmt.Errorf("failed to read algorithm config content: %w", err)
	}

	var cfg AlgorithmConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal algorithm config: %w", err)
	}

	return &cfg, nil
}
