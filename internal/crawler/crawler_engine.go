package crawler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/config"
	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type CrawlerEngine struct {
	catCrawler *CategoryCrawler
	docCrawler *DocCrawler
	repo       *store.Repository
	isBusy     bool
	mu         sync.Mutex
	cancelFunc context.CancelFunc
	onFinished func(success bool, msg string)
}

func NewCrawlerEngine(catCrawler *CategoryCrawler, docCrawler *DocCrawler, repo *store.Repository) *CrawlerEngine {
	return &CrawlerEngine{
		catCrawler: catCrawler,
		docCrawler: docCrawler,
		repo:       repo,
	}
}

func (e *CrawlerEngine) SetOnFinished(fn func(bool, string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onFinished = fn
}

func (e *CrawlerEngine) IsBusy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isBusy
}

func (e *CrawlerEngine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelFunc != nil {
		logger.Info("接收到停止爬虫引擎指令，正在取消上下文...")
		e.cancelFunc()
	}
}

func (e *CrawlerEngine) notifyFinished(success bool, msg string) {
	e.mu.Lock()
	fn := e.onFinished
	e.mu.Unlock()
	if fn != nil {
		fn(success, msg)
	}
}

type crawlWorkerTask struct {
	index    int
	total    int
	prod     store.Product
	lineName string
	catName  string
}

// crawlProductsConcurrent 并发抓取产品型号列表（支持 1-32 线程，日志包含 [爬虫-X] 编号）
func (e *CrawlerEngine) crawlProductsConcurrent(
	ctx context.Context,
	prods []store.Product,
	lineName, catName string,
	send func(string, ...interface{}),
	onProgress func(current, total int, currentItem string),
) (int, bool) {
	if len(prods) == 0 {
		return 0, false
	}

	cfg := config.GetConfig()
	numWorkers := 1
	if cfg.CrawlerThreads > 0 {
		numWorkers = cfg.CrawlerThreads
	}
	if numWorkers > 32 {
		numWorkers = 32
	}
	if numWorkers > len(prods) {
		numWorkers = len(prods)
	}

	logger.Info("启动产品型号并发爬取池",
		zap.Int("workers", numWorkers),
		zap.Int("prodsCount", len(prods)),
		zap.String("lineName", lineName),
	)

	taskChan := make(chan crawlWorkerTask, len(prods))
	for i, p := range prods {
		taskChan <- crawlWorkerTask{
			index:    i + 1,
			total:    len(prods),
			prod:     p,
			lineName: lineName,
			catName:  catName,
		}
	}
	close(taskChan)

	var totalDocsCount int64
	var completedCount int64
	var wsfBlocked atomic.Bool
	var wg sync.WaitGroup

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		workerID := w
		logger.SafeGo(fmt.Sprintf("crawler-worker-%d", workerID), func() {
			defer wg.Done()
			workerTag := fmt.Sprintf("[爬虫-%d]", workerID)

			for task := range taskChan {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if wsfBlocked.Load() {
					return
				}

				prod := task.prod
				send("   %s [%d/%d] 正在抓取型号: %s (PID: %s)...", workerTag, task.index, task.total, prod.Name, prod.ID)
				logger.Debug("爬虫工作协程开始抓取型号",
					zap.Int("workerId", workerID),
					zap.String("product", prod.Name),
					zap.String("pid", prod.ID),
				)

				e.catCrawler.FetchSubModelsAndVersions(prod.ID)
				docs, err := e.docCrawler.FetchDocsByProductWithContext(ctx, prod, task.lineName, task.catName)
				if err == nil && len(docs) > 0 {
					atomic.AddInt64(&totalDocsCount, int64(len(docs)))
					send("   %s 📄 型号 [%s] 发现并入库 %d 篇产品文档", workerTag, prod.Name, len(docs))
					logger.Info(fmt.Sprintf("%s 📄 型号 [%s] 发现并入库 %d 篇产品文档", workerTag, prod.Name, len(docs)),
						zap.Int("workerId", workerID),
						zap.String("product", prod.Name),
						zap.Int("docCount", len(docs)),
					)
				} else if IsWsfCheckError(err) {
					wsfBlocked.Store(true)
					logger.Warn(fmt.Sprintf("%s 触发华为网关安全拦截", workerTag),
						zap.Int("workerId", workerID),
						zap.String("product", prod.Name),
					)
					send("   %s 🚨【安全拦截与自动熔断】检测到华为网关 WSF Check 校验拦截！请在系统设置中填入 Cookie。", workerTag)
					return
				}

				done := atomic.AddInt64(&completedCount, 1)
				if onProgress != nil {
					onProgress(int(done), task.total, fmt.Sprintf("型号: %s (%s)", prod.Name, workerTag))
				}
			}
		})
	}

	wg.Wait()
	return int(totalDocsCount), wsfBlocked.Load()
}

