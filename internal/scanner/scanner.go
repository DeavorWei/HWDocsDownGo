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

	// 1. 获取数据库中所有文档
	allDocsResult, err := s.repo.QueryDocuments(store.DocFilterQuery{Page: 1, PageSize: 100000})
	if err != nil {
		logger.Error("扫描文档失败: 查询数据库文档错误", zap.Error(err))
		return nil, err
	}
	allDocs := allDocsResult.Items
	logger.Debug("加载数据库全量文档用于文件名与特征比对", zap.Int("dbDocsCount", len(allDocs)))

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
		normFileName := normalizeName(fileName)
		baseWithoutExt := normalizeName(strings.TrimSuffix(fileName, filepath.Ext(fileName)))

		for _, d := range allDocs {
			if _, already := matchedMap[d.NID]; already {
				continue
			}

			// 规则 1: 文件名中包含精确的 NID (例如 EDOC1100512860)
			if strings.Contains(strings.ToUpper(fileName), strings.ToUpper(d.NID)) {
				matchedMap[d.NID] = path
				logger.Debug("通过 NID 精确匹配到本地文件",
					zap.String("nid", d.NID),
					zap.String("docName", d.Name),
					zap.String("filePath", path),
				)
				updatedDocs = append(updatedDocs, Doc{
					NID:          d.NID,
					Name:         d.Name,
					LocalPath:    path,
					PublishDate:  d.PublishDate,
					IsNewVersion: d.IsNewVersion,
				})
				break
			}

			// 规则 2: 文件名与文档名称（去掉符号与空格）匹配
			normDocName := normalizeName(d.Name)
			normDocFileName := normalizeName(d.FileName)

			if normFileName == normDocName || normFileName == normDocFileName ||
				baseWithoutExt == normDocName || baseWithoutExt == normDocFileName ||
				strings.Contains(normFileName, normDocName) || strings.Contains(normDocName, baseWithoutExt) {
				matchedMap[d.NID] = path
				logger.Debug("通过文件名模糊特征匹配到本地文件",
					zap.String("nid", d.NID),
					zap.String("docName", d.Name),
					zap.String("filePath", path),
				)
				updatedDocs = append(updatedDocs, Doc{
					NID:          d.NID,
					Name:         d.Name,
					LocalPath:    path,
					PublishDate:  d.PublishDate,
					IsNewVersion: d.IsNewVersion,
				})
				break
			}
		}

		return nil
	})

	if err != nil {
		logger.Error("遍历本地目录失败", zap.String("dirPath", dirPath), zap.Error(err))
		return nil, err
	}

	// 3. 批量更新数据库打标
	if len(matchedMap) > 0 {
		s.repo.BatchUpdateDocsDownloaded(matchedMap)
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
