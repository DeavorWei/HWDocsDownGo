package store

import (
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"hwdocsdown/internal/logger"
)

var (
	reRepoDocTypeClean = regexp.MustCompile(`(?i)\s*[\(（](?:hdx|chm|pdf|zip|多媒体)[\)）]`)
	reRepoCleanMedia   = regexp.MustCompile(`(?i)\s*[\(（]多媒体[\)）]`)
	reRepoVersionTag   = regexp.MustCompile(`(?i)\s*V\d{3}R\d{3}(?:[,\s]*[CB]\d+)*(?:SP[HC]\d+)?`)
)

func calcBaseGroupKey(name, docType string) string {
	clean := reRepoDocTypeClean.ReplaceAllString(name, "")
	clean = reRepoCleanMedia.ReplaceAllString(clean, "")
	clean = reRepoVersionTag.ReplaceAllString(clean, "")
	clean = strings.Join(strings.Fields(clean), " ")
	return strings.TrimSpace(clean) + "::" + docType
}

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// UpsertCategories 批量保存大类与产品线
func (r *Repository) UpsertCategories(cats []Category) error {
	logger.Debug("批量持久化产品大类", zap.Int("count", len(cats)))
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&cats).Error
}

// UpsertProductLines 批量保存产品线
func (r *Repository) UpsertProductLines(lines []ProductLine) error {
	logger.Debug("批量持久化二级产品线", zap.Int("count", len(lines)))
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&lines).Error
}

// UpsertProducts 批量保存产品系列
func (r *Repository) UpsertProducts(prods []Product) error {
	logger.Debug("批量持久化产品型号", zap.Int("count", len(prods)))
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&prods).Error
}

// UpsertSubModels 批量保存子型号
func (r *Repository) UpsertSubModels(models []SubModel) error {
	logger.Debug("批量持久化子型号", zap.Int("count", len(models)))
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&models).Error
}

// UpsertVersions 批量保存版本
func (r *Repository) UpsertVersions(vers []Version) error {
	logger.Debug("批量持久化版本数据", zap.Int("count", len(vers)))
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&vers).Error
}

// UpsertDocuments 批量保存文档 (分批写入防锁表)
func (r *Repository) UpsertDocuments(docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	logger.Debug("分批持久化产品文档数据", zap.Int("count", len(docs)))
	return r.db.Clauses(clause.OnConflict{
		DoUpdates: clause.AssignmentColumns([]string{
			"product_name", "product_line_name", "category_name", "version_id", "version_name",
			"sub_model_id", "sub_model_name", "name", "doc_type", "doc_category", "doc_category_group",
			"file_name", "file_size_bytes", "file_size_str", "download_url", "part_no", "publish_date",
			"publish_time", "last_update_time", "is_new_version", "crawl_time", "updated_at",
		}),
	}).CreateInBatches(&docs, 200).Error
}

// GetAllCategories 获取所有大类及产品线
func (r *Repository) GetAllCategories() ([]Category, error) {
	var cats []Category
	err := r.db.Preload("Lines").Find(&cats).Error
	return cats, err
}

// GetProductLinesByCategoryID 根据大类获取产品线
func (r *Repository) GetProductLinesByCategoryID(catID string) ([]ProductLine, error) {
	var lines []ProductLine
	err := r.db.Where("category_id = ?", catID).Find(&lines).Error
	return lines, err
}

// GetProductByID 根据 ID 获取产品型号
func (r *Repository) GetProductByID(id string) (*Product, error) {
	var prod Product
	err := r.db.Where("id = ?", id).First(&prod).Error
	if err != nil {
		return nil, err
	}
	return &prod, nil
}

// GetProductLineByID 根据 ID 获取产品线
func (r *Repository) GetProductLineByID(id string) (*ProductLine, error) {
	var line ProductLine
	err := r.db.Where("id = ?", id).First(&line).Error
	if err != nil {
		return nil, err
	}
	return &line, nil
}

// GetCategoryByID 根据 ID 获取大类
func (r *Repository) GetCategoryByID(id string) (*Category, error) {
	var cat Category
	err := r.db.Preload("Lines").Where("id = ?", id).First(&cat).Error
	if err != nil {
		return nil, err
	}
	return &cat, nil
}

