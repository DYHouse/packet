// Package trace 提供 TraceID 在 context 中的传播工具。
//
// TraceID 是一次业务请求/操作的端到端追踪标识，应当在请求最外层入口生成，
// 通过 context.Context 在同步调用链中透传，跨越异步边界（Kafka/Redis Pub/Sub）
// 时由消息发布方从 context 提取注入消息 envelope，消费方从消息恢复到新 context。
//
// 设计原则：
//   - 入口生成（Gateway/gRPC handler）
//   - Context 传播（不污染方法签名）
//   - 跨边界注入/恢复
//   - TraceID 与幂等键分离（参考 CODING_STANDARD §20.2 SID-9）
package trace

import (
	"context"
	"fmt"

	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/google/uuid"
)

// contextKey 私有 context key 类型，避免与其他包冲突。
type contextKey struct{}

// WithTraceID 在 context 中注入 traceID。
// 若 traceID 为空则自动生成，保证返回的 context 一定携带 TraceID。
// 调用方 SHOULD 使用返回的 ctx 替换原 ctx。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		traceID = Generate()
	}
	return context.WithValue(ctx, contextKey{}, traceID)
}

// FromContext 从 context 提取 TraceID。
// 若 context 中不存在 TraceID 返回空字符串，不 panic。
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKey{}).(string); ok {
		return v
	}
	return ""
}

// Generate 生成新的 TraceID。
// 使用雪花 ID（非确定性），符合 CODING_STANDARD §20.2 SID-9（事件 TraceID 允许使用雪花 ID，但 MUST 与幂等键区分）。
// 格式：tr_<snowflake>，前缀便于日志检索时识别。
// 极端情况（idgen 未初始化或时钟回拨）降级用 UUID，保证不阻塞业务。
func Generate() string {
	gen, err := idgen.GetGenerator()
	if err != nil {
		logger.Warn("idgen not initialized, fallback to uuid for traceID", "error", err)
		return "tr_fallback_" + uuid.New().String()
	}
	id, err := gen.GenerateInt64()
	if err != nil {
		logger.Warn("generate traceID failed, fallback to uuid", "error", err)
		return "tr_fallback_" + uuid.New().String()
	}
	return fmt.Sprintf("tr_%d", id)
}
