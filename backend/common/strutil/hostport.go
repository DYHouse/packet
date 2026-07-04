package strutil

import (
	"net"
	"strconv"
)

// JoinHostPort 拼接 host:port，IPv6 安全（自动加方括号）。
// 包装 net.JoinHostPort，统一项目内 host:port 构建入口，禁止使用 fmt.Sprintf("%s:%d", ...)。
// 规约参考 CODING_STANDARD.md §16 SC-3。
// 例：
//
//	JoinHostPort("127.0.0.1", 8080) -> "127.0.0.1:8080"
//	JoinHostPort("::1", 8080) -> "[::1]:8080"
func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
