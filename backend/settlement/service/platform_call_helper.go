package service

import (
	"context"
	"errors"

	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/trace"
)

// resolveTraceID 从 ctx 提取 TraceID，若不存在则生成新的 TraceID。
// 平台调用日志 trace_id 字段为 NOT NULL，故空值时必须生成兜底。
func resolveTraceID(ctx context.Context) string {
	if tid := trace.FromContext(ctx); tid != "" {
		return tid
	}
	return trace.Generate()
}

// resolveNodeID 获取当前实例节点 ID，失败时返回 0。
// 平台调用日志 node_id 字段允许默认值 0，故失败不阻断业务。
func resolveNodeID() int {
	if nid, err := idgen.GetNodeID(); err == nil {
		return int(nid)
	}
	return 0
}

// isTimeoutError 判断错误是否为 RPC 超时（context 超时）。
// 用于区分 platform 调用的 timeout 状态与明确失败状态。
func isTimeoutError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}
