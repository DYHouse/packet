package trace

import (
	"context"
	"strings"
	"testing"

	"github.com/cashparty/backend/common/idgen"
)

// 初始化 idgen 以便 Generate() 能正常工作
func init() {
	// nodeID=1 用于测试环境
	_ = idgen.Init(1)
}

func TestWithTraceID_InjectExisting(t *testing.T) {
	ctx := WithTraceID(context.Background(), "tr_123")
	got := FromContext(ctx)
	if got != "tr_123" {
		t.Errorf("FromContext() = %q, want %q", got, "tr_123")
	}
}

func TestWithTraceID_AutoGenerate(t *testing.T) {
	ctx := WithTraceID(context.Background(), "")
	got := FromContext(ctx)
	if got == "" {
		t.Fatal("FromContext() returned empty, expected auto-generated traceID")
	}
	if !strings.HasPrefix(got, "tr_") {
		t.Errorf("auto-generated traceID = %q, want prefix %q", got, "tr_")
	}
}

func TestFromContext_Empty(t *testing.T) {
	got := FromContext(context.Background())
	if got != "" {
		t.Errorf("FromContext(empty ctx) = %q, want empty string", got)
	}
}

func TestGenerate_SnowflakeFormat(t *testing.T) {
	got := Generate()
	if got == "" {
		t.Fatal("Generate() returned empty")
	}
	if !strings.HasPrefix(got, "tr_") {
		t.Errorf("Generate() = %q, want prefix %q", got, "tr_")
	}
}

func TestGenerate_UniqueId(t *testing.T) {
	id1 := Generate()
	id2 := Generate()
	if id1 == id2 {
		t.Errorf("Generate() returned duplicate IDs: %q == %q", id1, id2)
	}
}

func TestWithTraceID_DoesNotOverrideExisting(t *testing.T) {
	// 验证已有 TraceID 不会被覆盖（Publisher 场景）
	ctx := WithTraceID(context.Background(), "tr_ctx_123")
	// 再次调用 WithTraceID 不应影响已有 ctx（每次返回新 ctx）
	ctx2 := WithTraceID(ctx, "tr_new_456")
	got := FromContext(ctx2)
	if got != "tr_new_456" {
		t.Errorf("FromContext() = %q, want %q (new value should override)", got, "tr_new_456")
	}
	// 原 ctx 不受影响
	gotOrig := FromContext(ctx)
	if gotOrig != "tr_ctx_123" {
		t.Errorf("original ctx FromContext() = %q, want %q", gotOrig, "tr_ctx_123")
	}
}
