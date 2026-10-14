package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetRuntimeDir(t *testing.T) {
	dir, err := getRuntimeDir()
	if err != nil {
		t.Fatalf("getRuntimeDir failed: %v", err)
	}
	if dir == "" {
		t.Error("expected non-empty runtime dir")
	}
}

func TestMatchesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")
	data := []byte("hello world payload data")

	if matchesExisting(filePath, data) {
		t.Error("expected non-existent file to not match")
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if !matchesExisting(filePath, data) {
		t.Error("expected identical content to match")
	}

	diffData := []byte("different data")
	if matchesExisting(filePath, diffData) {
		t.Error("expected different content not to match")
	}
}

func TestExtractPayloadIfNeeded(t *testing.T) {
	tmpDir := t.TempDir()
	if err := extractPayloadIfNeeded(tmpDir); err != nil {
		t.Fatalf("extractPayloadIfNeeded failed: %v", err)
	}
}