// GetProductsByProductLineID 获取产品线下的产品系列
func (r *Repository) GetProductsByProductLineID(lineID string) ([]Product, error) {
	var prods []Product
	err := r.db.Where("product_line_id = ?", lineID).Order("created_at ASC, id ASC").Find(&prods).Error
	return prods, err
}

// GetProductsByProductLineIDAndSeries 根据产品线和系列名称获取产品列表 (支持同一型号归属多个系列标签)
func (r *Repository) GetProductsByProductLineIDAndSeries(lineID, series string) ([]Product, error) {
	var prods []Product
	query := r.db.Where("product_line_id = ?", lineID)
	if series != "" {
		query = query.Where("instr(',' || navi_group || ',', ',' || ? || ',') > 0", series)
	}
	err := query.Order("created_at ASC, id ASC").Find(&prods).Error
	return prods, err
}

// GetSubModelsAndVersions 获取产品的子型号和版本
func (r *Repository) GetSubModelsAndVersions(productID string) ([]SubModel, []Version, error) {
	var subModels []SubModel
	var versions []Version
	if err := r.db.Where("product_id = ?", productID).Find(&subModels).Error; err != nil {
		return nil, nil, err
	}
	if err := r.db.Where("product_id = ?", productID).Find(&versions).Error; err != nil {
		return nil, nil, err
	}
	return subModels, versions, nil
}

// DocFilterQuery 文档筛选查询参数
type DocFilterQuery struct {
	CategoryID       string `form:"categoryId" json:"categoryId"`
	ProductLineID    string `form:"productLineId" json:"productLineId"`
	Series           string `form:"series" json:"series"`           // 产品系列（如：园区交换机、园区网络解决方案等）
	ProductID        string `form:"productId" json:"productId"`        // 具体产品型号
	VersionID        string `form:"versionId" json:"versionId"`
	SubModelID       string `form:"subModelId" json:"subModelId"`
	DocType          string `form:"docType" json:"docType"`          // 全部, HDX, CHM, PDF, 多媒体 等
	DocCategory      string `form:"docCategory" json:"docCategory"`      // 产品文档包, 资料书架, 方案概述, 特性描述 等
	DocCategoryGroup string `form:"docCategoryGroup" json:"docCategoryGroup"` // 文档合集, 了解产品, 了解方案 等
	IsDownloaded     *int   `form:"isDownloaded" json:"isDownloaded"`     // 0 或 1，空表示全部
	Keyword          string `form:"keyword" json:"keyword"`
	IsRegex          bool   `form:"isRegex" json:"isRegex"`          // 是否开启正则表达式搜索
	Page             int    `form:"page,default=1" json:"page"`
	PageSize         int    `form:"pageSize,default=20" json:"pageSize"`
}