// StartFullCrawl 启动全量深度爬取（大类 -> 产品线 -> 型号 -> 版本 -> 文档）
func (e *CrawlerEngine) StartFullCrawl(onLog func(string), onProgress func(current, total int, currentItem string)) error {
	e.mu.Lock()
	if e.isBusy {
		e.mu.Unlock()
		logger.Warn("尝试启动全量爬虫失败：已有任务正在运行中")
		return fmt.Errorf("爬虫任务正在运行中，请勿重复启动")
	}
	e.isBusy = true
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFunc = cancel
	e.mu.Unlock()

	cfg := config.GetConfig()
	threads := 1
	if cfg.CrawlerThreads > 0 {
		threads = cfg.CrawlerThreads
	}
	logger.Info("启动全量深度文档爬取任务", zap.Int("crawlerThreads", threads))

	logger.SafeGo("full-crawl-main", func() {
		defer func() {
			e.mu.Lock()
			e.isBusy = false
			e.cancelFunc = nil
			e.mu.Unlock()
		}()

		send := func(format string, a ...interface{}) {
			msg := fmt.Sprintf(format, a...)
			logger.Info(msg)
			if onLog != nil {
				onLog(msg)
			}
		}

		send("🚀 启动华为官网全量深度文档爬取任务 (并发线程数: %d)...", threads)
		startTime := time.Now()

		// 1. 抓取产品大类与产品线
		send("【步骤 1/3】正在连接华为官网，抓取全部产品大类与产品线列表...")
		categories, err := e.catCrawler.FetchCategories()
		if err != nil {
			send("❌ 获取产品分类失败: %v", err)
			e.notifyFinished(false, "获取分类失败")
			return
		}
		send("✅ 成功获取并入库 %d 个产品大类", len(categories))

		// 获取数据库中所有产品线
		var allLines []store.ProductLine
		for _, cat := range categories {
			lines, _ := e.repo.GetProductLinesByCategoryID(cat.ID)
			allLines = append(allLines, lines...)
		}
		totalLines := len(allLines)
		send("✅ 共找到 %d 条二级产品线", totalLines)

		var totalProductsCount int
		var totalDocsCount int

		// 2. 遍历每条产品线，抓取型号树并并发抓取文档
		send("【步骤 2/3】开始遍历所有产品线，抓取各产品线下的型号树与系列列表...")
		for lineIdx, line := range allLines {
			select {
			case <-ctx.Done():
				send("🛑 爬虫任务已被用户手动停止")
				e.notifyFinished(false, "任务已停止")
				return
			default:
			}

			if onProgress != nil {
				onProgress(lineIdx+1, totalLines, fmt.Sprintf("产品线: %s", line.Name))
			}

			send("   [%d/%d] 正在分析产品线: %s (PID: %s)...", lineIdx+1, totalLines, line.Name, line.ProID)
			prods, err := e.catCrawler.FetchProductsByLine(line.ID, line.ProID)
			if err != nil {
				send("   ⚠️ 产品线 [%s] 获取型号异常: %v", line.Name, err)
				continue
			}
			totalProductsCount += len(prods)
			send("   └─ 成功收录 %d 个产品型号，启动 %d 线程并发解析文档...", len(prods), threads)

			// 3. 并发抓取型号文档
			docsCount, wsfBlocked := e.crawlProductsConcurrent(ctx, prods, line.Name, line.CategoryID, send, onProgress)
			totalDocsCount += docsCount
			if wsfBlocked {
				e.notifyFinished(false, "网关安全拦截已熔断")
				return
			}
		}

		elapsed := time.Since(startTime).Round(time.Second)
		logger.Info("全量爬取任务完成",
			zap.Duration("elapsed", elapsed),
			zap.Int("lines", totalLines),
			zap.Int("products", totalProductsCount),
			zap.Int("docs", totalDocsCount),
		)
		send("🎉 全量爬取任务完成！耗时: %v", elapsed)
		send("📊 统计报告: 产品线 = %d 条, 产品型号 = %d 个, 成功入库文档 = %d 篇", totalLines, totalProductsCount, totalDocsCount)
		e.notifyFinished(true, "全量爬取完成")
	})

	return nil
}

