package api

import (
	"context"
	"errors"
	"net/http"

	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/service/dto"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func registerAPIKeyTimeUsageRoute(router gin.IRoutes, provider service.APIKeyTimeUsageProvider) {
	router.GET("/usage/api-key-time-usage", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "api_key_time_usage_unavailable"})
			return
		}
		query := c.Request.URL.Query()
		for name, values := range query {
			if (name != "start_date" && name != "end_date" && name != "api_key_id") || len(values) != 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": service.ErrInvalidAPIKeyTimeUsageQuery.Error()})
				return
			}
		}
		report, err := provider.GetAPIKeyTimeUsage(c.Request.Context(), dto.APIKeyTimeUsageQuery{
			StartDate: query.Get("start_date"), EndDate: query.Get("end_date"), APIKeyID: query.Get("api_key_id"),
		})
		switch {
		case errors.Is(err, service.ErrInvalidAPIKeyTimeUsageQuery):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrAPIKeyTimeUsageKeyNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case err != nil:
			logUnexpectedRequestError(c, "load api key time usage failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "api_key_time_usage_failed"})
		default:
			c.JSON(http.StatusOK, report)
		}
	})
}

// logUnexpectedRequestError records server-side failures. A request the client
// already abandoned is not an error worth alerting on.
func logUnexpectedRequestError(c *gin.Context, message string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(c.Request.Context().Err(), context.Canceled) {
		logrus.WithError(err).Debug(message)
		return
	}
	logrus.WithError(err).Error(message)
}
