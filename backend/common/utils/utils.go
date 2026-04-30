package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"
)

// GenerateUUID 生成UUID
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GenerateConnID 生成连接ID
func GenerateConnID() string {
	timestamp := time.Now().UnixMilli()
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("conn_%d%06d", timestamp, n.Int64())
}

// GenerateRoomID 生成房间ID
func GenerateRoomID(roomType int) int64 {
	timestamp := time.Now().UnixMilli()
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	return timestamp*1000000 + int64(roomType)*10000 + n.Int64()
}

// GenerateOrderNo 生成订单号
func GenerateOrderNo(prefix string) string {
	timestamp := time.Now().UnixMilli()
	n, _ := rand.Int(rand.Reader, big.NewInt(100000))
	return fmt.Sprintf("%s_%d_%05d", prefix, timestamp, n.Int64())
}

// GetClientIP 从HTTP请求中获取客户端IP
func GetClientIP(r *http.Request) string {
	// 优先从X-Forwarded-For获取
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			ip := strings.TrimSpace(ips[0])
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
	}

	// 从X-Real-IP获取
	xri := r.Header.Get("X-Real-IP")
	if xri != "" && net.ParseIP(xri) != nil {
		return xri
	}

	// 从RemoteAddr获取
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// IsValidPlatform 校验平台类型
func IsValidPlatform(platform string) bool {
	switch platform {
	case "web", "h5", "app":
		return true
	default:
		return false
	}
}

// NowMillis 当前时间戳（毫秒）
func NowMillis() int64 {
	return time.Now().UnixMilli()
}

// ContainsInt64 检查slice是否包含某个值
func ContainsInt64(slice []int64, val int64) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// ContainsString 检查slice是否包含某个字符串
func ContainsString(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// ContainsInt 检查slice是否包含某个int
func ContainsInt(slice []int, val int) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// MinInt64 返回两个int64中较小的
func MinInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// RandomInt64 生成指定范围内的随机int64
func RandomInt64(max int64) int64 {
	if max <= 0 {
		return 0
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(max))
	return n.Int64()
}

// ShuffleInt64 打乱int64切片顺序
func ShuffleInt64(slice []int64) {
	for i := len(slice) - 1; i > 0; i-- {
		j := int(RandomInt64(int64(i + 1)))
		slice[i], slice[j] = slice[j], slice[i]
	}
}

