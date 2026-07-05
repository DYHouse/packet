package idgen

import (
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
)

// 自定义纪元：2026-01-01 00:00:00 UTC
// 项目 2026 年上线，纪元与上线时间对齐，ID 数值更小更易读。
// 理论可用年限从 2026 年起算约 69 年（到 2095 年）。
const (
	customEpoch        = int64(1735689600000) // 2026-01-01 00:00:00 UTC
	maxClockBackwardMs = int64(5)             // 时钟回拨最大容忍毫秒数（小幅回拨等待追上）
	nodeIDMax          = int64(1023)          // nodeID 最大值（10 位）
)

func init() {
	// 设置自定义纪元（必须在 NewNode 前设置）
	// 位分配 NodeBits=10, StepBits=12 与 bwmarrin 默认值一致，无需修改
	snowflake.Epoch = customEpoch
}

// SnowflakeGenerator 雪花 ID 生成器，封装 bwmarrin/snowflake。
// 位分配：timestamp(41) + nodeID(10) + sequence(12) = 63 位 + 1 符号位。
//
// 在 bwmarrin/snowflake 基础上补齐时钟回拨检测：
//   - 小幅回拨（≤5ms）：等待时钟追上
//   - 大幅回拨（>5ms）：返回 ErrClockMovedBackwards，拒绝生成 ID
//
// 序列号溢出（同毫秒生成 > 4096 个 ID）由 bwmarrin 库内置处理（阻塞等待下一毫秒）。
type SnowflakeGenerator struct {
	mu            sync.Mutex
	node          *snowflake.Node
	nodeID        int64
	lastTimestamp int64 // 上次生成 ID 的毫秒时间戳，用于时钟回拨检测
}

// NewSnowflakeGenerator 创建雪花 ID 生成器。
// nodeID 必须在 [0, 1023] 范围内，否则返回 ErrNodeIDInvalid。
func NewSnowflakeGenerator(nodeID int64) (*SnowflakeGenerator, error) {
	if nodeID < 0 || nodeID > nodeIDMax {
		return nil, ErrNodeIDInvalid
	}
	node, err := snowflake.NewNode(nodeID)
	if err != nil {
		return nil, err
	}
	return &SnowflakeGenerator{
		node:   node,
		nodeID: nodeID,
	}, nil
}

// GenerateInt64 生成 int64 类型的雪花 ID。
// 当时钟回拨超过 maxClockBackwardMs 时返回 ErrClockMovedBackwards。
// 当同毫秒序列号溢出时由 bwmarrin/snowflake 阻塞等待到下一毫秒。
//
// 调用方 MUST 检查 error（规约 SID-3）。
func (g *SnowflakeGenerator) GenerateInt64() (int64, error) {
	id, err := g.GenerateID()
	if err != nil {
		return 0, err
	}
	return int64(id), nil
}

// GenerateString 生成字符串类型的雪花 ID（十进制表示）。
// 调用方 MUST 检查 error（规约 SID-3）。
func (g *SnowflakeGenerator) GenerateString() (string, error) {
	id, err := g.GenerateID()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// GenerateID 生成 bwmarrin/snowflake.ID 类型。
// 支持 JSON Marshal、Base32/58/64 编码、ID 反解析（Time/Node/Step）等特性。
//
// 时钟回拨检测（bwmarrin/snowflake 不处理，封装层补齐）：
//   - now < lastTimestamp 时检测回拨
//   - 小幅回拨（diff ≤ 5ms）：sleep 等待追上
//   - 大幅回拨（diff > 5ms）：返回 ErrClockMovedBackwards
func (g *SnowflakeGenerator) GenerateID() (snowflake.ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()

	// 时钟回拨检测（bwmarrin/snowflake 不处理，封装层补齐）
	if now < g.lastTimestamp {
		diff := g.lastTimestamp - now
		if diff <= maxClockBackwardMs {
			// 小幅回拨，等待时钟追上
			time.Sleep(time.Duration(diff) * time.Millisecond)
			now = time.Now().UnixMilli()
		} else {
			// 大幅回拨，拒绝生成
			return 0, ErrClockMovedBackwards
		}
	}

	// 调用 bwmarrin/snowflake 生成 ID（库内部处理序列号溢出，阻塞等待下一毫秒）
	id := g.node.Generate()

	g.lastTimestamp = now
	return id, nil
}

// GetNodeID 返回生成器的 nodeID。
func (g *SnowflakeGenerator) GetNodeID() int64 {
	return g.nodeID
}
