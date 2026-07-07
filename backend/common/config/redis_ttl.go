package config

import "time"

// RedisTTLConfig Redis key TTL 配置(用于 Lua 脚本通过 ARGV 传入)
type RedisTTLConfig struct {
	RoomDataTTL      time.Duration `mapstructure:"room_data_ttl"      yaml:"room_data_ttl"      json:"room_data_ttl"`      // 房间数据(roomHash/spectators/players/seats/seatOwner)
	UserRoomTTL      time.Duration `mapstructure:"user_room_ttl"      yaml:"user_room_ttl"      json:"user_room_ttl"`      // user-room 映射
	QueueTTL         time.Duration `mapstructure:"queue_ttl"          yaml:"queue_ttl"          json:"queue_ttl"`          // 排队队列
	PenaltyRecordTTL time.Duration `mapstructure:"penalty_record_ttl" yaml:"penalty_record_ttl" json:"penalty_record_ttl"` // 惩罚记录
	PacketDataTTL    time.Duration `mapstructure:"packet_data_ttl"    yaml:"packet_data_ttl"    json:"packet_data_ttl"`    // 红包数据(packetInfo/available/userGrab)
	RoundStateTTL    time.Duration `mapstructure:"round_state_ttl"    yaml:"round_state_ttl"    json:"round_state_ttl"`    // 回合状态
	PenaltyCountTTL  time.Duration `mapstructure:"penalty_count_ttl"  yaml:"penalty_count_ttl"  json:"penalty_count_ttl"`  // 惩罚计数
}

// SetRedisTTLDefaults 设置 Redis TTL 默认值(与历史硬编码 86400s 一致)
func SetRedisTTLDefaults(cfg *RedisTTLConfig) {
	if cfg == nil {
		return
	}
	const defaultTTL = 86400 * time.Second // 24h
	if cfg.RoomDataTTL == 0 {
		cfg.RoomDataTTL = defaultTTL
	}
	if cfg.UserRoomTTL == 0 {
		cfg.UserRoomTTL = defaultTTL
	}
	if cfg.QueueTTL == 0 {
		cfg.QueueTTL = defaultTTL
	}
	if cfg.PenaltyRecordTTL == 0 {
		cfg.PenaltyRecordTTL = defaultTTL
	}
	if cfg.PacketDataTTL == 0 {
		cfg.PacketDataTTL = defaultTTL
	}
	if cfg.RoundStateTTL == 0 {
		cfg.RoundStateTTL = defaultTTL
	}
	if cfg.PenaltyCountTTL == 0 {
		cfg.PenaltyCountTTL = defaultTTL
	}
}
