package message

import (
	"time"

	"github.com/google/uuid"
)

// EventHeaderVersion 是当前所有事件消息的 schema 版本。
// 当事件结构发生破坏性变更时递增此版本号,Consumer 端按 Version 路由处理。
// 替代原各事件类型独立定义的版本常量,统一为单一真相源。
const EventHeaderVersion = 1

// EventHeader 是所有事件消息(RoomEvent/GameEvent/BroadcastMessage/PushMessage)的共享元数据头。
// 各事件类型通过嵌入此结构获得统一的 EventID/TraceID/Timestamp/Version 字段,
// 避免字段重复定义,确保单一真相源。
// 嵌入后字段提升到外层结构,JSON 序列化/反序列化无嵌套层级。
type EventHeader struct {
	EventID   string `json:"event_id"`
	TraceID   string `json:"trace_id"`
	Timestamp int64  `json:"timestamp"` // Unix 毫秒
	Version   int    `json:"version"`
}

// NewEventHeader 创建 EventHeader 并自动填充 EventID/Timestamp/Version。
// traceID 为空时保留空字符串,由调用方(Publisher)负责从 context 注入或自动生成。
func NewEventHeader(traceID string) EventHeader {
	return EventHeader{
		EventID:   uuid.New().String(),
		TraceID:   traceID,
		Timestamp: time.Now().UnixMilli(),
		Version:   EventHeaderVersion,
	}
}

// FillIfEmpty 在字段为零值时自动填充。
// Publisher 在发送前调用此方法,确保 EventID/Timestamp/Version 一定有值。
// TraceID 不在此方法补齐,由 Publisher 从 context 注入。
func (h *EventHeader) FillIfEmpty() {
	if h.EventID == "" {
		h.EventID = uuid.New().String()
	}
	if h.Timestamp == 0 {
		h.Timestamp = time.Now().UnixMilli()
	}
	if h.Version == 0 {
		h.Version = EventHeaderVersion
	}
}

// IsEmpty 判断 Header 是否为零值(未初始化)。
func (h *EventHeader) IsEmpty() bool {
	return h.EventID == "" && h.TraceID == "" && h.Timestamp == 0 && h.Version == 0
}
