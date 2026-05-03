package currency

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Money 金额类型，内部以分为单位存储(int64)。
// JSON 序列化时自动输出为元的数值（如 1250 分 → 12.50）。
type Money int64

// MarshalJSON 实现 json.Marshaler，输出元值（精确到小数点后2位）。
func (m Money) MarshalJSON() ([]byte, error) {
	if m == 0 {
		return []byte("0.00"), nil
	}
	yuan := float64(m) / 100.0
	s := strconv.FormatFloat(yuan, 'f', 2, 64)
	return []byte(s), nil
}

// UnmarshalJSON 实现 json.Unmarshaler，从元值解析为分。
// 同时支持数值（12.50）和字符串（"12.50"）两种输入。
func (m *Money) UnmarshalJSON(data []byte) error {
	// 尝试作为数值解析
	var yuan float64
	if err := json.Unmarshal(data, &yuan); err == nil {
		*m = Money(math.Round(yuan * 100))
		return nil
	}
	// 尝试作为字符串解析（兼容平台 API 返回的 "12.50" 格式）
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		parsed, err := ParseAmount(s)
		if err != nil {
			return fmt.Errorf("parse money string failed: %w", err)
		}
		*m = Money(parsed)
		return nil
	}
	return fmt.Errorf("money must be number or string, got: %s", string(data))
}

// Fen 返回分值(int64)。
func (m Money) Fen() int64 {
	return int64(m)
}

// Yuan 返回元值(float64)。
func (m Money) Yuan() float64 {
	return float64(m) / 100.0
}

// String 返回元值字符串（如 "12.50"）。
func (m Money) String() string {
	abs := int64(m)
	sign := ""
	if abs < 0 {
		sign = "-"
		abs = -abs
	}
	yuan := abs / 100
	fen := abs % 100
	return fmt.Sprintf("%s%d.%02d", sign, yuan, fen)
}

// NewMoneyFromFen 从分创建 Money。
func NewMoneyFromFen(fen int64) Money {
	return Money(fen)
}

// NewMoneyFromYuan 从元(float64)创建 Money。
func NewMoneyFromYuan(yuan float64) Money {
	return Money(math.Round(yuan * 100))
}
