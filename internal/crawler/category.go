package crawler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type CategoryCrawler struct {
	client *HttpClient
	repo   *store.Repository
}

func NewCategoryCrawler(client *HttpClient, repo *store.Repository) *CategoryCrawler {
	return &CategoryCrawler{client: client, repo: repo}
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
	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/sub-model-and-version/pc?subModelOfferingId=&pbiId=%s", productID)
	referer := fmt.Sprintf("https://support.huawei.com/enterprise/zh/product-pid-%s", productID)
	body, err := c.client.DoRequest("GET", apiURL, nil, referer)
	if err != nil {
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

	return subModels, versions, nil
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
