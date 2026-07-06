package message

import (
	"encoding/json"
	"testing"
)

// TestBroadcastMessageJSONCompat 验证 BroadcastMessage 嵌入 EventHeader 后,
// 所有元数据字段与业务字段在 JSON 中均为顶层键。
func TestBroadcastMessageJSONCompat(t *testing.T) {
	msg := &BroadcastMessage{
		EventHeader: NewEventHeader("tr_bc"),
		Event:       "round_start",
		Data:        json.RawMessage(`{"room_id":"r1"}`),
		TargetType:  "room",
	}

	bytes, err := msg.Marshal()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	expectedKeys := []string{"event_id", "trace_id", "timestamp", "version", "event", "data", "target_type"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Fatalf("期望顶层键 %s 存在, 实际 map: %v", key, m)
		}
	}

	// 验证业务字段值正确
	if m["event"] != "round_start" {
		t.Fatalf("event 期望 round_start, 实际: %v", m["event"])
	}
	if m["target_type"] != "room" {
		t.Fatalf("target_type 期望 room, 实际: %v", m["target_type"])
	}
}

// TestNewBroadcastMessage 验证 NewBroadcastMessage 工厂方法正确填充 Header 与业务字段。
func TestNewBroadcastMessage(t *testing.T) {
	msg, err := NewBroadcastMessage("test_event", map[string]string{"k": "v"}, "room", "r1", "")
	if err != nil {
		t.Fatalf("NewBroadcastMessage 返回错误: %v", err)
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
	if msg.Event != "test_event" {
		t.Fatalf("Event 期望 test_event, 实际: %s", msg.Event)
	}
	if msg.TargetType != "room" {
		t.Fatalf("TargetType 期望 room, 实际: %s", msg.TargetType)
	}
	if msg.TargetID != "r1" {
		t.Fatalf("TargetID 期望 r1, 实际: %s", msg.TargetID)
	}
	if len(msg.Data) == 0 {
		t.Fatal("Data 不应为空 json.RawMessage")
	}

	// 验证 Data 可反序列化为原始 map
	var dataMap map[string]string
	if err := json.Unmarshal(msg.Data, &dataMap); err != nil {
		t.Fatalf("Data 反序列化失败: %v", err)
	}
	if dataMap["k"] != "v" {
		t.Fatalf("Data[k] 期望 v, 实际: %s", dataMap["k"])
	}
}

// TestParseBroadcastMessageForwardCompat 验证解析旧格式 JSON(无 version 字段)时,
// 各字段仍能被正确解析, 不报错。
func TestParseBroadcastMessageForwardCompat(t *testing.T) {
	// 旧格式 JSON: 缺少 version 字段
	raw := `{"event_id":"e1","trace_id":"t1","timestamp":123,"event":"test","data":{},"target_type":"room"}`

	msg, err := ParseBroadcastMessage([]byte(raw))
	if err != nil {
		t.Fatalf("ParseBroadcastMessage 返回错误: %v", err)
	}

	if msg.EventID != "e1" {
		t.Fatalf("EventID 期望 e1, 实际: %s", msg.EventID)
	}
	if msg.TraceID != "t1" {
		t.Fatalf("TraceID 期望 t1, 实际: %s", msg.TraceID)
	}
	if msg.Timestamp != 123 {
		t.Fatalf("Timestamp 期望 123, 实际: %d", msg.Timestamp)
	}
	if msg.Event != "test" {
		t.Fatalf("Event 期望 test, 实际: %s", msg.Event)
	}
	if msg.TargetType != "room" {
		t.Fatalf("TargetType 期望 room, 实际: %s", msg.TargetType)
	}
}
