package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func writeInternalError(c *gin.Context, message string, err error) {
	if err != nil {
		logUnexpectedRequestError(c, message, err)
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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
