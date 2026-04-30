package platform

import (
	"fmt"
	"strconv"
	"strings"
)

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

func FormatAmount(cents int64) string {
	yuan := cents / 100
	fen := cents % 100
	if fen == 0 {
		return fmt.Sprintf("%d.00", yuan)
	} else if fen < 10 {
		return fmt.Sprintf("%d.0%d", yuan, fen)
	}
	return fmt.Sprintf("%d.%d", yuan, fen)
}
