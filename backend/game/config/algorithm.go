package config

import (
	"time"

	commonconfig "github.com/cashparty/backend/common/config"
)

type AlgorithmConfig struct {
	MinPacketAmount     *int64   `mapstructure:"min_packet_amount" yaml:"min_packet_amount"`
	StraightProbability *float64 `mapstructure:"straight_probability" yaml:"straight_probability"`
	LeopardProbability  *float64 `mapstructure:"leopard_probability" yaml:"leopard_probability"`
	// PacketCacheTTL 红包结果缓存 TTL，nil 时使用默认值 1h。
	PacketCacheTTL *time.Duration       `mapstructure:"packet_cache_ttl" yaml:"packet_cache_ttl"`
	RewardControl  *RewardControlConfig `mapstructure:"reward_control" yaml:"reward_control"`
}

type RewardControlConfig struct {
	GlobalSwitchEnabled  bool     `mapstructure:"global_switch_enabled" yaml:"global_switch_enabled"`
	ProfitRatioThreshold *float64 `mapstructure:"profit_ratio_threshold" yaml:"profit_ratio_threshold"`
	// LeopardMultiplier 豹子奖励倍数，nil 时使用默认值 10。
	LeopardMultiplier *int64                      `mapstructure:"leopard_multiplier" yaml:"leopard_multiplier"`
	RoomConfigs       map[int64]*RoomRewardConfig `mapstructure:"room_configs" yaml:"room_configs"`
}

type RoomRewardConfig struct {
	GuaranteeEnabled    bool    `mapstructure:"guarantee_enabled" yaml:"guarantee_enabled"`
	GuaranteeStraight   bool    `mapstructure:"guarantee_straight" yaml:"guarantee_straight"`
	GuaranteeLeopard    bool    `mapstructure:"guarantee_leopard" yaml:"guarantee_leopard"`
	ProbabilityEnabled  bool    `mapstructure:"probability_enabled" yaml:"probability_enabled"`
	StraightProbability float64 `mapstructure:"straight_probability" yaml:"straight_probability"`
	LeopardProbability  float64 `mapstructure:"leopard_probability" yaml:"leopard_probability"`
}

func LoadAlgorithm(path string) (*AlgorithmConfig, error) {
	var cfg AlgorithmConfig
	if err := commonconfig.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadAlgorithmFromContent(content string) (*AlgorithmConfig, error) {
	var cfg AlgorithmConfig
	if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
