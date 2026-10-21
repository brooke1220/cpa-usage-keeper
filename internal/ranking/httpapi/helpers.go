package httpapi

import (
	"cpa-usage-keeper/internal/ranking"
	"github.com/gin-gonic/gin"
)

func setNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func validPeriod(period ranking.LeaderboardPeriod) bool {
	switch period {
	case ranking.LeaderboardToday, ranking.LeaderboardYesterday, ranking.LeaderboardCurrentMonth, ranking.LeaderboardPreviousMonth:
		return true
	default:
		return false
	}
}

func validMetric(metric ranking.LeaderboardMetric) bool {
	switch metric {
	case ranking.MetricTotalTokens, ranking.MetricRequestCount, ranking.MetricCacheReadRate, ranking.MetricTTFTAverage, ranking.MetricLatencyAverage, ranking.MetricPeakTPM, ranking.MetricPeakRPM, ranking.MetricOverall:
		return true
	default:
		return false
	}
}
