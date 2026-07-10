package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aurora-develop/grok2api/internal/account"
	"github.com/aurora-develop/grok2api/internal/config"
	"github.com/aurora-develop/grok2api/internal/grok"
	"github.com/aurora-develop/grok2api/internal/platform"
	"github.com/aurora-develop/grok2api/internal/storage"
)

// Server bundles the dependencies every handler needs.
type Server struct {
	Repo      account.Repository
	Directory *account.Directory
	Refresh   *account.RefreshService
	Transport *grok.Transport
	Media     *storage.LocalMediaCacheStore
	ConvTracker *grok.ConversationTracker
}

// NewServer constructs a Server bound to the given dependencies.
func NewServer(repo account.Repository, dir *account.Directory, refresh *account.RefreshService, transport *grok.Transport, media *storage.LocalMediaCacheStore) *Server {
	return &Server{
		Repo:      repo,
		Directory: dir,
		Refresh:   refresh,
		Transport: transport,
		Media:     media,
		ConvTracker: grok.NewConversationTracker(10 * time.Minute),
	}
}

// Router builds the gin.Engine for the whole API surface.
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(logMiddleware())
	engine.Use(configReloadMiddleware())
	engine.Use(corsMiddleware())

	// Health/meta (no auth).
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	engine.GET("/meta", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": "1.0.0"})
	})

	// Public local media serving (no auth — file IDs are unguessable).
	engine.GET("/v1/files/image", s.handleFileImage)
	engine.GET("/v1/files/video", s.handleFileVideo)

	// OpenAI-compatible endpoints.
	v1 := engine.Group("/v1")
	v1.Use(verifyAPIKey())
	{
		v1.GET("/models", s.handleModels)
		v1.GET("/models/:id", s.handleModelGet)
		v1.POST("/chat/completions", s.handleChatCompletions)
		v1.POST("/responses", s.handleResponses)
		v1.POST("/images/generations", s.handleImageGenerations)
		v1.POST("/images/edits", s.handleImageEdits)
		v1.POST("/videos", s.handleVideoCreate)
		v1.GET("/videos/:id", s.handleVideoGet)
		v1.GET("/videos/:id/content", s.handleVideoGet)
	}

	// Anthropic-compatible endpoints.
	msg := engine.Group("/v1/messages")
	msg.Use(verifyAPIKey())
	{
		msg.POST("", s.handleMessages)
	}

	// Admin endpoints.
	admin := engine.Group("/admin/api")
	admin.Use(verifyAdminKey())
	{
		admin.GET("/verify", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "success"})
		})
		admin.GET("/config", s.handleConfigGet)
		admin.POST("/config", s.handleConfigUpdate)
		admin.GET("/storage", s.handleStorageGet)
		admin.GET("/status", s.handleStatusGet)
		admin.POST("/sync", s.handleSync)
		admin.GET("/tokens", s.handleTokensList)
		admin.POST("/tokens", s.handleTokensReplace)
		admin.POST("/tokens/add", s.handleTokensAdd)
		admin.DELETE("/tokens", s.handleTokensDelete)
		admin.DELETE("/tokens/invalid", s.handleTokensDeleteInvalid)
		admin.PUT("/tokens/edit", s.handleTokensEdit)
		admin.POST("/tokens/disabled", s.handleTokensToggleDisabled)
		admin.POST("/tokens/disabled/batch", s.handleTokensToggleDisabledBatch)
		admin.PUT("/pool", s.handlePoolReplace)
		admin.POST("/batch/nsfw", s.handleBatchNSFW)
		admin.POST("/batch/refresh", s.handleBatchRefresh)
		admin.POST("/batch/cache-clear", s.handleBatchCacheClear)
		admin.GET("/assets", s.handleAssetsList)
		admin.POST("/assets/delete-item", s.handleAssetsDeleteItem)
		admin.POST("/assets/clear-token", s.handleAssetsClearToken)
		admin.GET("/cache", s.handleCacheStats)
		admin.GET("/cache/list", s.handleCacheList)
		admin.POST("/cache/clear", s.handleCacheClear)
		admin.POST("/cache/item/delete", s.handleCacheItemDelete)
		admin.POST("/cache/items/delete", s.handleCacheItemsDelete)
	}

	return engine
}

// configReloadMiddleware re-checks the config files on every request.
func configReloadMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = config.Load()
		c.Next()
	}
}

// --- shared helpers ---

func writeJSON(c *gin.Context, status int, v any) {
	c.JSON(status, v)
}

func writeAppError(c *gin.Context, err error) {
	var appErr *platform.AppError
	if errors.As(err, &appErr) {
		c.JSON(appErr.Status, appErr.ToDict())
		return
	}
	appErr = platform.NewAppError(err.Error(), platform.ErrServer, "internal_error", 500)
	c.JSON(http.StatusInternalServerError, appErr.ToDict())
}

// readJSON decodes the request body into v using gin's binding.
func readJSON(c *gin.Context, v any) error {
	if err := c.ShouldBindJSON(v); err != nil {
		return platform.ValidationError("Invalid JSON body: "+err.Error(), "body")
	}
	return nil
}

// corsMiddleware adds permissive CORS headers.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "*")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// logMiddleware logs each request line at debug level.
func logMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

// trimLeadingSlash removes exactly one leading "/".
func trimLeadingSlash(s string) string {
	if strings.HasPrefix(s, "/") {
		return s[1:]
	}
	return s
}

// marshalJSON is a helper for manual JSON marshalling in admin handlers.
func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
