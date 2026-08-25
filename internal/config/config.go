package config

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"hwdocsdown/internal/store"
)

type Config struct {
	Port           int    `json:"port"`
	DownloadDir    string `json:"downloadDir"`
	MaxConcurrent  int    `json:"maxConcurrent"`
	RequestDelayMs int    `json:"requestDelayMs"`
	CustomCookie   string `json:"customCookie"`
	DBPath         string `json:"dbPath"`
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

	cfg := &Config{
		Port:           8088,
		DownloadDir:    repo.GetSetting("download_dir", defaultDownloadDir),
		MaxConcurrent:  getIntSetting(repo, "max_concurrent", 3),
		RequestDelayMs: getIntSetting(repo, "request_delay_ms", 500),
		CustomCookie:   repo.GetSetting("custom_cookie", ""),
		DBPath:         dbPath,
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

func GetConfig() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	return GlobalConfig
}

func UpdateConfig(repo *store.Repository, downloadDir string, maxConcurrent, delayMs int, cookie string) {
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
	if delayMs >= 0 {
		GlobalConfig.RequestDelayMs = delayMs
		repo.SetSetting("request_delay_ms", strconv.Itoa(delayMs))
	}
	GlobalConfig.CustomCookie = cookie
	repo.SetSetting("custom_cookie", cookie)
}
