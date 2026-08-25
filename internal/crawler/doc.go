package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

var (
	reDocTypeParen = regexp.MustCompile(`(?i)[\(（](hdx|chm|pdf|zip|多媒体)[\)）]`)
	reDocTypeClean = regexp.MustCompile(`(?i)\s*[\(（](?:hdx|chm|pdf|zip|多媒体)[\)）]`)
	reCleanMedia   = regexp.MustCompile(`(?i)\s*[\(（]多媒体[\)）]`)
	reVersionTag   = regexp.MustCompile(`(?i)\s*V\d{3}R\d{3}(?:[,\s]*[CB]\d+)*(?:SP[HC]\d+)?`)
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
	return d.FetchDocFileInfoWithContext(context.Background(), nid)
}

// FetchDocFileInfoWithContext 带有上下文取消支持的文档下载直链解析
func (d *DocCrawler) FetchDocFileInfoWithContext(ctx context.Context, nid string) (*DocFileInfoResult, error) {
	apiURL := fmt.Sprintf("https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/doc/file-info?nid=%s", nid)
	referer := "https://support.huawei.com/enterprise/zh/index.html"
	body, err := d.client.DoRequest(ctx, "GET", apiURL, nil, referer)
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

// ParseDocType 根据文档标题及附加字段自动识别文档格式 (精准正则与后缀判定，避免型号误伤)
func ParseDocType(name string, fv map[string]interface{}) string {
	// 1. 优先判定多媒体格式
	if strings.Contains(name, "多媒体") || strings.Contains(name, "视频") {
		return "多媒体"
	}
	if fv != nil {
		if isVideo, ok := fv["isVideo"].(string); ok && isVideo == "EBOOL-true" {
			return "多媒体"
		}
		if secItem, ok := fv["secondItem"].(string); ok && secItem == "SEDPTNEW-STMV" {
			return "多媒体"
		}
	}

	// 2. 从标题括号中精准提取格式标记，例如 (hdx), (pdf), (chm), (zip)
	matches := reDocTypeParen.FindStringSubmatch(name)
	if len(matches) > 1 {
		return strings.ToUpper(matches[1])
	}

	// 3. 检查文件名自然扩展名
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "HDX", "CHM", "PDF", "ZIP":
		return ext
	}

	return "OTHER"
}

// ParseDocTypeFromName 根据文档标题自动识别文档格式 (兼容旧调用)
func ParseDocTypeFromName(name string) string {
	return ParseDocType(name, nil)
}

