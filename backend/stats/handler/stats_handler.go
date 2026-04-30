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

func respondError(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{
		"code":    code * 100,
		"message": msg,
	})
}

func (h *StatsHandler) GetDashboard(c *gin.Context) {
	startDate, endDate, ok := ParseDateRange(c, 0)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid date range: start_date must <= end_date, max 90 days")
		return
	}

	stats, err := h.statsService.GetDashboardStats(c.Request.Context(), startDate, endDate)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}

func (h *StatsHandler) GetHourlyTrend(c *gin.Context) {
	startDate, endDate, ok := ParseDateRange(c, 0)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid date range: start_date must <= end_date, max 90 days")
		return
	}

	trends, err := h.statsService.GetHourlyTrend(c.Request.Context(), startDate, endDate)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": trends,
	})
}

func (h *StatsHandler) GetDailyTrend(c *gin.Context) {
	startDate, endDate, ok := ParseDateRange(c, -6)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid date range: start_date must <= end_date, max 90 days")
		return
	}

	trends, err := h.statsService.GetDailyTrend(c.Request.Context(), startDate, endDate)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": trends,
	})
}

func (h *StatsHandler) GetAmountDistribution(c *gin.Context) {
	startDate, endDate, ok := ParseDateRange(c, 0)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid date range: start_date must <= end_date, max 90 days")
		return
	}

	distributions, err := h.statsService.GetAmountDistribution(c.Request.Context(), startDate, endDate)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": distributions,
	})
}

func (h *StatsHandler) GetRoomRanking(c *gin.Context) {
	startDate, endDate, ok := ParseDateRange(c, 0)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid date range: start_date must <= end_date, max 90 days")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	rankings, err := h.statsService.GetRoomRanking(c.Request.Context(), startDate, endDate, limit, offset)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": rankings,
	})
}

// GetSystemPacketStats 改用 start_date+end_date 参数风格, 与其他接口统一
func (h *StatsHandler) GetSystemPacketStats(c *gin.Context) {
	startDate, endDate, ok := ParseDateRange(c, 0)
	if !ok {
		respondError(c, http.StatusBadRequest, "invalid date range: start_date must <= end_date, max 90 days")
		return
	}

	stats, err := h.statsService.GetSystemPacketStats(c.Request.Context(), startDate, endDate)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": stats,
	})
}
