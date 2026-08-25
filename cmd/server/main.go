package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/api"
	"hwdocsdown/internal/config"
	"hwdocsdown/internal/crawler"
	"hwdocsdown/internal/downloader"
	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/scanner"
	"hwdocsdown/internal/store"
	"hwdocsdown/web"
)

func main() {
	port := flag.Int("port", 8088, "HTTP Server 监听端口")
	dbPath := flag.String("db", filepath.Join(".", "HWDDGoData", "hwdocs.db"), "SQLite 数据库路径")
	logDir := flag.String("log", filepath.Join(".", "HWDDGoData", "logs"), "日志保存目录")
	debug := flag.Bool("debug", false, "是否开启 Debug 调试日志")
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开浏览器")
	flag.Parse()

	// 1. 初始化 Zap 高性能日志记录器 (启动转储 log.log 为 日期+时间.log，默认级别 info)
	_, err := logger.InitLogger(*logDir, *debug, "info")
	if err != nil {
		fmt.Printf("初始化日志系统失败: %v\n", err)
		os.Exit(1)
	}

	logger.Info("==========================================================")
	logger.Info("     华为产品文档下载管理器 - HWDocsDownGo 正在启动...     ")
	logger.Info("==========================================================")
	logger.Debug("命令行参数配置",
		zap.Int("port", *port),
		zap.String("dbPath", *dbPath),
		zap.String("logDir", *logDir),
		zap.Bool("debug", *debug),
		zap.Bool("noBrowser", *noBrowser),
	)

	// 2. 初始化纯 Go SQLite 数据库
	db, err := store.InitDB(*dbPath)
	if err != nil {
		logger.Error("初始化数据库失败", zap.String("dbPath", *dbPath), zap.Error(err))
		os.Exit(1)
	}
	repo := store.NewRepository(db)

	// 3. 初始化全局系统配置并同步日志级别
	cfg := config.InitConfig(repo)
	if *port != 8088 {
		cfg.Port = *port
	}
	if !*debug && cfg.LogLevel != "" {
		logger.SetLevel(cfg.LogLevel)
	}
	logger.Info("系统配置加载完成",
		zap.String("downloadDir", cfg.DownloadDir),
		zap.Int("maxConcurrent", cfg.MaxConcurrent),
		zap.Int("fileThreads", cfg.FileThreads),
		zap.Int("crawlerThreads", cfg.CrawlerThreads),
		zap.String("logLevel", cfg.LogLevel),
		zap.Bool("autoSyncCategories", cfg.AutoSyncCategories),
	)

	// 4. 初始化爬虫引擎与业务模块
	httpClient := crawler.NewHttpClient()
	catCrawler := crawler.NewCategoryCrawler(httpClient, repo)
	docCrawler := crawler.NewDocCrawler(httpClient, repo)
	crawlerEngine := crawler.NewCrawlerEngine(catCrawler, docCrawler, repo)
	downManager := downloader.NewDownloadManager(repo, docCrawler)
	localScanner := scanner.NewLocalScanner(repo)

	// 5. 初始化 HTTP 服务与嵌入式 Web UI
	handler := api.NewServerHandler(repo, catCrawler, docCrawler, crawlerEngine, downManager, localScanner)
	staticFS := web.GetStaticFS()
	router := api.SetupRouter(handler, staticFS)

	// 6. 自动在后台同步产品大类、二级产品线与产品型号 (首次启动或设置开关开启时)
	cats, _ := repo.GetAllCategories()
	if cfg.AutoSyncCategories || len(cats) == 0 {
		go func() {
			time.Sleep(500 * time.Millisecond)
			logger.Info("正在后台自动同步产品大类、二级产品线与型号数据...")
			_ = catCrawler.SyncAllCategoriesAndProducts(func(st crawler.CategorySyncStatus) {
				handler.BroadcastCategorySyncProgress(st)
			}, func(st crawler.CategorySyncStatus) {
				handler.BroadcastCategorySyncFinished(st)
			})
		}()
	}

	// 7. 自动扫描本地下载目录并打标已下载文档
	if cfg.DownloadDir != "" {
		go func() {
			time.Sleep(1 * time.Second)
			logger.Info("自动扫描本地下载目录", zap.String("downloadDir", cfg.DownloadDir))
			localScanner.ScanDirectory(cfg.DownloadDir)
		}()
	}

	serverAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	srv := &http.Server{
		Addr:    serverAddr,
		Handler: router,
	}

	// 7. 异步启动 Web 服务
	go func() {
		logger.Info("Web GUI 管理服务已启动", zap.String("url", "http://"+serverAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP 服务启动失败", zap.Error(err))
			os.Exit(1)
		}
	}()

	// 8. 自动在 Windows 默认浏览器打开
	if !*noBrowser {
		go func() {
			time.Sleep(800 * time.Millisecond)
			openBrowser(fmt.Sprintf("http://%s", serverAddr))
		}()
	}

	// 9. 优雅退出信号监听
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("正在关闭服务...")
	logger.Info("华为产品文档下载管理器已安全退出。")
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("cmd", "/c", "start", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		logger.Warn("自动打开浏览器失败，请手动访问", zap.String("url", url), zap.Error(err))
	}
}
