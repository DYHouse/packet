package service

import (
	"strings"
	"testing"

	"github.com/bwmarrin/snowflake"
	// 注：此处 import bwmarrin/snowflake 是 SID-2 的已记录例外。
	// IDGenerator.GenerateID() 返回 snowflake.ID 类型（接口契约），测试桩必须实现该方法，
	// 因此必须 import 该包。生产代码禁止 import 此包（规约 SID-2）。
)

// stubIDGenerator 测试用的 IDGenerator 桩件，返回固定 ID。
type stubIDGenerator struct {
	id     snowflake.ID
	nodeID int64
}

func (s *stubIDGenerator) GenerateInt64() (int64, error) {
	return int64(s.id), nil
}

func (s *stubIDGenerator) GenerateString() (string, error) {
	return s.id.String(), nil
}

func (s *stubIDGenerator) GenerateID() (snowflake.ID, error) {
	return s.id, nil
}

func (s *stubIDGenerator) GetNodeID() int64 {
	return s.nodeID
}

// TestTraceIDGenerator_GenerateRoundTraceID 测试确定性方法的幂等性。
// 规约 SID-T6：MUST 覆盖确定性方法的幂等性（相同输入相同输出）。
func TestTraceIDGenerator_GenerateRoundTraceID(t *testing.T) {
	gen := NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})

	tests := []struct {
		sessionID int64
		roundNo   int
		expected  string
	}{
		{123, 1, "RT_123_1"},
		{456, 10, "RT_456_10"},
		{0, 0, "RT_0_0"},
		{9999999999, 999, "RT_9999999999_999"},
	}

	for _, tt := range tests {
		got := gen.GenerateRoundTraceID(tt.sessionID, tt.roundNo)
		if got != tt.expected {
			t.Errorf("GenerateRoundTraceID(%d, %d) = %q, want %q",
				tt.sessionID, tt.roundNo, got, tt.expected)
		}
		// 幂等性：相同输入必须产生相同输出
		got2 := gen.GenerateRoundTraceID(tt.sessionID, tt.roundNo)
		if got != got2 {
			t.Errorf("GenerateRoundTraceID not idempotent: %q vs %q", got, got2)
		}
	}
}

// TestTraceIDGenerator_GenerateBizOrderNo 测试确定性方法。
func TestTraceIDGenerator_GenerateBizOrderNo(t *testing.T) {
	gen := NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})

	got := gen.GenerateBizOrderNo("RT_123_1", 5, 6789)
	want := "RT_123_1_5_6789"
	if got != want {
		t.Errorf("GenerateBizOrderNo = %q, want %q", got, want)
	}

	// 幂等性
	got2 := gen.GenerateBizOrderNo("RT_123_1", 5, 6789)
	if got != got2 {
		t.Errorf("GenerateBizOrderNo not idempotent: %q vs %q", got, got2)
	}
}

// TestTraceIDGenerator_GenerateBatchID 测试非确定性方法返回 error。
func TestTraceIDGenerator_GenerateBatchID(t *testing.T) {
	gen := NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})

	got, err := gen.GenerateBatchID()
	if err != nil {
		t.Fatalf("GenerateBatchID failed: %v", err)
	}
	if !strings.HasPrefix(got, "BATCH_") {
		t.Errorf("GenerateBatchID = %q, want prefix %q", got, "BATCH_")
	}
}

// TestTraceIDGenerator_GenerateRefundOrderNo 测试确定性方法。
func TestTraceIDGenerator_GenerateRefundOrderNo(t *testing.T) {
	gen := NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})

	got := gen.GenerateRefundOrderNo(1001)
	want := "REFUND_1001"
	if got != want {
		t.Errorf("GenerateRefundOrderNo = %q, want %q", got, want)
	}
}

// TestTraceIDGenerator_GenerateExceptionNo 测试确定性方法。
func TestTraceIDGenerator_GenerateExceptionNo(t *testing.T) {
	gen := NewTraceIDGenerator(&stubIDGenerator{id: 12345, nodeID: 1})

	got := gen.GenerateExceptionNo(1001, "TIMEOUT")
	want := "EXC_1001_TIMEOUT"
	if got != want {
		t.Errorf("GenerateExceptionNo = %q, want %q", got, want)
	}
}
