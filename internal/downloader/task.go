package downloader

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/config"
	"hwdocsdown/internal/crawler"
	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type DownloadTaskRunner struct {
	task        *store.DownloadTask
	repo        *store.Repository
	docCrawler  *crawler.DocCrawler
	ctx         context.Context
	cancel      context.CancelFunc
	onProgress  func(event ProgressEvent)
	downloadDir string
}

func NewTaskRunner(
	task *store.DownloadTask,
	repo *store.Repository,
	docCrawler *crawler.DocCrawler,
	downloadDir string,
	onProgress func(event ProgressEvent),
) *DownloadTaskRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &DownloadTaskRunner{
		task:        task,
		repo:        repo,
		docCrawler:  docCrawler,
		ctx:         ctx,
		cancel:      cancel,
		onProgress:  onProgress,
		downloadDir: downloadDir,
	}
}

func (r *DownloadTaskRunner) Cancel() {
	r.cancel()
}

var probeClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
	},
}

func (r *DownloadTaskRunner) Run() error {
	r.task.Status = int(StatusDownloading)
	r.notifyProgress(0)

	// 1. 获取最新下载直链
	fileInfo, err := r.docCrawler.FetchDocFileInfo(r.task.DocNID)
	if err != nil {
		r.fail(fmt.Sprintf("获取下载链接失败: %v", err))
		return err
	}

	downloadURL := fileInfo.DownloadURL
	fileName := fileInfo.FileName
	if fileName == "" {
		fileName = r.task.DocName
	}
	// 确保合法的文件名
	fileName = sanitizeFileName(fileName)
	if fileInfo.Type != "" && filepath.Ext(fileName) == "" {
		fileName = fileName + "." + fileInfo.Type
	}

	// 【关键修复】：保存路径使用 [NID]文件名 隔离，杜绝不同产品同名手册覆写删除先前文件
	uniqueName := fmt.Sprintf("[%s]%s", r.task.DocNID, fileName)
	finalPath := filepath.Join(r.downloadDir, uniqueName)
	tempPath := finalPath + ".tmp"
	r.task.FileName = fileName
	r.task.SavePath = finalPath
	r.task.DownloadURL = downloadURL
	_ = r.repo.UpdateDownloadTaskProgress(r.task.ID, r.task.DownloadedBytes, r.task.TotalBytes, r.task.Progress, r.task.SpeedKBps, r.task.Status, r.task.ErrorMsg, finalPath, fileName)

	// 检查并清理已有伪 HTML 登录网页文件或 0 字节损坏文件
	if _, statErr := os.Stat(finalPath); statErr == nil {
		if isBad, reason := isFileCorruptedOrAuthHtml(finalPath); isBad {
			logger.Warn("检测到历史伪 HTML 登录文件，已自动清理", zap.String("finalPath", finalPath), zap.String("reason", reason))
			_ = os.Remove(finalPath)
			r.repo.UpdateDocDownloaded(r.task.DocNID, 0, "")
		}
	}

	// 2. 检查配置中的线程数 (1-32，默认 1)
	cfg := config.GetConfig()
	fileThreads := 1
	if cfg.FileThreads > 1 {
		fileThreads = cfg.FileThreads
	}
	if fileThreads > 32 {
		fileThreads = 32
	}

	// 3. 若线程数大于 1，探测服务器是否支持 Range 分片并发
	if fileThreads > 1 {
		totalBytes, supportsRange := r.probeRangeSupport(downloadURL)
		if supportsRange && totalBytes >= int64(fileThreads)*64*1024 {
			logger.Info("启动单文件多线程加速下载",
				zap.String("docName", r.task.DocName),
				zap.Int("threads", fileThreads),
				zap.Int64("totalBytes", totalBytes),
			)
			return r.downloadMultiThread(downloadURL, tempPath, finalPath, totalBytes, fileThreads)
		}
	}

	// 4. 默认或单线程下载
	return r.downloadSingleThread(downloadURL, tempPath, finalPath)
}

func (r *DownloadTaskRunner) applyHeaders(req *http.Request) {
	cfg := config.GetConfig()
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36")
	req.Header.Set("Referer", fmt.Sprintf("https://support.huawei.com/enterprise/zh/doc/%s", r.task.DocNID))
	cookieStr := "supportelang=zh; lang=zh; support_last_vist=enterprise; browsehappy=browsehappy"
	if strings.TrimSpace(cfg.CustomCookie) != "" {
		cleaned := strings.ReplaceAll(cfg.CustomCookie, "\r", "")
		cleaned = strings.ReplaceAll(cleaned, "\n", " ")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			cookieStr = cookieStr + "; " + cleaned
		}
	}
	req.Header.Set("Cookie", cookieStr)
}

