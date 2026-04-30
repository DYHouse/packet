package gateway

import "fmt"

const (
	keyPrefix = "cashparty"

	KeyGatewayConn = keyPrefix + ":gateway:conn:%s"
	KeyGatewayKick = keyPrefix + ":gateway:kick:%s"
	KeyPlayerRoom  = keyPrefix + ":player:room:%s"

	KeyRateLimitIP     = keyPrefix + ":ratelimit:ip:%s"
	KeyRateLimitUser   = keyPrefix + ":ratelimit:user:%s"
	KeyRateLimitGlobal = keyPrefix + ":ratelimit:global"
	KeyRateLimitCmd    = keyPrefix + ":ratelimit:cmd:%s:%s"

	KeyGatewayLockedIP = keyPrefix + ":gateway:locked_ip:%s"

	KeyRoomPlayers    = keyPrefix + ":room:players:%s"
	KeyRoomSpectators = keyPrefix + ":room:spectators:%s"
)

func GatewayConnKey(userID string) string {
	return fmt.Sprintf(KeyGatewayConn, userID)
}

func GatewayKickKey(nodeID string) string {
	return fmt.Sprintf(KeyGatewayKick, nodeID)
}

func PlayerRoomKey(userID string) string {
	return fmt.Sprintf(KeyPlayerRoom, userID)
}

func RateLimitIPKey(ip string) string {
	return fmt.Sprintf(KeyRateLimitIP, ip)
}

func RateLimitUserKey(userID string) string {
	return fmt.Sprintf(KeyRateLimitUser, userID)
}

func RateLimitGlobalKey() string {
	return KeyRateLimitGlobal
}

func RateLimitCmdKey(cmd, userID string) string {
	return fmt.Sprintf(KeyRateLimitCmd, cmd, userID)
}

func GatewayLockedIPKey(ip string) string {
	return fmt.Sprintf(KeyGatewayLockedIP, ip)
}

func RoomPlayersKey(roomID string) string {
	return fmt.Sprintf(KeyRoomPlayers, roomID)
}

func RoomSpectatorsKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSpectators, roomID)
}