// ExtractPublishDate 提取 YYYY-MM-DD 格式的发布/更新日期
func ExtractPublishDate(pubTime, updTime string) string {
	str := pubTime
	if str == "" {
		str = updTime
	}
	str = strings.TrimSpace(str)
	if len(str) >= 10 {
		datePart := str[:10]
		// 校验格式是否符合 2006-01-02
		if _, err := time.Parse("2006-01-02", datePart); err == nil {
			return datePart
		}
	}
	return ""
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

// ParseSizeStringToBytes 将华为接口返回的 KB 字符串转为 int64 字节数 (消除 10MB 魔法数字)
func ParseSizeStringToBytes(sizeStr string) int64 {
	clean := strings.ReplaceAll(sizeStr, ",", "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(clean, 64); err == nil && f > 0 {
		// 华为 API 中的 fileSize 统一以 KB 为单位
		return int64(f * 1024.0)
	}
	return 0
}

// getAttrZH 从 attributes map 中提取中文标签名
func getAttrZH(attrs map[string]interface{}) string {
	if attrs == nil {
		return ""
	}
	if zh, ok := attrs["ZH"].(string); ok && zh != "" {
		return zh
	}
	if zh, ok := attrs["zhName"].(string); ok && zh != "" {
		return zh
	}
	return ""
}

// ParseDocItemFromJSON 从接口 JSON 项解析为标准 Document
func ParseDocItemFromJSON(item map[string]interface{}, productID, productName, productLineName, categoryName, docCategoryGroup, docCategory string) *store.Document {
	nid, _ := item["nid"].(string)
	name, _ := item["name"].(string)
	if nid == "" || name == "" {
		return nil
	}

	publishTime, _ := item["publishTime"].(string)
	lastUpdateTime, _ := item["lastUpdateTime"].(string)
	versionName, _ := item["versionName"].(string)
	versionID, _ := item["versionId"].(string)

	fv, _ := item["fieldValues"].(map[string]interface{})
	docType := ParseDocType(name, fv)
	publishDate := ExtractPublishDate(publishTime, lastUpdateTime)

	var fileSizeBytes int64
	var fileName string

	if fv != nil {
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
		ext := strings.ToLower(docType)
		if ext == "多媒体" || ext == "other" {
			fileName = name
		} else {
			fileName = name + "." + ext
		}
	}
	fileSizeStr := FormatBytes(fileSizeBytes)

	now := time.Now()
	return &store.Document{
		NID:              nid,
		ProductID:        productID,
		ProductName:      productName,
		ProductLineName:  productLineName,
		CategoryName:     categoryName,
		VersionID:        versionID,
		VersionName:      versionName,
		Name:             name,
		DocType:          docType,
		DocCategory:      docCategory,
		DocCategoryGroup: docCategoryGroup,
		FileName:         fileName,
		FileSizeBytes:    fileSizeBytes,
		FileSizeStr:      fileSizeStr,
		PublishDate:      publishDate,
		PublishTime:      publishTime,
		LastUpdateTime:   lastUpdateTime,
		IsNewVersion:     false, // 稍后在 MarkNewVersions 中统筹计算
		CrawlTime:        now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// BaseDocGroupKey 计算文档同属系列/手册的基准 Key，用于识别跨版本的同一份文档
func BaseDocGroupKey(name, docType string) string {
	// 去除格式标记 (如 (hdx), (chm), (pdf), (多媒体), （多媒体）)
	clean := reDocTypeClean.ReplaceAllString(name, "")
	clean = reCleanMedia.ReplaceAllString(clean, "")

	// 去除 V/R/C/B 版本号部分 (如 V300R025C00, V300R024C10, C11, V200R005SPH019 等)
	clean = reVersionTag.ReplaceAllString(clean, "")

	// 去除多余空格
	clean = strings.Join(strings.Fields(clean), " ")
	return strings.TrimSpace(clean) + "::" + docType
}

// MarkNewVersions 对文档集合进行版本系列分组，并根据发布日期与版本号标记最新版本
func MarkNewVersions(docs []store.Document) {
	if len(docs) == 0 {
		return
	}

	// 1. 按照基准 Key 分组
	groups := make(map[string][]*store.Document)
	for i := range docs {
		key := BaseDocGroupKey(docs[i].Name, docs[i].DocType)
		groups[key] = append(groups[key], &docs[i])
	}

	// 2. 针对每组，对比发布时间并打标
	for _, groupDocs := range groups {
		if len(groupDocs) == 1 {
			groupDocs[0].IsNewVersion = true
			continue
		}

		var latestDate string
		var latestTime string
		for _, d := range groupDocs {
			pDate := d.PublishDate
			pTime := d.PublishTime
			if pDate > latestDate {
				latestDate = pDate
				latestTime = pTime
			} else if pDate == latestDate && pTime > latestTime {
				latestTime = pTime
			}
		}

		for _, d := range groupDocs {
			// 【关键修复】：必须同时满足日期与时间一致，避免同一天发布的多个补丁全部打标为最新
			if latestDate != "" && d.PublishDate == latestDate {
				if latestTime == "" || d.PublishTime == latestTime {
					d.IsNewVersion = true
					continue
				}
			}
			d.IsNewVersion = false
		}
	}
}

// FetchDocsByProduct 从在线 API 抓取产品文档并递归解析嵌套树 (兼容无 Context 调用)
func (d *DocCrawler) FetchDocsByProduct(product store.Product, lineName, catName string) ([]store.Document, error) {
	return d.FetchDocsByProductWithContext(context.Background(), product, lineName, catName)
}

// FetchDocsByProductWithContext 带有上下文取消支持的产品文档抓取
func (d *DocCrawler) FetchDocsByProductWithContext(ctx context.Context, product store.Product, lineName, catName string) ([]store.Document, error) {
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
	respBytes, err := d.client.DoRequest(ctx, "POST", apiURL, bodyBytes, referer)
	if err != nil {
		// 若 second-item 失败，再尝试 eos 接口
		apiURL2 := "https://support.huawei.com/supportgateway/supproductservice/v1/enterprise/aggregation/doc/eos"
		respBytes, err = d.client.DoRequest(ctx, "POST", apiURL2, bodyBytes, referer)
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

	// 2. 遍历 resp.Data 提取文档并保留其所属大类与二级分类标签（如：产品文档包、资料书架、方案概述、特性描述等）
	var docs []store.Document
	seenNID := make(map[string]bool)

	addDoc := func(item map[string]interface{}, groupName, subName string) {
		nid, _ := item["nid"].(string)
		if nid == "" || seenNID[nid] {
			return
		}
		doc := ParseDocItemFromJSON(item, product.ID, product.Name, lineName, catName, groupName, subName)
		if doc != nil {
			seenNID[doc.NID] = true
			docs = append(docs, *doc)
		}
	}

	for _, topItem := range resp.Data {
		var topAttrs map[string]interface{}
		if ta, ok := topItem["attributes"].(map[string]interface{}); ok {
			topAttrs = ta
		}
		groupName := getAttrZH(topAttrs)
		if groupName == "" {
			groupName, _ = topItem["name"].(string)
		}
		if groupName == "" {
			groupName = "其他"
		}

		if subAggs, ok := topItem["subAggs"].([]interface{}); ok && len(subAggs) > 0 {
			for _, sub := range subAggs {
				if subm, ok := sub.(map[string]interface{}); ok {
					var subAttrs map[string]interface{}
					if sa, ok := subm["attributes"].(map[string]interface{}); ok {
						subAttrs = sa
					}
					subName := getAttrZH(subAttrs)
					if subName == "" {
						subName, _ = subm["name"].(string)
					}
					if subName == "" {
						subName = groupName
					}

					for _, k := range []string{"docList", "list", "documents"} {
						if list, ok := subm[k].([]interface{}); ok {
							for _, docItem := range list {
								if dm, ok := docItem.(map[string]interface{}); ok {
									addDoc(dm, groupName, subName)
								}
							}
						}
					}
				}
			}
		}

		// 直属 docList
		for _, k := range []string{"docList", "list", "documents"} {
			if list, ok := topItem[k].([]interface{}); ok {
				for _, docItem := range list {
					if dm, ok := docItem.(map[string]interface{}); ok {
						addDoc(dm, groupName, groupName)
					}
				}
			}
		}
	}

	// 3. 对同一产品的所有文档进行新旧版本标记
	MarkNewVersions(docs)

	// 4. 批量入库
	if len(docs) > 0 {
		d.repo.UpsertDocuments(docs)
		logger.Debug("成功解析分类标签、识别新旧版本并入库产品文档",
			zap.String("product", product.Name),
			zap.Int("docCount", len(docs)),
		)
	}

	return docs, nil
}
