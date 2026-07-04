package service

import (
	"fmt"
	"time"
)

// FormatDateRange 返回日期范围的缓存 key 后缀（格式: "2006-01-02:2006-01-02"）。
// stats/service 与 stats/handler 共用此函数，确保日期范围格式单一真相源。
func FormatDateRange(startDate, endDate time.Time) string {
	return fmt.Sprintf("%s:%s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
}
