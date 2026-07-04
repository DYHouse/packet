package scheduler

import (
	"sync"
	"time"
)

// SchedulerStats 是单个调度器的指标快照。
type SchedulerStats struct {
	Name         string
	ExecCount    int64
	ErrorCount   int64
	PanicCount   int64
	LastRunTime  time.Time
	LastDuration time.Duration
}

// Metrics 收集所有调度器的运行指标（基于 map+mu，无 prometheus 依赖）。
type Metrics struct {
	mu    sync.Mutex
	stats map[string]*SchedulerStats
}

// NewMetrics 创建指标收集器。
func NewMetrics() *Metrics {
	return &Metrics{
		stats: make(map[string]*SchedulerStats),
	}
}

// RecordExecution 记录一次执行及其耗时。
func (m *Metrics) RecordExecution(name string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stats[name]
	if !ok {
		s = &SchedulerStats{Name: name}
		m.stats[name] = s
	}
	s.ExecCount++
	s.LastRunTime = time.Now()
	s.LastDuration = duration
}

// RecordError 记录一次错误。
func (m *Metrics) RecordError(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stats[name]
	if !ok {
		s = &SchedulerStats{Name: name}
		m.stats[name] = s
	}
	s.ErrorCount++
}

// RecordPanic 记录一次 panic。
func (m *Metrics) RecordPanic(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stats[name]
	if !ok {
		s = &SchedulerStats{Name: name}
		m.stats[name] = s
	}
	s.PanicCount++
}

// Snapshot 返回所有调度器指标的快照副本。
func (m *Metrics) Snapshot() []SchedulerStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]SchedulerStats, 0, len(m.stats))
	for _, s := range m.stats {
		result = append(result, *s)
	}
	return result
}
