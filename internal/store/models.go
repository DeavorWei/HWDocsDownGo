package store

import (
	"time"
)

// Category 一级产品大类（如：企业网络、数字能源、数据存储等）
type Category struct {
	ID        string        `gorm:"primaryKey;column:id" json:"id"`
	Name      string        `gorm:"column:name;index" json:"name"`
	NameURL   string        `gorm:"column:name_url" json:"nameUrl"`
	Lines     []ProductLine `gorm:"foreignKey:CategoryID" json:"lines,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// ProductLine 二级产品线（如：交换机、路由器、WLAN等）
type ProductLine struct {
	ID           string    `gorm:"primaryKey;column:id" json:"id"`
	CategoryID   string    `gorm:"column:category_id;index" json:"categoryId"`
	CategoryName string    `gorm:"column:category_name" json:"categoryName"`
	Name         string    `gorm:"column:name;index" json:"name"`
	NameURL      string    `gorm:"column:name_url" json:"nameUrl"`
	ProID        string    `gorm:"column:pro_id" json:"proId"`
	Products     []Product `gorm:"foreignKey:ProductLineID" json:"products,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Product 产品系列/具体产品（如：CloudEngine 58&68&78&88&98, CloudEngine S16700 等）
type Product struct {
	ID            string     `gorm:"primaryKey;column:id" json:"id"`
	ProductLineID string     `gorm:"column:product_line_id;index" json:"productLineId"`
	Name          string     `gorm:"column:name;index" json:"name"`
	NameURL       string     `gorm:"column:name_url" json:"nameUrl"`
	NaviGroup     string     `gorm:"column:navi_group" json:"naviGroup"`
	SubModels     []SubModel `gorm:"foreignKey:ProductID" json:"subModels,omitempty"`
	Versions      []Version  `gorm:"foreignKey:ProductID" json:"versions,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// SubModel 细分子型号（如：CE8875-24BQ8DQ, CE6881 等）
type SubModel struct {
	ID        string    `gorm:"primaryKey;column:id" json:"id"`
	ProductID string    `gorm:"column:product_id;index" json:"productId"`
	Name      string    `gorm:"column:name;index" json:"name"`
	Tag       string    `gorm:"column:tag;index" json:"tag"`
	PbiTids   string    `gorm:"column:pbi_tids" json:"pbiTids"` // 逗号分隔
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Version 大版本（如：V200R025, V300R024 等）
type Version struct {
	ID        string    `gorm:"primaryKey;column:id" json:"id"` // 组合主键 "ProductID_RawID"
	ProductID string    `gorm:"column:product_id;index" json:"productId"`
	RawID     string    `gorm:"column:raw_id;index" json:"rawId"`
	Name      string    `gorm:"column:name;index" json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Document 产品文档（HDX / CHM / PDF / ZIP / 多媒体 等）
type Document struct {
	NID             string    `gorm:"primaryKey;column:nid" json:"nid"`
	ProductID       string    `gorm:"column:product_id;index" json:"productId"`
	ProductName     string    `gorm:"column:product_name;index" json:"productName"`
	ProductLineName string    `gorm:"column:product_line_name;index" json:"productLineName"`
	CategoryName    string    `gorm:"column:category_name" json:"categoryName"`
	VersionID       string    `gorm:"column:version_id;index" json:"versionId"`
	VersionName     string    `gorm:"column:version_name" json:"versionName"`
	SubModelID      string    `gorm:"column:sub_model_id;index" json:"subModelId"`
	SubModelName    string    `gorm:"column:sub_model_name" json:"subModelName"`
	Name            string    `gorm:"column:name;index" json:"name"`
	DocType          string    `gorm:"column:doc_type;index" json:"docType"`                         // HDX | CHM | PDF | ZIP | 多媒体 | OTHER
	DocCategory      string    `gorm:"column:doc_category;index" json:"docCategory"`                 // 资料分类/标签：产品文档包 | 资料书架 | 方案概述 | 特性描述 | 配置指南 等
	DocCategoryGroup string    `gorm:"column:doc_category_group;index" json:"docCategoryGroup"`     // 顶级资料分类：文档合集 | 了解产品 | 了解方案 | 参考指南 等
	FileName         string    `gorm:"column:file_name" json:"fileName"`
	FileSizeBytes   int64     `gorm:"column:file_size_bytes" json:"fileSizeBytes"`
	FileSizeStr     string    `gorm:"column:file_size_str" json:"fileSizeStr"`
	DownloadURL     string    `gorm:"column:download_url" json:"downloadUrl"`
	PartNo          string    `gorm:"column:part_no" json:"partNo"`
	PublishDate     string    `gorm:"column:publish_date;index" json:"publishDate"`
	PublishTime     string    `gorm:"column:publish_time" json:"publishTime"`
	LastUpdateTime  string    `gorm:"column:last_update_time" json:"lastUpdateTime"`
	IsNewVersion    bool      `gorm:"column:is_new_version;index" json:"isNewVersion"`
	IsDownloaded    int       `gorm:"column:is_downloaded;default:0;index" json:"isDownloaded"` // 0: 未下载, 1: 已下载
	LocalPath       string    `gorm:"column:local_path" json:"localPath"`
	CrawlTime       time.Time `gorm:"column:crawl_time" json:"crawlTime"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// DownloadTask 下载任务表
type DownloadTask struct {
	ID              string    `gorm:"primaryKey;column:id" json:"id"`
	DocNID          string    `gorm:"column:doc_nid;index" json:"docNid"`
	DocName         string    `gorm:"column:doc_name" json:"docName"`
	DocType         string    `gorm:"column:doc_type" json:"docType"`
	FileName        string    `gorm:"column:file_name" json:"fileName"`
	SavePath        string    `gorm:"column:save_path" json:"savePath"`
	DownloadURL     string    `gorm:"column:download_url" json:"downloadUrl"`
	TotalBytes      int64     `gorm:"column:total_bytes" json:"totalBytes"`
	DownloadedBytes int64     `gorm:"column:downloaded_bytes" json:"downloadedBytes"`
	Status          int       `gorm:"column:status;index" json:"status"` // 0:排队中, 1:下载中, 2:已完成, 3:失败, 4:已暂停
	Progress        float64   `gorm:"column:progress" json:"progress"`
	SpeedKBps       float64   `gorm:"column:speed_kbps" json:"speedKbps"`
	ErrorMsg        string    `gorm:"column:error_msg" json:"errorMsg"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Setting 系统配置表
type Setting struct {
	Key       string    `gorm:"primaryKey;column:key" json:"key"`
	Value     string    `gorm:"column:value" json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}