func newDownloadHttpClient(timeout time.Duration, maxIdleConns int) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			urlStr := req.URL.String()
			if strings.Contains(req.URL.Host, "uniportal") ||
				strings.Contains(req.URL.Path, "login") ||
				strings.Contains(urlStr, "uniportal") ||
				(strings.Contains(urlStr, "redirect") && strings.Contains(urlStr, "login")) {
				return fmt.Errorf("AUTH_REQUIRED: 目标直链重定向至华为统一身份认证登录页 (%s)", urlStr)
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       60 * time.Second,
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConns,
		},
	}
}

func isHtmlOrAuthPage(resp *http.Response, firstBuf []byte) (bool, string) {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") {
		return true, "返回内容为网页 (text/html)"
	}
	bodySnippet := strings.ToLower(string(firstBuf))
	if strings.Contains(bodySnippet, "<!doctype html") ||
		strings.Contains(bodySnippet, "<html") ||
		strings.Contains(bodySnippet, "uniportal") ||
		strings.Contains(bodySnippet, "login") {
		return true, "检测到统一认证登录页面内容"
	}
	return false, ""
}

// probeRangeSupport 探测目标服务器是否支持 Range 分片及获取文件真实总大小 (复用连接池)
func (r *DownloadTaskRunner) probeRangeSupport(downloadURL string) (int64, bool) {
	req, err := http.NewRequestWithContext(r.ctx, "GET", downloadURL, nil)
	if err != nil {
		return 0, false
	}
	r.applyHeaders(req)
	req.Header.Set("Range", "bytes=0-0")

	client := newDownloadHttpClient(10*time.Second, 10)
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		// Content-Range: bytes 0-0/12345678
		if cr != "" {
			re := regexp.MustCompile(`/(\d+)`)
			matches := re.FindStringSubmatch(cr)
			if len(matches) > 1 {
				if total, err := strconv.ParseInt(matches[1], 10, 64); err == nil && total > 0 {
					return total, true
				}
			}
		}
		if resp.ContentLength > 0 {
			return resp.ContentLength, true
		}
	}
	return 0, false
}

