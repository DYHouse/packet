package domain

import (
	"testing"
)

// TestRoundSettlement_TransitionTo 校验 RoundSettlement 状态机守卫方法的合法与非法转换。
// 覆盖所有合法转换（返回 nil）、所有非法转换（返回 error）以及 nil 接收者。
func TestRoundSettlement_TransitionTo(t *testing.T) {
	// allStates 为 RoundSettlement 全部状态集合，用于枚举非法转换。
	allStates := []int{RoundStatusDeducting, RoundStatusDeducted, RoundStatusFailed, RoundStatusCredited}

	// legalTransitions 记录所有合法的状态转换。
	legalTransitions := map[[2]int]bool{
		{RoundStatusDeducting, RoundStatusDeducted}: true,
		{RoundStatusDeducting, RoundStatusFailed}:   true,
		{RoundStatusDeducted, RoundStatusCredited}:  true,
		{RoundStatusDeducted, RoundStatusFailed}:    true,
	}

	tests := []struct {
		name    string
		from    int
		to      int
		wantErr bool
	}{
		// 合法转换
		{"legal: Deducting -> Deducted", RoundStatusDeducting, RoundStatusDeducted, false},
		{"legal: Deducting -> Failed", RoundStatusDeducting, RoundStatusFailed, false},
		{"legal: Deducted -> Credited", RoundStatusDeducted, RoundStatusCredited, false},
		{"legal: Deducted -> Failed", RoundStatusDeducted, RoundStatusFailed, false},
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
				name:    "illegal: " + roundStatusName(from) + " -> " + roundStatusName(to),
				from:    from,
				to:      to,
				wantErr: true,
			})
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RoundSettlement{Status: tt.from}
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
		var r *RoundSettlement
		err := r.TransitionTo(RoundStatusDeducted)
		if err == nil {
			t.Fatal("TransitionTo on nil receiver: expected error, got nil")
		}
	})
}

// roundStatusName 返回 RoundSettlement 状态常量的可读名称，用于测试用例命名。
func roundStatusName(status int) string {
	switch status {
	case RoundStatusDeducting:
		return "Deducting"
	case RoundStatusDeducted:
		return "Deducted"
	case RoundStatusFailed:
		return "Failed"
	case RoundStatusCredited:
		return "Credited"
	default:
		return "Unknown"
	}
}
