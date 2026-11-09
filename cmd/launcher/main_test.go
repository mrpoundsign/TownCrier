package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrpoundsign/appinstaller/pkg/launcher"
)

func TestGetRuntimeDir(t *testing.T) {
	dir, err := launcher.GetRuntimeDir(AppID, AppVersion)
	if err != nil {
		t.Fatalf("GetRuntimeDir failed: %v", err)
	}
	if dir == "" {
		t.Error("expected non-empty runtime dir")
	}
}

func TestExtractPayloadIfNeeded(t *testing.T) {
	tmpDir := t.TempDir()
	if err := launcher.ExtractPayloadIfNeeded(payloadFS, "payload", tmpDir); err != nil {
		t.Fatalf("ExtractPayloadIfNeeded failed: %v", err)
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

	if err := launcher.CleanupOldVersions(baseDir, "0.0.7"); err != nil {
		t.Fatalf("CleanupOldVersions failed: %v", err)
	}

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("expected old version directory 0.0.5 to be removed")
	}
	if _, err := os.Stat(curDir); err != nil {
		t.Errorf("expected current version directory 0.0.7 to remain: %v", err)
	}
}