// downloadMultiThread 单文件多线程（1-32）分片并发下载
func (r *DownloadTaskRunner) downloadMultiThread(downloadURL, tempPath, finalPath string, totalBytes int64, numThreads int) error {
	r.task.TotalBytes = totalBytes

	// 预先创建并调整临时文件大小
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		r.fail(fmt.Sprintf("创建临时文件失败: %v", err))
		return err
	}
	if err := file.Truncate(totalBytes); err != nil {
		file.Close()
		r.fail(fmt.Sprintf("预分配文件空间失败: %v", err))
		return err
	}
	file.Close()

	var downloadedBytes int64
	chunkSize := totalBytes / int64(numThreads)

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()

	var wg sync.WaitGroup
	errChan := make(chan error, numThreads)

	// 进度与速度统计定时器
	stopTicker := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastTime := time.Now()
		var lastBytes int64

		for {
			select {
			case <-stopTicker:
				return
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				current := atomic.LoadInt64(&downloadedBytes)
				elapsed := now.Sub(lastTime).Seconds()
				if elapsed > 0 {
					speedKBps := float64(current-lastBytes) / elapsed / 1024.0
					r.task.DownloadedBytes = current
					r.notifyProgress(speedKBps)
					lastTime = now
					lastBytes = current
				}
			}
		}
	}()

	client := newDownloadHttpClient(0, numThreads)

	for i := 0; i < numThreads; i++ {
		startByte := int64(i) * chunkSize
		endByte := int64(i+1)*chunkSize - 1
		if i == numThreads-1 {
			endByte = totalBytes - 1
		}

		wg.Add(1)
		go func(chunkIdx int, start, end int64) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}

			f, openErr := os.OpenFile(tempPath, os.O_WRONLY, 0644)
			if openErr != nil {
				select {
				case errChan <- fmt.Errorf("分片 %d 打开文件失败: %w", chunkIdx, openErr):
				default:
				}
				cancel()
				return
			}
			defer f.Close()

			req, reqErr := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
			if reqErr != nil {
				select {
				case errChan <- reqErr:
				default:
				}
				cancel()
				return
			}
			r.applyHeaders(req)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

			resp, doErr := client.Do(req)
			if doErr != nil {
				if ctx.Err() == nil {
					if strings.Contains(doErr.Error(), "AUTH_REQUIRED") {
						select {
						case errChan <- fmt.Errorf("该文档需要华为账号登录权限 (请录入有效 Cookie)"):
						default:
						}
					} else {
						select {
						case errChan <- fmt.Errorf("分片 %d 下载网络错误: %w", chunkIdx, doErr):
						default:
						}
					}
					cancel()
				}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				select {
				case errChan <- fmt.Errorf("该文档需要华为账号登录权限 (HTTP %d，请录入有效 Cookie)", resp.StatusCode):
				default:
				}
				cancel()
				return
			}

			if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
				select {
				case errChan <- fmt.Errorf("分片 %d HTTP 响应错误: %d", chunkIdx, resp.StatusCode):
				default:
				}
				cancel()
				return
			}

			buf := make([]byte, 64*1024)
			currentOffset := start

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				n, rErr := resp.Body.Read(buf)
				if n > 0 {
					if _, wErr := f.WriteAt(buf[:n], currentOffset); wErr != nil {
						select {
						case errChan <- fmt.Errorf("分片 %d 写入错误: %w", chunkIdx, wErr):
						default:
						}
						cancel()
						return
					}
					currentOffset += int64(n)
					atomic.AddInt64(&downloadedBytes, int64(n))
				}

				if rErr != nil {
					if rErr == io.EOF {
						break
					}
					if ctx.Err() == nil {
						select {
						case errChan <- fmt.Errorf("分片 %d 读取中断: %w", chunkIdx, rErr):
						default:
						}
						cancel()
					}
					return
				}
			}
		}(i, startByte, endByte)
	}

	wg.Wait()
	close(stopTicker)

	if r.ctx.Err() != nil {
		r.pause()
		return nil
	}

	select {
	case err := <-errChan:
		r.fail(err.Error())
		return err
	default:
	}

	// 重命名为正式文件
	os.Remove(finalPath)
	if err := os.Rename(tempPath, finalPath); err != nil {
		r.fail(fmt.Sprintf("重命名文件失败: %v", err))
		return err
	}

	// 完成处理
	r.task.Status = int(StatusCompleted)
	r.task.Progress = 100.0
	r.task.DownloadedBytes = totalBytes
	r.task.SpeedKBps = 0
	r.repo.UpdateDownloadTaskProgress(r.task.ID, totalBytes, totalBytes, 100.0, 0, int(StatusCompleted), "", finalPath, r.task.FileName)
	r.repo.UpdateDocDownloaded(r.task.DocNID, 1, finalPath)

	r.notifyProgress(0)
	logger.Info("多线程文档下载完成",
		zap.String("docName", r.task.DocName),
		zap.String("savePath", finalPath),
		zap.Int("threads", numThreads),
		zap.Int64("bytes", totalBytes),
	)
	return nil
}

