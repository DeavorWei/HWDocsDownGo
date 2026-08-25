package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	L           *zap.Logger
	Sugar       *zap.SugaredLogger
	atomicLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	listeners   []func(level string, msg string)
	listenerMu  sync.RWMutex
	currentDir  string
)

// InitLogger 初始化 Zap 高性能日志记录器
// 1. 每次程序启动则创建一个新的 log.log 文件，旧的 log.log 按照 日期+时间 (YYYY-MM-DD_HH-mm-ss.log) 进行重命名转储
// 2. 单个日志文件最大为 10MB，超过 10MB 转储
// 3. 日志默认保留 1024MB 以及 180 天，超过则从最旧的文件开始删除
// 4. 支持动态日志级别控制，默认为 info
func InitLogger(logDir string, debug bool, initialLevel string) (*zap.Logger, error) {
	if logDir == "" {
		logDir = filepath.Join(".", "HWDDGoData", "logs")
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	currentDir = logDir

	logFilePath := filepath.Join(logDir, "log.log")

	// 1. 启动时转储现有的 log.log 为 日期+时间.log (精确到秒)
	ArchiveOldLogOnStartup(logDir, logFilePath)

	// 2. 检查并清理超过 180 天或总大小超过 1024MB 的旧日志
	CleanOldLogs(logDir, 1024, 180)

	// 3. 文件滚动配置：单个文件最大 10MB，最多保留 180 天
	lumberJackLogger := &lumberjack.Logger{
		Filename: logFilePath,
		MaxSize:  10,  // 单个日志文件最大 10 MB
		MaxAge:   180, // 最多保留 180 天
		Compress: false,
	}

	// 4. 编码器配置
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder, // 控制台带彩色
		EncodeTime:     customTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	fileEncoderConfig := encoderConfig
	fileEncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// 默认级别为 info
	lvl := zapcore.InfoLevel
	if debug {
		lvl = zapcore.DebugLevel
	} else if initialLevel != "" {
		lvl = parseLogLevel(initialLevel)
	}
	atomicLevel.SetLevel(lvl)

	// 核心输出
	fileCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(fileEncoderConfig),
		zapcore.AddSync(lumberJackLogger),
		atomicLevel,
	)

	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		atomicLevel,
	)

	core := zapcore.NewTee(fileCore, consoleCore)
	loggerInstance := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	L = loggerInstance
	Sugar = loggerInstance.Sugar()

	// 启动后台定时检查清理（每 1 小时检查清理一次）
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			CleanOldLogs(logDir, 1024, 180)
		}
	}()

	return loggerInstance, nil
}

// ArchiveOldLogOnStartup 启动时将旧的 log.log 转储命名为 日期+时间.log (精确到秒)
func ArchiveOldLogOnStartup(logDir, logFilePath string) {
	info, err := os.Stat(logFilePath)
	if err != nil || os.IsNotExist(err) {
		return
	}
	if info.Size() > 0 {
		modTime := info.ModTime()
		baseName := modTime.Format("2006-01-02_15-04-05")
		archiveName := baseName + ".log"
		archivePath := filepath.Join(logDir, archiveName)

		counter := 1
		for {
			if _, err := os.Stat(archivePath); os.IsNotExist(err) {
				break
			}
			archiveName = fmt.Sprintf("%s-%d.log", baseName, counter)
			archivePath = filepath.Join(logDir, archiveName)
			counter++
		}

		_ = os.Rename(logFilePath, archivePath)
	}
}

type logFileInfo struct {
	path    string
	size    int64
	modTime time.Time
}

