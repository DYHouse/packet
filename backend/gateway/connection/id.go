package connection

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// GenerateConnID 生成连接ID
func GenerateConnID() string {
	timestamp := time.Now().UnixMilli()
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("conn_%d%06d", timestamp, n.Int64())
}
