package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/cashparty/backend/stats/service"
	"github.com/gin-gonic/gin"
)

type StatsHandler struct {
	statsService *service.StatsService
}

func NewStatsHandler(statsService *service.StatsService) *StatsHandler {
	return &StatsHandler{statsService: statsService}
}

func (h *StatsHandler) RegisterRoutes(r *gin.RouterGroup) {
	stats := r.Group("/stats")
	{
		stats.GET("/dashboard", h.GetDashboard)
		stats.GET("/trend/hourly", h.GetHourlyTrend)
		stats.GET("/trend/daily", h.GetDailyTrend)
		stats.GET("/distribution", h.GetAmountDistribution)
		stats.GET("/rooms/ranking", h.GetRoomRanking)
		stats.GET("/system-packets", h.GetSystemPacketStats)
	}
}

func parseDate(dateStr string) (time.Time, bool) {
	if dateStr == "" {
		return time.Time{}, false
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return time.Time{}, false
	}
	return date, true
}

func (h *StatsHandler) GetDashboard(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	var ok bool

	if endDate, ok = parseDate(endDateStr); !ok {
		endDate = time.Now()
	}

	if startDate, ok = parseDate(startDateStr); !ok {
		startDate = endDate
	}

	stats, err := h.statsService.GetDashboardStats(c.Request.Context(), startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}

func (h *StatsHandler) GetHourlyTrend(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	var ok bool

	if endDate, ok = parseDate(endDateStr); !ok {
		endDate = time.Now()
	}

	if startDate, ok = parseDate(startDateStr); !ok {
		startDate = endDate
	}

	trends, err := h.statsService.GetHourlyTrend(c.Request.Context(), startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": trends,
	})
}

func (h *StatsHandler) GetDailyTrend(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	var ok bool

	if endDate, ok = parseDate(endDateStr); !ok {
		endDate = time.Now()
	}

	if startDate, ok = parseDate(startDateStr); !ok {
		startDate = endDate.AddDate(0, 0, -6)
	}

	trends, err := h.statsService.GetDailyTrend(c.Request.Context(), startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": trends,
	})
}

func (h *StatsHandler) GetAmountDistribution(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	var ok bool

	if endDate, ok = parseDate(endDateStr); !ok {
		endDate = time.Now()
	}

	if startDate, ok = parseDate(startDateStr); !ok {
		startDate = endDate
	}

	distributions, err := h.statsService.GetAmountDistribution(c.Request.Context(), startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": distributions,
	})
}

func (h *StatsHandler) GetRoomRanking(c *gin.Context) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	var ok bool

	if endDate, ok = parseDate(endDateStr); !ok {
		endDate = time.Now()
	}

	if startDate, ok = parseDate(startDateStr); !ok {
		startDate = endDate
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	rankings, err := h.statsService.GetRoomRanking(c.Request.Context(), startDate, endDate, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": rankings,
	})
}

func (h *StatsHandler) GetSystemPacketStats(c *gin.Context) {
	dateStr := c.Query("date")
	var date time.Time
	if d, ok := parseDate(dateStr); ok {
		date = d
	} else {
		date = time.Now()
	}

	stats, err := h.statsService.GetSystemPacketStats(c.Request.Context(), date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}
