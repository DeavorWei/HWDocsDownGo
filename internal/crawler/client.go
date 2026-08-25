package crawler

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/config"
	"hwdocsdown/internal/logger"
)

type HttpClient struct {
	client     *http.Client
	isFirstReq bool
	reqMu      sync.Mutex
}

func NewHttpClient() *HttpClient {
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &HttpClient{
		client: &http.Client{
			Jar:       jar,
			Transport: tr,
			Timeout:   30 * time.Second,
		},
		isFirstReq: true,
	}
}

// IsWsfCheckError 判断错误是否为华为网关 WSF 校验拦截
func IsWsfCheckError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "wsf check error") || strings.Contains(errMsg, "support-productservice-010001")
}

// DoRequest 发起带有完整现代 Chrome 浏览器特性的 HTTP 请求
func (c *HttpClient) DoRequest(method, urlStr string, body []byte, referer string) ([]byte, error) {
	cfg := config.GetConfig()
	if cfg != nil && cfg.RequestDelayMs > 0 {
		time.Sleep(time.Duration(cfg.RequestDelayMs) * time.Millisecond)
	}

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, urlStr, reqBody)
	if err != nil {
		logger.Error("创建 HTTP 请求失败", zap.String("url", urlStr), zap.Error(err))
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 完整对齐现代 Chrome 浏览器标头规范
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7,zh-HK;q=0.6,ja;q=0.5")
	req.Header.Set("sec-ch-ua", `"Not=A?Brand";v="99", "Google Chrome";v="151", "Chromium";v="151"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "same-origin")

	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://support.huawei.com")
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	} else {
		req.Header.Set("Referer", "https://support.huawei.com/enterprise/zh/index.html")
	}

	// Cookie 组装策略：基础 Cookie + 用户自定义 Cookie
	cookieStr := "supportelang=zh; lang=zh; support_last_vist=enterprise; browsehappy=browsehappy"
	if cfg != nil && strings.TrimSpace(cfg.CustomCookie) != "" {
		cookieStr = cookieStr + "; " + strings.TrimSpace(cfg.CustomCookie)
	}
	req.Header.Set("Cookie", cookieStr)

	// 打印请求携带的 Cookies
	c.reqMu.Lock()
	if c.isFirstReq {
		logger.Info("🌐 [首次请求] 发起 HTTP 请求，当前携带 Cookies 如下:",
			zap.String("method", method),
			zap.String("url", urlStr),
			zap.String("cookies", cookieStr),
		)
		c.isFirstReq = false
	} else {
		logger.Debug("发起 HTTP 请求",
			zap.String("method", method),
			zap.String("url", urlStr),
			zap.String("cookies", cookieStr),
		)
	}
	c.reqMu.Unlock()

	// 重试机制（最多 3 次）
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			logger.Warn("HTTP 请求连接失败，重试中...",
				zap.String("url", urlStr),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			time.Sleep(time.Duration(attempt*500) * time.Millisecond)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		// 成功返回 2xx
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if strings.Contains(string(respBody), `"WSF check error"`) {
				logger.Warn("🚨 接口被华为安全网关拦截 (WSF Check Error)",
					zap.String("url", urlStr),
					zap.Int("status", resp.StatusCode),
					zap.String("response", truncate(string(respBody), 150)),
					zap.String("cookies", cookieStr),
					zap.String("suggestion", "请在【系统设置】中粘贴有效浏览器 Cookie 后重试"),
				)
				return nil, fmt.Errorf("华为安全网关拦截 (WSF check error): %s", string(respBody))
			}
			return respBody, nil
		}

		// 400 WSF 拦截
		respBodyStr := string(respBody)
		if resp.StatusCode == 400 && strings.Contains(respBodyStr, "WSF check error") {
			logger.Warn("🚨 接口被华为安全网关拦截 (WSF Check Error 400)",
				zap.String("url", urlStr),
				zap.Int("status", resp.StatusCode),
				zap.String("response", truncate(respBodyStr, 150)),
				zap.String("cookies", cookieStr),
				zap.String("suggestion", "请在【系统设置】中粘贴有效浏览器 Cookie 后重试"),
			)
			return nil, fmt.Errorf("华为安全网关拦截 (WSF check error): %s", respBodyStr)
		}

		logger.Warn("HTTP 响应状态码异常",
			zap.String("url", urlStr),
			zap.Int("status", resp.StatusCode),
			zap.Int("attempt", attempt),
			zap.String("response", truncate(respBodyStr, 150)),
		)

		lastErr = fmt.Errorf("HTTP 状态码错误: %d, 返回内容: %s", resp.StatusCode, truncate(respBodyStr, 100))
		time.Sleep(time.Duration(attempt*500) * time.Millisecond)
	}

	return nil, lastErr
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
