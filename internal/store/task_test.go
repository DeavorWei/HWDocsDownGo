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

func TestDownloadTask_StableOrderingWhileDownloading(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "order_test.db")
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

	// 创建 3 个按序启动的正在下载的任务 (status 1)
	t1 := &DownloadTask{ID: "task_a", DocNID: "NID_A", DocName: "Doc A", Status: 1}
	t2 := &DownloadTask{ID: "task_b", DocNID: "NID_B", DocName: "Doc B", Status: 1}
	t3 := &DownloadTask{ID: "task_c", DocNID: "NID_C", DocName: "Doc C", Status: 1}

	_ = repo.CreateDownloadTask(t1)
	_ = repo.CreateDownloadTask(t2)
	_ = repo.CreateDownloadTask(t3)

	// 初始查询：顺序应为 task_a, task_b, task_c
	tasks, err := repo.GetAllDownloadTasks()
	if err != nil || len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d (err: %v)", len(tasks), err)
	}
	if tasks[0].ID != "task_a" || tasks[1].ID != "task_b" || tasks[2].ID != "task_c" {
		t.Fatalf("unexpected initial order: %v, %v, %v", tasks[0].ID, tasks[1].ID, tasks[2].ID)
	}

	// 模拟 task_b 汇报了新的下载进度，使得 task_b 的 updated_at 变成了最新
	_ = repo.UpdateDownloadTaskProgress("task_b", 500, 1000, 50.0, 120.0, 1, "")

	// 再次查询：即使 task_b 的 updated_at 更新了，正在下载的任务顺序必须保持固定！
	tasks, err = repo.GetAllDownloadTasks()
	if err != nil {
		t.Fatalf("GetAllDownloadTasks failed: %v", err)
	}
	if tasks[0].ID != "task_a" || tasks[1].ID != "task_b" || tasks[2].ID != "task_c" {
		t.Fatalf("order jumped after progress update! got: %v, %v, %v", tasks[0].ID, tasks[1].ID, tasks[2].ID)
	}

	// 模拟 task_a 下载完成 (status 1 -> 2)
	_ = repo.UpdateDownloadTaskProgress("task_a", 1000, 1000, 100.0, 0, 2, "")

	// 再次查询：task_b 和 task_c 仍在下载中且保持相对顺序不变，已完成的 task_a 排到最后
	tasks, err = repo.GetAllDownloadTasks()
	if err != nil {
		t.Fatalf("GetAllDownloadTasks failed: %v", err)
	}
	if tasks[0].ID != "task_b" || tasks[1].ID != "task_c" || tasks[2].ID != "task_a" {
		t.Fatalf("expected order after task_a completed to be [task_b, task_c, task_a], got: %v, %v, %v", tasks[0].ID, tasks[1].ID, tasks[2].ID)
	}
}