// DocFilterResult 文档筛选分页返回
type DocFilterResult struct {
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"pageSize"`
	Items    []Document `json:"items"`
}

// buildDocFilterQuery 构造文档组合筛选的 GORM 查询对象
func (r *Repository) buildDocFilterQuery(q DocFilterQuery) (*gorm.DB, error) {
	query := r.db.Model(&Document{})

	if q.ProductID != "" {
		query = query.Where("product_id = ?", q.ProductID)
	} else if q.Series != "" && q.ProductLineID != "" {
		// 根据产品线与系列过滤 (支持同一型号多系列标签)
		var pids []string
		r.db.Model(&Product{}).Where("product_line_id = ? AND instr(',' || navi_group || ',', ',' || ? || ',') > 0", q.ProductLineID, q.Series).Pluck("id", &pids)
		if len(pids) > 0 {
			query = query.Where("product_id IN ?", pids)
		} else {
			return nil, nil
		}
	} else if q.ProductLineID != "" {
		// 根据产品线过滤
		var pids []string
		r.db.Model(&Product{}).Where("product_line_id = ?", q.ProductLineID).Pluck("id", &pids)
		if len(pids) > 0 {
			query = query.Where("product_id IN ?", pids)
		} else {
			return nil, nil
		}
	} else if q.CategoryID != "" {
		// 根据产品大类过滤
		var lineIDs []string
		r.db.Model(&ProductLine{}).Where("category_id = ?", q.CategoryID).Pluck("id", &lineIDs)
		if len(lineIDs) > 0 {
			var pids []string
			r.db.Model(&Product{}).Where("product_line_id IN ?", lineIDs).Pluck("id", &pids)
			if len(pids) > 0 {
				query = query.Where("product_id IN ?", pids)
			} else {
				return nil, nil
			}
		} else {
			return nil, nil
		}
	}

	if q.VersionID != "" {
		query = query.Where("version_id = ?", q.VersionID)
	}
	if q.SubModelID != "" {
		query = query.Where("sub_model_id = ?", q.SubModelID)
	}
	if q.DocType != "" && strings.ToUpper(q.DocType) != "ALL" {
		query = query.Where("doc_type = ? OR UPPER(doc_type) = ?", q.DocType, strings.ToUpper(q.DocType))
	}
	if q.DocCategory != "" && strings.ToUpper(q.DocCategory) != "ALL" {
		query = query.Where("doc_category = ?", q.DocCategory)
	}
	if q.DocCategoryGroup != "" && strings.ToUpper(q.DocCategoryGroup) != "ALL" {
		query = query.Where("doc_category_group = ?", q.DocCategoryGroup)
	}
	if q.IsDownloaded != nil {
		query = query.Where("is_downloaded = ?", *q.IsDownloaded)
	}

	return query, nil
}

// QueryDocumentNIDs 获取当前筛选条件下的所有文档 NID 列表（用于跨页全选与批量下载）
func (r *Repository) QueryDocumentNIDs(q DocFilterQuery) ([]string, error) {
	query, err := r.buildDocFilterQuery(q)
	if err != nil {
		return nil, err
	}
	if query == nil {
		return []string{}, nil
	}

	kwText := strings.TrimSpace(q.Keyword)
	isRegexSearch := q.IsRegex || strings.HasPrefix(kwText, "regex:")
	if strings.HasPrefix(kwText, "regex:") {
		kwText = strings.TrimPrefix(kwText, "regex:")
		kwText = strings.TrimSpace(kwText)
	}

	// 1. 正则表达式模式
	if isRegexSearch && kwText != "" {
		re, err := regexp.Compile("(?i)" + kwText)
		if err != nil {
			return []string{}, nil
		}
		var candidateDocs []Document
		if err := query.Find(&candidateDocs).Error; err != nil {
			return nil, err
		}
		var nids []string
		for _, d := range candidateDocs {
			if re.MatchString(d.Name) ||
				re.MatchString(d.FileName) ||
				re.MatchString(d.ProductName) ||
				re.MatchString(d.NID) ||
				re.MatchString(d.DocCategory) ||
				re.MatchString(d.DocCategoryGroup) ||
				re.MatchString(d.ProductLineName) ||
				re.MatchString(d.CategoryName) {
				nids = append(nids, d.NID)
			}
		}
		return nids, nil
	}

	// 2. 多关键词 AND 模式
	if kwText != "" {
		tokens := strings.Fields(kwText)
		for _, token := range tokens {
			kw := "%" + token + "%"
			query = query.Where("(name LIKE ? OR file_name LIKE ? OR product_name LIKE ? OR nid LIKE ? OR doc_category LIKE ? OR doc_category_group LIKE ? OR product_line_name LIKE ? OR category_name LIKE ?)",
				kw, kw, kw, kw, kw, kw, kw, kw)
		}
	}

	var nids []string
	if err := query.Pluck("nid", &nids).Error; err != nil {
		return nil, err
	}
	return nids, nil
}

// QueryDocuments 多条件组合筛选文档 (支持多关键词 AND 检索与正则表达式高级搜索)
func (r *Repository) QueryDocuments(q DocFilterQuery) (*DocFilterResult, error) {
	query, err := r.buildDocFilterQuery(q)
	if err != nil {
		return nil, err
	}
	if query == nil {
		return &DocFilterResult{Total: 0, Page: q.Page, PageSize: q.PageSize, Items: []Document{}}, nil
	}

	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.Page = 20
	}
	offset := (q.Page - 1) * q.PageSize

	kwText := strings.TrimSpace(q.Keyword)
	isRegexSearch := q.IsRegex || strings.HasPrefix(kwText, "regex:")
	if strings.HasPrefix(kwText, "regex:") {
		kwText = strings.TrimPrefix(kwText, "regex:")
		kwText = strings.TrimSpace(kwText)
	}

	// ---------------- 1. 正则表达式搜索模式 ----------------
	if isRegexSearch && kwText != "" {
		re, err := regexp.Compile("(?i)" + kwText)
		if err != nil {
			logger.Warn("用户输入的正则表达式不合法", zap.String("pattern", kwText), zap.Error(err))
			return &DocFilterResult{Total: 0, Page: q.Page, PageSize: q.PageSize, Items: []Document{}}, nil
		}

		var candidateDocs []Document
		if err := query.Order("is_downloaded DESC, is_new_version DESC, publish_date DESC, name ASC").Find(&candidateDocs).Error; err != nil {
			return nil, err
		}

		var matchedDocs []Document
		for _, d := range candidateDocs {
			if re.MatchString(d.Name) ||
				re.MatchString(d.FileName) ||
				re.MatchString(d.ProductName) ||
				re.MatchString(d.NID) ||
				re.MatchString(d.DocCategory) ||
				re.MatchString(d.DocCategoryGroup) ||
				re.MatchString(d.ProductLineName) ||
				re.MatchString(d.CategoryName) {
				matchedDocs = append(matchedDocs, d)
			}
		}

		total := int64(len(matchedDocs))
		var docs []Document
		if offset >= len(matchedDocs) {
			docs = []Document{}
		} else {
			end := offset + q.PageSize
			if end > len(matchedDocs) {
				end = len(matchedDocs)
			}
			docs = matchedDocs[offset:end]
		}

		r.fillHasLocalOlderVersion(docs)
		return &DocFilterResult{
			Total:    total,
			Page:     q.Page,
			PageSize: q.PageSize,
			Items:    docs,
		}, nil
	}

	// ---------------- 2. 多关键词 AND 联合搜索模式 (空格分隔多个关键词) ----------------
	if kwText != "" {
		tokens := strings.Fields(kwText)
		for _, token := range tokens {
			kw := "%" + token + "%"
			query = query.Where("(name LIKE ? OR file_name LIKE ? OR product_name LIKE ? OR nid LIKE ? OR doc_category LIKE ? OR doc_category_group LIKE ? OR product_line_name LIKE ? OR category_name LIKE ?)",
				kw, kw, kw, kw, kw, kw, kw, kw)
		}
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	var docs []Document
	err = query.Order("is_downloaded DESC, is_new_version DESC, publish_date DESC, name ASC").Offset(offset).Limit(q.PageSize).Find(&docs).Error
	if err != nil {
		return nil, err
	}

	r.fillHasLocalOlderVersion(docs)

	return &DocFilterResult{
		Total:    total,
		Page:     q.Page,
		PageSize: q.PageSize,
		Items:    docs,
	}, nil
}

// fillHasLocalOlderVersion 针对当前页文档集合，计算并打标是否存在同产品同系列本地已下载的历史旧版本
func (r *Repository) fillHasLocalOlderVersion(docs []Document) {
	if len(docs) == 0 {
		return
	}

	var productIDs []string
	pidSet := make(map[string]bool)
	for _, d := range docs {
		if d.IsNewVersion && d.ProductID != "" && !pidSet[d.ProductID] {
			pidSet[d.ProductID] = true
			productIDs = append(productIDs, d.ProductID)
		}
	}

	if len(productIDs) == 0 {
		return
	}

	var downloadedDocs []Document
	r.db.Model(&Document{}).
		Where("product_id IN ? AND is_downloaded = 1", productIDs).
		Select("nid, product_id, name, doc_type, publish_date, publish_time").
		Find(&downloadedDocs)

	// 分组存储已下载旧版本的最高发布时间与集合
	downloadedGroupMap := make(map[string][]Document)
	for _, dd := range downloadedDocs {
		gKey := dd.ProductID + "::" + calcBaseGroupKey(dd.Name, dd.DocType)
		downloadedGroupMap[gKey] = append(downloadedGroupMap[gKey], dd)
	}

	for i := range docs {
		if docs[i].IsNewVersion {
			gKey := docs[i].ProductID + "::" + calcBaseGroupKey(docs[i].Name, docs[i].DocType)
			if dlist, ok := downloadedGroupMap[gKey]; ok {
				for _, dl := range dlist {
					// 存在已下载记录且 NID 不同（或已下载记录的发布时间早于当前文档），表明本地存在旧版
					if dl.NID != docs[i].NID || (dl.PublishDate != "" && dl.PublishDate < docs[i].PublishDate) {
						docs[i].HasLocalOlderVersion = true
						break
					}
				}
			}
		}
	}
}

// GetDocCategories 获取数据库中已有的所有资料分类标签列表（如：产品文档包、资料书架、方案概述、特性描述等）
func (r *Repository) GetDocCategories(productID, productLineID, categoryID string) ([]string, error) {
	query := r.db.Model(&Document{}).Where("doc_category != '' AND doc_category IS NOT NULL")
	if productID != "" {
		query = query.Where("product_id = ?", productID)
	} else if productLineID != "" {
		var pids []string
		r.db.Model(&Product{}).Where("product_line_id = ?", productLineID).Pluck("id", &pids)
		if len(pids) > 0 {
			query = query.Where("product_id IN ?", pids)
		}
	} else if categoryID != "" {
		var lineIDs []string
		r.db.Model(&ProductLine{}).Where("category_id = ?", categoryID).Pluck("id", &lineIDs)
		if len(lineIDs) > 0 {
			var pids []string
			r.db.Model(&Product{}).Where("product_line_id IN ?", lineIDs).Pluck("id", &pids)
			if len(pids) > 0 {
				query = query.Where("product_id IN ?", pids)
			}
		}
	}
	var cats []string
	err := query.Distinct("doc_category").Pluck("doc_category", &cats).Error
	return cats, err
}

// GetDocumentByNID 根据 nid 获取单个文档
func (r *Repository) GetDocumentByNID(nid string) (*Document, error) {
	var doc Document
	err := r.db.Where("nid = ?", nid).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// UpdateDocDownloaded 更新文档下载状态与本地路径
func (r *Repository) UpdateDocDownloaded(nid string, isDownloaded int, localPath string) error {
	return r.db.Model(&Document{}).Where("nid = ?", nid).Updates(map[string]interface{}{
		"is_downloaded": isDownloaded,
		"local_path":    localPath,
		"updated_at":    time.Now(),
	}).Error
}

// SyncDownloadedDocs 全量同步已下载文档状态（双向对齐：匹配到的打标 1，磁盘不存在的重置为 0）
func (r *Repository) SyncDownloadedDocs(nidPathMap map[string]string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// 1. 如果匹配结果为空，说明本地无任何已下载文档，全部置 0
		if len(nidPathMap) == 0 {
			return tx.Model(&Document{}).Where("is_downloaded = ?", 1).Updates(map[string]interface{}{
				"is_downloaded": 0,
				"local_path":    "",
				"updated_at":    now,
			}).Error
		}

		// 2. 提取匹配的 NID 列表
		matchedNIDs := make([]string, 0, len(nidPathMap))
		for nid := range nidPathMap {
			matchedNIDs = append(matchedNIDs, nid)
		}

		// 3. 将不在 matchedNIDs 范围内但此前被标记为已下载的文档重置为 0
		if err := tx.Model(&Document{}).Where("is_downloaded = ? AND nid NOT IN ?", 1, matchedNIDs).Updates(map[string]interface{}{
			"is_downloaded": 0,
			"local_path":    "",
			"updated_at":    now,
		}).Error; err != nil {
			return err
		}

		// 4. 批量更新当前匹配到的文档为已下载
		for nid, path := range nidPathMap {
			if err := tx.Model(&Document{}).Where("nid = ?", nid).Updates(map[string]interface{}{
				"is_downloaded": 1,
				"local_path":    path,
				"updated_at":    now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// BatchUpdateDocsDownloaded 批量更新文档已下载状态（事务批处理防锁库与提升写入性能）
func (r *Repository) BatchUpdateDocsDownloaded(nidPathMap map[string]string) error {
	if len(nidPathMap) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for nid, path := range nidPathMap {
			if err := tx.Model(&Document{}).Where("nid = ?", nid).Updates(map[string]interface{}{
				"is_downloaded": 1,
				"local_path":    path,
				"updated_at":    now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ResetAllDocsDownloaded 重置所有文档的已下载状态为 0 (带 Where 条件规避 GORM v2 安全更新拦截)
func (r *Repository) ResetAllDocsDownloaded() error {
	return r.db.Model(&Document{}).Where("is_downloaded = ?", 1).Updates(map[string]interface{}{
		"is_downloaded": 0,
		"local_path":    "",
		"updated_at":    time.Now(),
	}).Error
}

// CreateDownloadTask 创建或更新下载任务
func (r *Repository) CreateDownloadTask(task *DownloadTask) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(task).Error
}

// GetDownloadTaskByID 根据 ID 精确获取单个下载任务 (主键索引查询)
func (r *Repository) GetDownloadTaskByID(id string) (*DownloadTask, error) {
	var task DownloadTask
	err := r.db.Where("id = ?", id).First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// UpdateDownloadTaskProgress 更新下载任务进度与路径 (可选附带 savePath 与 fileName)
func (r *Repository) UpdateDownloadTaskProgress(id string, downloaded int64, total int64, progress float64, speed float64, status int, errMsg string, extra ...string) error {
	updates := map[string]interface{}{
		"downloaded_bytes": downloaded,
		"total_bytes":      total,
		"progress":         progress,
		"speed_kbps":       speed,
		"status":           status,
		"error_msg":        errMsg,
		"updated_at":       time.Now(),
	}
	if len(extra) > 0 && extra[0] != "" {
		updates["save_path"] = extra[0]
	}
	if len(extra) > 1 && extra[1] != "" {
		updates["file_name"] = extra[1]
	}
	return r.db.Model(&DownloadTask{}).Where("id = ?", id).Updates(updates).Error
}

// GetAllDownloadTasks 获取所有下载任务 (下载中与排队中置顶，已完成排在最后，并自动回填对齐已完成任务的本地文件路径)
func (r *Repository) GetAllDownloadTasks() ([]DownloadTask, error) {
	var tasks []DownloadTask
	err := r.db.Order(`
		CASE status 
			WHEN 1 THEN 1 
			WHEN 0 THEN 2 
			WHEN 4 THEN 3 
			WHEN 3 THEN 4 
			WHEN 2 THEN 5 
			ELSE 6 
		END ASC,
		CASE WHEN status = 2 THEN updated_at END DESC,
		created_at ASC, id ASC`).Find(&tasks).Error
	if err != nil {
		return nil, err
	}

	// 自动补偿与对齐已完成任务的保存路径 (若历史记录中 save_path 为空，则从关联文档表回填)
	for i := range tasks {
		if tasks[i].SavePath == "" && tasks[i].DocNID != "" {
			var doc Document
			if err := r.db.Select("local_path, file_name").Where("nid = ?", tasks[i].DocNID).First(&doc).Error; err == nil {
				if doc.LocalPath != "" {
					tasks[i].SavePath = doc.LocalPath
					_ = r.db.Model(&DownloadTask{}).Where("id = ?", tasks[i].ID).Update("save_path", doc.LocalPath)
				}
				if tasks[i].FileName == "" && doc.FileName != "" {
					tasks[i].FileName = doc.FileName
					_ = r.db.Model(&DownloadTask{}).Where("id = ?", tasks[i].ID).Update("file_name", doc.FileName)
				}
			}
		}
	}

	return tasks, nil
}

// DeleteDownloadTask 删除下载任务
func (r *Repository) DeleteDownloadTask(id string) error {
	return r.db.Where("id = ?", id).Delete(&DownloadTask{}).Error
}

// GetSetting 获取配置
func (r *Repository) GetSetting(key, defaultVal string) string {
	var s Setting
	if err := r.db.Where("key = ?", key).First(&s).Error; err != nil {
		return defaultVal
	}
	return s.Value
}

// SetSetting 保存配置
func (r *Repository) SetSetting(key, val string) error {
	s := Setting{Key: key, Value: val, UpdatedAt: time.Now()}
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&s).Error
}

// GetStatistics 概览统计数据
func (r *Repository) GetStatistics() (map[string]interface{}, error) {
	var catCount, lineCount, prodCount, docCount, downloadedCount, taskCount int64
	r.db.Model(&Category{}).Count(&catCount)
	r.db.Model(&ProductLine{}).Count(&lineCount)
	r.db.Model(&Product{}).Count(&prodCount)
	r.db.Model(&Document{}).Count(&docCount)
	r.db.Model(&Document{}).Where("is_downloaded = 1").Count(&downloadedCount)
	r.db.Model(&DownloadTask{}).Count(&taskCount)

	return map[string]interface{}{
		"categories": catCount,
		"lines":      lineCount,
		"products":   prodCount,
		"documents":  docCount,
		"downloaded": downloadedCount,
		"tasks":      taskCount,
	}, nil
}
