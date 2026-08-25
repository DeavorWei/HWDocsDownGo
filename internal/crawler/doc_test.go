package crawler

import (
	"os"
	"path/filepath"
	"testing"

	"hwdocsdown/internal/store"
)

func TestParseDocType(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		fv       map[string]interface{}
		expected string
	}{
		{
			name:     "HDX Doc",
			title:    "CloudEngine XH9000, XH8000系列交换机 V300R025C00 产品文档(hdx)",
			fv:       nil,
			expected: "HDX",
		},
		{
			name:     "CHM Doc",
			title:    "CloudEngine XH9000, XH8000系列交换机 V300R025C10 产品文档(chm)",
			fv:       nil,
			expected: "CHM",
		},
		{
			name:     "PDF Doc",
			title:    "CloudEngine 系列交换机 快速指南(pdf)",
			fv:       nil,
			expected: "PDF",
		},
		{
			name:     "ZIP Doc",
			title:    "CloudEngine 系列交换机 软件补丁(zip)",
			fv:       nil,
			expected: "ZIP",
		},
		{
			name:     "Multimedia Doc via title (Chinese paren)",
			title:    "（多媒体）HedEx 2.0 ――下一代信息阅读与定制的文档服务",
			fv:       nil,
			expected: "多媒体",
		},
		{
			name:     "Multimedia Doc via title (video)",
			title:    "CloudEngine 智能运维视频演示",
			fv:       nil,
			expected: "多媒体",
		},
		{
			name:     "Multimedia Doc via fieldValues isVideo",
			title:    "超融合网络自动化编排",
			fv:       map[string]interface{}{"isVideo": "EBOOL-true"},
			expected: "多媒体",
		},
		{
			name:     "Multimedia Doc via secondItem STMV",
			title:    "快速查询硬件配套版本",
			fv:       map[string]interface{}{"secondItem": "SEDPTNEW-STMV"},
			expected: "多媒体",
		},
		{
			name:     "Other Doc",
			title:    "CloudEngine 9800, 8800, 6800 V300R025C10 配置指南-基础配置",
			fv:       nil,
			expected: "OTHER",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDocType(tt.title, tt.fv)
			if got != tt.expected {
				t.Errorf("ParseDocType(%q) = %q, expected %q", tt.title, got, tt.expected)
			}
		})
	}
}

func TestExtractPublishDate(t *testing.T) {
	tests := []struct {
		pubTime  string
		updTime  string
		expected string
	}{
		{"2026-08-07 20:46:31", "2026-08-07 20:56:15", "2026-08-07"},
		{"2026-06-24 18:46:41", "2026-06-24 18:54:29", "2026-06-24"},
		{"", "2026-08-06 17:20:53", "2026-08-06"},
		{"2026-05-22", "", "2026-05-22"},
		{"invalid", "", ""},
	}

	for _, tt := range tests {
		got := ExtractPublishDate(tt.pubTime, tt.updTime)
		if got != tt.expected {
			t.Errorf("ExtractPublishDate(%q, %q) = %q, expected %q", tt.pubTime, tt.updTime, got, tt.expected)
		}
	}
}

func TestMarkNewVersions(t *testing.T) {
	docs := []store.Document{
		{
			NID:         "EDOC1",
			Name:        "CloudEngine XH9000, XH8000系列交换机 V300R025C00 产品文档(hdx)",
			DocType:     "HDX",
			PublishDate: "2026-08-07",
			PublishTime: "2026-08-07 20:46:31",
		},
		{
			NID:         "EDOC2",
			Name:        "CloudEngine XH9000, XH8000系列交换机 V300R024C10, C11 产品文档(hdx)",
			DocType:     "HDX",
			PublishDate: "2026-06-24",
			PublishTime: "2026-06-24 18:46:41",
		},
		{
			NID:         "EDOC3",
			Name:        "CloudEngine XH9000, XH8000系列交换机 V300R024C00 产品文档(hdx)",
			DocType:     "HDX",
			PublishDate: "2026-08-06",
			PublishTime: "2026-08-06 17:20:53",
		},
		{
			NID:         "EDOC4",
			Name:        "CloudEngine 9800, 8800, 6800系列交换机 V300R025C00 产品文档(hdx)",
			DocType:     "HDX",
			PublishDate: "2026-08-07",
			PublishTime: "2026-08-07 20:46:26",
		},
		{
			NID:         "EDOC5",
			Name:        "CloudEngine 9800, 8800, 6800系列交换机 V300R024C10, C11 产品文档(hdx)",
			DocType:     "HDX",
			PublishDate: "2026-06-24",
			PublishTime: "2026-06-24 18:46:32",
		},
	}

	MarkNewVersions(docs)

	// EDOC1 (2026-08-07) should be IsNewVersion = true
	if !docs[0].IsNewVersion {
		t.Errorf("EDOC1 should be IsNewVersion = true")
	}
	// EDOC2 (2026-06-24) should be IsNewVersion = false
	if docs[1].IsNewVersion {
		t.Errorf("EDOC2 should be IsNewVersion = false")
	}
	// EDOC3 (2026-08-06) should be IsNewVersion = false
	if docs[2].IsNewVersion {
		t.Errorf("EDOC3 should be IsNewVersion = false")
	}
	// EDOC4 (2026-08-07) should be IsNewVersion = true (distinct group: 9800, 8800, 6800)
	if !docs[3].IsNewVersion {
		t.Errorf("EDOC4 should be IsNewVersion = true")
	}
	// EDOC5 (2026-06-24) should be IsNewVersion = false
	if docs[4].IsNewVersion {
		t.Errorf("EDOC5 should be IsNewVersion = false")
	}
}

