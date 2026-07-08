package redis

import (
	"context"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// useEvalSHA 控制是否走 EVALSHA 路径，默认 true。
// 运行时可通过 SetUseEvalSHA 切换，实现秒级回退到 EVAL，无需重启服务。
var useEvalSHA atomic.Bool

func init() {
	useEvalSHA.Store(true)
}

// SetUseEvalSHA 运行时切换 EVALSHA / EVAL 路径。
// true：走 goredis.NewScript 的 SCRIPT LOAD + EVALSHA + NOSCRIPT fallback
// false：回退到 Client.Eval，每次发送整段脚本源码
func SetUseEvalSHA(enabled bool) {
	useEvalSHA.Store(enabled)
}

// UseEvalSHA 返回当前是否启用 EVALSHA 路径。
func UseEvalSHA() bool {
	return useEvalSHA.Load()
}

// Script 包装 go-redis 的 redis.Script，统一脚本调用入口。
// 自动处理 SCRIPT LOAD + EVALSHA + NOSCRIPT fallback（由 go-redis 内部完成）。
type Script struct {
	script *redis.Script
	name   string
	src    string
}

// NewScript 创建脚本实例。
// name 用于指标/日志标识；src 为 Lua 脚本源码。
func NewScript(name, src string) *Script {
	return &Script{script: redis.NewScript(src), name: name, src: src}
}

// Name 返回脚本名（用于指标/日志）。
func (s *Script) Name() string { return s.name }

// Src 返回脚本源码（回退路径需要）。
func (s *Script) Src() string { return s.src }

// Run 执行脚本。
// 当 useEvalSHA=true 时走 goredis.NewScript 的 EVALSHA + NOSCRIPT fallback 路径；
// 当 useEvalSHA=false 时回退到 Client.Eval（每次发送整段源码）。
// 两种路径返回相同的 *redis.Cmd，调用方无感知。
func (s *Script) Run(ctx context.Context, c RedisClient, keys []string, args ...interface{}) *redis.Cmd {
	if useEvalSHA.Load() {
		return s.script.Run(ctx, c.Raw(), keys, args...)
	}
	return c.Eval(ctx, s.src, keys, args...)
}
