package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"hwdocsdown/internal/config"
	"hwdocsdown/internal/crawler"
	"hwdocsdown/internal/downloader"
	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/scanner"
	"hwdocsdown/internal/store"
)

type ServerHandler struct {
	repo          *store.Repository
	catCrawler    *crawler.CategoryCrawler
	docCrawler    *crawler.DocCrawler
	crawlerEngine *crawler.CrawlerEngine
	downManager   *downloader.DownloadManager
	localScanner  *scanner.LocalScanner
	wsUpgrader    websocket.Upgrader
	wsClients     map[*websocket.Conn]bool
	wsMu          sync.Mutex
}

func NewServerHandler(
	repo *store.Repository,
	catCrawler *crawler.CategoryCrawler,
	docCrawler *crawler.DocCrawler,
	crawlerEngine *crawler.CrawlerEngine,
	downManager *downloader.DownloadManager,
	localScanner *scanner.LocalScanner,
) *ServerHandler {
	h := &ServerHandler{
		repo:          repo,
		catCrawler:    catCrawler,
		docCrawler:    docCrawler,
		crawlerEngine: crawlerEngine,
		downManager:   downManager,
		localScanner:  localScanner,
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		wsClients: make(map[*websocket.Conn]bool),
	}

	// 监听下载管理器的进度并推送到 WebSocket
	downManager.Subscribe(func(event downloader.ProgressEvent) {
		h.broadcastWS(map[string]interface{}{
			"type": "DOWNLOAD_PROGRESS",
			"data": event,
		})
	})

	// 监听爬虫结束事件并广播
	crawlerEngine.SetOnFinished(func(success bool, msg string) {
		logger.Info("爬虫引擎任务完成", zap.Bool("success", success), zap.String("msg", msg))
		h.broadcastWS(map[string]interface{}{
			"type": "CRAWLER_FINISHED",
			"data": gin.H{
				"isBusy":  false,
				"success": success,
				"msg":     msg,
			},
		})
	})

	return h
}

func success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    data,
	})
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{
		"code":    code,
		"message": msg,
		"data":    nil,
	})
}

// GetCategories 获取大类及二级产品线
func (h *ServerHandler) GetCategories(c *gin.Context) {
	cats, err := h.repo.GetAllCategories()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	success(c, cats)
}

// GetProducts 获取产品线下的产品系列
func (h *ServerHandler) GetProducts(c *gin.Context) {
	lineID := c.Query("lineId")
	if lineID == "" {
		fail(c, 400, "lineId 不能为空")
		return
	}
	prods, err := h.repo.GetProductsByProductLineID(lineID)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	success(c, prods)
}

// GetSubModelsAndVersions 获取产品下的子型号和版本
func (h *ServerHandler) GetSubModelsAndVersions(c *gin.Context) {
	productID := c.Query("productId")
	if productID == "" {
		fail(c, 400, "productId 不能为空")
		return
	}
	subModels, versions, err := h.repo.GetSubModelsAndVersions(productID)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	success(c, gin.H{
		"subModels": subModels,
		"versions":  versions,
	})
}

// GetDocCategories 获取资料分类/标签列表
func (h *ServerHandler) GetDocCategories(c *gin.Context) {
	productID := c.Query("productId")
	lineID := c.Query("lineId")
	categoryID := c.Query("categoryId")
	cats, err := h.repo.GetDocCategories(productID, lineID, categoryID)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	success(c, cats)
}

// QueryDocuments 多条件筛选文档
func (h *ServerHandler) QueryDocuments(c *gin.Context) {
	var q store.DocFilterQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		fail(c, 400, "参数格式错误")
		return
	}
	res, err := h.repo.QueryDocuments(q)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	success(c, res)
}

