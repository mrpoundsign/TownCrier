package tts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

var (
	ErrChecksumMismatch = errors.New("downloaded file checksum mismatch")
)

// ModelFile holds metadata for a downloadable model component.
type ModelFile struct {
	Name   string
	URL    string
	SHA256 string // Optional SHA256 hex verification
}

// ModelManager manages discovery, verification, and downloading of neural TTS models.
type ModelManager struct {
	baseDir string
}

// NewModelManager creates a ModelManager for the specified base directory.
func NewModelManager(baseDir string) *ModelManager {
	return &ModelManager{baseDir: baseDir}
}

// DefaultModelDir resolves the target directory for Kokoro model files.
// It checks local ./models/kokoro-en first, then falls back to user local data dir.
func DefaultModelDir() string {
	candidates := []string{
		"models/kokoro-en",
		"../models/kokoro-en",
		"../../models/kokoro-en",
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "models", "kokoro-en"),
			filepath.Join(exeDir, "..", "models", "kokoro-en"),
			filepath.Join(exeDir, "..", "..", "models", "kokoro-en"),
		)
	}

	for _, c := range candidates {
		if IsModelInstalled(c) {
			return c
		}
	}

	// Host OS data directory
	configDir, err := os.UserConfigDir()
	if err == nil {
		newPath := filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "models", "kokoro-en")
		if IsModelInstalled(newPath) {
			return newPath
		}
		for _, legacy := range []string{
			filepath.Join(configDir, "TownCrier", "models", "kokoro-en"),
			filepath.Join(configDir, "com.github.mrpoundsign.octochorpebot", "models", "kokoro-en"),
			filepath.Join(configDir, "OctochorpeBot", "models", "kokoro-en"),
		} {
			if IsModelInstalled(legacy) {
				return legacy
			}
		}
		return newPath
	}

	return "models/kokoro-en"
}

// IsModelInstalled checks if all required Kokoro model assets are present in the directory.
func IsModelInstalled(dir string) bool {
	required := []string{
		"model.int8.onnx",
		"voices.bin",
		"tokens.txt",
		"espeak-ng-data",
	}

	for _, req := range required {
		path := filepath.Join(dir, req)
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if req == "espeak-ng-data" && !info.IsDir() {
			return false
		}
	}
	return true
}

// VerifyFileChecksum computes the SHA-256 hash of a file and verifies it against expected.
func VerifyFileChecksum(filePath string, expectedSHA256 string) (bool, error) {
	if expectedSHA256 == "" {
		return true, nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return false, fmt.Errorf("open file for checksum: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return false, fmt.Errorf("hash file: %w", err)
	}

	computed := hex.EncodeToString(hasher.Sum(nil))
	return computed == expectedSHA256, nil
}

// DownloadFile downloads a file from URL to destPath with atomic .part renaming and optional SHA-256 verification.
func DownloadFile(url string, destPath string, expectedSHA256 string) (err error) {
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create dest directory: %w", err)
	}

	partPath := destPath + ".part"
	partFile, err := os.Create(partPath)
	if err != nil {
		return fmt.Errorf("create part file: %w", err)
	}
	defer func() {
		_ = partFile.Close()
		if err != nil {
			_ = os.Remove(partPath)
		}
	}()

	resp, err := http.Get(url) //nolint:gosec // user model download URL
	if err != nil {
		return fmt.Errorf("fetch url %s: %w", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP status %d", resp.StatusCode)
	}

	hasher := sha256.New()
	writer := io.MultiWriter(partFile, hasher)

	if _, err := io.Copy(writer, resp.Body); err != nil {
		return fmt.Errorf("write download data: %w", err)
	}

	if expectedSHA256 != "" {
		computed := hex.EncodeToString(hasher.Sum(nil))
		if computed != expectedSHA256 {
			return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expectedSHA256, computed)
		}
	}

	if err := partFile.Close(); err != nil {
		return fmt.Errorf("close part file: %w", err)
	}

	if err := os.Rename(partPath, destPath); err != nil {
		return fmt.Errorf("atomic rename part file: %w", err)
	}

	return nil
}

// DownloadProgressFunc reports progress during model downloads.
type DownloadProgressFunc func(current, total int64)

// DownloadKokoroModel checks if Kokoro model files are installed, or returns an error if not present.
func DownloadKokoroModel(destDir string, onProgress DownloadProgressFunc) error {
	if IsModelInstalled(destDir) {
		return nil
	}

	// If already installed in default candidate paths, no download needed
	candidate := DefaultModelDir()
	if IsModelInstalled(candidate) {
		return nil
	}

	return fmt.Errorf("model files not found in %s; please place kokoro-en model files in that directory", destDir)
}

