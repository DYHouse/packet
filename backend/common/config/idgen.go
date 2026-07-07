package config

import "fmt"

// IDGeneratorConfig 雪花 ID 生成器配置。
// 规约参考 CODING_STANDARD.md §20 SID-CFG1~SID-CFG4。
type IDGeneratorConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// NodeID 节点 ID，范围 [0, 1023]。
	// 0 表示 Redis 自动分配（多实例生产环境推荐）。
	// >0 表示显式指定（单实例/测试环境，多实例部署时每个实例必须唯一）。
	NodeID int64 `mapstructure:"node_id" yaml:"node_id"`
}

// SetIDGeneratorDefaults 设置雪花 ID 生成器默认值。
// 规约 SID-CFG1：Enabled=true 时 NodeID MUST 在 [0, 1023] 范围内。
// 规约 SID-CFG2：超出范围时 panic（fail-fast）。
// 默认 Enabled=true、NodeID=1（单实例/测试环境兼容值）。
// 多实例生产环境 MUST 通过 nacos 配置 node_id=0 触发 Redis 自动分配。
func SetIDGeneratorDefaults(cfg *IDGeneratorConfig) {
	if !cfg.Enabled {
		cfg.Enabled = true
	}
	if cfg.NodeID == 0 {
		// 0 是合法值，表示 Redis 自动分配，保持不变
		return
	}
	// 校验 NodeID 范围 [1, 1023]（0 已在上面处理）
	// 负数或 >1023 视为配置错误，fail-fast
	if cfg.NodeID < 0 || cfg.NodeID > 1023 {
		panic(fmt.Sprintf("invalid node_id %d: must be in range [0, 1023]", cfg.NodeID))
	}
}

// AvatarConfig 头像配置
type AvatarConfig struct {
	BaseURL      string `mapstructure:"base_url" yaml:"base_url"`
	DefaultCount int    `mapstructure:"default_count" yaml:"default_count"`
}
