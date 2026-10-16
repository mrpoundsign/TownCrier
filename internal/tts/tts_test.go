package tts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestResolveSpeakerID(t *testing.T) {
	if id := ResolveSpeakerID("af"); id != 0 {
		t.Errorf("expected 0 for af, got %d", id)
	}
	if id := ResolveSpeakerID("af_bella"); id != 1 {
		t.Errorf("expected 1 for af_bella, got %d", id)
	}
	if id := ResolveSpeakerID("af_nicole"); id != 2 {
		t.Errorf("expected 2 for af_nicole, got %d", id)
	}
	if id := ResolveSpeakerID("  AF_SARAH  "); id != 3 {
		t.Errorf("expected 3 for uppercase spaced AF_SARAH, got %d", id)
	}
	if id := ResolveSpeakerID("af_sky"); id != 4 {
		t.Errorf("expected 4 for af_sky, got %d", id)
	}
	if id := ResolveSpeakerID("am_adam"); id != 5 {
		t.Errorf("expected 5 for am_adam, got %d", id)
	}
	if id := ResolveSpeakerID("am_michael"); id != 6 {
		t.Errorf("expected 6 for am_michael, got %d", id)
	}
	if id := ResolveSpeakerID("bf_emma"); id != 7 {
		t.Errorf("expected 7 for bf_emma, got %d", id)
	}
	if id := ResolveSpeakerID("bf_isabella"); id != 8 {
		t.Errorf("expected 8 for bf_isabella, got %d", id)
	}
	if id := ResolveSpeakerID("bm_george"); id != 9 {
		t.Errorf("expected 9 for bm_george, got %d", id)
	}
	if id := ResolveSpeakerID("bm_lewis"); id != 10 {
		t.Errorf("expected 10 for bm_lewis, got %d", id)
	}
	if id := ResolveSpeakerID("Bella — US Female (af_bella)"); id != 1 {
		t.Errorf("expected 1 for display label Bella, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("af_bella", 53); id != 2 {
		t.Errorf("expected 2 for af_bella in 53-speaker v1.0 model, got %d", id)
	}
	if id := ResolveSpeakerIDForModel("Adam — US Male (am_adam)", 53); id != 11 {
		t.Errorf("expected 11 for am_adam in 53-speaker v1.0 model, got %d", id)
	}
	if got := VoiceIDFromLabel("Emma — UK Female (bf_emma)"); got != "bf_emma" {
		t.Errorf("expected bf_emma from label, got %q", got)
	}
	if got := VoiceDisplayName("am_michael"); got != "Michael — US Male (am_michael)" {
		t.Errorf("unexpected display name: %q", got)
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

	// Create only int8 + shared assets -> still missing model.onnx
	_ = os.WriteFile(filepath.Join(tmpDir, "model.int8.onnx"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(tmpDir, "voices.bin"), []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(tmpDir, "tokens.txt"), []byte("x"), 0600)
	_ = os.Mkdir(filepath.Join(tmpDir, "espeak-ng-data"), 0755)

	if IsModelInstalled(tmpDir) {
		t.Error("dir without model.onnx should not be reported as installed")
	}

	_ = os.WriteFile(filepath.Join(tmpDir, "model.onnx"), []byte("x"), 0600)
	if !IsModelInstalled(tmpDir) {
		t.Error("dir with all assets (both model.onnx and model.int8.onnx) should be reported as installed")
	}
}

func TestDownloadKokoroModelParallel(t *testing.T) {
	const fp32B64 = "QlpoOTFBWSZTWYefy4YAAVl/gsKgACBAA/+wIQBAAP/v38BAADAIMAE4FBpQ0AGgAAAAAGMhkNBoNGgDQANDBFEmk0aBR+SBM0aGjTao2mp1+R53/M/dNZgQWjmDLeSCRoQFtxxupukGAksHZFIndUcwOAwIEjCyOQyOk28TIfoxHZbzj9eUQmkPHAHYCG/0eHD9iFQe3XQaC03f4Yv8qwREFRi0YR9hp4ZtY1sGh4xSgSwM2DnEsXaWUsPYGo2jJEToDUNaA0NEjI3D2SPgnIgeSccg+E34dMTpdaPHgZEmY4ZwaC4PxeDDb1j5BAwJVqIFyCbMRPBREH2Be4CYRD3wNQenMxG8c4N198RB1Hv6Rcmcd2PXb1HAa1nTq7BA/4u5IpwoSEPP5cMA"
	const int8B64 = "QlpoOTFBWSZTWZDFmCAAAWN/gsKgACBAA//kIAgAAP7v38BAADAIMAE4a0GkgAGgAAABoAaIygRiYJoaMRk0MTARSKYQ1HqZpqMjTTR6jDUNPUy5D1/bOc5d5cQUjUGPAkCRoQFpv2VznIGAkXjbBE76qRhS4YECQwshxGQ2FvyeY4j0Ngzp6ylr4RCWBbrBtBCOJx941CFAMDAP4OzfOHOMigYVwHdhYNdQwRhASlIJpEwEDY0ARBGoFAJ9wEgJGIDVFAdBKaoYHMUbQJjNNA3yHMnQgeifWIck36u2B2rpHS8ICQOG8XAwDoaBOnvC449V+MZQEy9IC6BHUInAtImB4dB/vg3HiOuBoB5lw6RrBq06IiDIdvaMwFaZx2XZW5DcNFEq+68QP8XckU4UJCQxZggA"

	fp32Archive, err := base64.StdEncoding.DecodeString(fp32B64)
	if err != nil {
		t.Fatalf("decode fp32B64: %v", err)
	}
	int8Archive, err := base64.StdEncoding.DecodeString(int8B64)
	if err != nil {
		t.Fatalf("decode int8B64: %v", err)
	}

	fp32Sum := sha256.Sum256(fp32Archive)
	int8Sum := sha256.Sum256(int8Archive)

	origFP32URL, origFP32SHA, origFP32Size := KokoroFP32ArchiveURL, KokoroFP32ArchiveSHA256, KokoroFP32ArchiveSize
	origINT8URL, origINT8SHA, origINT8Size := KokoroINT8ArchiveURL, KokoroINT8ArchiveSHA256, KokoroINT8ArchiveSize
	defer func() {
		KokoroFP32ArchiveURL, KokoroFP32ArchiveSHA256, KokoroFP32ArchiveSize = origFP32URL, origFP32SHA, origFP32Size
		KokoroINT8ArchiveURL, KokoroINT8ArchiveSHA256, KokoroINT8ArchiveSize = origINT8URL, origINT8SHA, origINT8Size
	}()

	var fp32Hits, int8Hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fp32.tar.bz2":
			fp32Hits.Add(1)
			_, _ = w.Write(fp32Archive)
		case "/int8.tar.bz2":
			int8Hits.Add(1)
			_, _ = w.Write(int8Archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	KokoroFP32ArchiveURL = server.URL + "/fp32.tar.bz2"
	KokoroFP32ArchiveSHA256 = hex.EncodeToString(fp32Sum[:])
	KokoroFP32ArchiveSize = int64(len(fp32Archive))

	KokoroINT8ArchiveURL = server.URL + "/int8.tar.bz2"
	KokoroINT8ArchiveSHA256 = hex.EncodeToString(int8Sum[:])
	KokoroINT8ArchiveSize = int64(len(int8Archive))

	destDir := t.TempDir()
	var lastCur, lastTot int64
	err = DownloadKokoroModel(destDir, func(cur, tot int64) {
		lastCur = cur
		lastTot = tot
	})
	if err != nil {
		t.Fatalf("DownloadKokoroModel failed: %v", err)
	}

	if !IsModelInstalled(destDir) {
		t.Fatal("expected model to be installed after parallel download")
	}
	if fp32Hits.Load() != 1 || int8Hits.Load() != 1 {
		t.Errorf("expected 1 hit each for fp32 and int8, got fp32=%d int8=%d", fp32Hits.Load(), int8Hits.Load())
	}
	if lastCur != lastTot || lastTot == 0 {
		t.Errorf("expected final progress %d == %d > 0", lastCur, lastTot)
	}

	// Verify file contents
	if got, _ := os.ReadFile(filepath.Join(destDir, "model.onnx")); string(got) != "fp32-weights" {
		t.Errorf("unexpected model.onnx content: %q", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(destDir, "model.int8.onnx")); string(got) != "int8-weights" {
		t.Errorf("unexpected model.int8.onnx content: %q", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(destDir, "espeak-ng-data", "en_dict")); string(got) != "dict-data" {
		t.Errorf("unexpected espeak-ng-data/en_dict content: %q", string(got))
	}

	// Verify checksum failure cleans up staged files and returns ErrChecksumMismatch
	badDir := t.TempDir()
	KokoroINT8ArchiveSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	err = DownloadKokoroModel(badDir, nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(badDir, "model.int8.onnx")); !os.IsNotExist(statErr) {
		t.Error("expected model.int8.onnx not to exist after checksum mismatch")
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
