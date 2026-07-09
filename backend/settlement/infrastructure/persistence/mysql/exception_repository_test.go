package mysql

import (
	"testing"
	"time"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/model"
)

// TestExceptionModelToDomain 覆盖 exceptionModelToDomain 的正常转换与 nil 兜底。
// 该函数为 Phase 7 Task 7.3 新增 GetByStatus/GetByID 出口转换的依赖，
// 属纯函数，无需 DB 基础设施。
func TestExceptionModelToDomain(t *testing.T) {
	t.Run("nil 输入返回 nil", func(t *testing.T) {
		if got := exceptionModelToDomain(nil); got != nil {
			t.Fatalf("期望 nil, 实际 %#v", got)
		}
	})

	t.Run("正常转换保留全部字段", func(t *testing.T) {
		handledAt := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
		m := &model.ExceptionRecord{
			ID:              100,
			ExceptionNo:     "EXC-20260709-0001",
			ExceptionType:   domain.ExceptionTypeDebitFailed,
			BillID:          200,
			RoundTraceID:    "trace-001",
			RoundID:         300,
			BillType:        1,
			UserID:          400,
			Amount:          500,
			Status:          domain.ExceptionStatusPending,
			ExceptionDetail: "debit failed",
			HandleType:      domain.HandleTypeManual,
			HandleRemark:    "manual handled",
			HandledAt:       &handledAt,
			HandledBy:       600,
			CreatedAt:       handledAt,
			UpdatedAt:       handledAt,
		}

		d := exceptionModelToDomain(m)
		if d == nil {
			t.Fatal("期望非 nil domain")
		}
		if d.ID != m.ID {
			t.Errorf("ID 不匹配: got %d, want %d", d.ID, m.ID)
		}
		if d.ExceptionNo != m.ExceptionNo {
			t.Errorf("ExceptionNo 不匹配: got %q, want %q", d.ExceptionNo, m.ExceptionNo)
		}
		if d.ExceptionType != m.ExceptionType {
			t.Errorf("ExceptionType 不匹配: got %d, want %d", d.ExceptionType, m.ExceptionType)
		}
		if d.BillID != m.BillID {
			t.Errorf("BillID 不匹配: got %d, want %d", d.BillID, m.BillID)
		}
		if d.RoundTraceID != m.RoundTraceID {
			t.Errorf("RoundTraceID 不匹配: got %q, want %q", d.RoundTraceID, m.RoundTraceID)
		}
		if d.RoundID != m.RoundID {
			t.Errorf("RoundID 不匹配: got %d, want %d", d.RoundID, m.RoundID)
		}
		if d.BillType != m.BillType {
			t.Errorf("BillType 不匹配: got %d, want %d", d.BillType, m.BillType)
		}
		if d.UserID != m.UserID {
			t.Errorf("UserID 不匹配: got %d, want %d", d.UserID, m.UserID)
		}
		if d.Amount != m.Amount {
			t.Errorf("Amount 不匹配: got %d, want %d", d.Amount, m.Amount)
		}
		if d.Status != m.Status {
			t.Errorf("Status 不匹配: got %d, want %d", d.Status, m.Status)
		}
		if d.ExceptionDetail != m.ExceptionDetail {
			t.Errorf("ExceptionDetail 不匹配: got %q, want %q", d.ExceptionDetail, m.ExceptionDetail)
		}
		if d.HandleType != m.HandleType {
			t.Errorf("HandleType 不匹配: got %d, want %d", d.HandleType, m.HandleType)
		}
		if d.HandleRemark != m.HandleRemark {
			t.Errorf("HandleRemark 不匹配: got %q, want %q", d.HandleRemark, m.HandleRemark)
		}
		if d.HandledBy != m.HandledBy {
			t.Errorf("HandledBy 不匹配: got %d, want %d", d.HandledBy, m.HandledBy)
		}
		if d.HandledAt == nil || !d.HandledAt.Equal(*m.HandledAt) {
			t.Errorf("HandledAt 不匹配: got %v, want %v", d.HandledAt, m.HandledAt)
		}
		if !d.CreatedAt.Equal(m.CreatedAt) {
			t.Errorf("CreatedAt 不匹配: got %v, want %v", d.CreatedAt, m.CreatedAt)
		}
		if !d.UpdatedAt.Equal(m.UpdatedAt) {
			t.Errorf("UpdatedAt 不匹配: got %v, want %v", d.UpdatedAt, m.UpdatedAt)
		}
	})

	t.Run("HandledAt 为 nil 时 domain 也为 nil", func(t *testing.T) {
		m := &model.ExceptionRecord{ID: 1, HandledAt: nil}
		d := exceptionModelToDomain(m)
		if d.HandledAt != nil {
			t.Errorf("期望 HandledAt 为 nil, 实际 %v", d.HandledAt)
		}
	})
}

