package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"hwdocsdown/internal/logger"
	"hwdocsdown/internal/store"
)

type LocalScanner struct {
	repo *store.Repository
}

func NewLocalScanner(repo *store.Repository) *LocalScanner {
	return &LocalScanner{repo: repo}
}

type ScanResult struct {
	TotalScannedFiles int   `json:"totalScannedFiles"`
	MatchedDocsCount  int   `json:"matchedDocsCount"`
	UpdatedDocs       []Doc `json:"updatedDocs"`
}

type Doc struct {
	NID          string `json:"nid"`
	Name         string `json:"name"`
	LocalPath    string `json:"localPath"`
	PublishDate  string `json:"publishDate"`
	IsNewVersion bool   `json:"isNewVersion"`
}

// ScanDirectory 扫描指定目录并自动打标已下载文档
func (s *LocalScanner) ScanDirectory(dirPath string) (*ScanResult, error) {
	if dirPath == "" {
		logger.Warn("扫描本地文档失败: 目录路径为空")
		return nil, os.ErrNotExist
	}

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		logger.Warn("扫描本地文档失败: 目标目录不存在", zap.String("dirPath", dirPath))
		return nil, err
	}

	logger.Info("开始扫描本地文档目录", zap.String("dirPath", dirPath))

	// 1. 获取数据库中所有文档并构建内存倒排索引
	allDocsResult, err := s.repo.QueryDocuments(store.DocFilterQuery{Page: 1, PageSize: 100000})
	if err != nil {
		logger.Error("扫描文档失败: 查询数据库文档错误", zap.Error(err))
		return nil, err
	}
	allDocs := allDocsResult.Items
	logger.Debug("加载数据库全量文档用于文件名与特征比对", zap.Int("dbDocsCount", len(allDocs)))

	// 构建 Hash 索引：NID 索引、规范化文档名索引、规范化文件名索引
	nidDocMap := make(map[string]*store.Document, len(allDocs))
	exactNameMap := make(map[string]*store.Document, len(allDocs))
	exactFileNameMap := make(map[string]*store.Document, len(allDocs))

	for i := range allDocs {
		d := &allDocs[i]
		if d.NID != "" {
			nidDocMap[strings.ToUpper(d.NID)] = d
		}
		normName := normalizeName(d.Name)
		if normName != "" {
			exactNameMap[normName] = d
		}
		normFileName := normalizeName(d.FileName)
		if normFileName != "" {
			exactFileNameMap[normFileName] = d
		}
	}

	totalFiles := 0
	matchedMap := make(map[string]string) // nid -> localPath
	var updatedDocs []Doc

	// 2. 遍历本地目录
	err = filepath.Walk(dirPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// 忽略临时文件
		if strings.HasSuffix(info.Name(), ".tmp") {
			return nil
		}

		totalFiles++
		fileName := info.Name()
		upperFileName := strings.ToUpper(fileName)
		baseWithoutExt := normalizeName(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
		normFileName := normalizeName(fileName)

		var targetDoc *store.Document

		// 规则 1: 检查文件名中是否包含 [NID] 或以 NID 为特征
		for nid, d := range nidDocMap {
			if strings.Contains(upperFileName, nid) {
				targetDoc = d
				break
			}
		}

		// 规则 2: 精准全字匹配 (去除符号与空格后一致)
		if targetDoc == nil {
			if d, ok := exactFileNameMap[normFileName]; ok {
				targetDoc = d
			} else if d, ok := exactNameMap[normFileName]; ok {
				targetDoc = d
			} else if d, ok := exactFileNameMap[baseWithoutExt]; ok {
				targetDoc = d
			} else if d, ok := exactNameMap[baseWithoutExt]; ok {
				targetDoc = d
			}
		}

		if targetDoc != nil {
			// 防御性校验：检查文件是否为登录重定向生成的伪 HTML 页面或 0 字节损坏文件
			if isBad, reason := IsCorruptedOrAuthHtmlFile(path); isBad {
				logger.Warn("扫描检测到登录重定向伪网页文件或损坏文件，已自动删除",
					zap.String("path", path),
					zap.String("reason", reason),
					zap.String("docName", targetDoc.Name),
				)
				_ = os.Remove(path)
				return nil
			}

			if _, already := matchedMap[targetDoc.NID]; !already {
				matchedMap[targetDoc.NID] = path
				logger.Debug("本地文件精准匹配到文档",
					zap.String("nid", targetDoc.NID),
					zap.String("docName", targetDoc.Name),
					zap.String("filePath", path),
				)
				updatedDocs = append(updatedDocs, Doc{
					NID:          targetDoc.NID,
					Name:         targetDoc.Name,
					LocalPath:    path,
					PublishDate:  targetDoc.PublishDate,
					IsNewVersion: targetDoc.IsNewVersion,
				})
			}
		}

		return nil
	})

	if err != nil {
		logger.Error("遍历本地目录失败", zap.String("dirPath", dirPath), zap.Error(err))
		return nil, err
	}

	// 3. 批量更新数据库打标（事务批量更新）
	if len(matchedMap) > 0 {
		_ = s.repo.BatchUpdateDocsDownloaded(matchedMap)
		logger.Info("批量打标已下载文档成功", zap.Int("updatedDocs", len(matchedMap)))
	}

	logger.Info("本地下载目录扫描完成",
		zap.String("dirPath", dirPath),
		zap.Int("totalFiles", totalFiles),
		zap.Int("matchedDocs", len(matchedMap)),
	)

	return &ScanResult{
		TotalScannedFiles: totalFiles,
		MatchedDocsCount:  len(matchedMap),
		UpdatedDocs:       updatedDocs,
	}, nil
}

func normalizeName(s string) string {
	s = strings.ToLower(s)
	// 移除标点符号与空格
	replacer := strings.NewReplacer(
		" ", "",
		"_", "",
		"-", "",
		",", "",
		"，", "",
		".", "",
		"(", "",
		")", "",
		"（", "",
		"）", "",
		"[", "",
		"]", "",
		"【", "",
		"】", "",
	)
	return replacer.Replace(s)
}

// IsCorruptedOrAuthHtmlFile 检查文件是否为登录重定向生成的伪 HTML 页面或 0 字节损坏文件
func IsCorruptedOrAuthHtmlFile(filePath string) (bool, string) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return true, "无法读取文件状态"
	}
	if fi.Size() == 0 {
		return true, "文件大小为 0 字节"
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	// 对于 zip, hdx, chm, pdf, docx, 7z 等非 html 格式，若大小较小且内容是 HTML 则为重定向假文件
	if ext != ".html" && ext != ".htm" {
		if fi.Size() < 500*1024 { // 小于 500KB 嗅探文件头
			f, err := os.Open(filePath)
			if err != nil {
				return false, ""
			}
			defer f.Close()
			buf := make([]byte, 512)
			n, _ := f.Read(buf)
			if n > 0 {
				content := strings.ToLower(string(buf[:n]))
				if strings.Contains(content, "<!doctype html") ||
					strings.Contains(content, "<html") ||
					strings.Contains(content, "uniportal") ||
					strings.Contains(content, "login") ||
					strings.Contains(content, "<head>") {
					return true, "文件内容为 HTML 登录或重定向网页"
				}
			}
		}
	}
	return false, ""
}
