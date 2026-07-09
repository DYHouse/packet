package domain

import (
	"testing"
)

// TestBillRecord_TransitionTo 校验 BillRecord 状态机守卫方法的合法与非法转换。
// 覆盖所有合法转换（返回 nil）、所有非法转换（返回 error）以及 nil 接收者。
func TestBillRecord_TransitionTo(t *testing.T) {
	// allStates 为 BillRecord 全部状态集合，用于枚举非法转换。
	allStates := []int{BillStatusProcessing, BillStatusSuccess, BillStatusFailed, BillStatusRefunded}

	// legalTransitions 记录所有合法的状态转换。
	legalTransitions := map[[2]int]bool{
		{BillStatusProcessing, BillStatusSuccess}: true,
		{BillStatusProcessing, BillStatusFailed}:  true,
		{BillStatusSuccess, BillStatusRefunded}:   true,
	}

	tests := []struct {
		name    string
		from    int
		to      int
		wantErr bool
	}{
		// 合法转换
		{"legal: Processing -> Success", BillStatusProcessing, BillStatusSuccess, false},
		{"legal: Processing -> Failed", BillStatusProcessing, BillStatusFailed, false},
		{"legal: Success -> Refunded", BillStatusSuccess, BillStatusRefunded, false},
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
				name:    "illegal: " + billStatusName(from) + " -> " + billStatusName(to),
				from:    from,
				to:      to,
				wantErr: true,
			})
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &BillRecord{Status: tt.from}
			err := b.TransitionTo(tt.to)
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
			if b.Status != tt.from {
				t.Fatalf("TransitionTo modified status: got %d, want %d", b.Status, tt.from)
			}
		})
	}

	// nil 接收者返回 error。
	t.Run("nil receiver returns error", func(t *testing.T) {
		var b *BillRecord
		err := b.TransitionTo(BillStatusSuccess)
		if err == nil {
			t.Fatal("TransitionTo on nil receiver: expected error, got nil")
		}
	})
}

// billStatusName 返回 BillRecord 状态常量的可读名称，用于测试用例命名。
func billStatusName(status int) string {
	switch status {
	case BillStatusProcessing:
		return "Processing"
	case BillStatusSuccess:
		return "Success"
	case BillStatusFailed:
		return "Failed"
	case BillStatusRefunded:
		return "Refunded"
	default:
		return "Unknown"
	}
}
