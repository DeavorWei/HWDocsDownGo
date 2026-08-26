package crawler

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

type workerCtxKey struct{}

// WithWorkerContext 为 context 注入爬虫 Worker ID (从 1 开始)
func WithWorkerContext(ctx context.Context, workerID int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, workerCtxKey{}, workerID)
}

// GetWorkerFromContext 从 context 中获取 workerID 与格式化标签 (例如 1, "[爬虫-1]")
func GetWorkerFromContext(ctx context.Context) (int, string) {
	if ctx != nil {
		if id, ok := ctx.Value(workerCtxKey{}).(int); ok && id > 0 {
			return id, fmt.Sprintf("[爬虫-%d]", id)
		}
	}
	return 0, ""
}

// FormatWorkerMsg 格式化日志消息，若存在 workerTag 则拼接前缀
func FormatWorkerMsg(workerTag, msg string) string {
	if workerTag != "" {
		return fmt.Sprintf("%s %s", workerTag, msg)
	}
	return msg
}

// WorkerZapFields 返回包含 workerId 的 zap 字段列表
func WorkerZapFields(workerID int) []zap.Field {
	if workerID > 0 {
		return []zap.Field{zap.Int("workerId", workerID)}
	}
	return nil
}
