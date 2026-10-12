package api

import (
	"context"
	"errors"
	"net/http"

	"cpa-usage-keeper/internal/workhours"
	"github.com/gin-gonic/gin"
)

type WorkHoursProvider interface {
	GetWorkHours(context.Context) (workhours.Schedule, error)
	UpdateWorkHours(context.Context, string, string) (workhours.Schedule, error)
}

func registerWorkHoursRoutes(router gin.IRoutes, provider WorkHoursProvider) {
	router.GET("/usage/work-hours", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "work_hours_unavailable"})
			return
		}
		schedule, err := provider.GetWorkHours(c.Request.Context())
		if err != nil {
			logUnexpectedRequestError(c, "load work hours failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "work_hours_failed"})
			return
		}
		c.JSON(http.StatusOK, schedule)
	})
	router.PUT("/usage/work-hours", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "work_hours_unavailable"})
			return
		}
		var body struct {
			Start string `json:"start"`
			End   string `json:"end"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": workhours.ErrInvalid.Error()})
			return
		}
		schedule, err := provider.UpdateWorkHours(c.Request.Context(), body.Start, body.End)
		switch {
		case errors.Is(err, workhours.ErrInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"error": workhours.ErrInvalid.Error()})
		case err != nil:
			logUnexpectedRequestError(c, "save work hours failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "work_hours_failed"})
		default:
			c.JSON(http.StatusOK, schedule)
		}
	})
}
