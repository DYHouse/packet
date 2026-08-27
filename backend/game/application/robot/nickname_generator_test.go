package robot

import (
	"strings"
	"testing"
	"unicode"
)

// TestGenerateNickname_Basic 验证生成的昵称非空且为 ASCII 可打印字符。
func TestGenerateNickname_Basic(t *testing.T) {
	for i := 0; i < 200; i++ {
		n := GenerateNickname()
		if n == "" {
			t.Fatalf("GenerateNickname returned empty string (iteration %d)", i)
		}
		for _, r := range n {
			if r > unicode.MaxASCII || !unicode.IsPrint(r) {
				t.Fatalf("non-ASCII or non-printable rune %q in nickname %q (iteration %d)", r, n, i)
			}
		}
		// 禁止包含空格
		if strings.ContainsAny(n, " \t\n") {
			t.Fatalf("whitespace in nickname %q (iteration %d)", n, i)
		}
	}
}

// TestGenerateNickname_Variety 验证在大量样本下能产出足够多样的昵称。
// 1000 次调用的不同昵称占比应超过 85%（不查重情况下仍应具有较好离散度）。
func TestGenerateNickname_Variety(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		seen[GenerateNickname()] = struct{}{}
	}
	ratio := float64(len(seen)) / n
	if ratio < 0.85 {
		t.Errorf("nickname variety too low: unique=%d / %d (ratio=%.2f, expect >= 0.85)",
			len(seen), n, ratio)
	}
}

// TestGenerateNickname_LengthBound 验证昵称长度在合理范围（6~28 字符）。
func TestGenerateNickname_LengthBound(t *testing.T) {
	const n = 500
	minLen, maxLen := 999, 0
	for i := 0; i < n; i++ {
		l := len(GenerateNickname())
		if l < minLen {
			minLen = l
		}
		if l > maxLen {
			maxLen = l
		}
		if l > 100 {
			// users 表 nickname 为 varchar(100)
			t.Errorf("nickname exceeds varchar(100): len=%d", l)
		}
	}
	if minLen < 3 {
		t.Errorf("nickname min length too short: %d", minLen)
	}
	_ = maxLen
}

// BenchmarkGenerateNickname 验证生成性能（无需对每只机器人太敏感，仅作为冒烟基准）。
func BenchmarkGenerateNickname(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GenerateNickname()
	}
}
