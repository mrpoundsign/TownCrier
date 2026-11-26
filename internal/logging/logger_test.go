package logging

import (
	"errors"
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

func TestReadTail(t *testing.T) {
	tmpDir := t.TempDir()

	_, _, err := ReadTail(filepath.Join(tmpDir, "missing.log"), 0)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for missing log file, got %v", err)
	}

	smallPath := filepath.Join(tmpDir, "small.log")
	if writeErr := os.WriteFile(smallPath, []byte("line 1\r\nline 2\r\n"), 0o600); writeErr != nil {
		t.Fatalf("write small log: %v", writeErr)
	}
	content, truncated, err := ReadTail(smallPath, 1024)
	if err != nil {
		t.Fatalf("ReadTail small failed: %v", err)
	}
	if truncated {
		t.Errorf("expected truncated=false for small file")
	}
	if content != "line 1\nline 2\n" {
		t.Errorf("unexpected normalized content: %q", content)
	}

	largePath := filepath.Join(tmpDir, "large.log")
	var sb strings.Builder
	sb.WriteString("partial-first-line-to-drop-completely-when-reading-tail\n")
	for range 20 {
		sb.WriteString("middle log entry payload line\n")
	}
	sb.WriteString("newest-tail-entry\n")
	if writeErr := os.WriteFile(largePath, []byte(sb.String()), 0o600); writeErr != nil {
		t.Fatalf("write large log: %v", writeErr)
	}

	tail, truncated, err := ReadTail(largePath, 128)
	if err != nil {
		t.Fatalf("ReadTail large failed: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncated=true for large file")
	}
	if strings.Contains(tail, "partial-first-line") {
		t.Errorf("expected partial leading line to be discarded, got %q", tail)
	}
	if !strings.HasPrefix(tail, "middle log entry payload line\n") {
		t.Errorf("expected tail to start on clean line boundary, got %q", tail)
	}
	if !strings.Contains(tail, "newest-tail-entry") {
		t.Errorf("expected tail to include newest entry, got %q", tail)
	}
}
