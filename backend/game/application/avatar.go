package application

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// GetRandomAvatar 根据基础URL和头像数量随机返回一个头像URL
// 使用 crypto/rand 保证安全随机性（规约 §6.4）
func GetRandomAvatar(baseURL string, count int) string {
	if baseURL == "" || count <= 0 {
		return ""
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(count)))
	if err != nil {
		return fmt.Sprintf("%s/1.png", baseURL)
	}
	return fmt.Sprintf("%s/%d.png", baseURL, n.Int64()+1)
}