func TestStoreAndQueryWithNewFields(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hwdocs_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := store.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	repo := store.NewRepository(db)

	docs := []store.Document{
		{
			NID:          "DOC001",
			ProductID:    "PID1",
			ProductName:  "CE6800",
			Name:         "CE6800 V300R025C00 产品文档(hdx)",
			DocType:          "HDX",
			DocCategory:      "产品文档包",
			DocCategoryGroup: "文档合集",
			PublishDate:      "2026-08-07",
			PublishTime:      "2026-08-07 20:46:31",
			IsNewVersion:     true,
			IsDownloaded:     0,
		},
		{
			NID:              "DOC002",
			ProductID:        "PID1",
			ProductName:      "CE6800",
			Name:             "CE6800 V300R024C00 产品文档(hdx)",
			DocType:          "HDX",
			DocCategory:      "产品文档包",
			DocCategoryGroup: "文档合集",
			PublishDate:      "2026-08-06",
			PublishTime:      "2026-08-06 17:20:53",
			IsNewVersion:     false,
			IsDownloaded:     1,
		},
		{
			NID:              "DOC003",
			ProductID:        "PID1",
			ProductName:      "CE6800",
			Name:             "（多媒体）CE6800 智能运维视频",
			DocType:          "多媒体",
			DocCategory:      "方案概述",
			DocCategoryGroup: "了解方案",
			PublishDate:      "2026-08-01",
			PublishTime:      "2026-08-01 10:00:00",
			IsNewVersion:     true,
			IsDownloaded:     0,
		},
	}

	err = repo.UpsertDocuments(docs)
	if err != nil {
		t.Fatalf("UpsertDocuments failed: %v", err)
	}

	// 1. Query all
	res, err := repo.QueryDocuments(store.DocFilterQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryDocuments failed: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("Expected 3 docs, got %d", res.Total)
	}
	// First should be DOC002 because IsDownloaded=1
	if res.Items[0].NID != "DOC002" {
		t.Errorf("Expected first item to be downloaded DOC002, got %s", res.Items[0].NID)
	}

	// 2. Filter by DocType "多媒体"
	resMedia, err := repo.QueryDocuments(store.DocFilterQuery{DocType: "多媒体", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryDocuments for 多媒体 failed: %v", err)
	}
	if resMedia.Total != 1 || resMedia.Items[0].NID != "DOC003" {
		t.Errorf("Expected 1 multimedia doc DOC003, got total %d", resMedia.Total)
	}

	// 3. Filter by DocCategory "方案概述"
	resCat, err := repo.QueryDocuments(store.DocFilterQuery{DocCategory: "方案概述", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryDocuments for 方案概述 failed: %v", err)
	}
	if resCat.Total != 1 || resCat.Items[0].NID != "DOC003" {
		t.Errorf("Expected 1 doc for 方案概述, got total %d", resCat.Total)
	}

	// 4. Keyword search matching DocCategory "产品文档包"
	resKw, err := repo.QueryDocuments(store.DocFilterQuery{Keyword: "产品文档包", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryDocuments by keyword failed: %v", err)
	}
	if resKw.Total != 2 {
		t.Errorf("Expected 2 docs matching keyword 产品文档包, got %d", resKw.Total)
	}

	// 5. GetDocCategories distinct tags
	cats, err := repo.GetDocCategories("PID1", "", "")
	if err != nil {
		t.Fatalf("GetDocCategories failed: %v", err)
	}
	if len(cats) != 2 {
		t.Errorf("Expected 2 distinct doc categories, got %v", cats)
	}

	// 6. Verify fields persisted
	doc1, err := repo.GetDocumentByNID("DOC001")
	if err != nil {
		t.Fatalf("GetDocumentByNID failed: %v", err)
	}
	if doc1.PublishDate != "2026-08-07" || !doc1.IsNewVersion || doc1.DocCategory != "产品文档包" {
		t.Errorf("Doc1 fields mismatch: PublishDate=%q, IsNewVersion=%v, DocCategory=%q",
			doc1.PublishDate, doc1.IsNewVersion, doc1.DocCategory)
	}
}