// StartScopedCrawl 定向抓取指定层级（产品大类 / 二级产品线 / 产品系列与型号）
func (e *CrawlerEngine) StartScopedCrawl(
	categoryID, lineID, productID string,
	onLog func(string),
	onProgress func(current, total int, currentItem string),
) error {
	// 【关键修复】：若入参全为空，在未加锁前直接分流至 StartFullCrawl，杜绝自锁
	if categoryID == "" && lineID == "" && productID == "" {
		return e.StartFullCrawl(onLog, onProgress)
	}

	e.mu.Lock()
	if e.isBusy {
		e.mu.Unlock()
		logger.Warn("尝试启动定向爬虫失败：已有任务正在运行中")
		return fmt.Errorf("爬虫任务正在运行中，请稍候")
	}
	e.isBusy = true
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFunc = cancel
	e.mu.Unlock()

	cfg := config.GetConfig()
	threads := 1
	if cfg.CrawlerThreads > 0 {
		threads = cfg.CrawlerThreads
	}

	logger.Info("启动定向爬取任务",
		zap.String("categoryId", categoryID),
		zap.String("lineId", lineID),
		zap.String("productId", productID),
		zap.Int("crawlerThreads", threads),
	)

	logger.SafeGo("scoped-crawl-main", func() {
		defer func() {
			e.mu.Lock()
			e.isBusy = false
			e.cancelFunc = nil
			e.mu.Unlock()
		}()

		send := func(format string, a ...interface{}) {
			msg := fmt.Sprintf(format, a...)
			logger.Info(msg)
			if onLog != nil {
				onLog(msg)
			}
		}

		// 1. 单个产品型号定向爬取
		if productID != "" {
			prod, _ := e.repo.GetProductByID(productID)
			prodName := productID
			lineName := ""
			if prod != nil {
				prodName = prod.Name
				lineName = prod.ProductLineID
			} else {
				prod = &store.Product{ID: productID, Name: productID}
			}

			logger.Info("定向抓取单个型号", zap.String("product", prodName), zap.String("productId", productID))
			send("🚀 [爬虫-1] 启动指定产品型号 [%s] 定向爬取...", prodName)
			if onProgress != nil {
				onProgress(1, 1, fmt.Sprintf("型号: %s ([爬虫-1])", prodName))
			}

			e.catCrawler.FetchSubModelsAndVersions(prod.ID)
			docs, err := e.docCrawler.FetchDocsByProductWithContext(ctx, *prod, lineName, "")
			if err != nil {
				if IsWsfCheckError(err) {
					logger.Warn("网关 WSF 安全校验拦截熔断", zap.String("product", prodName))
					send("🚨 [爬虫-1]【安全拦截与自动熔断】检测到华为网关 WSF Check 校验拦截！请在系统设置中填入 Cookie。")
					e.notifyFinished(false, "网关安全拦截已熔断")
					return
				}
				logger.Error("抓取型号文档异常", zap.String("product", prodName), zap.Error(err))
				send("⚠️ [爬虫-1] 抓取型号 [%s] 文档异常: %v", prodName, err)
				e.notifyFinished(false, "抓取文档异常")
				return
			}

			logger.Info("型号定向爬取完成", zap.String("product", prodName), zap.Int("docCount", len(docs)))
			send("🎉 [爬虫-1] 产品型号 [%s] 爬取完成！共入库 %d 篇产品文档", prodName, len(docs))
			e.notifyFinished(true, "产品型号爬取完成")
			return
		}

		// 2. 二级产品线定向爬取
		if lineID != "" {
			line, _ := e.repo.GetProductLineByID(lineID)
			lineName := lineID
			linePID := lineID
			if line != nil {
				lineName = line.Name
				if line.ProID != "" {
					linePID = line.ProID
				}
			}

			logger.Info("定向抓取产品线", zap.String("lineName", lineName), zap.String("lineId", lineID))
			send("🚀 启动指定产品线 [%s] 快速深度爬取 (并发线程数: %d)...", lineName, threads)
			prods, err := e.catCrawler.FetchProductsByLine(lineID, linePID)
			if err != nil {
				logger.Error("获取产品型号列表失败", zap.String("lineName", lineName), zap.Error(err))
				send("❌ 获取产品型号列表失败: %v", err)
				e.notifyFinished(false, "获取型号列表失败")
				return
			}
			send("✅ 成功发现 %d 个产品型号，启动并发抓取型号版本与文档...", len(prods))

			docCount, wsfBlocked := e.crawlProductsConcurrent(ctx, prods, lineName, "", send, onProgress)
			if wsfBlocked {
				e.notifyFinished(false, "网关安全拦截已熔断")
				return
			}

			send("🎉 产品线 [%s] 爬取完成！共收录 %d 个型号，%d 篇产品文档", lineName, len(prods), docCount)
			e.notifyFinished(true, "产品线爬取完成")
			return
		}

		// 3. 产品大类定向爬取
		if categoryID != "" {
			cat, _ := e.repo.GetCategoryByID(categoryID)
			catName := categoryID
			if cat != nil {
				catName = cat.Name
			}

			logger.Info("定向抓取产品大类", zap.String("catName", catName), zap.String("categoryId", categoryID))
			send("🚀 启动产品大类 [%s] 全量深度爬取 (并发线程数: %d)...", catName, threads)
			lines, _ := e.repo.GetProductLinesByCategoryID(categoryID)
			if len(lines) == 0 {
				e.catCrawler.FetchCategories()
				lines, _ = e.repo.GetProductLinesByCategoryID(categoryID)
			}

			send("✅ 该大类下共发现 %d 条二级产品线", len(lines))
			var totalProds int
			var totalDocs int

			for lineIdx, line := range lines {
				select {
				case <-ctx.Done():
					send("🛑 任务已被用户手动停止")
					e.notifyFinished(false, "任务已停止")
					return
				default:
				}

				if onProgress != nil {
					onProgress(lineIdx+1, len(lines), fmt.Sprintf("产品线: %s", line.Name))
				}

				send("   [%d/%d] 正在分析产品线: %s (PID: %s)...", lineIdx+1, len(lines), line.Name, line.ProID)
				prods, err := e.catCrawler.FetchProductsByLine(line.ID, line.ProID)
				if err != nil {
					send("   ⚠️ 产品线 [%s] 获取型号异常: %v", line.Name, err)
					continue
				}
				totalProds += len(prods)

				docsCount, wsfBlocked := e.crawlProductsConcurrent(ctx, prods, line.Name, catName, send, onProgress)
				totalDocs += docsCount
				if wsfBlocked {
					e.notifyFinished(false, "网关安全拦截已熔断")
					return
				}
			}

			send("🎉 产品大类 [%s] 爬取完成！共处理 %d 条产品线，%d 个型号，%d 篇产品文档", catName, len(lines), totalProds, totalDocs)
			e.notifyFinished(true, "大类爬取完成")
			return
		}

		// 4. 若无指定，执行全量深度爬取
		_ = e.StartFullCrawl(onLog, onProgress)
	})

	return nil
}

// StartLineCrawl 单独抓取某个指定产品线 (兼容旧调用)
func (e *CrawlerEngine) StartLineCrawl(lineID string, onLog func(string)) error {
	return e.StartScopedCrawl("", lineID, "", onLog, nil)
}
