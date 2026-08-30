package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/config"
	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type CategorySyncStatus struct {
	IsSyncing       bool   `json:"isSyncing"`
	IsInitial       bool   `json:"isInitial"`
	CurrentLine     int    `json:"currentLine"`
	TotalLines      int    `json:"totalLines"`
	CurrentLineName string `json:"currentLineName"`
	TotalProducts   int    `json:"totalProducts"`
	TotalCategories int    `json:"totalCategories"`
}

type CategoryCrawler struct {
	client     *HttpClient
	repo       *store.Repository
	syncStatus CategorySyncStatus
	mu         sync.RWMutex
}

func NewCategoryCrawler(client *HttpClient, repo *store.Repository) *CategoryCrawler {
	return &CategoryCrawler{
		client: client,
		repo:   repo,
	}
}

func (c *CategoryCrawler) GetSyncStatus() CategorySyncStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.syncStatus
}

func (c *CategoryCrawler) setSyncStatus(st CategorySyncStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncStatus = st
}

// FetchCategories 抓取全部分类和产品线
func (c *CategoryCrawler) FetchCategories() ([]store.Category, error) {
	apiURL := "https://support.huawei.com/supportgateway/mysupport/v1/enterprise/index/product/category"
	body, err := c.client.DoRequest(context.Background(), "GET", apiURL, nil, "https://support.huawei.com/enterprise/zh/index.html")
	if err != nil {
		logger.Error("抓取产品分类失败", zap.String("url", apiURL), zap.Error(err))
		return nil, fmt.Errorf("抓取产品分类失败: %w", err)
	}

	var resp struct {
		Status string `json:"status"`
		Data   []struct {
			Name     string `json:"name"`
			Icon     string `json:"icon"`
			SubTerms []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"subTerms"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Error("解析产品分类 JSON 失败", zap.Error(err))
		return nil, fmt.Errorf("解析产品分类 JSON 失败: %w", err)
	}

	var categories []store.Category
	var productLines []store.ProductLine
	now := time.Now()

	for i, catItem := range resp.Data {
		catID := fmt.Sprintf("CAT-%d", i+1)
		cat := store.Category{
			ID:        catID,
			Name:      catItem.Name,
			NameURL:   "",
			CreatedAt: now,
			UpdatedAt: now,
		}
		categories = append(categories, cat)

		for _, sub := range catItem.SubTerms {
			pid := extractPidFromURL(sub.URL)
			lineID := pid
			if lineID == "" {
				lineID = fmt.Sprintf("%s-%s", catID, sub.Name)
			}
			line := store.ProductLine{
				ID:           lineID,
				CategoryID:   catID,
				CategoryName: catItem.Name, // 冗余记录大类名称
				Name:         sub.Name,
				NameURL:      sub.URL,
				ProID:        pid,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			productLines = append(productLines, line)
		}
	}

	if c.repo != nil {
		if len(categories) > 0 {
			c.repo.UpsertCategories(categories)
		}
		if len(productLines) > 0 {
			c.repo.UpsertProductLines(productLines)
		}
	}

	logger.Info("成功抓取产品大类与产品线",
		zap.Int("categories", len(categories)),
		zap.Int("productLines", len(productLines)),
	)
	return categories, nil
}

// FetchProductsByLine 抓取指定产品线下的产品系列 (兼容无 Context 调用)
func (c *CategoryCrawler) FetchProductsByLine(lineID string, linePid string) ([]store.Product, error) {
	return c.FetchProductsByLineWithContext(context.Background(), lineID, linePid)
}

// FetchProductsByLineWithContext 带有上下文 (可附带 Worker 编号与取消信号) 的产品型号树抓取
func (c *CategoryCrawler) FetchProductsByLineWithContext(ctx context.Context, lineID string, linePid string) ([]store.Product, error) {
	if linePid == "" {
		linePid = lineID
	}
	workerID, workerTag := GetWorkerFromContext(ctx)

	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/sub-model?selfPbiId=%s&submodel=doc", linePid)
	referer := fmt.Sprintf("https://support.huawei.com/enterprise/zh/category/switches-pid-%s?submodel=doc", linePid)
	body, err := c.client.DoRequest(ctx, "GET", apiURL, nil, referer)
	if err != nil {
		logger.Warn(FormatWorkerMsg(workerTag, "抓取产品线型号树失败"),
			append([]zap.Field{
				zap.String("lineId", lineID),
				zap.String("url", apiURL),
				zap.Error(err),
			}, WorkerZapFields(workerID)...)...,
		)
		return nil, fmt.Errorf("抓取产品型号树失败: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Data struct {
			CategoryName        string `json:"categoryName"`
			ProductLineName     string `json:"productLineName"`
			CustomLink          []struct {
				Title string `json:"title"`
				Links [][]struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"links"`
			} `json:"customLink"`
			ProductNaviTermList []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
				SubTerms    []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
					NameURL     string `json:"nameUrl"`
					SubModels   []struct {
						ID           string `json:"id"`
						SubModelID   string `json:"subModelId"`
						Name         string `json:"name"`
						SubModelName string `json:"subModelName"`
						NameURL      string `json:"nameUrl"`
					} `json:"subModels"`
				} `json:"subTerms"`
				SubModels []struct {
					ID           string `json:"id"`
					SubModelID   string `json:"subModelId"`
					Name         string `json:"name"`
					SubModelName string `json:"subModelName"`
					NameURL      string `json:"nameUrl"`
				} `json:"subModels"`
			} `json:"productNaviTermList"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Error(FormatWorkerMsg(workerTag, "解析产品型号树 JSON 失败"),
			append([]zap.Field{
				zap.String("lineId", lineID),
				zap.Error(err),
			}, WorkerZapFields(workerID)...)...,
		)
		return nil, fmt.Errorf("解析产品型号树 JSON 失败: %w", err)
	}

	productMap := make(map[string]*store.Product)
	productGroups := make(map[string][]string)
	now := time.Now()

	addGroup := func(id string, group string) {
		group = strings.TrimSpace(group)
		if group == "" {
			return
		}
		for _, g := range productGroups[id] {
			if g == group {
				return
			}
		}
		productGroups[id] = append(productGroups[id], group)
	}

	// 1. 解析 customLink（包括“推荐产品”、“解决方案”等所有置顶分组）
	for _, cl := range resp.Data.CustomLink {
		groupTitle := strings.TrimSpace(cl.Title)
		if groupTitle == "" {
			groupTitle = "推荐产品"
		}
		for _, linkGroup := range cl.Links {
			for _, item := range linkGroup {
				name := strings.TrimSpace(item.Name)
				urlStr := strings.TrimSpace(item.URL)
				if name == "" || urlStr == "" {
					continue
				}
				pid := extractPidFromURL(urlStr)
				if pid != "" {
					addGroup(pid, groupTitle)
					if _, exists := productMap[pid]; !exists {
						productMap[pid] = &store.Product{
							ID:            pid,
							ProductLineID: lineID,
							Name:          name,
							NameURL:       urlStr,
							CreatedAt:     now,
							UpdatedAt:     now,
						}
					}
				}
			}
		}
	}

	// 2. 解析 productNaviTermList（包括 防火墙&VPN网关、入侵防御&检测系统 等所有正规业务系列）
	for _, nav := range resp.Data.ProductNaviTermList {
		groupName := strings.TrimSpace(nav.Name)
		if groupName == "" {
			groupName = "产品系列"
		}

		// (1) subTerms 系列/型号 (如 USG 系列、SVN5600、SVN5800 等)
		for _, st := range nav.SubTerms {
			stID := strings.TrimSpace(st.ID)
			stName := strings.TrimSpace(st.Name)
			if stID != "" && stName != "" {
				addGroup(stID, groupName)
				if _, exists := productMap[stID]; !exists {
					productMap[stID] = &store.Product{
						ID:            stID,
						ProductLineID: lineID,
						Name:          stName,
						NameURL:       st.NameURL,
						CreatedAt:     now,
						UpdatedAt:     now,
					}
				}
			}
		}

		// (2) 直属型号
		for _, sm := range nav.SubModels {
			smID := strings.TrimSpace(sm.ID)
			if smID == "" {
				smID = strings.TrimSpace(sm.SubModelID)
			}
			smName := strings.TrimSpace(sm.Name)
			if smName == "" {
				smName = strings.TrimSpace(sm.SubModelName)
			}
			if smID != "" && smName != "" {
				addGroup(smID, groupName)
				if _, exists := productMap[smID]; !exists {
					productMap[smID] = &store.Product{
						ID:            smID,
						ProductLineID: lineID,
						Name:          smName,
						NameURL:       sm.NameURL,
						CreatedAt:     now,
						UpdatedAt:     now,
					}
				}
			}
		}
	}

	var products []store.Product
	for id, p := range productMap {
		groups := productGroups[id]
		if len(groups) > 0 {
			p.NaviGroup = strings.Join(groups, ",")
		} else {
			p.NaviGroup = "其他"
		}
		products = append(products, *p)
	}

	lineName := resp.Data.ProductLineName
	if lineName == "" {
		if c.repo != nil {
			if l, _ := c.repo.GetProductLineByID(lineID); l != nil && l.Name != "" {
				lineName = l.Name
			} else {
				lineName = lineID
			}
		} else {
			lineName = lineID
		}
	}

	if len(products) == 0 {
		logger.Warn(FormatWorkerMsg(workerTag, fmt.Sprintf("产品线 [%s] 解析结果为空（未获取到型号列表）", lineName)),
			append([]zap.Field{
				zap.String("lineName", lineName),
				zap.String("lineId", lineID),
				zap.String("linePid", linePid),
				zap.String("requestUrl", apiURL),
				zap.String("responseBody", string(body)),
			}, WorkerZapFields(workerID)...)...,
		)

		// 容错兜底：若该产品线本身是独立单品/解决方案（华为未分配 sub-model 树，直接挂载版本与文档），将其自身收录为产品型号
		if c.repo != nil {
			if line, _ := c.repo.GetProductLineByID(lineID); line != nil && line.ProID != "" {
				singleProd := store.Product{
					ID:            line.ProID,
					ProductLineID: line.ID,
					Name:          line.Name,
					NameURL:       line.NameURL,
					NaviGroup:     "独立产品/解决方案",
					CreatedAt:     now,
					UpdatedAt:     now,
				}
				products = append(products, singleProd)
				c.repo.UpsertProducts(products)
				logger.Info(FormatWorkerMsg(workerTag, fmt.Sprintf("产品线 [%s] 本身为独立单品/解决方案，已作为独立型号自动收录", line.Name)),
					append([]zap.Field{
						zap.String("lineName", line.Name),
						zap.String("productId", line.ProID),
					}, WorkerZapFields(workerID)...)...,
				)
			}
		}
	} else {
		if c.repo != nil {
			c.repo.UpsertProducts(products)
		}
		logger.Debug(FormatWorkerMsg(workerTag, fmt.Sprintf("产品线 [%s] 型号入库完成", lineName)),
			append([]zap.Field{
				zap.String("lineName", lineName),
				zap.Int("productCount", len(products)),
			}, WorkerZapFields(workerID)...)...,
		)
	}

	return products, nil
}

// FetchSubModelsAndVersions 抓取产品型号下的细分子型号与版本 (兼容无 Context 调用)
func (c *CategoryCrawler) FetchSubModelsAndVersions(productID string) ([]store.SubModel, []store.Version, error) {
	return c.FetchSubModelsAndVersionsWithContext(context.Background(), productID)
}

// FetchSubModelsAndVersionsWithContext 带有上下文的产品型号细分子型号与版本抓取
func (c *CategoryCrawler) FetchSubModelsAndVersionsWithContext(ctx context.Context, productID string) ([]store.SubModel, []store.Version, error) {
	workerID, workerTag := GetWorkerFromContext(ctx)

	logger.Debug(FormatWorkerMsg(workerTag, "开始抓取产品型号的细分子型号与版本"),
		append([]zap.Field{zap.String("productId", productID)}, WorkerZapFields(workerID)...)...,
	)
	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/sub-model-and-version/pc?subModelOfferingId=&pbiId=%s", productID)
	referer := fmt.Sprintf("https://support.huawei.com/enterprise/zh/product-pid-%s", productID)
	body, err := c.client.DoRequest(ctx, "GET", apiURL, nil, referer)
	if err != nil {
		logger.Warn(FormatWorkerMsg(workerTag, "抓取子型号与版本网络失败"),
			append([]zap.Field{zap.String("productId", productID), zap.Error(err)}, WorkerZapFields(workerID)...)...,
		)
		return nil, nil, fmt.Errorf("抓取子型号与版本失败: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Data struct {
			SubModels []struct {
				ID     string   `json:"id"`
				Name   string   `json:"name"`
				Tag    string   `json:"tag"`
				PbiTid []string `json:"pbiTid"`
			} `json:"subModels"`
			VersionR map[string]struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"versionR"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Error(FormatWorkerMsg(workerTag, "解析子型号与版本 JSON 失败"),
			append([]zap.Field{zap.String("productId", productID), zap.Error(err)}, WorkerZapFields(workerID)...)...,
		)
		return nil, nil, fmt.Errorf("解析子型号与版本 JSON 失败: %w", err)
	}

	now := time.Now()
	var subModels []store.SubModel
	for _, sm := range resp.Data.SubModels {
		if sm.ID != "" && sm.Name != "" {
			subModels = append(subModels, store.SubModel{
				ID:        sm.ID,
				ProductID: productID,
				Name:      sm.Name,
				Tag:       sm.Tag,
				PbiTids:   strings.Join(sm.PbiTid, ","),
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
	}

	var versions []store.Version
	for _, v := range resp.Data.VersionR {
		if v.ID != "" && v.Name != "" {
			versions = append(versions, store.Version{
				ID:        fmt.Sprintf("%s_%s", productID, v.ID), // 联合主键隔离，防止跨产品同名版本相互覆盖
				ProductID: productID,
				RawID:     v.ID,
				Name:      v.Name,
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
	}

	if c.repo != nil {
		if len(subModels) > 0 {
			c.repo.UpsertSubModels(subModels)
		}
		if len(versions) > 0 {
			c.repo.UpsertVersions(versions)
		}
	}

	logger.Debug(FormatWorkerMsg(workerTag, "子型号与版本解析入库成功"),
		append([]zap.Field{
			zap.String("productId", productID),
			zap.Int("subModels", len(subModels)),
			zap.Int("versions", len(versions)),
		}, WorkerZapFields(workerID)...)...,
	)
	return subModels, versions, nil
}

// SyncAllCategoriesAndProducts 同步全量产品大类、二级产品线以及产品型号树
func (c *CategoryCrawler) SyncAllCategoriesAndProducts(onProgress func(CategorySyncStatus), onFinished func(CategorySyncStatus)) error {
	existingCats, _ := c.repo.GetAllCategories()
	isInitial := len(existingCats) == 0

	status := CategorySyncStatus{
		IsSyncing: true,
		IsInitial: isInitial,
	}
	c.setSyncStatus(status)

	if onProgress != nil {
		onProgress(status)
	}

	logger.Info("正在连接华为官网更新产品大类与二级产品线数据...")
	categories, err := c.FetchCategories()
	if err != nil {
		logger.Warn("更新产品大类失败", zap.Error(err))
		status.IsSyncing = false
		c.setSyncStatus(status)
		if onFinished != nil {
			onFinished(status)
		}
		return err
	}

	status.TotalCategories = len(categories)
	var allLines []store.ProductLine
	for _, cat := range categories {
		lines, _ := c.repo.GetProductLinesByCategoryID(cat.ID)
		allLines = append(allLines, lines...)
	}
	status.TotalLines = len(allLines)
	c.setSyncStatus(status)
	if onProgress != nil {
		onProgress(status)
	}

	numWorkers := 1
	if c.client != nil && c.client.GetLimiter() != nil {
		numWorkers = c.client.GetLimiter().GetLimit()
	} else {
		cfg := config.GetConfig()
		if cfg.CrawlerThreads > 0 {
			numWorkers = cfg.CrawlerThreads
		}
	}
	if numWorkers > 32 {
		numWorkers = 32
	}
	if numWorkers > len(allLines) && len(allLines) > 0 {
		numWorkers = len(allLines)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	logger.Info("启动产品分类树多线程同步",
		zap.Int("workers", numWorkers),
		zap.Int("totalLines", len(allLines)),
	)

	type lineTask struct {
		index int
		line  store.ProductLine
	}

	taskChan := make(chan lineTask, len(allLines))
	for i, l := range allLines {
		taskChan <- lineTask{index: i + 1, line: l}
	}
	close(taskChan)

	var totalProducts int64
	var completedLines int64
	var statusMu sync.Mutex
	var wg sync.WaitGroup

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		workerID := w
		logger.SafeGo(fmt.Sprintf("category-sync-worker-%d", workerID), func() {
			defer wg.Done()
			workerTag := fmt.Sprintf("[爬虫-%d]", workerID)
			workerCtx := WithWorkerContext(context.Background(), workerID)

			for {
				// 自适应降线程检查
				if c.client != nil && c.client.GetLimiter() != nil && workerID > c.client.GetLimiter().GetLimit() {
					return
				}

				task, ok := <-taskChan
				if !ok {
					return
				}

				line := task.line
				prods, err := c.FetchProductsByLineWithContext(workerCtx, line.ID, line.ProID)
				if err == nil {
					atomic.AddInt64(&totalProducts, int64(len(prods)))
					logger.Info(fmt.Sprintf("%s 产品线 [%s] 解析成功", workerTag, line.Name),
						zap.Int("workerId", workerID),
						zap.String("lineName", line.Name),
						zap.Int("productCount", len(prods)),
					)
				} else {
					logger.Warn(fmt.Sprintf("%s 产品线 [%s] 解析失败", workerTag, line.Name),
						zap.Int("workerId", workerID),
						zap.String("lineName", line.Name),
						zap.Error(err),
					)
				}

				done := atomic.AddInt64(&completedLines, 1)

				statusMu.Lock()
				status.CurrentLine = int(done)
				status.CurrentLineName = fmt.Sprintf("%s (%s)", line.Name, workerTag)
				status.TotalProducts = int(atomic.LoadInt64(&totalProducts))
				curStatus := status
				c.setSyncStatus(curStatus)
				statusMu.Unlock()

				if onProgress != nil {
					onProgress(curStatus)
				}
			}
		})
	}

	wg.Wait()

	status.IsSyncing = false
	status.CurrentLine = len(allLines)
	status.TotalProducts = int(atomic.LoadInt64(&totalProducts))
	c.setSyncStatus(status)
	if onFinished != nil {
		onFinished(status)
	}

	logger.Info("产品分类树更新完成",
		zap.Int("categories", len(categories)),
		zap.Int("lines", len(allLines)),
		zap.Int("products", int(totalProducts)),
		zap.Int("workers", numWorkers),
	)
	return nil
}

func extractPidFromURL(urlStr string) string {
	if idx := strings.Index(urlStr, "-pid-"); idx != -1 {
		rest := urlStr[idx+5:]
		if qIdx := strings.Index(rest, "?"); qIdx != -1 {
			rest = rest[:qIdx]
		}
		if sIdx := strings.Index(rest, "/"); sIdx != -1 {
			rest = rest[:sIdx]
		}
		if hIdx := strings.Index(rest, "#"); hIdx != -1 {
			rest = rest[:hIdx]
		}
		return strings.TrimSpace(rest)
	}
	u, err := url.Parse(urlStr)
	if err == nil {
		return strings.TrimSpace(u.Query().Get("pid"))
	}
	return ""
}
