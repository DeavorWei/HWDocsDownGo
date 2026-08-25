package crawler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

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

// StartFullCrawl 启动全量深度爬取（大类 -> 产品线 -> 型号 -> 版本 -> 文档）
func (e *CrawlerEngine) StartFullCrawl(onLog func(string), onProgress func(current, total int, currentItem string)) error {
	e.mu.Lock()
	if e.isBusy {
		e.mu.Unlock()
		return fmt.Errorf("爬虫任务正在运行中，请勿重复启动")
	}
	e.isBusy = true
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFunc = cancel
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			e.isBusy = false
			e.cancelFunc = nil
			e.mu.Unlock()
		}()

		send := func(format string, a ...interface{}) {
			msg := fmt.Sprintf(format, a...)
			log.Println("[Crawler]", msg)
			if onLog != nil {
				onLog(msg)
			}
		}

		send("🚀 启动华为官网全量深度文档爬取任务...")
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

		// 2. 遍历每条产品线，抓取型号树
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
			send("   └─ 成功收录 %d 个产品型号", len(prods))

			// 3. 遍历型号，抓取子型号与版本，并尝试抓取文档
			for _, prod := range prods {
				select {
				case <-ctx.Done():
					send("🛑 爬虫任务已被用户手动停止")
					e.notifyFinished(false, "任务已停止")
					return
				default:
				}

				e.catCrawler.FetchSubModelsAndVersions(prod.ID)

				docs, err := e.docCrawler.FetchDocsByProduct(prod, line.Name, line.CategoryID)
				if err == nil && len(docs) > 0 {
					totalDocsCount += len(docs)
					send("      📄 型号 [%s] 发现并入库 %d 篇产品文档 (HDX/CHM/PDF)", prod.Name, len(docs))
				} else if IsWsfCheckError(err) {
					// 首次被拦截立即触发自动熔断
					send("🚨【安全拦截与自动熔断】检测到华为网关 WSF Check 校验拦截！")
					send("🛑 为避免无意义的无效请求，爬虫引擎已自动停止后续爬取任务。")
					send("💡【解决方案】请在浏览器打开 support.huawei.com 复制请求头中的 Cookie，粘贴到本系统的【系统设置】->【自定义 Cookie】并保存，然后重新启动爬取即可！")
					e.notifyFinished(false, "网关安全拦截已熔断")
					return
				}
			}
		}

		elapsed := time.Since(startTime).Round(time.Second)
		send("🎉 全量爬取任务完成！耗时: %v", elapsed)
		send("📊 统计报告: 产品线 = %d 条, 产品型号 = %d 个, 成功入库文档 = %d 篇", totalLines, totalProductsCount, totalDocsCount)
		e.notifyFinished(true, "全量爬取完成")
	}()

	return nil
}

// StartLineCrawl 单独抓取某个指定产品线
func (e *CrawlerEngine) StartLineCrawl(lineID string, onLog func(string)) error {
	e.mu.Lock()
	if e.isBusy {
		e.mu.Unlock()
		return fmt.Errorf("爬虫任务正在运行中，请稍候")
	}
	e.isBusy = true
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFunc = cancel
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			e.isBusy = false
			e.cancelFunc = nil
			e.mu.Unlock()
		}()

		send := func(format string, a ...interface{}) {
			msg := fmt.Sprintf(format, a...)
			log.Println("[Crawler]", msg)
			if onLog != nil {
				onLog(msg)
			}
		}

		send("🚀 启动指定产品线 [%s] 快速深度爬取...", lineID)
		prods, err := e.catCrawler.FetchProductsByLine(lineID, lineID)
		if err != nil {
			send("❌ 获取产品型号列表失败: %v", err)
			e.notifyFinished(false, "获取型号列表失败")
			return
		}
		send("✅ 成功发现 %d 个产品型号，开始抓取型号版本与文档...", len(prods))

		var docCount int

		for i, prod := range prods {
			select {
			case <-ctx.Done():
				send("🛑 任务已被用户手动停止")
				e.notifyFinished(false, "任务已停止")
				return
			default:
			}

			send("   [%d/%d] 正在抓取型号: %s (PID: %s)...", i+1, len(prods), prod.Name, prod.ID)
			e.catCrawler.FetchSubModelsAndVersions(prod.ID)

			docs, err := e.docCrawler.FetchDocsByProduct(prod, lineID, "")
			if err == nil && len(docs) > 0 {
				docCount += len(docs)
				send("   └─ 发现并入库 %d 篇产品文档", len(docs))
			} else if IsWsfCheckError(err) {
				// 首次被拦截立即触发自动熔断
				send("🚨【安全拦截与自动熔断】检测到华为网关 WSF Check 校验拦截！")
				send("🛑 为避免无意义的无效请求，爬虫引擎已自动停止后续爬取任务。")
				send("💡【解决方案】请在浏览器打开 support.huawei.com 复制请求头中的 Cookie，粘贴到本系统的【系统设置】->【自定义 Cookie】并保存，然后重新启动爬取即可！")
				e.notifyFinished(false, "网关安全拦截已熔断")
				return
			}
		}

		send("🎉 产品线 [%s] 爬取完成！共收录 %d 个型号，%d 篇产品文档", lineID, len(prods), docCount)
		e.notifyFinished(true, "产品线爬取完成")
	}()

	return nil
}