// downloadSingleThread 单流标准下载（支持断点续传与完整性断言）
func (r *DownloadTaskRunner) downloadSingleThread(downloadURL, tempPath, finalPath string) error {
	var existingBytes int64
	if fi, err := os.Stat(tempPath); err == nil {
		existingBytes = fi.Size()
		// 若此前为预分配截断文件或残存破损文件，重置为 0
		if r.task.TotalBytes > 0 && existingBytes >= r.task.TotalBytes {
			existingBytes = 0
			_ = os.Remove(tempPath)
		}
	}

	req, err := http.NewRequestWithContext(r.ctx, "GET", downloadURL, nil)
	if err != nil {
		r.fail(fmt.Sprintf("创建下载请求失败: %v", err))
		return err
	}

	r.applyHeaders(req)
	if existingBytes > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingBytes))
	}

	client := newDownloadHttpClient(0, 10)
	resp, err := client.Do(req)
	if err != nil {
		if r.ctx.Err() != nil {
			r.pause()
			return nil
		}
		if strings.Contains(err.Error(), "AUTH_REQUIRED") {
			_ = os.Remove(tempPath)
			r.fail("下载失败：该文档需要华为账号登录权限 (请在【系统设置】中录入有效登录态 Cookie)")
			return err
		}
		r.fail(fmt.Sprintf("连接服务器失败: %v", err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = os.Remove(tempPath)
		r.fail(fmt.Sprintf("下载失败：该文档需要华为账号登录权限 (HTTP %d，请录入有效 Cookie)", resp.StatusCode))
		return fmt.Errorf("需要登录权限: %d", resp.StatusCode)
	}

	var totalBytes int64
	var file *os.File

	if resp.StatusCode == http.StatusPartialContent {
		totalBytes = existingBytes + resp.ContentLength
		file, err = os.OpenFile(tempPath, os.O_WRONLY|os.O_APPEND, 0644)
	} else if resp.StatusCode == http.StatusOK {
		existingBytes = 0
		totalBytes = resp.ContentLength
		file, err = os.Create(tempPath)
	} else {
		_ = os.Remove(tempPath)
		errMsg := fmt.Sprintf("HTTP 响应错误: %d", resp.StatusCode)
		r.fail(errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	if err != nil {
		r.fail(fmt.Sprintf("打开本地文件失败: %v", err))
		return err
	}
	defer file.Close()

	r.task.TotalBytes = totalBytes
	r.task.DownloadedBytes = existingBytes

	buf := make([]byte, 64*1024)
	var downloadedSoFar = existingBytes
	lastTime := time.Now()
	var bytesSinceLastTime int64

	for {
		select {
		case <-r.ctx.Done():
			r.pause()
			return nil
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// 如果是首个数据块，检查是否为 HTML 网页/登录页
			if downloadedSoFar == 0 {
				if isHtml, reason := isHtmlOrAuthPage(resp, buf[:n]); isHtml {
					file.Close()
					_ = os.Remove(tempPath)
					errMsg := fmt.Sprintf("下载失败：该文档需要华为账号登录权限 (%s，请录入有效 Cookie)", reason)
					r.fail(errMsg)
					return fmt.Errorf("%s", errMsg)
				}
			}

			if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				r.fail(fmt.Sprintf("写入磁盘失败: %v", writeErr))
				return writeErr
			}
			downloadedSoFar += int64(n)
			bytesSinceLastTime += int64(n)
			r.task.DownloadedBytes = downloadedSoFar

			now := time.Now()
			elapsed := now.Sub(lastTime)
			if elapsed >= 500*time.Millisecond {
				speedKBps := float64(bytesSinceLastTime) / elapsed.Seconds() / 1024.0
				r.notifyProgress(speedKBps)
				lastTime = now
				bytesSinceLastTime = 0
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			if r.ctx.Err() != nil {
				r.pause()
				return nil
			}
			r.fail(fmt.Sprintf("下载中断: %v", readErr))
			return readErr
		}
	}

	file.Close()

	// 退出读取循环后强制断言长度一致性与有效性
	if downloadedSoFar <= 0 {
		_ = os.Remove(tempPath)
		errMsg := "下载失败：获得文件大小为 0 字节，可能需要登录权限或直链已失效"
		r.fail(errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	if r.task.TotalBytes > 0 && downloadedSoFar != r.task.TotalBytes {
		_ = os.Remove(tempPath)
		errMsg := fmt.Sprintf("文件下载不完整: 预期 %d 字节, 实际获得 %d 字节", r.task.TotalBytes, downloadedSoFar)
		r.fail(errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	// 重命名为正式文件
	os.Remove(finalPath)
	if err := os.Rename(tempPath, finalPath); err != nil {
		r.fail(fmt.Sprintf("重命名文件失败: %v", err))
		return err
	}

	// 完成处理
	r.task.Status = int(StatusCompleted)
	r.task.Progress = 100.0
	r.task.SpeedKBps = 0
	r.repo.UpdateDownloadTaskProgress(r.task.ID, totalBytes, totalBytes, 100.0, 0, int(StatusCompleted), "", finalPath, r.task.FileName)
	r.repo.UpdateDocDownloaded(r.task.DocNID, 1, finalPath)

	r.notifyProgress(0)
	logger.Info("文档下载完成",
		zap.String("docName", r.task.DocName),
		zap.String("savePath", finalPath),
		zap.Int64("bytes", totalBytes),
	)
	return nil
}

func (r *DownloadTaskRunner) notifyProgress(speedKBps float64) {
	var progress float64
	var speedStr string

	if r.task.Status == int(StatusCompleted) {
		progress = 100.0
		speedKBps = 0
		speedStr = ""
		r.task.DownloadedBytes = r.task.TotalBytes
	} else if r.task.Status == int(StatusFailed) || r.task.Status == int(StatusPaused) {
		if r.task.TotalBytes > 0 {
			progress = float64(r.task.DownloadedBytes) / float64(r.task.TotalBytes) * 100.0
		}
		speedKBps = 0
		speedStr = ""
	} else if r.task.TotalBytes > 0 {
		progress = float64(r.task.DownloadedBytes) / float64(r.task.TotalBytes) * 100.0
		if progress > 99.9 {
			progress = 99.9 // 未完成落盘校验前最高展示 99.9%，杜绝提前假死
		}
		if speedKBps > 1024 {
			speedStr = fmt.Sprintf("%.2f MB/s", speedKBps/1024.0)
		} else {
			speedStr = fmt.Sprintf("%.1f KB/s", speedKBps)
		}
	} else {
		if speedKBps > 1024 {
			speedStr = fmt.Sprintf("%.2f MB/s", speedKBps/1024.0)
		} else {
			speedStr = fmt.Sprintf("%.1f KB/s", speedKBps)
		}
	}

	r.task.Progress = progress
	r.task.SpeedKBps = speedKBps

	// 存库
	r.repo.UpdateDownloadTaskProgress(r.task.ID, r.task.DownloadedBytes, r.task.TotalBytes, progress, speedKBps, r.task.Status, r.task.ErrorMsg, r.task.SavePath, r.task.FileName)

	if r.onProgress != nil {
		r.onProgress(ProgressEvent{
			TaskID:          r.task.ID,
			DocNID:          r.task.DocNID,
			DocName:         r.task.DocName,
			FileName:        r.task.FileName,
			SavePath:        r.task.SavePath,
			TotalBytes:      r.task.TotalBytes,
			DownloadedBytes: r.task.DownloadedBytes,
			Progress:        progress,
			SpeedKBps:       speedKBps,
			SpeedStr:        speedStr,
			Status:          TaskStatus(r.task.Status),
			StatusStr:       TaskStatus(r.task.Status).String(),
			ErrorMsg:        r.task.ErrorMsg,
		})
	}
}

func (r *DownloadTaskRunner) fail(msg string) {
	r.task.Status = int(StatusFailed)
	r.task.ErrorMsg = msg
	logger.Error("❌ 文档下载失败",
		zap.String("taskId", r.task.ID),
		zap.String("docNid", r.task.DocNID),
		zap.String("docName", r.task.DocName),
		zap.String("error", msg),
	)
	r.notifyProgress(0)
}

func (r *DownloadTaskRunner) pause() {
	r.task.Status = int(StatusPaused)
	r.notifyProgress(0)
}

// sanitizeFileName 安全清洗文件名 (先截取文件名 Base 消除路径穿越，再过滤非法字符及 Windows 系统保留设备名)
func sanitizeFileName(name string) string {
	// 1. 先截取纯文件名，彻底消除 ../ 和绝对路径穿越
	name = filepath.Base(filepath.Clean(name))

	// 2. 替换文件名中的非法字符
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\r", "\n", "\t"}
	result := name
	for _, char := range invalid {
		result = strings.ReplaceAll(result, char, "_")
	}
	result = strings.TrimSpace(result)
	result = strings.Trim(result, ".")

	if result == "" || result == "." || result == ".." {
		result = fmt.Sprintf("doc_%d", time.Now().Unix())
	}

	// 3. 过滤 Windows 系统保留设备名
	upper := strings.ToUpper(result)
	reserved := []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}
	for _, res := range reserved {
		if upper == res || strings.HasPrefix(upper, res+".") {
			result = "_" + result
			break
		}
	}
	return result
}

// isFileCorruptedOrAuthHtml 检查文件是否为登录重定向生成的伪 HTML 页面或 0 字节损坏文件
func isFileCorruptedOrAuthHtml(filePath string) (bool, string) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return true, "无法读取文件状态"
	}
	if fi.Size() == 0 {
		return true, "文件大小为 0 字节"
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".html" && ext != ".htm" {
		if fi.Size() < 500*1024 {
			f, err := os.Open(filePath)
			if err != nil {
				return false, ""
			}
			defer f.Close()
			buf := make([]byte, 512)
			n, _ := f.Read(buf)
			if n > 0 {
				content := strings.ToLower(string(buf[:n]))
				if strings.Contains(content, "<!doctype html") ||
					strings.Contains(content, "<html") ||
					strings.Contains(content, "uniportal") ||
					strings.Contains(content, "login") ||
					strings.Contains(content, "<head>") {
					return true, "文件内容为 HTML 登录或重定向网页"
				}
			}
		}
	}
	return false, ""
}

