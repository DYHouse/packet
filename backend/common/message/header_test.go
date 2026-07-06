package message

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNewEventHeader 验证 NewEventHeader 工厂方法正确填充各字段,
// 并验证 TraceID 透传逻辑(空字符串保留为空)。
func TestNewEventHeader(t *testing.T) {
	// 携带 traceID 的场景:全部字段应被自动填充
	h := NewEventHeader("tr_test123")
	if h.EventID == "" {
		t.Fatal("EventID 不应为空")
	}
	if h.Timestamp <= 0 {
		t.Fatalf("Timestamp 应大于 0, 实际: %d", h.Timestamp)
	}
	if h.Version != EventHeaderVersion {
		t.Fatalf("Version 期望 %d, 实际: %d", EventHeaderVersion, h.Version)
	}
	if h.TraceID != "tr_test123" {
		t.Fatalf("TraceID 期望 tr_test123, 实际: %s", h.TraceID)
	}

	// traceID 为空的场景:TraceID 应保持空字符串(由 Publisher 后续注入)
	hEmpty := NewEventHeader("")
	if hEmpty.EventID == "" {
		t.Fatal("EventID 不应为空")
	}
	if hEmpty.Timestamp <= 0 {
		t.Fatalf("Timestamp 应大于 0, 实际: %d", hEmpty.Timestamp)
	}
	if hEmpty.Version != EventHeaderVersion {
		t.Fatalf("Version 期望 %d, 实际: %d", EventHeaderVersion, hEmpty.Version)
	}
	if hEmpty.TraceID != "" {
		t.Fatalf("TraceID 期望空字符串, 实际: %s", hEmpty.TraceID)
	}
}

// TestFillIfEmpty 验证零值 EventHeader 在调用 FillIfEmpty 后,
// EventID/Timestamp/Version 被补齐, 而 TraceID 保持空(Publisher 负责注入)。
func TestFillIfEmpty(t *testing.T) {
	var h EventHeader
	if !h.IsEmpty() {
		t.Fatal("零值 EventHeader 应为空")
	}

	h.FillIfEmpty()

	if h.EventID == "" {
		t.Fatal("FillIfEmpty 后 EventID 不应为空")
	}
	if h.Timestamp <= 0 {
		t.Fatalf("FillIfEmpty 后 Timestamp 应大于 0, 实际: %d", h.Timestamp)
	}
	if h.Version != EventHeaderVersion {
		t.Fatalf("FillIfEmpty 后 Version 期望 %d, 实际: %d", EventHeaderVersion, h.Version)
	}
	// TraceID 不由 FillIfEmpty 补齐, 应保持空
	if h.TraceID != "" {
		t.Fatalf("TraceID 应保持空字符串, 实际: %s", h.TraceID)
	}
}

// TestIsEmpty 验证零值 Header 返回 true, 任一字段非零值后返回 false。
func TestIsEmpty(t *testing.T) {
	// 零值场景
	var zero EventHeader
	if !zero.IsEmpty() {
		t.Fatal("零值 EventHeader 应判定为空")
	}

	// 设置 EventID 后应非空
	onlyID := EventHeader{EventID: "e1"}
	if onlyID.IsEmpty() {
		t.Fatal("设置 EventID 后应判定为非空")
	}

	// 设置 TraceID 后应非空
	onlyTrace := EventHeader{TraceID: "t1"}
	if onlyTrace.IsEmpty() {
		t.Fatal("设置 TraceID 后应判定为非空")
	}

	// 设置 Timestamp 后应非空
	onlyTs := EventHeader{Timestamp: 123}
	if onlyTs.IsEmpty() {
		t.Fatal("设置 Timestamp 后应判定为非空")
	}

	// 设置 Version 后应非空
	onlyVer := EventHeader{Version: 1}
	if onlyVer.IsEmpty() {
		t.Fatal("设置 Version 后应判定为非空")
	}
}

// TestEventHeaderJSONEmbed 验证嵌入 EventHeader 后字段被提升到顶层,
// JSON 序列化结果中 event_id/trace_id/timestamp/version 与 name 均为顶层键,
// 不存在嵌套的 "EventHeader" 键。
func TestEventHeaderJSONEmbed(t *testing.T) {
	// 定义嵌入 EventHeader 的测试结构
	type testMsg struct {
		EventHeader
		Name string `json:"name"`
	}

	msg := testMsg{
		EventHeader: NewEventHeader("tr_embed"),
		Name:        "hello",
	}

	bytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	// 检查原始 JSON 文本中不存在嵌套的 "EventHeader" 键
	raw := string(bytes)
	if strings.Contains(raw, "EventHeader") {
		t.Fatalf("JSON 不应包含嵌套的 EventHeader 键, 实际: %s", raw)
	}

	// 反序列化为 map 验证字段提升到顶层
	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	expectedKeys := []string{"event_id", "trace_id", "timestamp", "version", "name"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Fatalf("期望顶层键 %s 存在, 实际 map: %v", key, m)
		}
	}

	// 确保不存在嵌套的 EventHeader 键
	if _, ok := m["EventHeader"]; ok {
		t.Fatal("不应存在嵌套的 EventHeader 键")
	}

	// 验证 name 字段值正确
	if m["name"] != "hello" {
		t.Fatalf("name 期望 hello, 实际: %v", m["name"])
	}
}
