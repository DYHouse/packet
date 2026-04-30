package algorithm

import (
	"context"
	"testing"
)

func TestStraightGenerator_Check(t *testing.T) {
	generator := NewStraightGenerator(&Config{})

	tests := []struct {
		name     string
		amounts  []int64
		expected bool
	}{
		{
			name:     "顺子 - 整数位连续（1-5元）",
			amounts:  []int64{110, 280, 310, 400, 580},
			expected: true,
		},
		{
			name:     "顺子 - 整数位连续（12-16元）",
			amounts:  []int64{1200, 1305, 1499, 1500, 1610},
			expected: true,
		},
		{
			name:     "顺子 - 整数位连续（121-125元）",
			amounts:  []int64{12100, 12250, 12399, 12401, 12599},
			expected: true,
		},
		{
			name:     "非顺子 - 整数位不连续",
			amounts:  []int64{110, 210, 310, 410, 610},
			expected: false,
		},
		{
			name:     "非顺子 - 整数位重复",
			amounts:  []int64{150, 180, 310, 410, 510},
			expected: false,
		},
		{
			name:     "单个红包 - 不是顺子",
			amounts:  []int64{100},
			expected: false,
		},
		{
			name:     "两个红包 - 整数位连续",
			amounts:  []int64{150, 280},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generator.Check(tt.amounts)
			if result != tt.expected {
				t.Errorf("Check() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestStraightGenerator_Generate(t *testing.T) {
	generator := NewStraightGenerator(&Config{})

	tests := []struct {
		name        string
		packetCount int
		totalAmount int64
	}{
		{
			name:        "生成5个红包 - 总金额2000分",
			packetCount: 5,
			totalAmount: 2000,
		},
		{
			name:        "生成3个红包 - 总金额800分",
			packetCount: 3,
			totalAmount: 800,
		},
		{
			name:        "生成10个红包 - 总金额10000分",
			packetCount: 10,
			totalAmount: 10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &GenerateRequest{
				PacketCount: tt.packetCount,
				TotalAmount: tt.totalAmount,
			}

			result, err := generator.Generate(context.Background(), req, "test-trace-id")
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}

			if len(result.PacketAmounts) != tt.packetCount {
				t.Errorf("Generate() packet count = %d, expected %d", len(result.PacketAmounts), tt.packetCount)
			}

			var sum int64
			for _, amount := range result.PacketAmounts {
				sum += amount
			}
			if sum != tt.totalAmount {
				t.Errorf("Generate() total amount = %d, expected %d", sum, tt.totalAmount)
			}

			if !generator.Check(result.PacketAmounts) {
				t.Errorf("Generate() result is not a straight pattern: %v", result.PacketAmounts)
			}

			for i, amount := range result.PacketAmounts {
				if amount < 1 {
					t.Errorf("Generate() packet[%d] amount = %d, must be at least 1", i, amount)
				}
			}
		})
	}
}

func TestStraightGenerator_GenerateAndCheck(t *testing.T) {
	generator := NewStraightGenerator(&Config{})

	req := &GenerateRequest{
		PacketCount: 5,
		TotalAmount: 1500,
	}

	result, err := generator.Generate(context.Background(), req, "test-trace-id")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	t.Logf("生成的红包金额（分）: %v", result.PacketAmounts)

	for i, amount := range result.PacketAmounts {
		yuan := float64(amount) / 100
		t.Logf("红包%d: %.2f元 (整数位: %d)", i+1, yuan, amount/100)
	}

	if !generator.Check(result.PacketAmounts) {
		t.Errorf("生成的红包不符合顺子规则")
	}
}

func TestStraightGenerator_GenerateWithDecimal(t *testing.T) {
	generator := NewStraightGenerator(&Config{})

	req := &GenerateRequest{
		PacketCount: 5,
		TotalAmount: 2543,
	}

	result, err := generator.Generate(context.Background(), req, "test-trace-id")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	t.Logf("生成的红包金额（分）: %v", result.PacketAmounts)

	for i, amount := range result.PacketAmounts {
		yuan := float64(amount) / 100
		t.Logf("红包%d: %.2f元 (整数位: %d)", i+1, yuan, amount/100)
	}

	var sum int64
	for _, amount := range result.PacketAmounts {
		sum += amount
	}
	if sum != 2543 {
		t.Errorf("总金额 = %d, expected 2543", sum)
	}

	if !generator.Check(result.PacketAmounts) {
		t.Errorf("生成的红包不符合顺子规则")
	}

	hasDecimal := false
	for _, amount := range result.PacketAmounts {
		if amount%100 != 0 {
			hasDecimal = true
			break
		}
	}
	if !hasDecimal {
		t.Logf("注意: 所有红包都是整数金额，可能是因为剩余金额正好能整除")
	}
}
