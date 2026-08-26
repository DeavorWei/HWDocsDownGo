package store

import (
	"path/filepath"
	"testing"
)

func TestMultiKeywordAndRegexSearch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "search_test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	repo := NewRepository(db)

	testDocs := []Document{
		{
			NID:         "EDOC110001",
			Name:        "CloudEngine 9800 V300R022C00 配置指南",
			ProductName: "CloudEngine 9800",
			FileName:    "CloudEngine 9800 V300R022C00 配置指南.zip",
			DocType:     "ZIP",
			DocCategory: "配置指南",
		},
		{
			NID:         "EDOC110002",
			Name:        "CloudEngine 8800 V200R020C10 命令参考",
			ProductName: "CloudEngine 8800",
			FileName:    "CloudEngine 8800 V200R020C10 命令参考.hdx",
			DocType:     "HDX",
			DocCategory: "命令参考",
		},
		{
			NID:         "EDOC110003",
			Name:        "S8700 V600R024C10 日志参考",
			ProductName: "S8700",
			FileName:    "S8700 V600R024C10 日志参考.pdf",
			DocType:     "PDF",
			DocCategory: "日志参考",
		},
	}

	if err := repo.UpsertDocuments(testDocs); err != nil {
		t.Fatalf("UpsertDocuments failed: %v", err)
	}

	// 1. 测试单关键词搜索
	res1, err := repo.QueryDocuments(DocFilterQuery{Keyword: "CloudEngine", Page: 1, PageSize: 10})
	if err != nil || res1.Total != 2 {
		t.Fatalf("Expected 2 docs for 'CloudEngine', got %d (err: %v)", res1.Total, err)
	}

	// 2. 测试多关键词 AND 搜索 (同时满足 CloudEngine 和 9800)
	res2, err := repo.QueryDocuments(DocFilterQuery{Keyword: "CloudEngine 9800", Page: 1, PageSize: 10})
	if err != nil || res2.Total != 1 || res2.Items[0].NID != "EDOC110001" {
		t.Fatalf("Expected 1 doc (EDOC110001) for 'CloudEngine 9800', got %d (err: %v)", res2.Total, err)
	}

	// 3. 测试多关键词 AND 搜索无交集
	res3, err := repo.QueryDocuments(DocFilterQuery{Keyword: "CloudEngine S8700", Page: 1, PageSize: 10})
	if err != nil || res3.Total != 0 {
		t.Fatalf("Expected 0 docs for 'CloudEngine S8700', got %d (err: %v)", res3.Total, err)
	}

	// 4. 测试正则表达式搜索 (版本号正则)
	res4, err := repo.QueryDocuments(DocFilterQuery{Keyword: `V\d+R\d+C\d+`, IsRegex: true, Page: 1, PageSize: 10})
	if err != nil || res4.Total != 3 {
		t.Fatalf("Expected 3 docs for regex 'V\\d+R\\d+C\\d+', got %d (err: %v)", res4.Total, err)
	}

	// 5. 测试前缀 regex: 搜索
	res5, err := repo.QueryDocuments(DocFilterQuery{Keyword: `regex:.*(9800|8800).*配置指南`, Page: 1, PageSize: 10})
	if err != nil || res5.Total != 1 || res5.Items[0].NID != "EDOC110001" {
		t.Fatalf("Expected 1 doc for regex '.*(9800|8800).*配置指南', got %d (err: %v)", res5.Total, err)
	}

	// 6. 测试非法正则表达式容错
	res6, err := repo.QueryDocuments(DocFilterQuery{Keyword: `[invalid_regex(`, IsRegex: true, Page: 1, PageSize: 10})
	if err != nil || res6.Total != 0 {
		t.Fatalf("Expected 0 docs for invalid regex, got %d (err: %v)", res6.Total, err)
	}
}
