package message

import (
	"encoding/json"
	"testing"
)

// TestPushMessageJSONCompat 验证 PushMessage 嵌入 EventHeader 后,
// type/data/timestamp/event_id/trace_id/version 均为顶层键,
// 且 data 为 JSON 对象(map), 而非 base64 字符串。
func TestPushMessageJSONCompat(t *testing.T) {
	msg := NewPushMessage("round_start", map[string]string{"room_id": "r1"})

	bytes, err := msg.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON 失败: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	expectedKeys := []string{"type", "data", "timestamp", "event_id", "trace_id", "version"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Fatalf("期望顶层键 %s 存在, 实际 map: %v", key, m)
		}
	}

	// 验证 type 字段值正确(E2 方案核心契约: 保留 type 而非 event)
	if m["type"] != "round_start" {
		t.Fatalf("type 期望 round_start, 实际: %v", m["type"])
	}

	// 验证 data 是 JSON 对象(map), 而非 base64 字符串
	dataVal, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data 应为 JSON 对象(map), 实际类型: %T, 值: %v", m["data"], m["data"])
	}
	if dataVal["room_id"] != "r1" {
		t.Fatalf("data[room_id] 期望 r1, 实际: %v", dataVal["room_id"])
	}
}

// TestNewPushMessageFromJSON 验证从已序列化 JSON 创建 PushMessage 时,
// Header 被自动填充且 Data 原样保留(无重复 marshal)。
func TestNewPushMessageFromJSON(t *testing.T) {
	input := json.RawMessage(`{"user_id":"u1"}`)
	msg := NewPushMessageFromJSON("kicked", input)

	if msg.Type != "kicked" {
		t.Fatalf("Type 期望 kicked, 实际: %s", msg.Type)
	}
	// Data 应等于输入的 json.RawMessage
	if string(msg.Data) != string(input) {
		t.Fatalf("Data 期望 %s, 实际: %s", string(input), string(msg.Data))
	}
	if msg.EventID == "" {
		t.Fatal("EventID 不应为空")
	}
	if msg.Timestamp <= 0 {
		t.Fatalf("Timestamp 应大于 0, 实际: %d", msg.Timestamp)
	}
	if msg.Version != EventHeaderVersion {
		t.Fatalf("Version 期望 %d, 实际: %d", EventHeaderVersion, msg.Version)
	}
}

// TestNewPushMessageWithRawMessage 验证当传入 json.RawMessage 时走 fast path,
// Data 字段等于输入字节(无重复 marshal)。
func TestNewPushMessageWithRawMessage(t *testing.T) {
	input := json.RawMessage(`{"a":1}`)
	msg := NewPushMessage("test", input)

	if msg.Type != "test" {
		t.Fatalf("Type 期望 test, 实际: %s", msg.Type)
	}
	// fast path: Data 应直接等于输入的 json.RawMessage 字节
	if string(msg.Data) != string(input) {
		t.Fatalf("Data 期望 %s, 实际: %s", string(input), string(msg.Data))
	}
}

// TestNewPushMessageWithStruct 验证当传入普通结构体时,
// Data 被正确序列化为可反序列化的 JSON。
func TestNewPushMessageWithStruct(t *testing.T) {
	msg := NewPushMessage("kicked", map[string]string{"user_id": "u1"})

	if msg.Type != "kicked" {
		t.Fatalf("Type 期望 kicked, 实际: %s", msg.Type)
	}

	// Data 应为合法 JSON, 反序列化后得到期望 map
	var dataMap map[string]string
	if err := json.Unmarshal(msg.Data, &dataMap); err != nil {
		t.Fatalf("Data 反序列化失败: %v", err)
	}
	if dataMap["user_id"] != "u1" {
		t.Fatalf("data[user_id] 期望 u1, 实际: %s", dataMap["user_id"])
	}
}

// TestParsePushMessageForwardCompat 验证解析旧格式 JSON(无 event_id/trace_id/version)时,
// 仍能成功解析并保留已有字段。
func TestParsePushMessageForwardCompat(t *testing.T) {
	// 旧格式 JSON: 仅 type/data/timestamp, 无 event_id/trace_id/version
	raw := `{"type":"kicked","data":{"user_id":"u1"},"timestamp":123}`

	msg, err := ParsePushMessage([]byte(raw))
	if err != nil {
		t.Fatalf("ParsePushMessage 返回错误: %v", err)
	}

	if msg.Type != "kicked" {
		t.Fatalf("Type 期望 kicked, 实际: %s", msg.Type)
	}
	if msg.Timestamp != 123 {
		t.Fatalf("Timestamp 期望 123, 实际: %d", msg.Timestamp)
	}
}

// TestPushMessageTypeFieldPreserved 验证 E2 方案核心契约:
// PushMessage 的 JSON 顶层键为 "type", 不存在 "event" 键。
func TestPushMessageTypeFieldPreserved(t *testing.T) {
	msg := NewPushMessage("round_start", nil)

	bytes, err := msg.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON 失败: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	// "type" 键必须存在
	if _, ok := m["type"]; !ok {
		t.Fatalf("期望顶层键 type 存在, 实际 map: %v", m)
	}
	// "event" 键不应存在(E2 方案核心契约)
	if _, ok := m["event"]; ok {
		t.Fatalf("不应存在 event 键, 实际 map: %v", m)
	}

	if m["type"] != "round_start" {
		t.Fatalf("type 期望 round_start, 实际: %v", m["type"])
	}
}
