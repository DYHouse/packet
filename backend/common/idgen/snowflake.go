package idgen

import (
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	envNodeID = "NODE_ID"

	epoch          = int64(1704067200000)
	nodeIDBits     = uint(10)
	sequenceBits   = uint(12)
	nodeIDMax      = int64(-1 ^ (-1 << nodeIDBits))
	sequenceMask   = int64(-1 ^ (-1 << sequenceBits))
	nodeIDShift    = sequenceBits
	timestampShift = sequenceBits + nodeIDBits
)

type SnowflakeGenerator struct {
	mu        sync.Mutex
	nodeID    int64
	timestamp int64
	sequence  int64
}

func NewSnowflakeGenerator(nodeID int64) *SnowflakeGenerator {
	if nodeID < 0 || nodeID > nodeIDMax {
		nodeID = 1
	}
	return &SnowflakeGenerator{
		nodeID: nodeID,
	}
}

func getNodeIDFromEnv() int64 {
	envValue := os.Getenv(envNodeID)
	if envValue == "" {
		return 1
	}
	nodeID, err := strconv.ParseInt(envValue, 10, 64)
	if err != nil {
		return 1
	}
	if nodeID < 0 || nodeID > nodeIDMax {
		return 1
	}
	return nodeID
}

func GetNodeID() int64 {
	return getNodeIDFromEnv()
}

func GetNodeIDString() string {
	envValue := os.Getenv(envNodeID)
	if envValue != "" {
		return envValue
	}
	return "1"
}

func (g *SnowflakeGenerator) GenerateInt64() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()

	if now == g.timestamp {
		g.sequence = (g.sequence + 1) & sequenceMask
		if g.sequence == 0 {
			for now <= g.timestamp {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		g.sequence = 0
	}

	g.timestamp = now

	return ((now - epoch) << timestampShift) | (g.nodeID << nodeIDShift) | g.sequence
}

func (g *SnowflakeGenerator) GenerateString() string {
	id := g.GenerateInt64()
	return int64ToString(id)
}

func int64ToString(n int64) string {
	if n == 0 {
		return "0"
	}

	var negative bool
	if n < 0 {
		negative = true
		n = -n
	}

	var buf [20]byte
	i := len(buf)

	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}

	if negative {
		i--
		buf[i] = '-'
	}

	return string(buf[i:])
}

var defaultGenerator *SnowflakeGenerator
var once sync.Once

func Init(nodeID int64) {
	once.Do(func() {
		defaultGenerator = NewSnowflakeGenerator(nodeID)
	})
}

func InitFromEnv() {
	once.Do(func() {
		nodeID := getNodeIDFromEnv()
		defaultGenerator = NewSnowflakeGenerator(nodeID)
	})
}

func GetDefaultGenerator() *SnowflakeGenerator {
	if defaultGenerator == nil {
		InitFromEnv()
	}
	return defaultGenerator
}

func GenerateInt64() int64 {
	if defaultGenerator == nil {
		InitFromEnv()
	}
	return defaultGenerator.GenerateInt64()
}

func GenerateString() string {
	if defaultGenerator == nil {
		InitFromEnv()
	}
	return defaultGenerator.GenerateString()
}
