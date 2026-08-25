package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"
)

func TestArchiveOldLogOnStartup(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "log_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	logFilePath := filepath.Join(tempDir, "log.log")
	err = os.WriteFile(logFilePath, []byte("2026-08-25 Old log content from previous run\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ArchiveOldLogOnStartup(tempDir, logFilePath)

	if _, err := os.Stat(logFilePath); !os.IsNotExist(err) {
		t.Errorf("log.log should have been moved/archived")
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 archived log file, got %d", len(entries))
	}
	archivedName := entries[0].Name()
	if !strings.HasSuffix(archivedName, ".log") || archivedName == "log.log" {
		t.Errorf("Unexpected archived file name: %s", archivedName)
	}
}

func TestCleanOldLogs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "log_clean_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	now := time.Now()

	oldFile := filepath.Join(tempDir, "2025-01-01_10-00-00.log")
	_ = os.WriteFile(oldFile, []byte("very old log"), 0644)
	oldTime := now.Add(-200 * 24 * time.Hour)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	for i := 1; i <= 3; i++ {
		p := filepath.Join(tempDir, fmt.Sprintf("2026-08-2%d_10-00-00.log", i))
		content := strings.Repeat(fmt.Sprintf("log content %d\n", i), 60)
		_ = os.WriteFile(p, []byte(content), 0644)
		fileTime := now.Add(-time.Duration(10-i) * 24 * time.Hour)
		_ = os.Chtimes(p, fileTime, fileTime)
	}

	activeFile := filepath.Join(tempDir, "log.log")
	_ = os.WriteFile(activeFile, []byte("active current log"), 0644)

	CleanOldLogs(tempDir, 0, 180)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("200-day-old log should have been deleted by maxAge")
	}

	if _, err := os.Stat(activeFile); err != nil {
		t.Errorf("Active log.log should never be deleted by retention cleaner")
	}
}

func TestDynamicLogLevel(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "log_level_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	_, err = InitLogger(tempDir, false, "warn")
	if err != nil {
		t.Fatalf("InitLogger failed: %v", err)
	}

	if GetLevel() != "warn" {
		t.Errorf("Expected initial level warn, got %s", GetLevel())
	}
	if atomicLevel.Enabled(zapcore.InfoLevel) {
		t.Errorf("Info level should be disabled when level is warn")
	}
	if !atomicLevel.Enabled(zapcore.WarnLevel) {
		t.Errorf("Warn level should be enabled when level is warn")
	}

	SetLevel("debug")
	if GetLevel() != "debug" {
		t.Errorf("Expected level debug, got %s", GetLevel())
	}
	if !atomicLevel.Enabled(zapcore.DebugLevel) {
		t.Errorf("Debug level should be enabled when level is debug")
	}

	SetLevel("error")
	if GetLevel() != "error" {
		t.Errorf("Expected level error, got %s", GetLevel())
	}
	if atomicLevel.Enabled(zapcore.WarnLevel) {
		t.Errorf("Warn level should be disabled when level is error")
	}
}
