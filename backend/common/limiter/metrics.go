package limiter

import "sync/atomic"

// Metrics 限流指标收集,统计 allow/reject/error 计数
type Metrics struct {
	AllowCount  int64
	RejectCount int64
	ErrorCount  int64
}

// IncAllow 放行计数加 1
func (m *Metrics) IncAllow() { atomic.AddInt64(&m.AllowCount, 1) }

// IncReject 拒绝计数加 1
func (m *Metrics) IncReject() { atomic.AddInt64(&m.RejectCount, 1) }

// IncError 错误计数加 1
func (m *Metrics) IncError() { atomic.AddInt64(&m.ErrorCount, 1) }

// Snapshot 返回当前指标快照
func (m *Metrics) Snapshot() (allow, reject, errCount int64) {
	return atomic.LoadInt64(&m.AllowCount),
		atomic.LoadInt64(&m.RejectCount),
		atomic.LoadInt64(&m.ErrorCount)
}