// TestExceptionModelSliceToDomain 覆盖 exceptionModelSliceToDomain 的批量转换、
// nil 兜底与空切片语义。该函数为 Phase 7 Task 7.3 GetByStatus 出口转换依赖。
func TestExceptionModelSliceToDomain(t *testing.T) {
	t.Run("nil 输入返回 nil", func(t *testing.T) {
		if got := exceptionModelSliceToDomain(nil); got != nil {
			t.Fatalf("期望 nil, 实际 %#v", got)
		}
	})

	t.Run("空切片返回长度 0 的非 nil 切片", func(t *testing.T) {
		got := exceptionModelSliceToDomain([]*model.ExceptionRecord{})
		if got == nil {
			t.Fatal("期望非 nil 切片")
		}
		if len(got) != 0 {
			t.Errorf("期望长度 0, 实际 %d", len(got))
		}
	})

	t.Run("多元素批量转换保持顺序与长度", func(t *testing.T) {
		ms := []*model.ExceptionRecord{
			{ID: 1, ExceptionNo: "EXC-1", Status: domain.ExceptionStatusPending},
			{ID: 2, ExceptionNo: "EXC-2", Status: domain.ExceptionStatusResolved},
			{ID: 3, ExceptionNo: "EXC-3", Status: domain.ExceptionStatusIgnored},
		}
		got := exceptionModelSliceToDomain(ms)
		if len(got) != len(ms) {
			t.Fatalf("长度不匹配: got %d, want %d", len(got), len(ms))
		}
		for i, d := range got {
			if d.ID != ms[i].ID {
				t.Errorf("第 %d 个元素 ID 不匹配: got %d, want %d", i, d.ID, ms[i].ID)
			}
			if d.ExceptionNo != ms[i].ExceptionNo {
				t.Errorf("第 %d 个元素 ExceptionNo 不匹配: got %q, want %q", i, d.ExceptionNo, ms[i].ExceptionNo)
			}
			if d.Status != ms[i].Status {
				t.Errorf("第 %d 个元素 Status 不匹配: got %d, want %d", i, d.Status, ms[i].Status)
			}
		}
	})

	t.Run("包含 nil 元素时跳过不 panic（nil 转为 nil 元素）", func(t *testing.T) {
		ms := []*model.ExceptionRecord{
			{ID: 1, ExceptionNo: "EXC-1"},
			nil,
			{ID: 3, ExceptionNo: "EXC-3"},
		}
		got := exceptionModelSliceToDomain(ms)
		if len(got) != 3 {
			t.Fatalf("长度不匹配: got %d, want 3", len(got))
		}
		if got[0].ID != 1 {
			t.Errorf("第 0 个元素 ID 不匹配: got %d, want 1", got[0].ID)
		}
		if got[1] != nil {
			t.Errorf("第 1 个元素期望 nil, 实际 %#v", got[1])
		}
		if got[2].ID != 3 {
			t.Errorf("第 2 个元素 ID 不匹配: got %d, want 3", got[2].ID)
		}
	})
}

// TestExceptionDomainToModel 覆盖 exceptionDomainToModel 的正常转换与 nil 兜底，
// 确保往返转换（domain → model → domain）字段不丢失。
func TestExceptionDomainToModel(t *testing.T) {
	t.Run("nil 输入返回 nil", func(t *testing.T) {
		if got := exceptionDomainToModel(nil); got != nil {
			t.Fatalf("期望 nil, 实际 %#v", got)
		}
	})

	t.Run("往返转换字段保持一致", func(t *testing.T) {
		handledAt := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
		orig := &domain.ExceptionRecord{
			ID:              100,
			ExceptionNo:     "EXC-RT-0001",
			ExceptionType:   domain.ExceptionTypeCreditRetryExceed,
			BillID:          200,
			RoundTraceID:    "trace-rt",
			RoundID:         300,
			BillType:        2,
			UserID:          400,
			Amount:          500,
			Status:          domain.ExceptionStatusResolved,
			ExceptionDetail: "credit retry exceeded",
			HandleType:      domain.HandleTypeRetry,
			HandleRemark:    "retried",
			HandledAt:       &handledAt,
			HandledBy:       600,
			CreatedAt:       handledAt,
			UpdatedAt:       handledAt,
		}

		m := exceptionDomainToModel(orig)
		if m == nil {
			t.Fatal("期望非 nil model")
		}
		roundTrip := exceptionModelToDomain(m)

		if roundTrip.ID != orig.ID {
			t.Errorf("ID 往返不匹配: got %d, want %d", roundTrip.ID, orig.ID)
		}
		if roundTrip.ExceptionNo != orig.ExceptionNo {
			t.Errorf("ExceptionNo 往返不匹配: got %q, want %q", roundTrip.ExceptionNo, orig.ExceptionNo)
		}
		if roundTrip.ExceptionType != orig.ExceptionType {
			t.Errorf("ExceptionType 往返不匹配: got %d, want %d", roundTrip.ExceptionType, orig.ExceptionType)
		}
		if roundTrip.Status != orig.Status {
			t.Errorf("Status 往返不匹配: got %d, want %d", roundTrip.Status, orig.Status)
		}
		if roundTrip.HandleType != orig.HandleType {
			t.Errorf("HandleType 往返不匹配: got %d, want %d", roundTrip.HandleType, orig.HandleType)
		}
		if roundTrip.HandledBy != orig.HandledBy {
			t.Errorf("HandledBy 往返不匹配: got %d, want %d", roundTrip.HandledBy, orig.HandledBy)
		}
		if roundTrip.HandledAt == nil || !roundTrip.HandledAt.Equal(*orig.HandledAt) {
			t.Errorf("HandledAt 往返不匹配: got %v, want %v", roundTrip.HandledAt, orig.HandledAt)
		}
	})
}
