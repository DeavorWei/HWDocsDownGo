package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"hwdocsdown/internal/store"
)

func TestLocalScanner(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "scanner_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := store.InitDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := store.NewRepository(db)

	docs := []store.Document{
		{
			NID:          "EDOC1100512860",
			Name:         "CloudEngine 6800 V300R025C00 配置指南(pdf)",
			FileName:     "CloudEngine 6800 V300R025C00 配置指南.pdf",
			IsDownloaded: 0,
		},
		{
			NID:          "EDOC1100512861",
			Name:         "指南", // 短文本测试，不应因其他文件包含“指南”二字而误打标
			FileName:     "指南.pdf",
			IsDownloaded: 0,
		},
	}
	_ = repo.UpsertDocuments(docs)

	// 创建本地模拟文件
	file1 := filepath.Join(tempDir, "[EDOC1100512860]CloudEngine 6800 V300R025C00 配置指南.pdf")
	_ = os.WriteFile(file1, []byte("fake content 1"), 0644)

	// 创建一个包含“指南”但属于其他无关文档的文件
	file2 := filepath.Join(tempDir, "某种完全无关的其他设备配置指南.pdf")
	_ = os.WriteFile(file2, []byte("fake content 2"), 0644)

	scanner := NewLocalScanner(repo)
	res, err := scanner.ScanDirectory(tempDir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	if res.MatchedDocsCount != 1 {
		t.Errorf("Expected 1 matched doc (EDOC1100512860), got %d", res.MatchedDocsCount)
	}

	d1, _ := repo.GetDocumentByNID("EDOC1100512860")
	if d1.IsDownloaded != 1 {
		t.Errorf("EDOC1100512860 should be marked as downloaded (is_downloaded=1)")
	}

	d2, _ := repo.GetDocumentByNID("EDOC1100512861")
	if d2.IsDownloaded != 0 {
		t.Errorf("EDOC1100512861 ('指南') should NOT be falsely matched to irrelevant file")
	}
}
