package currency

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseAmount 将元字符串(如 "12.50")解析为分(int64)。
func ParseAmount(amountStr string) (int64, error) {
	amountStr = strings.TrimSpace(amountStr)
	if amountStr == "" {
		return 0, nil
	}

	parts := strings.Split(amountStr, ".")
	var cents int64

	intPart, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse integer part failed: %w", err)
	}
	cents = intPart * 100

	if len(parts) > 1 {
		decPart := parts[1]
		if len(decPart) > 2 {
			decPart = decPart[:2]
		}
		decValue, err := strconv.ParseInt(decPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse decimal part failed: %w", err)
		}
		if len(decPart) == 1 {
			decValue *= 10
		}
		cents += decValue
	}

	return cents, nil
}

// FormatAmount 将分(int64)格式化为元字符串(如 "12.50")。
func FormatAmount(cents int64) string {
	return NewMoneyFromFen(cents).String()
}
