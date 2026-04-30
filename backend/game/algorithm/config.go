package algorithm

type Config struct {
	MinPacketAmount     int64   `yaml:"min_packet_amount"`
	StraightProbability float64 `yaml:"straight_probability"`
	LeopardProbability  float64 `yaml:"leopard_probability"`

	RewardControl *RewardControlConfig `yaml:"reward_control"`
}

type RewardControlConfig struct {
	GlobalSwitchEnabled  bool                        `yaml:"global_switch_enabled"`
	ProfitRatioThreshold float64                     `yaml:"profit_ratio_threshold"`
	RoomConfigs          map[int64]*RoomRewardConfig `yaml:"room_configs"`
}

type RoomRewardConfig struct {
	GuaranteeEnabled    bool    `yaml:"guarantee_enabled"`
	GuaranteeStraight   bool    `yaml:"guarantee_straight"`
	GuaranteeLeopard    bool    `yaml:"guarantee_leopard"`
	ProbabilityEnabled  bool    `yaml:"probability_enabled"`
	StraightProbability float64 `yaml:"straight_probability"`
	LeopardProbability  float64 `yaml:"leopard_probability"`
}

func DefaultConfig() *Config {
	return &Config{
		MinPacketAmount:     1,
		StraightProbability: 0.08,
		LeopardProbability:  0.002,
		RewardControl: &RewardControlConfig{
			GlobalSwitchEnabled:  false,
			ProfitRatioThreshold: 0.05,
			RoomConfigs:          make(map[int64]*RoomRewardConfig),
		},
	}
}
