package downloader

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/config"
	"hwdocsdown/internal/crawler"
	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type DownloadManager struct {
	repo          *store.Repository
	docCrawler    *crawler.DocCrawler
	runners       map[string]*DownloadTaskRunner
	taskQueue     chan string // taskId 队列
	mu            sync.Mutex
	listeners     []func(event ProgressEvent)
	listenerMu    sync.RWMutex
	stopChan      chan struct{}
}

func NewDownloadManager(repo *store.Repository, docCrawler *crawler.DocCrawler) *DownloadManager {
	dm := &DownloadManager{
		repo:       repo,
		docCrawler: docCrawler,
		runners:    make(map[string]*DownloadTaskRunner),
		taskQueue:  make(chan string, 1000),
		stopChan:   make(chan struct{}),
	}
	go dm.workerLoop()
	return dm
}

// Subscribe 订阅下载进度事件
func (dm *DownloadManager) Subscribe(listener func(event ProgressEvent)) {
	dm.listenerMu.Lock()
	defer dm.listenerMu.Unlock()
	dm.listeners = append(dm.listeners, listener)
}

func (dm *DownloadManager) broadcast(event ProgressEvent) {
	dm.listenerMu.RLock()
	defer dm.listenerMu.RUnlock()
	for _, l := range dm.listeners {
		go l(event)
	}
}

// AddTask 添加文档下载任务
func (dm *DownloadManager) AddTask(doc *store.Document) (*store.DownloadTask, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	taskID := fmt.Sprintf("TASK-%s", doc.NID)
	if runner, exists := dm.runners[taskID]; exists {
		if runner.task.Status == int(StatusDownloading) || runner.task.Status == int(StatusQueued) {
			return runner.task, nil
		}
	}

	task := &store.DownloadTask{
		ID:              taskID,
		DocNID:          doc.NID,
		DocName:         doc.Name,
		DocType:         doc.DocType,
		FileName:        doc.FileName,
		TotalBytes:      doc.FileSizeBytes,
		DownloadedBytes: 0,
		Status:          int(StatusQueued),
		Progress:        0,
		SpeedKBps:       0,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := dm.repo.CreateDownloadTask(task); err != nil {
		logger.Error("保存下载任务记录失败", zap.String("taskId", taskID), zap.Error(err))
		return nil, err
	}

	dm.taskQueue <- taskID
	logger.Info("添加下载任务到队列",
		zap.String("taskId", taskID),
		zap.String("docName", doc.Name),
		zap.String("docType", doc.DocType),
	)
	return task, nil
}

// BatchAddTasks 批量添加下载任务
func (dm *DownloadManager) BatchAddTasks(docs []store.Document) ([]*store.DownloadTask, error) {
	logger.Info("批量添加下载任务", zap.Int("count", len(docs)))
	var tasks []*store.DownloadTask
	for _, d := range docs {
		t, err := dm.AddTask(&d)
		if err == nil {
			tasks = append(tasks, t)
		}
	}
	logger.Info("批量添加下载任务完成", zap.Int("enqueued", len(tasks)))
	return tasks, nil
}

// PauseTask 暂停任务
func (dm *DownloadManager) PauseTask(taskID string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if runner, exists := dm.runners[taskID]; exists {
		runner.Cancel()
		delete(dm.runners, taskID)
		logger.Info("下载任务已暂停", zap.String("taskId", taskID))
	}
}

// ResumeTask 继续任务
func (dm *DownloadManager) ResumeTask(taskID string) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	tasks, err := dm.repo.GetAllDownloadTasks()
	if err != nil {
		logger.Error("恢复下载任务失败: 读取任务列表错误", zap.String("taskId", taskID), zap.Error(err))
		return err
	}
	for _, t := range tasks {
		if t.ID == taskID {
			t.Status = int(StatusQueued)
			dm.repo.CreateDownloadTask(&t)
			dm.taskQueue <- taskID
			logger.Info("下载任务已恢复排队", zap.String("taskId", taskID))
			break
		}
	}
	return nil
}

// CancelTask 取消并删除下载任务
func (dm *DownloadManager) CancelTask(taskID string) {
	dm.PauseTask(taskID)
	dm.repo.DeleteDownloadTask(taskID)
	logger.Info("下载任务已移除", zap.String("taskId", taskID))
}

func (dm *DownloadManager) workerLoop() {
	cfg := config.GetConfig()
	maxWorkers := 3
	if cfg != nil && cfg.MaxConcurrent > 0 {
		maxWorkers = cfg.MaxConcurrent
	}
	sem := make(chan struct{}, maxWorkers)
	logger.Info("下载管理器工作池启动", zap.Int("maxWorkers", maxWorkers))

	for {
		select {
		case <-dm.stopChan:
			logger.Info("下载管理器工作池停止")
			return
		case taskID := <-dm.taskQueue:
			sem <- struct{}{}
			go func(tid string) {
				defer func() { <-sem }()
				dm.executeTask(tid)
			}(taskID)
		}
	}
}

func (dm *DownloadManager) executeTask(taskID string) {
	logger.Debug("开始调度执行下载任务", zap.String("taskId", taskID))
	tasks, err := dm.repo.GetAllDownloadTasks()
	if err != nil {
		logger.Error("获取任务失败", zap.String("taskId", taskID), zap.Error(err))
		return
	}
	var task *store.DownloadTask
	for i := range tasks {
		if tasks[i].ID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		logger.Warn("任务未找到，跳过执行", zap.String("taskId", taskID))
		return
	}

	cfg := config.GetConfig()
	downloadDir := cfg.DownloadDir

	runner := NewTaskRunner(task, dm.repo, dm.docCrawler, downloadDir, func(event ProgressEvent) {
		dm.broadcast(event)
	})

	dm.mu.Lock()
	dm.runners[taskID] = runner
	dm.mu.Unlock()

	startTime := time.Now()
	runner.Run()
	logger.Debug("下载任务执行结束", zap.String("taskId", taskID), zap.Duration("elapsed", time.Since(startTime)))

	dm.mu.Lock()
	delete(dm.runners, taskID)
	dm.mu.Unlock()
}

// StopAll 优雅终止所有正在运行的下载任务
func (dm *DownloadManager) StopAll() {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	for tid, runner := range dm.runners {
		runner.Cancel()
		logger.Info("退出系统: 已终止下载任务", zap.String("taskId", tid))
	}
	dm.runners = make(map[string]*DownloadTaskRunner)
}
