package api

import (
	"cpa-usage-keeper/internal/ingest"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

func registerIngestAdminRoutes(router gin.IRoutes, receiver *ingest.Receiver) {
	router.GET("/ingest/quarantine", func(c *gin.Context) {
		rows, err := receiver.Quarantine(c.Request.Context())
		if err != nil {
			c.JSON(503, gin.H{"error": "ingest quarantine unavailable"})
			return
		}
		c.JSON(200, gin.H{"items": rows})
	})
	router.POST("/ingest/quarantine/:id/retry", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(400, gin.H{"error": "invalid quarantine ID"})
			return
		}
		ok, err := receiver.Retry(c.Request.Context(), id)
		if err != nil {
			c.JSON(503, gin.H{"error": "retry unavailable"})
			return
		}
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
