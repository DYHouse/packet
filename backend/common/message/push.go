package message

import "encoding/json"

// PushMessage 是 gateway 层向 WS 客户端直连推送的消息格式。
//
// E2 方案改造说明:
//  1. 嵌入 EventHeader,补齐 EventID/TraceID/Version 字段(原 PushMessage 仅 Type/Data/Timestamp)
//  2. Data 字段类型由任意值改为 json.RawMessage,消除二次序列化(满足 MQ_REFACTOR_PLAN P1-11)
//  3. 保留 Type 字段名不变(JSON tag 仍为 "type",不改为 "event"),前端零改动
//  4. Timestamp 字段由 EventHeader 提供,删除 PushMessage 自身的 Timestamp 字段(避免重复)
//
// 与 BroadcastMessage 的关系:PushMessage 仍并行存在,用于 WS 直连推送出口;
// BroadcastMessage 用于 Redis Pub/Sub + Kafka 跨节点广播。两者通过 EventHeader 共享元数据。
type PushMessage struct {
	EventHeader                 // 嵌入共享 Header(补齐 event_id/trace_id/timestamp/version)
	Type        string          `json:"type"` // 保留原字段名,前端零改动
	Data        json.RawMessage `json:"data"` // 原为任意值类型,现改为 json.RawMessage
}

// NewPushMessageFromJSON 从已序列化的 JSON 数据创建 PushMessage。
// 当 data 已是 json.RawMessage 时优先使用此工厂,避免重复 marshal。
// 推荐调用方:gateway/broadcast/broadcast.go(其 msg.Data 已是 json.RawMessage)。
func NewPushMessageFromJSON(msgType string, data json.RawMessage) *PushMessage {
	return &PushMessage{
		EventHeader: NewEventHeader(""),
		Type:        msgType,
		Data:        data,
	}
}

// NewPushMessage 创建 PushMessage 并自动填充 EventHeader。
// 签名保持兼容:接受 interface{} 数据,内部转 json.RawMessage。
// 调用方代码不变(gateway/broadcast/broadcast.go、gateway/connection/manager.go)。
// 若 data 已是 json.RawMessage,直接使用(fast path,无重复 marshal)。
// 若 data marshal 失败,Data 保留 nil,序列化输出 {"data":null};调用方应避免传入无法序列化的数据。
func NewPushMessage(msgType string, data interface{}) *PushMessage {
	if v, ok := data.(json.RawMessage); ok {
		return NewPushMessageFromJSON(msgType, v)
	}
	var dataBytes json.RawMessage
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			dataBytes = b
		}
	}
	return NewPushMessageFromJSON(msgType, dataBytes)
}

// ToJSON 将 PushMessage 序列化为 JSON 字节(保持原方法签名)。
func (p *PushMessage) ToJSON() ([]byte, error) {
	// 兜底:EventHeader 字段为零值时补齐
	p.EventHeader.FillIfEmpty()
	return json.Marshal(p)
}

// ParsePushMessage 从 JSON 字节解析 PushMessage。
// 向前兼容:旧消息无 EventID/TraceID/Version 字段时用零值。
func ParsePushMessage(data []byte) (*PushMessage, error) {
	var msg PushMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
