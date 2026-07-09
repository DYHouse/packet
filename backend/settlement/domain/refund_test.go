package domain

import (
	"testing"
)

// TestRefundAudit_TransitionTo 校验 RefundAudit 状态机守卫方法的合法与非法转换。
// 覆盖所有合法转换（返回 nil）、所有非法转换（返回 error）以及 nil 接收者。
func TestRefundAudit_TransitionTo(t *testing.T) {
	// allStates 为 RefundAudit 全部状态集合，用于枚举非法转换。
	allStates := []int{
		RefundStatusNone,
		RefundStatusPending,
		RefundStatusApproved,
		RefundStatusRefunded,
		RefundStatusRejected,
		RefundStatusProcessing,
	}

	// legalTransitions 记录所有合法的状态转换。
	legalTransitions := map[[2]int]bool{
		{RefundStatusNone, RefundStatusPending}:        true,
		{RefundStatusPending, RefundStatusApproved}:    true,
		{RefundStatusPending, RefundStatusRejected}:    true,
		{RefundStatusApproved, RefundStatusRefunded}:   true,
		{RefundStatusApproved, RefundStatusProcessing}: true,
		{RefundStatusProcessing, RefundStatusRefunded}: true,
		{RefundStatusProcessing, RefundStatusPending}:  true,
	}

	tests := []struct {
		name    string
		from    int
		to      int
		wantErr bool
	}{
		// 合法转换
		{"legal: None -> Pending", RefundStatusNone, RefundStatusPending, false},
		{"legal: Pending -> Approved", RefundStatusPending, RefundStatusApproved, false},
		{"legal: Pending -> Rejected", RefundStatusPending, RefundStatusRejected, false},
		{"legal: Approved -> Refunded", RefundStatusApproved, RefundStatusRefunded, false},
		{"legal: Approved -> Processing", RefundStatusApproved, RefundStatusProcessing, false},
		{"legal: Processing -> Refunded", RefundStatusProcessing, RefundStatusRefunded, false},
		{"legal: Processing -> Pending (RPC failure rollback)", RefundStatusProcessing, RefundStatusPending, false},
	}

	// 自动枚举所有非法转换（含自环与终态转出）。
	for _, from := range allStates {
		for _, to := range allStates {
			key := [2]int{from, to}
			if legalTransitions[key] {
				continue
			}
			tests = append(tests, struct {
				name    string
				from    int
				to      int
				wantErr bool
			}{
				name:    "illegal: " + refundStatusName(from) + " -> " + refundStatusName(to),
				from:    from,
				to:      to,
				wantErr: true,
			})
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RefundAudit{Status: tt.from}
			err := r.TransitionTo(tt.to)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("TransitionTo(%d) from status %d: expected error, got nil", tt.to, tt.from)
				}
			} else {
				if err != nil {
					t.Fatalf("TransitionTo(%d) from status %d: expected nil, got error: %v", tt.to, tt.from, err)
				}
			}
			// 校验守卫方法不修改状态（无副作用）。
			if r.Status != tt.from {
				t.Fatalf("TransitionTo modified status: got %d, want %d", r.Status, tt.from)
			}
		})
	}

	// nil 接收者返回 error。
	t.Run("nil receiver returns error", func(t *testing.T) {
		var r *RefundAudit
		err := r.TransitionTo(RefundStatusPending)
		if err == nil {
			t.Fatal("TransitionTo on nil receiver: expected error, got nil")
		}
	})
}

// refundStatusName 返回 RefundAudit 状态常量的可读名称，用于测试用例命名。
func refundStatusName(status int) string {
	switch status {
	case RefundStatusNone:
		return "None"
	case RefundStatusPending:
		return "Pending"
	case RefundStatusApproved:
		return "Approved"
	case RefundStatusRefunded:
		return "Refunded"
	case RefundStatusRejected:
		return "Rejected"
	case RefundStatusProcessing:
		return "Processing"
	default:
		return "Unknown"
	}
}
