package crawler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type DocCrawler struct {
	client *HttpClient
	repo   *store.Repository
}

func NewDocCrawler(client *HttpClient, repo *store.Repository) *DocCrawler {
	return &DocCrawler{client: client, repo: repo}
}

// DocFileInfoResult 文档下载直链响应
type DocFileInfoResult struct {
	NID         string `json:"nid"`
	FileName    string `json:"fileName"`
	DownloadURL string `json:"downloadUrl"`
	FileSizeKB  string `json:"fileSize"`
	PartNo      string `json:"partNo"`
	Type        string `json:"type"`
}

// FetchDocFileInfo 实时解析文档下载直链 (支持任意合法文档 NID)
func (d *DocCrawler) FetchDocFileInfo(nid string) (*DocFileInfoResult, error) {
	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/doc/file-info?nid=%s", nid)
	referer := "https://support.huawei.com/enterprise/zh/index.html"
	body, err := d.client.DoRequest("GET", apiURL, nil, referer)
	if err != nil {
		logger.Error("请求文档下载直链失败", zap.String("nid", nid), zap.Error(err))
		return nil, fmt.Errorf("请求文档下载信息失败 (nid: %s): %w", nid, err)
	}

	var resp struct {
		Code string `json:"code"`
		Data struct {
			IsSingleDoc bool `json:"isSingleDoc"`
			MainDocs    []struct {
				NID         string `json:"nid"`
				FileName    string `json:"fileName"`
				FileSize    string `json:"fileSize"`
				PartNo      string `json:"partNo"`
				Type        string `json:"type"`
				DownloadURL string `json:"downloadUrl"`
			} `json:"mainDocs"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Error("解析文档下载信息 JSON 失败", zap.String("nid", nid), zap.Error(err))
		return nil, fmt.Errorf("解析文档下载信息 JSON 失败: %w", err)
	}

	if len(resp.Data.MainDocs) == 0 {
		logger.Warn("未返回文档下载直链", zap.String("nid", nid))
		return nil, fmt.Errorf("未找到文档下载直链 (nid: %s)", nid)
	}

	mainDoc := resp.Data.MainDocs[0]
	logger.Info("成功解析文档下载直链",
		zap.String("nid", mainDoc.NID),
		zap.String("fileName", mainDoc.FileName),
		zap.String("type", mainDoc.Type),
	)

	return &DocFileInfoResult{
		NID:         mainDoc.NID,
		FileName:    mainDoc.FileName,
		DownloadURL: mainDoc.DownloadURL,
		FileSizeKB:  mainDoc.FileSize,
		PartNo:      mainDoc.PartNo,
		Type:        mainDoc.Type,
	}, nil
}

// ParseDocTypeFromName 根据文档标题自动识别文档格式
func ParseDocTypeFromName(name string) string {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "(hdx)") || strings.Contains(lower, "hdx") {
		return "HDX"
	}
	if strings.Contains(lower, "(chm)") || strings.Contains(lower, "chm") {
		return "CHM"
	}
	if strings.Contains(lower, "(pdf)") || strings.Contains(lower, "pdf") {
		return "PDF"
	}
	if strings.Contains(lower, "(zip)") || strings.Contains(lower, "zip") {
		return "ZIP"
	}
	return "OTHER"
}

// FormatBytes 把字节数格式化为人性化字符串
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ParseSizeStringToBytes 将如 "715,836.18" (KB) 或 "166558042" (Bytes) 字符串转为 int64
func ParseSizeStringToBytes(sizeStr string) int64 {
	clean := strings.ReplaceAll(sizeStr, ",", "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(clean, 64); err == nil {
		if f > 10000000 {
			return int64(f)
		}
		return int64(f * 1024)
	}
	return 0
}

// ParseDocItemFromJSON 从接口 JSON 项解析为标准 Document
func ParseDocItemFromJSON(item map[string]interface{}, productID, productName, productLineName, categoryName string) *store.Document {
	nid, _ := item["nid"].(string)
	name, _ := item["name"].(string)
	if nid == "" || name == "" {
		return nil
	}

	docType := ParseDocTypeFromName(name)
	var fileSizeBytes int64
	var fileName string

	if fv, ok := item["fieldValues"].(map[string]interface{}); ok {
		if entityStr, ok := fv["contentEntityListStr"].(string); ok && entityStr != "" {
			var entities []map[string]interface{}
			if err := json.Unmarshal([]byte(entityStr), &entities); err == nil && len(entities) > 0 {
				if fn, ok := entities[0]["fileName"].(string); ok {
					fileName = fn
				}
				if fsStr, ok := entities[0]["fileSize"].(string); ok {
					fileSizeBytes = ParseSizeStringToBytes(fsStr)
				}
			}
		}
	}

	if fileName == "" {
		fileName = name + "." + strings.ToLower(docType)
	}
	fileSizeStr := FormatBytes(fileSizeBytes)

	now := time.Now()
	return &store.Document{
		NID:             nid,
		ProductID:       productID,
		ProductName:     productName,
		ProductLineName: productLineName,
		CategoryName:    categoryName,
		Name:            name,
		DocType:         docType,
		FileName:        fileName,
		FileSizeBytes:   fileSizeBytes,
		FileSizeStr:     fileSizeStr,
		CrawlTime:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// FetchDocsByProduct 从在线 API 抓取产品文档并递归解析嵌套树
func (d *DocCrawler) FetchDocsByProduct(product store.Product, lineName, catName string) ([]store.Document, error) {
	referer := fmt.Sprintf("https://support.huawei.com/enterprise/zh/product-pid-%s", product.ID)

	// 精确对齐真实请求体参数：isAsc 必须为 0, orderBy 必须为 "name"
	reqPayload := map[string]interface{}{
		"productId":        product.ID,
		"businessScenario": "",
		"relateProductId":  "",
		"subModelId":       "",
		"isAsc":            0,
		"orderBy":          "name",
		"versionId":        "",
		"offeringName":     product.Name,
	}
	bodyBytes, _ := json.Marshal(reqPayload)

	// 1. 请求 second-item 核心文档树接口
	apiURL := "https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/doc/second-item"
	respBytes, err := d.client.DoRequest("POST", apiURL, bodyBytes, referer)
	if err != nil {
		// 若 second-item 失败，再尝试 eos 接口
		apiURL2 := "https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/doc/eos"
		respBytes, err = d.client.DoRequest("POST", apiURL2, bodyBytes, referer)
		if err != nil {
			logger.Warn("获取产品文档列表失败",
				zap.String("product", product.Name),
				zap.String("pid", product.ID),
				zap.Error(err),
			)
			return nil, err
		}
	}

	var resp struct {
		Code string                   `json:"code"`
		Data []map[string]interface{} `json:"data"`
	}

	if err := json.Unmarshal(respBytes, &resp); err != nil {
		logger.Error("解析产品文档列表 JSON 失败", zap.String("product", product.Name), zap.Error(err))
		return nil, fmt.Errorf("解析文档列表 JSON 失败: %w", err)
	}

	// 2. 递归遍历 subAggs 嵌套树提取所有文档
	var rawDocs []map[string]interface{}
	var findDocs func(item map[string]interface{})
	findDocs = func(item map[string]interface{}) {
		if nid, ok := item["nid"].(string); ok && nid != "" {
			rawDocs = append(rawDocs, item)
			return
		}
		if subAggs, ok := item["subAggs"].([]interface{}); ok {
			for _, sub := range subAggs {
				if subm, ok := sub.(map[string]interface{}); ok {
					findDocs(subm)
				}
			}
		}
		for _, k := range []string{"docList", "list", "documents", "data"} {
			if list, ok := item[k].([]interface{}); ok {
				for _, sub := range list {
					if subm, ok := sub.(map[string]interface{}); ok {
						findDocs(subm)
					}
				}
			}
		}
	}

	for _, dItem := range resp.Data {
		findDocs(dItem)
	}

	// 3. 转换为标准 Document 结构并去重入库
	var docs []store.Document
	seenNID := make(map[string]bool)

	for _, item := range rawDocs {
		doc := ParseDocItemFromJSON(item, product.ID, product.Name, lineName, catName)
		if doc != nil && !seenNID[doc.NID] {
			seenNID[doc.NID] = true
			docs = append(docs, *doc)
		}
	}

	if len(docs) > 0 {
		d.repo.UpsertDocuments(docs)
		logger.Info("🎉 成功解析并入库产品文档",
			zap.String("product", product.Name),
			zap.Int("docCount", len(docs)),
		)
	}

	return docs, nil
}
