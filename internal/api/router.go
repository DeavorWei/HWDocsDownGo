package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SetupRouter 配置 Gin HTTP 路由
func SetupRouter(h *ServerHandler, staticFS fs.FS) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// CORS 中间件
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// API 路由组
	apiGroup := r.Group("/api")
	{
		apiGroup.GET("/categories", h.GetCategories)
		apiGroup.GET("/products", h.GetProducts)
		apiGroup.GET("/submodels-versions", h.GetSubModelsAndVersions)
		apiGroup.GET("/doc-categories", h.GetDocCategories)
		apiGroup.GET("/documents", h.QueryDocuments)
		apiGroup.POST("/download", h.AddDownloadTask)
		apiGroup.GET("/tasks", h.GetAllTasks)
		apiGroup.POST("/tasks/:id/pause", h.PauseTask)
		apiGroup.POST("/tasks/:id/resume", h.ResumeTask)
		apiGroup.DELETE("/tasks/:id", h.DeleteTask)
		apiGroup.POST("/scan", h.TriggerLocalScan)

		// 爬虫接口
		apiGroup.POST("/crawl/start", h.StartCrawl)
		apiGroup.POST("/crawl/stop", h.StopCrawl)
		apiGroup.GET("/crawl/status", h.GetCrawlStatus)
		apiGroup.GET("/category-sync/status", h.GetCategorySyncStatus)
		apiGroup.POST("/category-sync/start", h.StartCategorySync)

		apiGroup.GET("/statistics", h.GetStatistics)
		apiGroup.GET("/settings", h.GetSettings)
		apiGroup.POST("/settings", h.UpdateSettings)
		apiGroup.POST("/open-folder", h.OpenFolder)
		apiGroup.POST("/system/shutdown", h.Shutdown)
	}

	// WebSocket 端点
	r.GET("/ws", h.HandleWebSocket)

	// 嵌入式静态资源与 SPA 页面
	if staticFS != nil {
		indexHTML, _ := fs.ReadFile(staticFS, "index.html")
		r.NoRoute(func(c *gin.Context) {
			path := strings.TrimPrefix(c.Request.URL.Path, "/")
			if path != "" && path != "index.html" {
				if data, err := fs.ReadFile(staticFS, path); err == nil {
					c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
					return
				}
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
		})
	}

	return r
}
