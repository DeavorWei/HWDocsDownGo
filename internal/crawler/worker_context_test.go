package crawler

import (
	"context"
	"testing"
)

func TestWorkerContext(t *testing.T) {
	// 1. 测试未设置 Worker 的 Context
	ctx := context.Background()
	id, tag := GetWorkerFromContext(ctx)
	if id != 0 || tag != "" {
		t.Errorf("GetWorkerFromContext(ctx) = (%d, %q), expected (0, \"\")", id, tag)
	}

	fields := WorkerZapFields(id)
	if len(fields) != 0 {
		t.Errorf("WorkerZapFields(0) should be empty, got %v", fields)
	}

	msg := FormatWorkerMsg(tag, "测试消息")
	if msg != "测试消息" {
		t.Errorf("FormatWorkerMsg(\"\", msg) = %q, expected %q", msg, "测试消息")
	}

	// 2. 测试注入合法 Worker ID
	workerCtx := WithWorkerContext(ctx, 3)
	id, tag = GetWorkerFromContext(workerCtx)
	if id != 3 || tag != "[爬虫-3]" {
		t.Errorf("GetWorkerFromContext(workerCtx) = (%d, %q), expected (3, \"[爬虫-3]\")", id, tag)
	}

	fields = WorkerZapFields(id)
	if len(fields) != 1 || fields[0].Key != "workerId" || fields[0].Integer != 3 {
		t.Errorf("WorkerZapFields(3) = %v, expected [workerId: 3]", fields)
	}

	msg = FormatWorkerMsg(tag, "产品线解析成功")
	if msg != "[爬虫-3] 产品线解析成功" {
		t.Errorf("FormatWorkerMsg(%q, msg) = %q, expected %q", tag, msg, "[爬虫-3] 产品线解析成功")
	}

	// 3. 测试 nil context
	id, tag = GetWorkerFromContext(nil)
	if id != 0 || tag != "" {
		t.Errorf("GetWorkerFromContext(nil) = (%d, %q), expected (0, \"\")", id, tag)
	}
}
