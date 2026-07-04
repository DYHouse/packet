// Package gateway 提供 gateway 层的核心功能。
// 本文件为 key 定义的兼容层（re-export），所有 key 常量与工厂函数的真正定义
// 已迁移到 common/rediskeys/keys.go（单一真相源），此处仅做 re-export 以兼容现有调用方。
// 规约参考 CODING_STANDARD.md §6.2 与 §16 SC-5。
package gateway

import (
	"github.com/cashparty/backend/common/rediskeys"
)

// ============================================================================
// 常量 re-export
// ============================================================================

const (
	KeyGatewayConn = rediskeys.KeyGatewayConn
	KeyGatewayKick = rediskeys.KeyGatewayKick
	KeyPlayerRoom  = rediskeys.KeyPlayerRoom

	KeyRateLimitIP     = rediskeys.KeyRateLimitIP
	KeyRateLimitUser   = rediskeys.KeyRateLimitUser
	KeyRateLimitGlobal = rediskeys.KeyRateLimitGlobal
	KeyRateLimitCmd    = rediskeys.KeyRateLimitCmd

	KeyGatewayLockedIP = rediskeys.KeyGatewayLockedIP

	KeyRoomPlayers    = rediskeys.KeyRoomPlayers
	KeyRoomSpectators = rediskeys.KeyRoomSpectators
)

// ============================================================================
// 工厂函数 re-export
// ============================================================================

func GatewayConnKey(userID string) string {
	return rediskeys.GatewayConnKey(userID)
}

func GatewayKickKey(nodeID string) string {
	return rediskeys.GatewayKickKey(nodeID)
}

func PlayerRoomKey(userID string) string {
	return rediskeys.PlayerRoomKey(userID)
}

func RateLimitIPKey(ip string) string {
	return rediskeys.RateLimitIPKey(ip)
}

func RateLimitUserKey(userID string) string {
	return rediskeys.RateLimitUserKey(userID)
}

func RateLimitGlobalKey() string {
	return rediskeys.RateLimitGlobalKey()
}

func RateLimitCmdKey(cmd, userID string) string {
	return rediskeys.RateLimitCmdKey(cmd, userID)
}

func GatewayLockedIPKey(ip string) string {
	return rediskeys.GatewayLockedIPKey(ip)
}

func RoomPlayersKey(roomID string) string {
	return rediskeys.RoomPlayersKey(roomID)
}

func RoomSpectatorsKey(roomID string) string {
	return rediskeys.RoomSpectatorsKey(roomID)
}
