package api

import (
	"errors"
	"net/http"
	"strconv"

	"cpa-usage-keeper/internal/ingest"
	"github.com/gin-gonic/gin"
)

// ingestUploadPath 是 CPA 插件上传的机器接口相对路径；页面拼上浏览器地址和 base path 后展示给管理员。
const ingestUploadPath = "/api/v1/ingest/usage"

type pushSettingsResponse struct {
	Enabled    bool            `json:"enabled"`
	UploadPath string          `json:"upload_path"`
	Sources    []ingest.Source `json:"sources"`
}

// registerPushSourceRoutes 挂在管理员路由组下：写操作同时受会话和请求意图头保护。
// token 明文只出现在创建和更换的响应里，日志与列表都不包含。
func registerPushSourceRoutes(router gin.IRoutes, basePath string, sources *ingest.Sources) {
	settings := func(c *gin.Context) {
		items, err := sources.List(c.Request.Context())
		if err != nil {
			logUnexpectedRequestError(c, "load push sources failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "push_settings_failed"})
			return
		}
		c.JSON(http.StatusOK, pushSettingsResponse{Enabled: sources.Enabled(), UploadPath: basePath + ingestUploadPath, Sources: items})
	}
	router.GET("/ingest/settings", func(c *gin.Context) {
		setNoStoreHeaders(c)
		settings(c)
	})
	router.PUT("/ingest/settings", func(c *gin.Context) {
		setNoStoreHeaders(c)
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_push_settings"})
			return
		}
		if err := sources.SetEnabled(c.Request.Context(), *body.Enabled); err != nil {
			logUnexpectedRequestError(c, "save push settings failed", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "push_settings_failed"})
			return
		}
		settings(c)
	})
	router.POST("/ingest/sources", func(c *gin.Context) {
		setNoStoreHeaders(c)
		var body struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_push_source_name"})
			return
		}
		issued, err := sources.Create(c.Request.Context(), body.Name)
		if err != nil {
			writePushSourceError(c, "create push source failed", err)
			return
		}
		c.JSON(http.StatusCreated, issued)
	})
	router.PATCH("/ingest/sources/:id", func(c *gin.Context) {
		setNoStoreHeaders(c)
		id, ok := pushSourceID(c)
		if !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_push_source_name"})
			return
		}
		source, err := sources.Rename(c.Request.Context(), id, body.Name)
		if err != nil {
			writePushSourceError(c, "rename push source failed", err)
			return
		}
		c.JSON(http.StatusOK, source)
	})
	router.POST("/ingest/sources/:id/rotate", func(c *gin.Context) {
		setNoStoreHeaders(c)
		id, ok := pushSourceID(c)
		if !ok {
			return
		}
		issued, err := sources.Rotate(c.Request.Context(), id)
		if err != nil {
			writePushSourceError(c, "rotate push source token failed", err)
			return
		}
		c.JSON(http.StatusOK, issued)
	})
	router.POST("/ingest/sources/:id/revoke", func(c *gin.Context) {
		setNoStoreHeaders(c)
		id, ok := pushSourceID(c)
		if !ok {
			return
		}
		source, err := sources.Revoke(c.Request.Context(), id)
		if err != nil {
			writePushSourceError(c, "revoke push source failed", err)
			return
		}
		c.JSON(http.StatusOK, source)
	})
	router.POST("/ingest/sources/:id/restore", func(c *gin.Context) {
		setNoStoreHeaders(c)
		id, ok := pushSourceID(c)
		if !ok {
			return
		}
		source, err := sources.Restore(c.Request.Context(), id)
		if err != nil {
			writePushSourceError(c, "restore push source failed", err)
			return
		}
		c.JSON(http.StatusOK, source)
	})
	router.DELETE("/ingest/sources/:id", func(c *gin.Context) {
		setNoStoreHeaders(c)
		id, ok := pushSourceID(c)
		if !ok {
			return
		}
		if err := sources.Delete(c.Request.Context(), id); err != nil {
			writePushSourceError(c, "delete push source failed", err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func pushSourceID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_push_source_id"})
		return 0, false
	}
	return id, true
}

func writePushSourceError(c *gin.Context, message string, err error) {
	switch {
	case errors.Is(err, ingest.ErrInvalidSourceName):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_push_source_name"})
	case errors.Is(err, ingest.ErrSourceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "push_source_not_found"})
	case errors.Is(err, ingest.ErrSourceActive):
		c.JSON(http.StatusConflict, gin.H{"error": "push_source_active"})
	default:
		logUnexpectedRequestError(c, message, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "push_settings_failed"})
	}
}
