package crawler

import (
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
	body, err := c.client.DoRequest("GET", apiURL, nil, "https://support.huawei.com/enterprise/zh/index.html")
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
				ID:         lineID,
				CategoryID: catID,
				Name:       sub.Name,
				NameURL:    sub.URL,
				ProID:      pid,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			productLines = append(productLines, line)
		}
	}

	if len(categories) > 0 {
		c.repo.UpsertCategories(categories)
	}
	if len(productLines) > 0 {
		c.repo.UpsertProductLines(productLines)
	}

	logger.Info("成功抓取产品大类与产品线",
		zap.Int("categories", len(categories)),
		zap.Int("productLines", len(productLines)),
	)
	return categories, nil
}

// FetchProductsByLine 抓取指定产品线下的产品系列
func (c *CategoryCrawler) FetchProductsByLine(lineID string, linePid string) ([]store.Product, error) {
	if linePid == "" {
		linePid = lineID
	}
	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/sub-model?selfPbiId=%s&submodel=doc", linePid)
	referer := fmt.Sprintf("https://support.huawei.com/enterprise/zh/category/switches-pid-%s?submodel=doc", linePid)
	body, err := c.client.DoRequest("GET", apiURL, nil, referer)
	if err != nil {
		logger.Warn("抓取产品线型号树失败", zap.String("lineId", lineID), zap.String("url", apiURL), zap.Error(err))
		return nil, fmt.Errorf("抓取产品型号树失败: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Data struct {
			CategoryName        string `json:"categoryName"`
			ProductLineName     string `json:"productLineName"`
			ProductNaviTermList []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
				SubTerms    []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					NameURL   string `json:"nameUrl"`
					SubModels []struct {
						ID      string `json:"id"`
						Name    string `json:"name"`
						NameURL string `json:"nameUrl"`
					} `json:"subModels"`
				} `json:"subTerms"`
				SubModels []struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					NameURL string `json:"nameUrl"`
				} `json:"subModels"`
			} `json:"productNaviTermList"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Error("解析产品型号树 JSON 失败", zap.String("lineId", lineID), zap.Error(err))
		return nil, fmt.Errorf("解析产品型号树 JSON 失败: %w", err)
	}

	var products []store.Product
	now := time.Now()

	for _, nav := range resp.Data.ProductNaviTermList {
		groupName := nav.Name
		// 1. 直属型号
		for _, sm := range nav.SubModels {
			if sm.ID != "" && sm.Name != "" {
				products = append(products, store.Product{
					ID:            sm.ID,
					ProductLineID: lineID,
					Name:          sm.Name,
					NameURL:       sm.NameURL,
					NaviGroup:     groupName,
					CreatedAt:     now,
					UpdatedAt:     now,
				})
			}
		}
		// 2. subTerms 系列/型号 (如 CloudEngine 58&68&78&88&98, S12700 等)
		for _, st := range nav.SubTerms {
			if st.ID != "" && st.Name != "" {
				products = append(products, store.Product{
					ID:            st.ID,
					ProductLineID: lineID,
					Name:          st.Name,
					NameURL:       st.NameURL,
					NaviGroup:     groupName,
					CreatedAt:     now,
					UpdatedAt:     now,
				})
			}
			// 3. subTerms 下的细分子型号
			for _, sm := range st.SubModels {
				if sm.ID != "" && sm.Name != "" {
					products = append(products, store.Product{
						ID:            sm.ID,
						ProductLineID: lineID,
						Name:          sm.Name,
						NameURL:       sm.NameURL,
						NaviGroup:     groupName + " / " + st.Name,
						CreatedAt:     now,
						UpdatedAt:     now,
					})
				}
			}
		}
	}

	if len(products) > 0 {
		c.repo.UpsertProducts(products)
	}
	logger.Info("产品线解析成功",
		zap.String("lineName", resp.Data.ProductLineName),
		zap.Int("productCount", len(products)),
	)
	return products, nil
}

// FetchSubModelsAndVersions 抓取产品型号下的细分子型号与版本
func (c *CategoryCrawler) FetchSubModelsAndVersions(productID string) ([]store.SubModel, []store.Version, error) {
	logger.Debug("开始抓取产品型号的细分子型号与版本", zap.String("productId", productID))
	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/sub-model-and-version/pc?subModelOfferingId=&pbiId=%s", productID)
	referer := fmt.Sprintf("https://support.huawei.com/enterprise/zh/product-pid-%s", productID)
	body, err := c.client.DoRequest("GET", apiURL, nil, referer)
	if err != nil {
		logger.Warn("抓取子型号与版本网络失败", zap.String("productId", productID), zap.Error(err))
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
		logger.Error("解析子型号与版本 JSON 失败", zap.String("productId", productID), zap.Error(err))
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
				ID:        v.ID,
				ProductID: productID,
				Name:      v.Name,
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
	}

	if len(subModels) > 0 {
		c.repo.UpsertSubModels(subModels)
	}
	if len(versions) > 0 {
		c.repo.UpsertVersions(versions)
	}

	logger.Debug("子型号与版本解析入库成功",
		zap.String("productId", productID),
		zap.Int("subModels", len(subModels)),
		zap.Int("versions", len(versions)),
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

	cfg := config.GetConfig()
	numWorkers := 1
	if cfg != nil && cfg.CrawlerThreads > 0 {
		numWorkers = cfg.CrawlerThreads
	}
	if numWorkers > 32 {
		numWorkers = 32
	}
	if numWorkers > len(allLines) && len(allLines) > 0 {
		numWorkers = len(allLines)
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
		go func(workerID int) {
			defer wg.Done()
			workerTag := fmt.Sprintf("[爬虫-%d]", workerID)

			for task := range taskChan {
				line := task.line
				logger.Debug("分类同步协程开始解析产品线",
					zap.Int("workerId", workerID),
					zap.String("lineName", line.Name),
					zap.String("lineId", line.ID),
				)

				prods, err := c.FetchProductsByLine(line.ID, line.ProID)
				if err == nil {
					atomic.AddInt64(&totalProducts, int64(len(prods)))
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
		}(w)
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
			return rest[:qIdx]
		}
		return rest
	}
	u, err := url.Parse(urlStr)
	if err == nil {
		return u.Query().Get("pid")
	}
	return ""
}
