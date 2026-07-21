package application

import (
	"fmt"
	"math/rand"
	neturl "net/url"
)

// GetRandomAvatar 根据基础URL和头像数量随机返回一个头像URL。
// 头像随机属 UI 表现层，不涉及资金分配，使用 math/rand 即可。
func GetRandomAvatar(baseURL string, count int) string {
	if baseURL == "" || count <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d.png", baseURL, rand.Intn(count)+1)
}

// ValidateAvatarURL 校验头像 URL：长度、scheme、host 白名单。
// allowedHosts 为允许的 host 列表（如 ["opc.narrytech.cn"]），maxLen 为 URL 最大长度。
// 用于 update_avatar 命令服务端校验，防止 SSRF 与任意 URL 写入。
// 允许 http 与 https,兼容开发环境本地上传服务。
func ValidateAvatarURL(url string, allowedHosts []string, maxLen int) error {
	if len(url) == 0 {
		return fmt.Errorf("avatar url is empty")
	}
	if len(url) > maxLen {
		return fmt.Errorf("avatar url too long: %d > %d", len(url), maxLen)
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		return fmt.Errorf("invalid avatar url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("avatar url must be http(s), got scheme: %s", parsed.Scheme)
	}
	return nil
}
