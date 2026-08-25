package downloader

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hwdocsdown/internal/crawler"
	"hwdocsdown/internal/store"
)

func TestMultiThreadDownload(t *testing.T) {
	// 生成 512KB 测试数据
	testData := make([]byte, 512*1024)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// 启动支持 Range 的本地测试服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(testData)))
			w.WriteHeader(http.StatusOK)
			w.Write(testData)
			return
		}

		// 解析 Range: bytes=start-end
		if strings.HasPrefix(rangeHeader, "bytes=") {
			parts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
			start, _ := strconv.Atoi(parts[0])
			end := len(testData) - 1
			if len(parts) > 1 && parts[1] != "" {
				end, _ = strconv.Atoi(parts[1])
			}
			if start >= len(testData) || end >= len(testData) || start > end {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}

			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(testData)))
			w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(testData[start : end+1])
			return
		}

		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	tempDir, err := os.MkdirTemp("", "dl_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := store.InitDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := store.NewRepository(db)

	task := &store.DownloadTask{
		ID:         "TASK-TEST001",
		DocNID:     "TEST001",
		DocName:    "Test Multi-thread Document",
		FileName:   "test_doc.zip",
		TotalBytes: int64(len(testData)),
	}

	runner := NewTaskRunner(task, repo, crawler.NewDocCrawler(crawler.NewHttpClient(), repo), tempDir, nil)

	tempPath := filepath.Join(tempDir, "test_doc.zip.tmp")
	finalPath := filepath.Join(tempDir, "test_doc.zip")

	// 测试 4 线程下载
	err = runner.downloadMultiThread(ts.URL, tempPath, finalPath, int64(len(testData)), 4)
	if err != nil {
		t.Fatalf("downloadMultiThread failed: %v", err)
	}

	// 校验最终文件
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("ReadFile finalPath failed: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Errorf("Downloaded data does not match expected testData")
	}
}
