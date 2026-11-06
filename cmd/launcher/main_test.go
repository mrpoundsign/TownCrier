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

func TestCleanupOldVersions(t *testing.T) {
	baseDir := t.TempDir()
	oldDir := filepath.Join(baseDir, "0.0.5")
	curDir := filepath.Join(baseDir, "0.0.7")

	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(curDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := cleanupOldVersions(baseDir, "0.0.7"); err != nil {
		t.Fatalf("cleanupOldVersions failed: %v", err)
	}

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("expected old version directory 0.0.5 to be removed")
	}
	if _, err := os.Stat(curDir); err != nil {
		t.Errorf("expected current version directory 0.0.7 to remain: %v", err)
	}
}