// CleanOldLogs 清理旧日志：
// 1. 删除修改时间超过 maxAgeDays (默认180天) 的旧日志
// 2. 若目录内日志总大小超过 maxTotalMB (默认1024MB)，按修改时间从最旧到最新依次删除，直到总大小 <= maxTotalMB
func CleanOldLogs(logDir string, maxTotalMB int64, maxAgeDays int) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}

	now := time.Now()
	maxAgeDuration := time.Duration(maxAgeDays) * 24 * time.Hour
	maxTotalBytes := maxTotalMB * 1024 * 1024

	var archiveLogs []logFileInfo
	var totalBytes int64

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.gz") {
			continue
		}

		filePath := filepath.Join(logDir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// 排除当前活动的 log.log
		if name != "log.log" {
			// 1. 超过 maxAgeDays 则删除
			if now.Sub(info.ModTime()) > maxAgeDuration {
				_ = os.Remove(filePath)
				continue
			}
			archiveLogs = append(archiveLogs, logFileInfo{
				path:    filePath,
				size:    info.Size(),
				modTime: info.ModTime(),
			})
		}
		totalBytes += info.Size()
	}

	// 2. 若总大小超过设定阈值，从最旧的归档文件开始删除
	if totalBytes > maxTotalBytes && len(archiveLogs) > 0 {
		sort.Slice(archiveLogs, func(i, j int) bool {
			return archiveLogs[i].modTime.Before(archiveLogs[j].modTime)
		})

		for _, file := range archiveLogs {
			if totalBytes <= maxTotalBytes {
				break
			}
			if err := os.Remove(file.path); err == nil {
				totalBytes -= file.size
			}
		}
	}
}

func parseLogLevel(levelStr string) zapcore.Level {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.WarnLevel
	}
}

// SetLevel 动态调整日志输出级别 (debug / info / warn / error)
func SetLevel(levelStr string) {
	lvl := parseLogLevel(levelStr)
	atomicLevel.SetLevel(lvl)
}

// GetLevel 获取当前日志级别字符串
func GetLevel() string {
	return atomicLevel.Level().String()
}

func customTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
}

// SubscribeLog 订阅日志事件（用于推送到 Web 前端）
func SubscribeLog(fn func(level string, msg string)) {
	listenerMu.Lock()
	defer listenerMu.Unlock()
	listeners = append(listeners, fn)
}

func broadcast(level string, msg string) {
	listenerMu.RLock()
	defer listenerMu.RUnlock()
	for _, fn := range listeners {
		go fn(level, msg)
	}
}

func Info(msg string, fields ...zap.Field) {
	if L != nil {
		L.Info(msg, fields...)
	}
	if atomicLevel.Enabled(zapcore.InfoLevel) {
		broadcast("INFO", formatLogMsg(msg, fields...))
	}
}

func Warn(msg string, fields ...zap.Field) {
	if L != nil {
		L.Warn(msg, fields...)
	}
	if atomicLevel.Enabled(zapcore.WarnLevel) {
		broadcast("WARN", formatLogMsg(msg, fields...))
	}
}

func Error(msg string, fields ...zap.Field) {
	if L != nil {
		L.Error(msg, fields...)
	}
	if atomicLevel.Enabled(zapcore.ErrorLevel) {
		broadcast("ERROR", formatLogMsg(msg, fields...))
	}
}

func Debug(msg string, fields ...zap.Field) {
	if L != nil {
		L.Debug(msg, fields...)
	}
	if atomicLevel.Enabled(zapcore.DebugLevel) {
		broadcast("DEBUG", formatLogMsg(msg, fields...))
	}
}

func Infof(template string, args ...interface{}) {
	msg := fmt.Sprintf(template, args...)
	if Sugar != nil {
		Sugar.Infof(template, args...)
	}
	if atomicLevel.Enabled(zapcore.InfoLevel) {
		broadcast("INFO", msg)
	}
}

func Warnf(template string, args ...interface{}) {
	msg := fmt.Sprintf(template, args...)
	if Sugar != nil {
		Sugar.Warnf(template, args...)
	}
	if atomicLevel.Enabled(zapcore.WarnLevel) {
		broadcast("WARN", msg)
	}
}

func Errorf(template string, args ...interface{}) {
	msg := fmt.Sprintf(template, args...)
	if Sugar != nil {
		Sugar.Errorf(template, args...)
	}
	if atomicLevel.Enabled(zapcore.ErrorLevel) {
		broadcast("ERROR", msg)
	}
}

func formatLogMsg(msg string, fields ...zap.Field) string {
	if len(fields) == 0 {
		return msg
	}
	res := msg + " |"
	for _, f := range fields {
		if f.String != "" {
			res += fmt.Sprintf(" %s=%s", f.Key, f.String)
		} else if f.Integer != 0 {
			res += fmt.Sprintf(" %s=%d", f.Key, f.Integer)
		} else if f.Interface != nil {
			res += fmt.Sprintf(" %s=%v", f.Key, f.Interface)
		}
	}
	return res
}
