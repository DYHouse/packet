package application

import (
	"fmt"
	"math/rand"
)

// GetRandomAvatar 根据基础URL和头像数量随机返回一个头像URL。
// 头像随机属 UI 表现层，不涉及资金分配，使用 math/rand 即可。
func GetRandomAvatar(baseURL string, count int) string {
	if baseURL == "" || count <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d.png", baseURL, rand.Intn(count)+1)
}
