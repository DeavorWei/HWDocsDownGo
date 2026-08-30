package crawler

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"hwdocsdown/internal/logger"
)

// AdaptiveRateLimiter 自适应并发与限流退避协调器
// 规则：
// 1. 初次遇到错误/429限流时，执行随机退避（Jitter），并发线程数减半；
// 2. 若持续报错（还报错），并发线程数强制降为 1（单线程安全模式）；
// 3. 在单线程模式下若仍报错，继续保持 1 线程并加大随机退避时长，保证任务不丢失。
type AdaptiveRateLimiter struct {
	mu            sync.Mutex
	initialLimit  int
	currentLimit  int
	errorStreak   int
	successStreak int
	cooldownUntil time.Time
	onLimitChange func(oldLimit, newLimit int, reason string, backoff time.Duration)
}

// NewAdaptiveRateLimiter 创建自适应并发协调器
func NewAdaptiveRateLimiter(initialLimit int) *AdaptiveRateLimiter {
	if initialLimit <= 0 {
		initialLimit = 1
	}
	if initialLimit > 32 {
		initialLimit = 32
	}
	return &AdaptiveRateLimiter{
		initialLimit: initialLimit,
		currentLimit: initialLimit,
	}
}

// SetOnLimitChange 设置线程数变动或退避回调
func (l *AdaptiveRateLimiter) SetOnLimitChange(fn func(oldLimit, newLimit int, reason string, backoff time.Duration)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onLimitChange = fn
}

// GetLimit 获取当前允许的并发线程上限 (1-32)
func (l *AdaptiveRateLimiter) GetLimit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.currentLimit <= 0 {
		return 1
	}
	return l.currentLimit
}

// Reset 重置协调器状态（通常在全量或定向爬取任务启动时调用）
func (l *AdaptiveRateLimiter) Reset(initialLimit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if initialLimit <= 0 {
		initialLimit = 1
	}
	if initialLimit > 32 {
		initialLimit = 32
	}
	l.initialLimit = initialLimit
	l.currentLimit = initialLimit
	l.errorStreak = 0
	l.successStreak = 0
	l.cooldownUntil = time.Time{}
	logger.Debug("自适应并发协调器已重置", zap.Int("initialLimit", initialLimit))
}

// IsRateLimitError 判断是否为频控限流或临时错误
func IsRateLimitError(statusCode int, respBodyStr string, err error) bool {
	if statusCode == http.StatusTooManyRequests { // 429
		return true
	}
	if statusCode == http.StatusServiceUnavailable || statusCode == http.StatusBadGateway || statusCode == http.StatusGatewayTimeout {
		return true
	}
	if respBodyStr != "" {
		lower := strings.ToLower(respBodyStr)
		if strings.Contains(lower, "access_rate_api_user_overhead_code") ||
			strings.Contains(lower, "too many requests") ||
			strings.Contains(lower, "throttled") ||
			strings.Contains(lower, "overload") {
			return true
		}
	}
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "too many requests") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "throttled") {
			return true
		}
	}
	return false
}

