package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	mathrand "math/rand"
	"net"
	"net/http"
	"strings"
	"time"
)

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

// CryptoRandPerm 返回 [0, n) 的随机置换切片，使用 crypto/rand 实现 Fisher-Yates 洗牌。
// 用于红包金额索引随机化等安全敏感场景（规约 §6.4）。
// 与 math/rand 的 rand.Perm 等价，但使用加密安全的随机源。
func CryptoRandPerm(n int) ([]int, error) {
	if n <= 0 {
		return nil, fmt.Errorf("n must be positive")
	}
	perm := make([]int, n)
	for i := 0; i < n; i++ {
		perm[i] = i
	}
	// Fisher-Yates shuffle with crypto/rand
	for i := n - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, fmt.Errorf("crypto rand perm failed: %w", err)
		}
		perm[i], perm[j.Int64()] = perm[j.Int64()], perm[i]
	}
	return perm, nil
}

// RandomDelay 返回 [min, max) 范围内的随机延迟。若 max <= min，则返回 min 不变。
// 用于机器人 AI 行为（延迟、跳过概率等），不涉及资金分配，因此使用 math/rand。
func RandomDelay(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(mathrand.Int63n(int64(max-min)))
}
