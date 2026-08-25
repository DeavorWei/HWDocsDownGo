package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type Config struct {
	Port               int    `json:"port"`
	DownloadDir        string `json:"downloadDir"`
	MaxConcurrent      int    `json:"maxConcurrent"`
	FileThreads        int    `json:"fileThreads"` // 单文件多线程数 1-32，默认 1
	RequestDelayMs     int    `json:"requestDelayMs"`
	CustomCookie       string `json:"customCookie"`
	AutoSyncCategories bool   `json:"autoSyncCategories"`
	LogLevel           string `json:"logLevel"` // debug | info | warn | error, 默认 warn
	DBPath             string `json:"dbPath"`
}

var (
	GlobalConfig *Config
	configLock   sync.RWMutex
)

// InitConfig 初始化全局配置
func InitConfig(repo *store.Repository) *Config {
	configLock.Lock()
	defer configLock.Unlock()

	// 数据根目录为 HWDDGoData
	dataRootDir := filepath.Join(".", "HWDDGoData")
	defaultDownloadDir := filepath.Join(dataRootDir, "Downloads")
	if abs, err := filepath.Abs(defaultDownloadDir); err == nil {
		defaultDownloadDir = abs
	}
	os.MkdirAll(defaultDownloadDir, 0755)

	dbPath := filepath.Join(dataRootDir, "hwdocs.db")
	if abs, err := filepath.Abs(dbPath); err == nil {
		dbPath = abs
	}

	logLevel := repo.GetSetting("log_level", "warn")
	if logLevel == "" {
		logLevel = "warn"
	}

	cfg := &Config{
		Port:               8088,
		DownloadDir:        repo.GetSetting("download_dir", defaultDownloadDir),
		MaxConcurrent:      getIntSetting(repo, "max_concurrent", 3),
		FileThreads:        getIntSetting(repo, "file_threads", 1),
		RequestDelayMs:     getIntSetting(repo, "request_delay_ms", 500),
		CustomCookie:       repo.GetSetting("custom_cookie", ""),
		AutoSyncCategories: getBoolSetting(repo, "auto_sync_categories", true),
		LogLevel:           logLevel,
		DBPath:             dbPath,
	}

	GlobalConfig = cfg
	return cfg
}

func getIntSetting(repo *store.Repository, key string, defaultVal int) int {
	str := repo.GetSetting(key, "")
	if str == "" {
		return defaultVal
	}
	if v, err := strconv.Atoi(str); err == nil && v > 0 {
		return v
	}
	return defaultVal
}

func getBoolSetting(repo *store.Repository, key string, defaultVal bool) bool {
	str := repo.GetSetting(key, "")
	if str == "" {
		return defaultVal
	}
	return str == "1" || str == "true"
}

func GetConfig() *Config {
	configLock.RLock()
	configLock.RUnlock()
	return GlobalConfig
}

func UpdateConfig(repo *store.Repository, downloadDir string, maxConcurrent, delayMs int, cookie string, autoSync bool, logLevel string, fileThreads int) {
	configLock.Lock()
	defer configLock.Unlock()

	if downloadDir != "" {
		GlobalConfig.DownloadDir = downloadDir
		repo.SetSetting("download_dir", downloadDir)
		os.MkdirAll(downloadDir, 0755)
	}
	if maxConcurrent > 0 {
		GlobalConfig.MaxConcurrent = maxConcurrent
		repo.SetSetting("max_concurrent", strconv.Itoa(maxConcurrent))
	}
	if fileThreads >= 1 && fileThreads <= 32 {
		GlobalConfig.FileThreads = fileThreads
		repo.SetSetting("file_threads", strconv.Itoa(fileThreads))
	}
	if delayMs >= 0 {
		GlobalConfig.RequestDelayMs = delayMs
		repo.SetSetting("request_delay_ms", strconv.Itoa(delayMs))
	}
	GlobalConfig.CustomCookie = cookie
	repo.SetSetting("custom_cookie", cookie)

	GlobalConfig.AutoSyncCategories = autoSync
	if autoSync {
		repo.SetSetting("auto_sync_categories", "true")
	} else {
		repo.SetSetting("auto_sync_categories", "false")
	}

	if logLevel != "" {
		GlobalConfig.LogLevel = strings.ToLower(logLevel)
		repo.SetSetting("log_level", GlobalConfig.LogLevel)
		logger.SetLevel(GlobalConfig.LogLevel)
	}
}