// ReportError 记录一次请求错误，并依据当前状态机决定随机退避时长和调减并发线程数
// 返回值：
// - backoff: 本次建议的随机退避等待时间
// - newLimit: 调整后的并发线程数
// - changed: 并发线程数是否发生了变动
func (l *AdaptiveRateLimiter) ReportError(statusCode int, respBodyStr string, err error) (time.Duration, int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	oldLimit := l.currentLimit
	l.errorStreak++
	l.successStreak = 0

	var backoff time.Duration
	var reason string
	changed := false

	isRateLimit := IsRateLimitError(statusCode, respBodyStr, err)

	if isRateLimit {
		// 针对 429 频控的随机退避计算 (带 Jitter)
		switch {
		case l.errorStreak == 1:
			// 规则 1：首次遇到错误，随机退避并减少线程数量（减半，最低为 1）
			jitter := time.Duration(rand.IntN(1500)+1000) * time.Millisecond // 1.0s ~ 2.5s Jitter
			backoff = 1500*time.Millisecond + jitter                        // 总计 2.5s ~ 4.0s
			newLimit := oldLimit / 2
			if newLimit < 1 {
				newLimit = 1
			}
			if newLimit != oldLimit {
				l.currentLimit = newLimit
				changed = true
			}
			reason = fmt.Sprintf("初次触发限流 (HTTP %d)，执行随机退避并调减并发线程数 (%d -> %d)", statusCode, oldLimit, l.currentLimit)

		case l.errorStreak >= 2:
			// 规则 2：还报错，则线程降低为 1
			jitter := time.Duration(rand.IntN(2500)+1500) * time.Millisecond // 1.5s ~ 4.0s Jitter
			backoff = 3000*time.Millisecond + jitter                        // 总计 4.5s ~ 7.0s
			if oldLimit > 1 {
				l.currentLimit = 1
				changed = true
				reason = fmt.Sprintf("持续触发限流 (连续 %d 次)，执行随机退避并强制将并发线程数降为 1", l.errorStreak)
			} else {
				// 已在单线程状态下，继续保持 1 线程并退避
				reason = fmt.Sprintf("单线程模式下持续限流 (连续 %d 次)，执行随机退避以恢复频控桶", l.errorStreak)
			}
		}
	} else {
		// 普通网络/服务异常退避
		if l.errorStreak == 1 {
			jitter := time.Duration(rand.IntN(1000)+500) * time.Millisecond
			backoff = 1000*time.Millisecond + jitter
			newLimit := oldLimit / 2
			if newLimit < 1 {
				newLimit = 1
			}
			if newLimit != oldLimit {
				l.currentLimit = newLimit
				changed = true
			}
			reason = fmt.Sprintf("遇到请求异常，执行随机退避并减半并发线程数 (%d -> %d)", oldLimit, l.currentLimit)
		} else {
			jitter := time.Duration(rand.IntN(1500)+1000) * time.Millisecond
			backoff = 2000*time.Millisecond + jitter
			if oldLimit > 1 {
				l.currentLimit = 1
				changed = true
				reason = fmt.Sprintf("持续请求异常，强制将并发线程数降低为 1")
			} else {
				reason = fmt.Sprintf("单线程模式下遭遇请求异常，执行随机退避")
			}
		}
	}

	// 设定全局冷却时间戳，避免所有协程集体并发唤醒引发惊群
	now := time.Now()
	newCooldown := now.Add(backoff)
	if newCooldown.After(l.cooldownUntil) {
		l.cooldownUntil = newCooldown
	}

	logger.Warn("自适应并发协调器捕获到错误",
		zap.Int("statusCode", statusCode),
		zap.Int("errorStreak", l.errorStreak),
		zap.Int("oldLimit", oldLimit),
		zap.Int("currentLimit", l.currentLimit),
		zap.Bool("limitChanged", changed),
		zap.Duration("backoff", backoff),
		zap.String("reason", reason),
	)

	fn := l.onLimitChange
	if fn != nil {
		// 在锁外调用回调，防止阻塞
		current := l.currentLimit
		go fn(oldLimit, current, reason, backoff)
	}

	return backoff, l.currentLimit, changed
}

// ReportSuccess 报告一次成功的网络请求
func (l *AdaptiveRateLimiter) ReportSuccess() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.successStreak++
	if l.errorStreak > 0 {
		l.errorStreak = 0
	}

	// 平滑自愈探测：若连续成功 20 次且当前线程数低于初始设定，谨慎尝试恢复 +1 线程
	if l.successStreak >= 20 && l.currentLimit < l.initialLimit {
		oldLimit := l.currentLimit
		l.currentLimit++
		l.successStreak = 0
		reason := fmt.Sprintf("连续稳定请求成功，尝试平滑恢复 1 个并发线程 (%d -> %d)", oldLimit, l.currentLimit)
		logger.Info("自适应并发恢复探测", zap.Int("from", oldLimit), zap.Int("to", l.currentLimit))
		fn := l.onLimitChange
		if fn != nil {
			go fn(oldLimit, l.currentLimit, reason, 0)
		}
	}
}

// WaitCooldown 在发起网络请求前等待全局限流冷却期结束（包含随机微抖动，避免瞬时惊群打在同一毫秒）
func (l *AdaptiveRateLimiter) WaitCooldown(ctx context.Context) error {
	l.mu.Lock()
	until := l.cooldownUntil
	l.mu.Unlock()

	now := time.Now()
	if !until.After(now) {
		return nil
	}

	// 附加 50ms ~ 250ms 的微抖动，使唤醒时刻均匀分布
	microJitter := time.Duration(rand.IntN(200)+50) * time.Millisecond
	waitTime := until.Sub(now) + microJitter

	if ctx == nil {
		time.Sleep(waitTime)
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(waitTime):
		return nil
	}
}