// AddDownloadTask 添加下载任务
func (h *ServerHandler) AddDownloadTask(c *gin.Context) {
	var req struct {
		NIDs []string `json:"nids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.NIDs) == 0 {
		logger.Warn("API 添加下载任务失败: 参数为空或格式错误")
		fail(c, 400, "请提供要下载的文档 NID 列表")
		return
	}

	logger.Info("API 接收到添加下载任务请求", zap.Int("nidCount", len(req.NIDs)))
	var addedTasks []*store.DownloadTask
	for _, nid := range req.NIDs {
		doc, err := h.repo.GetDocumentByNID(nid)
		if err == nil && doc != nil {
			t, err := h.downManager.AddTask(doc)
			if err == nil {
				addedTasks = append(addedTasks, t)
			}
		} else {
			logger.Warn("未在数据库中找到文档记录", zap.String("nid", nid))
		}
	}

	success(c, gin.H{
		"addedCount": len(addedTasks),
		"tasks":      addedTasks,
	})
}

// GetAllTasks 获取所有下载任务
func (h *ServerHandler) GetAllTasks(c *gin.Context) {
	tasks, err := h.repo.GetAllDownloadTasks()
	if err != nil {
		logger.Error("API 获取下载任务失败", zap.Error(err))
		fail(c, 500, err.Error())
		return
	}
	success(c, tasks)
}

// PauseTask 暂停任务
func (h *ServerHandler) PauseTask(c *gin.Context) {
	taskID := c.Param("id")
	logger.Info("API 触发暂停下载任务", zap.String("taskId", taskID))
	h.downManager.PauseTask(taskID)
	success(c, true)
}

// ResumeTask 继续任务
func (h *ServerHandler) ResumeTask(c *gin.Context) {
	taskID := c.Param("id")
	logger.Info("API 触发恢复下载任务", zap.String("taskId", taskID))
	err := h.downManager.ResumeTask(taskID)
	if err != nil {
		logger.Error("API 恢复下载任务失败", zap.String("taskId", taskID), zap.Error(err))
		fail(c, 500, err.Error())
		return
	}
	success(c, true)
}

// DeleteTask 删除任务
func (h *ServerHandler) DeleteTask(c *gin.Context) {
	taskID := c.Param("id")
	logger.Info("API 触发删除下载任务", zap.String("taskId", taskID))
	h.downManager.CancelTask(taskID)
	success(c, true)
}

// TriggerLocalScan 触发本地扫描与自动打标
func (h *ServerHandler) TriggerLocalScan(c *gin.Context) {
	var req struct {
		DirPath string `json:"dirPath"`
	}
	c.ShouldBindJSON(&req)

	cfg := config.GetConfig()
	dirPath := req.DirPath
	if dirPath == "" {
		dirPath = cfg.DownloadDir
	}
	logger.Info("API 接收到触发本地扫描请求", zap.String("dirPath", dirPath))

	res, err := h.localScanner.ScanDirectory(dirPath)
	if err != nil {
		logger.Error("API 本地目录扫描执行失败", zap.String("dirPath", dirPath), zap.Error(err))
		fail(c, 500, "本地目录扫描失败: "+err.Error())
		return
	}
	success(c, res)
}

// StartCrawl 启动在线爬虫（支持全量、大类、产品线或单个型号）
func (h *ServerHandler) StartCrawl(c *gin.Context) {
	var req struct {
		CategoryID string `json:"categoryId"`
		LineID     string `json:"lineId"`
		ProductID  string `json:"productId"`
	}
	c.ShouldBindJSON(&req)
	logger.Info("API 接收到启动爬虫请求",
		zap.String("categoryId", req.CategoryID),
		zap.String("lineId", req.LineID),
		zap.String("productId", req.ProductID),
	)

	onLog := func(msg string) {
		h.broadcastWS(map[string]interface{}{
			"type": "CRAWLER_LOG",
			"data": msg,
		})
	}

	onProgress := func(cur, tot int, item string) {
		h.broadcastWS(map[string]interface{}{
			"type": "CRAWLER_PROGRESS",
			"data": gin.H{
				"current": cur,
				"total":   tot,
				"item":    item,
			},
		})
	}

	if req.CategoryID != "" || req.LineID != "" || req.ProductID != "" {
		err := h.crawlerEngine.StartScopedCrawl(req.CategoryID, req.LineID, req.ProductID, onLog, onProgress)
		if err != nil {
			logger.Warn("API 启动定向爬取失败", zap.Error(err))
			fail(c, 400, err.Error())
			return
		}
		success(c, "已启动定向爬取任务")
		return
	}

	err := h.crawlerEngine.StartFullCrawl(onLog, onProgress)
	if err != nil {
		logger.Warn("API 启动全量爬取失败", zap.Error(err))
		fail(c, 400, err.Error())
		return
	}
	success(c, "已启动全量深度文档爬取")
}

// StopCrawl 停止爬虫
func (h *ServerHandler) StopCrawl(c *gin.Context) {
	logger.Info("API 接收到停止爬虫请求")
	h.crawlerEngine.Stop()
	success(c, "已发送停止爬虫指令")
}

// GetCrawlStatus 获取爬虫运行状态
func (h *ServerHandler) GetCrawlStatus(c *gin.Context) {
	success(c, gin.H{
		"isBusy": h.crawlerEngine.IsBusy(),
	})
}

// GetStatistics 获取统计
func (h *ServerHandler) GetStatistics(c *gin.Context) {
	stats, err := h.repo.GetStatistics()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	success(c, stats)
}

// GetSettings 获取配置
func (h *ServerHandler) GetSettings(c *gin.Context) {
	cfg := config.GetConfig()
	success(c, cfg)
}

// GetCategorySyncStatus 获取分类树同步状态
func (h *ServerHandler) GetCategorySyncStatus(c *gin.Context) {
	st := h.catCrawler.GetSyncStatus()
	success(c, st)
}

// StartCategorySync 手动触发产品分类树同步
func (h *ServerHandler) StartCategorySync(c *gin.Context) {
	if h.catCrawler.GetSyncStatus().IsSyncing {
		logger.Warn("API 触发同步分类树被拒: 当前已有同步正在进行中")
		fail(c, 400, "产品分类树正在同步中，请稍候")
		return
	}
	logger.Info("API 接收到手动触发产品分类树同步请求")
	go func() {
		h.catCrawler.SyncAllCategoriesAndProducts(func(st crawler.CategorySyncStatus) {
			h.broadcastWS(map[string]interface{}{
				"type": "CATEGORY_SYNC_PROGRESS",
				"data": st,
			})
		}, func(st crawler.CategorySyncStatus) {
			h.broadcastWS(map[string]interface{}{
				"type": "CATEGORY_SYNC_FINISHED",
				"data": st,
			})
		})
	}()
	success(c, "已启动产品分类树同步")
}

// UpdateSettings 更新配置
func (h *ServerHandler) UpdateSettings(c *gin.Context) {
	var req struct {
		DownloadDir        string `json:"downloadDir"`
		MaxConcurrent      int    `json:"maxConcurrent"`
		FileThreads        int    `json:"fileThreads"`
		CrawlerThreads     int    `json:"crawlerThreads"`
		RequestDelayMs     int    `json:"requestDelayMs"`
		CustomCookie       string `json:"customCookie"`
		AutoSyncCategories bool   `json:"autoSyncCategories"`
		LogLevel           string `json:"logLevel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("API 更新配置失败: 参数解析错误", zap.Error(err))
		fail(c, 400, "参数格式错误")
		return
	}
	logger.Info("API 保存系统配置",
		zap.String("downloadDir", req.DownloadDir),
		zap.Int("maxConcurrent", req.MaxConcurrent),
		zap.Int("fileThreads", req.FileThreads),
		zap.Int("crawlerThreads", req.CrawlerThreads),
		zap.String("logLevel", req.LogLevel),
	)
	config.UpdateConfig(h.repo, req.DownloadDir, req.MaxConcurrent, req.RequestDelayMs, req.CustomCookie, req.AutoSyncCategories, req.LogLevel, req.FileThreads, req.CrawlerThreads)
	success(c, config.GetConfig())
}

