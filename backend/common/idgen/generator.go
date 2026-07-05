// Package idgen 提供雪花 ID 生成能力，基于 bwmarrin/snowflake 封装。
//
// 设计原则：
//   - 采用成熟框架 bwmarrin/snowflake，不自研
//   - 封装层 SnowflakeGenerator 补齐时钟回拨检测（bwmarrin 原生不处理）
//   - 业务代码依赖 IDGenerator 接口，不依赖具体类型
//   - 显式初始化（bootstrap 层调用 Init），禁止运行时懒加载
//   - nodeID 支持显式配置（单实例/测试）与 Redis 自动分配（多实例生产）
//
// 自定义纪元：2026-01-01 00:00:00 UTC（1735689600000）
// 位分配：timestamp(41) + nodeID(10) + sequence(12) = 63 位 + 1 符号位
// 理论可用年限：从 2026 年起约 69 年（到 2095 年）
package idgen

import (
	"errors"

	"github.com/bwmarrin/snowflake"
)

// IDGenerator 雪花 ID 生成器接口。
// 业务代码依赖此接口，不依赖具体 SnowflakeGenerator 类型，便于 mock 测试。
// 规约参考 CODING_STANDARD.md §20 SID-2。
type IDGenerator interface {
	// GenerateInt64 生成 int64 类型的雪花 ID。
	// 当时钟回拨超过 maxClockBackwardMs 时返回 ErrClockMovedBackwards，调用方 MUST 处理该错误。
	GenerateInt64() (int64, error)

	// GenerateString 生成字符串类型的雪花 ID（十进制表示）。
	// 当时钟回拨时返回 ErrClockMovedBackwards。
	GenerateString() (string, error)

	// GenerateID 生成 bwmarrin/snowflake.ID 类型。
	// 支持 JSON Marshal/Base32/58/64 编码、ID 反解析（Time/Node/Step）等特性。
	// 当时钟回拨时返回 ErrClockMovedBackwards。
	GenerateID() (snowflake.ID, error)

	// GetNodeID 返回生成器的 nodeID。
	GetNodeID() int64
}

// 雪花 ID 生成相关错误。规约参考 CODING_STANDARD.md §20 SID-3、SID-4。
var (
	// ErrClockMovedBackwards 时钟回拨，拒绝生成 ID 以避免重复。
	// 调用方应记录 Warn 日志并 retry，或返回错误给上游。
	ErrClockMovedBackwards = errors.New("clock moved backwards, refusing to generate id")

	// ErrNodeIDInvalid nodeID 超出合法范围 [0, 1023]。
	ErrNodeIDInvalid = errors.New("node_id must be in range [0, 1023]")

	// ErrGeneratorNotInitialized 生成器未初始化，必须先调用 Init。
	ErrGeneratorNotInitialized = errors.New("id generator not initialized, call Init first")
)
