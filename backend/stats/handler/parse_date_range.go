package handler

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

const maxDateRangeDays = 90

// ParseDateRange 从 query 参数解析日期范围.
// defaultOffset: 未提供 start_date 时, 相对 endDate 的天数偏移 (0=当天, -6=近7天).
// 返回 (startDate, endDate, ok), ok=false 表示参数非法.
func ParseDateRange(c *gin.Context, defaultOffset int) (time.Time, time.Time, bool) {
	endDateStr := c.Query("end_date")
	startDateStr := c.Query("start_date")

	var endDate, startDate time.Time
	var ok bool

	if endDate, ok = parseDate(endDateStr); !ok {
		endDate = time.Now()
	}

	if startDate, ok = parseDate(startDateStr); !ok {
		startDate = endDate.AddDate(0, 0, defaultOffset)
	}

	if startDate.After(endDate) {
		return time.Time{}, time.Time{}, false
	}

	if endDate.Sub(startDate).Hours() > float64(maxDateRangeDays*24) {
		return time.Time{}, time.Time{}, false
	}

	return startDate, endDate, true
}

// FormatDateRange 返回日期范围的缓存 key 后缀.
func FormatDateRange(startDate, endDate time.Time) string {
	return fmt.Sprintf("%s:%s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
}
