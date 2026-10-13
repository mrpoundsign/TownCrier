package tts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSpeakerID(t *testing.T) {
	if id := ResolveSpeakerID("af_bella"); id != 0 {
		t.Errorf("expected 0 for af_bella, got %d", id)
	}
	if id := ResolveSpeakerID("am_adam"); id != 2 {
		t.Errorf("expected 2 for am_adam, got %d", id)
	}
	if id := ResolveSpeakerID("  AF_SARAH  "); id != 1 {
		t.Errorf("expected 1 for uppercase spaced AF_SARAH, got %d", id)
	}
	if id := ResolveSpeakerID("non_existent_voice"); id != 0 {
		t.Errorf("expected fallback 0 for unknown voice, got %d", id)
	}
}

func TestVerifyFileChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "sample.txt")
	content := []byte("Kokoro TTS Test Content")

	if err := os.WriteFile(testFile, content, 0600); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(h[:])

	ok, err := VerifyFileChecksum(testFile, expectedHash)
	if err != nil {
		t.Fatalf("VerifyFileChecksum failed: %v", err)
	}
	if !ok {
		t.Error("expected checksum match")
	}

	ok, err = VerifyFileChecksum(testFile, "0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("VerifyFileChecksum failed: %v", err)
	}
	if ok {
		t.Error("expected checksum mismatch for bad hash")
	}
}

func TestDownloadFileWithChecksum(t *testing.T) {
	testData := "Download test data for atomic part rename"
	h := sha256.Sum256([]byte(testData))
	expectedHash := hex.EncodeToString(h[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testData))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destFile := filepath.Join(tmpDir, "downloaded.txt")

	// 1. Success case
	err := DownloadFile(server.URL, destFile, expectedHash)
	if err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}

	data, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != testData {
		t.Errorf("content mismatch: got %q", string(data))
	}

	// Verify .part file was cleaned up
	if _, err := os.Stat(destFile + ".part"); !os.IsNotExist(err) {
		t.Error("part file should not remain after successful download")
	}

	// 2. Checksum mismatch case
	badDestFile := filepath.Join(tmpDir, "bad.txt")
	err = DownloadFile(server.URL, badDestFile, "badhash123")
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got %v", err)
	}
	if _, err := os.Stat(badDestFile); !os.IsNotExist(err) {
		t.Error("target file should not exist after failed checksum")
	}
}

func TestIsModelInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	if IsModelInstalled(tmpDir) {
		t.Error("empty dir should not be reported as installed")
	}

	// Create dummy files
	_ = os.WriteFile(filepath.Join(tmpDir, "model.int8.onnx"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(tmpDir, "voices.bin"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(tmpDir, "tokens.txt"), []byte("x"), 0600)
	_ = os.Mkdir(filepath.Join(tmpDir, "espeak-ng-data"), 0755)

	if !IsModelInstalled(tmpDir) {
		t.Error("dir with all assets should be reported as installed")
	}
}

func TestSherpaEngineSynthesis(t *testing.T) {
	modelDir := DefaultModelDir()
	if !IsModelInstalled(modelDir) {
		t.Skip("local Kokoro model not installed, skipping live synthesis test")
	}

	engine, err := NewSherpaEngine(modelDir, 2, false)
	if err != nil {
		t.Fatalf("NewSherpaEngine failed: %v", err)
	}
	defer func() { _ = engine.Close() }()

	if len(engine.Voices()) == 0 {
		t.Error("expected non-empty voices list")
	}

	samples, sr, err := engine.Synthesize("Testing audio output.", "af_bella", 1.0)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(samples) == 0 {
		t.Error("expected non-empty samples")
	}
	if sr != 24000 {
		t.Errorf("expected 24000 Hz, got %d", sr)
	}
}
