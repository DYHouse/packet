package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

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
