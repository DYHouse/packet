package config

// defaultMinRoomFee 默认最小房费：500 分（5 元）
const defaultMinRoomFee int64 = 500

// RoomConfig 房间业务配置
type RoomConfig struct {
	// MinRoomFee 房间列表与自动匹配的最小房费（单位：分）。
	// 小于该房费的房间不会出现在房间列表中，自动匹配也不会把用户分配进去。
	// 未配置（0）或负数时取默认值 500（5 元）。
	MinRoomFee int64 `mapstructure:"min_room_fee" yaml:"min_room_fee"`
}

// SetRoomDefaults 设置房间配置默认值。MinRoomFee 未配置或非法时取默认值 500（5 元）。
func SetRoomDefaults(cfg *RoomConfig) {
	if cfg.MinRoomFee <= 0 {
		cfg.MinRoomFee = defaultMinRoomFee
	}
}
