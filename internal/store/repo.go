package store

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// UpsertCategories 批量保存大类与产品线
func (r *Repository) UpsertCategories(cats []Category) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&cats).Error
}

// UpsertProductLines 批量保存产品线
func (r *Repository) UpsertProductLines(lines []ProductLine) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&lines).Error
}

// UpsertProducts 批量保存产品系列
func (r *Repository) UpsertProducts(prods []Product) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&prods).Error
}

// UpsertSubModels 批量保存子型号
func (r *Repository) UpsertSubModels(models []SubModel) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&models).Error
}

// UpsertVersions 批量保存版本
func (r *Repository) UpsertVersions(vers []Version) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&vers).Error
}

// UpsertDocuments 批量保存文档 (分批写入防锁表)
func (r *Repository) UpsertDocuments(docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
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
	err := r.db.Where("product_line_id = ?", lineID).Find(&prods).Error
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
	CategoryID       string `form:"categoryId"`
	ProductLineID    string `form:"productLineId"`
	ProductID        string `form:"productId"`
	VersionID        string `form:"versionId"`
	SubModelID       string `form:"subModelId"`
	DocType          string `form:"docType"`          // 全部, HDX, CHM, PDF, 多媒体 等
	DocCategory      string `form:"docCategory"`      // 产品文档包, 资料书架, 方案概述, 特性描述 等
	DocCategoryGroup string `form:"docCategoryGroup"` // 文档合集, 了解产品, 了解方案 等
	IsDownloaded     *int   `form:"isDownloaded"`     // 0 或 1，空表示全部
	Keyword          string `form:"keyword"`
	Page             int    `form:"page,default=1"`
	PageSize         int    `form:"pageSize,default=20"`
}

// DocFilterResult 文档筛选分页返回
type DocFilterResult struct {
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"pageSize"`
	Items    []Document `json:"items"`
}

// QueryDocuments 多条件组合筛选文档
func (r *Repository) QueryDocuments(q DocFilterQuery) (*DocFilterResult, error) {
	query := r.db.Model(&Document{})

	if q.ProductID != "" {
		query = query.Where("product_id = ?", q.ProductID)
	} else if q.ProductLineID != "" {
		// 根据产品线过滤
		var pids []string
		r.db.Model(&Product{}).Where("product_line_id = ?", q.ProductLineID).Pluck("id", &pids)
		if len(pids) > 0 {
			query = query.Where("product_id IN ?", pids)
		} else {
			return &DocFilterResult{Total: 0, Page: q.Page, PageSize: q.PageSize, Items: []Document{}}, nil
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
				return &DocFilterResult{Total: 0, Page: q.Page, PageSize: q.PageSize, Items: []Document{}}, nil
			}
		} else {
			return &DocFilterResult{Total: 0, Page: q.Page, PageSize: q.PageSize, Items: []Document{}}, nil
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
	if q.Keyword != "" {
		kw := "%" + q.Keyword + "%"
		query = query.Where("name LIKE ? OR file_name LIKE ? OR product_name LIKE ? OR doc_category LIKE ? OR doc_category_group LIKE ?", kw, kw, kw, kw, kw)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.Page = 20
	}
	offset := (q.Page - 1) * q.PageSize

	var docs []Document
	err := query.Order("is_downloaded DESC, is_new_version DESC, publish_date DESC, name ASC").Offset(offset).Limit(q.PageSize).Find(&docs).Error
	if err != nil {
		return nil, err
	}

	return &DocFilterResult{
		Total:    total,
		Page:     q.Page,
		PageSize: q.PageSize,
		Items:    docs,
	}, nil
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

// BatchUpdateDocsDownloaded 批量更新文档已下载状态
func (r *Repository) BatchUpdateDocsDownloaded(nidPathMap map[string]string) error {
	for nid, path := range nidPathMap {
		r.db.Model(&Document{}).Where("nid = ?", nid).Updates(map[string]interface{}{
			"is_downloaded": 1,
			"local_path":    path,
			"updated_at":    time.Now(),
		})
	}
	return nil
}

// ResetAllDocsDownloaded 重置所有文档的已下载状态为 0
func (r *Repository) ResetAllDocsDownloaded() error {
	return r.db.Model(&Document{}).Updates(map[string]interface{}{
		"is_downloaded": 0,
		"local_path":    "",
	}).Error
}

// CreateDownloadTask 创建或更新下载任务
func (r *Repository) CreateDownloadTask(task *DownloadTask) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(task).Error
}

// UpdateDownloadTaskProgress 更新下载任务进度
func (r *Repository) UpdateDownloadTaskProgress(id string, downloaded int64, total int64, progress float64, speed float64, status int, errMsg string) error {
	return r.db.Model(&DownloadTask{}).Where("id = ?", id).Updates(map[string]interface{}{
		"downloaded_bytes": downloaded,
		"total_bytes":      total,
		"progress":         progress,
		"speed_kbps":       speed,
		"status":           status,
		"error_msg":        errMsg,
		"updated_at":       time.Now(),
	}).Error
}

// GetAllDownloadTasks 获取所有下载任务
func (r *Repository) GetAllDownloadTasks() ([]DownloadTask, error) {
	var tasks []DownloadTask
	err := r.db.Order("created_at DESC").Find(&tasks).Error
	return tasks, err
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
