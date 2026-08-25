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
	NID       string `json:"nid"`
	Name      string `json:"name"`
	LocalPath string `json:"localPath"`
}

// ScanDirectory 扫描指定目录并自动打标已下载文档
func (s *LocalScanner) ScanDirectory(dirPath string) (*ScanResult, error) {
	if dirPath == "" {
		return nil, os.ErrNotExist
	}

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, err
	}

	// 1. 获取数据库中所有文档
	allDocsResult, err := s.repo.QueryDocuments(store.DocFilterQuery{Page: 1, PageSize: 100000})
	if err != nil {
		return nil, err
	}
	allDocs := allDocsResult.Items

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
				updatedDocs = append(updatedDocs, Doc{NID: d.NID, Name: d.Name, LocalPath: path})
				break
			}

			// 规则 2: 文件名与文档名称（去掉符号与空格）匹配
			normDocName := normalizeName(d.Name)
			normDocFileName := normalizeName(d.FileName)

			if normFileName == normDocName || normFileName == normDocFileName ||
				baseWithoutExt == normDocName || baseWithoutExt == normDocFileName ||
				strings.Contains(normFileName, normDocName) || strings.Contains(normDocName, baseWithoutExt) {
				matchedMap[d.NID] = path
				updatedDocs = append(updatedDocs, Doc{NID: d.NID, Name: d.Name, LocalPath: path})
				break
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 3. 批量更新数据库打标
	if len(matchedMap) > 0 {
		s.repo.BatchUpdateDocsDownloaded(matchedMap)
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
