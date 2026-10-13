package logging

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggingInitAndWrite(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	path, err := Init(logPath)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if path != logPath {
		t.Errorf("expected path %q, got %q", logPath, path)
	}

	log.Printf("Hello test log %d", 42)
	Sync()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Hello test log 42") {
		t.Errorf("expected log to contain message, got: %s", content)
	}

	Close()
}

func TestRecoverAndLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "panic.log")

	_, err := Init(logPath)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()

	func() {
		defer RecoverAndLog("test-panic", false)
		panic("something went horribly wrong")
	}()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "something went horribly wrong") {
		t.Errorf("expected log to contain panic message, got: %s", content)
	}
	if !strings.Contains(content, "FATAL PANIC [test-panic]") {
		t.Errorf("expected log to contain panic context, got: %s", content)
	}
}