// OpenFolder 在 Windows 资源管理器中打开指定目录或定位文件
func (h *ServerHandler) OpenFolder(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		fail(c, 400, "请提供有效路径")
		return
	}

	cleanPath := filepath.Clean(req.Path)
	logger.Info("API 在 Windows 资源管理器中定位文件/目录", zap.String("path", cleanPath))
	cmd := exec.Command("explorer.exe", "/select,", cleanPath)
	if err := cmd.Start(); err != nil {
		exec.Command("explorer.exe", filepath.Dir(cleanPath)).Start()
	}
	success(c, true)
}

// HandleWebSocket 处理 WebSocket 客户端连接
func (h *ServerHandler) HandleWebSocket(c *gin.Context) {
	conn, err := h.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	h.wsMu.Lock()
	h.wsClients[conn] = true
	h.wsMu.Unlock()

	defer func() {
		h.wsMu.Lock()
		delete(h.wsClients, conn)
		h.wsMu.Unlock()
		conn.Close()
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (h *ServerHandler) BroadcastCategorySyncProgress(st crawler.CategorySyncStatus) {
	h.broadcastWS(map[string]interface{}{
		"type": "CATEGORY_SYNC_PROGRESS",
		"data": st,
	})
}

func (h *ServerHandler) BroadcastCategorySyncFinished(st crawler.CategorySyncStatus) {
	h.broadcastWS(map[string]interface{}{
		"type": "CATEGORY_SYNC_FINISHED",
		"data": st,
	})
}

func (h *ServerHandler) broadcastWS(data interface{}) {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	for conn := range h.wsClients {
		conn.WriteJSON(data)
	}
}

// Shutdown 优雅关闭系统：终止爬虫与下载，并退出主进程
func (h *ServerHandler) Shutdown(c *gin.Context) {
	logger.Info("📢 收到 Web 端退出系统请求，正在执行优雅停机...")

	// 1. 停止爬虫任务
	if h.crawlerEngine != nil {
		h.crawlerEngine.Stop()
	}

	// 2. 终止所有下载任务
	if h.downManager != nil {
		h.downManager.StopAll()
	}

	// 3. 广播停机通知给所有 WebSocket 客户端
	h.broadcastWS(map[string]interface{}{
		"type": "SYSTEM_SHUTDOWN",
		"data": gin.H{"message": "系统正在安全关闭"},
	})

	// 4. 返回 HTTP 成功响应
	success(c, gin.H{
		"message": "系统正在优雅关闭，所有正在运行的任务已终止",
	})

	// 5. 延迟异步退出进程，确保 HTTP/WS 响应正常发出
	go func() {
		time.Sleep(500 * time.Millisecond)
		logger.Info("==========================================================")
		logger.Info("     华为产品文档下载管理器 - HWDocsDownGo 已安全退出     ")
		logger.Info("==========================================================")
		_ = logger.Sync()
		os.Exit(0)
	}()
}
