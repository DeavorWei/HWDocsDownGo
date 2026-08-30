package store

import (
	"path/filepath"
	"testing"
)

func TestDownloadTask_SavePathAndBackfill(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "task_test.db")
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

	// 1. 创建文档记录（包含 local_path）
	doc := Document{
		NID:          "EDOC999999",
		Name:         "测试文档手册",
		FileName:     "test_manual.zip",
		LocalPath:    "C:/Downloads/[EDOC999999]test_manual.zip",
		IsDownloaded: 1,
	}
	if err := repo.UpsertDocuments([]Document{doc}); err != nil {
		t.Fatalf("UpsertDocuments failed: %v", err)
	}

	// 2. 创建初始下载任务（SavePath 为空）
	task := &DownloadTask{
		ID:       "task_1",
		DocNID:   "EDOC999999",
		DocName:  "测试文档手册",
		SavePath: "",
		Status:   2, // 已完成
	}
	if err := repo.CreateDownloadTask(task); err != nil {
		t.Fatalf("CreateDownloadTask failed: %v", err)
	}

	// 3. 调用 GetAllDownloadTasks，验证自动从文档表中回填 SavePath
	tasks, err := repo.GetAllDownloadTasks()
	if err != nil {
		t.Fatalf("GetAllDownloadTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].SavePath != doc.LocalPath {
		t.Fatalf("expected backfilled SavePath '%s', got '%s'", doc.LocalPath, tasks[0].SavePath)
	}

	// 4. 测试 UpdateDownloadTaskProgress 显式更新 SavePath
	newPath := "C:/NewDownloads/[EDOC999999]test_manual_new.zip"
	if err := repo.UpdateDownloadTaskProgress("task_1", 1000, 1000, 100.0, 0, 2, "", newPath, "test_manual_new.zip"); err != nil {
		t.Fatalf("UpdateDownloadTaskProgress failed: %v", err)
	}

	updatedTask, err := repo.GetDownloadTaskByID("task_1")
	if err != nil {
		t.Fatalf("GetDownloadTaskByID failed: %v", err)
	}
	if updatedTask.SavePath != newPath {
		t.Fatalf("expected updated SavePath '%s', got '%s'", newPath, updatedTask.SavePath)
	}
	if updatedTask.FileName != "test_manual_new.zip" {
		t.Fatalf("expected updated FileName 'test_manual_new.zip', got '%s'", updatedTask.FileName)
	}
}
