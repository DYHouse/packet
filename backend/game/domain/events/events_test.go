package events

import (
	"encoding/json"
	"testing"

	"github.com/cashparty/backend/common/message"
)

// TestRoomEventJSONCompat 验证 RoomEvent 嵌入 EventHeader 后,
// event_id/trace_id/timestamp/version/event_type/room_id/user_id/payload 均为顶层键。
func TestRoomEventJSONCompat(t *testing.T) {
	event := NewSpectatorJoinEvent("room1", "user1", "nick", "avatar")

	bytes, err := event.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON 失败: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	expectedKeys := []string{"event_id", "trace_id", "timestamp", "version", "event_type", "room_id", "user_id", "payload"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Fatalf("期望顶层键 %s 存在, 实际 map: %v", key, m)
		}
	}

	// 验证业务字段值正确
	if m["event_type"] != string(RoomEventSpectatorJoin) {
		t.Fatalf("event_type 期望 %s, 实际: %v", RoomEventSpectatorJoin, m["event_type"])
	}
	if m["event_type"] != "spectator_join" {
		t.Fatalf("event_type 期望 spectator_join, 实际: %v", m["event_type"])
	}
	if m["room_id"] != "room1" {
		t.Fatalf("room_id 期望 room1, 实际: %v", m["room_id"])
	}
}

// TestNewSpectatorJoinEvent 验证 NewSpectatorJoinEvent 工厂方法正确填充 Header 与业务字段。
func TestNewSpectatorJoinEvent(t *testing.T) {
	event := NewSpectatorJoinEvent("room1", "user1", "nick", "avatar")

	if event.EventID == "" {
		t.Fatal("EventID 不应为空")
	}
	if event.Timestamp <= 0 {
		t.Fatalf("Timestamp 应大于 0, 实际: %d", event.Timestamp)
	}
	// 通过嵌入的 EventHeader 访问 Version
	if event.EventHeader.Version != message.EventHeaderVersion {
		t.Fatalf("Version 期望 %d, 实际: %d", message.EventHeaderVersion, event.EventHeader.Version)
	}
	if event.EventType != RoomEventSpectatorJoin {
		t.Fatalf("EventType 期望 %s, 实际: %s", RoomEventSpectatorJoin, event.EventType)
	}
	if event.RoomID != "room1" {
		t.Fatalf("RoomID 期望 room1, 实际: %s", event.RoomID)
	}
	if event.UserID != "user1" {
		t.Fatalf("UserID 期望 user1, 实际: %s", event.UserID)
	}

	// 验证 Payload 已被设置并可反序列化
	var payload SpectatorJoinPayload
	if err := event.GetPayload(&payload); err != nil {
		t.Fatalf("GetPayload 失败: %v", err)
	}
	if payload.Nickname != "nick" {
		t.Fatalf("Payload.Nickname 期望 nick, 实际: %s", payload.Nickname)
	}
	if payload.Avatar != "avatar" {
		t.Fatalf("Payload.Avatar 期望 avatar, 实际: %s", payload.Avatar)
	}
}

// TestParseRoomEventForwardCompat 验证解析旧格式 JSON(无 version 字段)时,
// ParseRoomEvent 调用 FillIfEmpty 补齐 Version, 且已有字段被正确解析。
func TestParseRoomEventForwardCompat(t *testing.T) {
	// 旧格式 JSON: 缺少 version 字段
	raw := `{"event_id":"e1","event_type":"spectator_join","room_id":"r1","trace_id":"t1","timestamp":123,"payload":{}}`

	event, err := ParseRoomEvent([]byte(raw))
	if err != nil {
		t.Fatalf("ParseRoomEvent 返回错误: %v", err)
	}

	if event.EventID != "e1" {
		t.Fatalf("EventID 期望 e1, 实际: %s", event.EventID)
	}
	// version 缺失时 FillIfEmpty 应补齐为 EventHeaderVersion
	if event.Version != message.EventHeaderVersion {
		t.Fatalf("Version 期望 %d(FillIfEmpty 补齐), 实际: %d", message.EventHeaderVersion, event.Version)
	}
	if event.RoomID != "r1" {
		t.Fatalf("RoomID 期望 r1, 实际: %s", event.RoomID)
	}
	if event.EventType != RoomEventSpectatorJoin {
		t.Fatalf("EventType 期望 %s, 实际: %s", RoomEventSpectatorJoin, event.EventType)
	}
	if event.TraceID != "t1" {
		t.Fatalf("TraceID 期望 t1, 实际: %s", event.TraceID)
	}
	if event.Timestamp != 123 {
		t.Fatalf("Timestamp 期望 123, 实际: %d", event.Timestamp)
	}
}

// TestGameEventJSONCompat 验证 GameEvent 嵌入 EventHeader 后,
// 各元数据与业务字段均为顶层键, 且 Phase 4 重命名后 Payload 字段键为 "payload" 而非 "data"。
func TestGameEventJSONCompat(t *testing.T) {
	event := &GameEvent{
		EventHeader: message.NewEventHeader("tr_game"),
		EventType:   GameEventSessionStart,
		RoomID:      "r1",
		SessionID:   "s1",
		Payload:     json.RawMessage(`{}`),
	}

	bytes, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("反序列化为 map 失败: %v", err)
	}

	expectedKeys := []string{"event_id", "trace_id", "timestamp", "version", "event_type", "room_id", "session_id", "payload"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Fatalf("期望顶层键 %s 存在, 实际 map: %v", key, m)
		}
	}

	// Phase 4 重命名核心契约: 键应为 "payload", 不应存在 "data"
	if _, ok := m["payload"]; !ok {
		t.Fatal("期望顶层键 payload 存在")
	}
	if _, ok := m["data"]; ok {
		t.Fatalf("不应存在 data 键(Phase 4 已重命名为 payload), 实际 map: %v", m)
	}

	// 验证业务字段值正确
	if m["event_type"] != string(GameEventSessionStart) {
		t.Fatalf("event_type 期望 %s, 实际: %v", GameEventSessionStart, m["event_type"])
	}
	if m["room_id"] != "r1" {
		t.Fatalf("room_id 期望 r1, 实际: %v", m["room_id"])
	}
	if m["session_id"] != "s1" {
		t.Fatalf("session_id 期望 s1, 实际: %v", m["session_id"])
	}
}
