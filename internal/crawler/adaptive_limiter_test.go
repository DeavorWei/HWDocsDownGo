package crawler

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdaptiveRateLimiter_StateTransitions(t *testing.T) {
	limiter := NewAdaptiveRateLimiter(8)
	if limiter.GetLimit() != 8 {
		t.Fatalf("expected initial limit 8, got %d", limiter.GetLimit())
	}

	// 1. 初次遇到 429 错误：随机退避，线程数减半 (8 -> 4)
	backoff1, limit1, changed1 := limiter.ReportError(429, `{"error":"Too Many Requests"}`, nil)
	if limit1 != 4 {
		t.Fatalf("expected limit after 1st error to be 4, got %d", limit1)
	}
	if !changed1 {
		t.Fatalf("expected changed to be true on 1st error")
	}
	if backoff1 < 2*time.Second || backoff1 > 5*time.Second {
		t.Fatalf("expected backoff1 between 2s and 5s, got %v", backoff1)
	}

	// 2. 还报错：线程数强制降低为 1
	backoff2, limit2, changed2 := limiter.ReportError(429, `<xml><error>ACCESS_RATE_API_USER_OVERHEAD_CODE</error></xml>`, nil)
	if limit2 != 1 {
		t.Fatalf("expected limit after 2nd error to be 1, got %d", limit2)
	}
	if !changed2 {
		t.Fatalf("expected changed to be true on 2nd error")
	}
	if backoff2 < 4*time.Second || backoff2 > 8*time.Second {
		t.Fatalf("expected backoff2 between 4s and 8s, got %v", backoff2)
	}

	// 3. 单线程下继续报错：保持在 1 线程，继续随机退避
	backoff3, limit3, changed3 := limiter.ReportError(429, "", errors.New("HTTP 状态码错误: 429"))
	if limit3 != 1 {
		t.Fatalf("expected limit after 3rd error to remain 1, got %d", limit3)
	}
	if changed3 {
		t.Fatalf("expected changed to be false when already at 1")
	}
	if backoff3 < 4*time.Second {
		t.Fatalf("expected backoff3 >= 4s, got %v", backoff3)
	}

	// 4. 重置协调器
	limiter.Reset(8)
	if limiter.GetLimit() != 8 {
		t.Fatalf("expected limit after reset to be 8, got %d", limiter.GetLimit())
	}
}

func TestAdaptiveRateLimiter_WaitCooldownAndContext(t *testing.T) {
	limiter := NewAdaptiveRateLimiter(4)

	// 无冷却时，WaitCooldown 立即返回
	ctx := context.Background()
	start := time.Now()
	if err := limiter.WaitCooldown(ctx); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("WaitCooldown took too long without cooldown: %v", time.Since(start))
	}

	// 触发一次错误，设置冷却
	_, _, _ = limiter.ReportError(429, "", nil)

	// Context 取消测试
	cancelCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := limiter.WaitCooldown(cancelCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestAdaptiveRateLimiter_ReportSuccessRecovery(t *testing.T) {
	limiter := NewAdaptiveRateLimiter(4)

	// 触发 2 次错误降至 1
	limiter.ReportError(429, "", nil)
	limiter.ReportError(429, "", nil)
	if limiter.GetLimit() != 1 {
		t.Fatalf("expected limit 1, got %d", limiter.GetLimit())
	}

	// 连续报告 20 次成功，平滑恢复到 2 线程
	for i := 0; i < 20; i++ {
		limiter.ReportSuccess()
	}
	if limiter.GetLimit() != 2 {
		t.Fatalf("expected limit recovered to 2 after 20 successes, got %d", limiter.GetLimit())
	}
}
