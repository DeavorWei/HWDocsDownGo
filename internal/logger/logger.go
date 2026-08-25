package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	L          *zap.Logger
	Sugar      *zap.SugaredLogger
	listeners  []func(level string, msg string)
	listenerMu sync.RWMutex
)

// InitLogger 初始化 Zap 高性能日志记录器
func InitLogger(logDir string, debug bool) (*zap.Logger, error) {
	if logDir == "" {
		logDir = filepath.Join(".", "HWDDGoData", "logs")
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	logFilePath := filepath.Join(logDir, "hwdocsdown.log")

	// 文件滚动配置
	lumberJackLogger := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    20, // 单个日志文件最大 20 MB
		MaxBackups: 5,  // 最多保留 5 个备份
		MaxAge:     30, // 最多保留 30 天
		Compress:   true,
	}

	// 编码器配置
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

	// 文件端编码器（不带彩色）
	fileEncoderConfig := encoderConfig
	fileEncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	level := zap.InfoLevel
	if debug {
		level = zap.DebugLevel
	}

	// 核心输出
	fileCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(fileEncoderConfig),
		zapcore.AddSync(lumberJackLogger),
		level,
	)

	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	)

	core := zapcore.NewTee(fileCore, consoleCore)
	loggerInstance := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	L = loggerInstance
	Sugar = loggerInstance.Sugar()

	Info("Zap 高性能日志系统初始化成功", zap.String("logFile", logFilePath))
	return loggerInstance, nil
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
	broadcast("INFO", formatLogMsg(msg, fields...))
}

func Warn(msg string, fields ...zap.Field) {
	if L != nil {
		L.Warn(msg, fields...)
	}
	broadcast("WARN", formatLogMsg(msg, fields...))
}

func Error(msg string, fields ...zap.Field) {
	if L != nil {
		L.Error(msg, fields...)
	}
	broadcast("ERROR", formatLogMsg(msg, fields...))
}

func Debug(msg string, fields ...zap.Field) {
	if L != nil {
		L.Debug(msg, fields...)
	}
}

func Infof(template string, args ...interface{}) {
	msg := fmt.Sprintf(template, args...)
	if Sugar != nil {
		Sugar.Infof(template, args...)
	}
	broadcast("INFO", msg)
}

func Warnf(template string, args ...interface{}) {
	msg := fmt.Sprintf(template, args...)
	if Sugar != nil {
		Sugar.Warnf(template, args...)
	}
	broadcast("WARN", msg)
}

func Errorf(template string, args ...interface{}) {
	msg := fmt.Sprintf(template, args...)
	if Sugar != nil {
		Sugar.Errorf(template, args...)
	}
	broadcast("ERROR", msg)
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
