package gateway

import (
	"fmt"
	"testing"
)

func TestGatewayConnKey(t *testing.T) {
	userID := "user123"
	expected := "cashparty:gateway:conn:user123"
	actual := GatewayConnKey(userID)

	if actual != expected {
		t.Errorf("GatewayConnKey() = %v, want %v", actual, expected)
	}
}

func TestGatewayKickKey(t *testing.T) {
	nodeID := "node1"
	expected := "cashparty:gateway:kick:node1"
	actual := GatewayKickKey(nodeID)

	if actual != expected {
		t.Errorf("GatewayKickKey() = %v, want %v", actual, expected)
	}
}

func TestPlayerRoomKey(t *testing.T) {
	userID := "user456"
	expected := "cashparty:player:room:user456"
	actual := PlayerRoomKey(userID)

	if actual != expected {
		t.Errorf("PlayerRoomKey() = %v, want %v", actual, expected)
	}
}

func TestRateLimitIPKey(t *testing.T) {
	ip := "192.168.1.1"
	expected := "cashparty:ratelimit:ip:192.168.1.1"
	actual := RateLimitIPKey(ip)

	if actual != expected {
		t.Errorf("RateLimitIPKey() = %v, want %v", actual, expected)
	}
}

func TestRateLimitUserKey(t *testing.T) {
	userID := "user789"
	expected := "cashparty:ratelimit:user:user789"
	actual := RateLimitUserKey(userID)

	if actual != expected {
		t.Errorf("RateLimitUserKey() = %v, want %v", actual, expected)
	}
}

func TestRateLimitGlobalKey(t *testing.T) {
	expected := "cashparty:ratelimit:global"
	actual := RateLimitGlobalKey()

	if actual != expected {
		t.Errorf("RateLimitGlobalKey() = %v, want %v", actual, expected)
	}
}

func TestRateLimitCmdKey(t *testing.T) {
	cmd := "grab"
	userID := "user123"
	expected := "cashparty:ratelimit:cmd:grab:user123"
	actual := RateLimitCmdKey(cmd, userID)

	if actual != expected {
		t.Errorf("RateLimitCmdKey() = %v, want %v", actual, expected)
	}
}

func TestGatewayLockedIPKey(t *testing.T) {
	ip := "192.168.1.100"
	expected := "cashparty:gateway:locked_ip:192.168.1.100"
	actual := GatewayLockedIPKey(ip)

	if actual != expected {
		t.Errorf("GatewayLockedIPKey() = %v, want %v", actual, expected)
	}
}

func TestRoomPlayersKey(t *testing.T) {
	roomID := "room123"
	expected := "cashparty:room:players:room123"
	actual := RoomPlayersKey(roomID)

	if actual != expected {
		t.Errorf("RoomPlayersKey() = %v, want %v", actual, expected)
	}
}

func TestRoomSpectatorsKey(t *testing.T) {
	roomID := "room456"
	expected := "cashparty:room:spectators:room456"
	actual := RoomSpectatorsKey(roomID)

	if actual != expected {
		t.Errorf("RoomSpectatorsKey() = %v, want %v", actual, expected)
	}
}

func TestKeyPrefix(t *testing.T) {
	keys := []string{
		KeyGatewayConn,
		KeyGatewayKick,
		KeyPlayerRoom,
		KeyRateLimitIP,
		KeyRateLimitUser,
		KeyRateLimitGlobal,
		KeyRateLimitCmd,
		KeyGatewayLockedIP,
		KeyRoomPlayers,
		KeyRoomSpectators,
	}

	for _, key := range keys {
		if len(key) < len(keyPrefix) || key[:len(keyPrefix)] != keyPrefix {
			t.Errorf("Key %s does not start with keyPrefix %s", key, keyPrefix)
		}
	}
}

func TestKeyFormat(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		format   string
		args     []interface{}
		expected string
	}{
		{
			name:     "single string param",
			key:      KeyGatewayConn,
			format:   "%s",
			args:     []interface{}{"user123"},
			expected: "cashparty:gateway:conn:user123",
		},
		{
			name:     "two string params",
			key:      KeyRateLimitCmd,
			format:   "%s:%s",
			args:     []interface{}{"grab", "user123"},
			expected: "cashparty:ratelimit:cmd:grab:user123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := fmt.Sprintf(tt.key, tt.args...)
			if actual != tt.expected {
				t.Errorf("Format key = %v, want %v", actual, tt.expected)
			}
		})
	}
}
