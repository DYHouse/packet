package converter

import (
	"fmt"
	"strconv"

	"github.com/cashparty/backend/common/logger"
)

func ParseID(id string) int64 {
	result, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		logger.Warn("parse id failed", "id", id, "error", err)
		return 0
	}
	return result
}

func ParseIDStrict(id string) (int64, error) {
	return strconv.ParseInt(id, 10, 64)
}

func FormatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func ParseIDs(ids []string) []int64 {
	result := make([]int64, len(ids))
	for i, id := range ids {
		result[i] = ParseID(id)
	}
	return result
}

func FormatIDs(ids []int64) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = FormatID(id)
	}
	return result
}

func ParseInt(v interface{}) int {
	switch val := v.(type) {
	case int64:
		return int(val)
	case float64:
		return int(val)
	case int:
		return val
	default:
		logger.Warn("parse int from interface failed",
			"value", v, "type", fmt.Sprintf("%T", v))
		return 0
	}
}

func ParseInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	case string:
		return ParseID(val)
	default:
		logger.Warn("parse int64 from interface failed",
			"value", v, "type", fmt.Sprintf("%T", v))
		return 0
	}
}

func ParseString(v interface{}) string {
	if str, ok := v.(string); ok {
		return str
	}
	return fmt.Sprintf("%v", v)
}

func ParseStringSlice(v interface{}) []string {
	var result []string
	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			result = append(result, ParseString(item))
		}
	}
	return result
}

func ParseInt64Slice(v interface{}) []int64 {
	var result []int64
	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			result = append(result, ParseInt64(item))
		}
	}
	return result
}
