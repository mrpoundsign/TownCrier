package tts

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	ErrChecksumMismatch = errors.New("downloaded file checksum mismatch")

	// KokoroFP32ArchiveURL is the upstream sherpa-onnx release archive containing model.onnx (FP32 for DirectML GPU) and shared assets.
	KokoroFP32ArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2"
	KokoroFP32ArchiveSHA256       = "912804855a04745fa77a30be545b3f9a5d15c4d66db00b88cbcd4921df605ac7"
	KokoroFP32ArchiveSize   int64 = 319625534

	// KokoroINT8ArchiveURL is the upstream sherpa-onnx release archive containing model.int8.onnx (INT8 for CPU SIMD fallback).
	KokoroINT8ArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-int8-en-v0_19.tar.bz2"
	KokoroINT8ArchiveSHA256       = "c9f0dd393615805b0bab050c340834d5e684e732aec91c0e860cd30e982c08bd"
	KokoroINT8ArchiveSize   int64 = 103248205
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

// IsModelInstalled checks if all required Kokoro model assets (both FP32 GPU and INT8 CPU models + shared assets) are present in the directory.
func IsModelInstalled(dir string) bool {
	required := []string{
		"model.onnx",
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

func hasSharedAssets(dir string) bool {
	shared := []string{
		"voices.bin",
		"tokens.txt",
		"espeak-ng-data",
	}
	for _, req := range shared {
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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

type archiveTask struct {
	name           string
	url            string
	expectedSHA256 string
	expectedSize   int64
	onlyFile       string
}

type stagedFile struct {
	stagePath  string
	targetPath string
}

type progressHashReader struct {
	r      io.Reader
	hasher hash.Hash
	onRead func(n int64)
}

func (p *progressHashReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		_, _ = p.hasher.Write(buf[:n])
		if p.onRead != nil {
			p.onRead(int64(n))
		}
	}
	return n, err
}

// DownloadKokoroModel downloads and extracts any missing Kokoro model archives (FP32 + INT8) concurrently in parallel.
func DownloadKokoroModel(destDir string, onProgress DownloadProgressFunc) error {
	if IsModelInstalled(destDir) {
		return nil
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create model directory %s: %w", destDir, err)
	}

	sharedPresent := hasSharedAssets(destDir)
	needFP32 := !fileExists(filepath.Join(destDir, "model.onnx")) || !sharedPresent
	needINT8 := !fileExists(filepath.Join(destDir, "model.int8.onnx"))

	var tasks []archiveTask
	var totalBytes int64

	if needFP32 {
		tasks = append(tasks, archiveTask{
			name:           "fp32",
			url:            KokoroFP32ArchiveURL,
			expectedSHA256: KokoroFP32ArchiveSHA256,
			expectedSize:   KokoroFP32ArchiveSize,
		})
		totalBytes += KokoroFP32ArchiveSize
	}

	if needINT8 {
		only := ""
		if needFP32 || sharedPresent {
			only = "model.int8.onnx"
		}
		tasks = append(tasks, archiveTask{
			name:           "int8",
			url:            KokoroINT8ArchiveURL,
			expectedSHA256: KokoroINT8ArchiveSHA256,
			expectedSize:   KokoroINT8ArchiveSize,
			onlyFile:       only,
		})
		totalBytes += KokoroINT8ArchiveSize
	}

	if len(tasks) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		downloadedBytes atomic.Int64
		totalAtomic     atomic.Int64
		progMu          sync.Mutex
		wg              sync.WaitGroup
		errOnce         sync.Once
		firstErr        error
	)
	totalAtomic.Store(totalBytes)

	reportProgress := func(delta int64) {
		cur := downloadedBytes.Add(delta)
		tot := max(cur, totalAtomic.Load())
		if onProgress != nil {
			progMu.Lock()
			onProgress(cur, tot)
			progMu.Unlock()
		}
	}

	for _, task := range tasks {
		t := task
		wg.Go(func() {
			err := downloadAndExtractTarBz2(ctx, t, destDir, &totalAtomic, reportProgress)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
			}
		})
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	if !IsModelInstalled(destDir) {
		return fmt.Errorf("kokoro model extraction finished but required files are still missing in %s", destDir)
	}

	return nil
}

func downloadAndExtractTarBz2(
	ctx context.Context,
	task archiveTask,
	destDir string,
	totalAtomic *atomic.Int64,
	onBytesRead func(delta int64),
) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, task.url, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", task.name, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s archive: %w", task.name, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s archive failed with HTTP status %d", task.name, resp.StatusCode)
	}

	if resp.ContentLength > 0 && task.expectedSize > 0 && resp.ContentLength != task.expectedSize {
		totalAtomic.Add(resp.ContentLength - task.expectedSize)
	}

	var staged []stagedFile
	defer func() {
		if err != nil {
			for _, sf := range staged {
				_ = os.Remove(sf.stagePath)
			}
		}
	}()

	hasher := sha256.New()
	pr := &progressHashReader{
		r:      resp.Body,
		hasher: hasher,
		onRead: onBytesRead,
	}

	bzReader := bzip2.NewReader(pr)
	tr := tar.NewReader(bzReader)

	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read %s tar stream: %w", task.name, nextErr)
		}

		relPath, ok := stripTopLevelDir(hdr.Name)
		if !ok || relPath == "" {
			continue
		}

		if task.onlyFile != "" && relPath != task.onlyFile {
			continue
		}

		targetPath := filepath.Join(destDir, filepath.FromSlash(relPath))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if mkErr := os.MkdirAll(targetPath, 0755); mkErr != nil {
				return fmt.Errorf("create directory %s: %w", targetPath, mkErr)
			}
		case tar.TypeReg:
			if mkErr := os.MkdirAll(filepath.Dir(targetPath), 0755); mkErr != nil {
				return fmt.Errorf("create parent directory for %s: %w", targetPath, mkErr)
			}
			stagePath := fmt.Sprintf("%s.%s.part", targetPath, task.name)
			f, createErr := os.OpenFile(stagePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if createErr != nil {
				return fmt.Errorf("create staged file %s: %w", stagePath, createErr)
			}
			staged = append(staged, stagedFile{stagePath: stagePath, targetPath: targetPath})

			if _, copyErr := io.Copy(f, tr); copyErr != nil {
				_ = f.Close()
				return fmt.Errorf("extract %s from %s: %w", relPath, task.name, copyErr)
			}
			if closeErr := f.Close(); closeErr != nil {
				return fmt.Errorf("close staged file %s: %w", stagePath, closeErr)
			}
		}
	}

	// Drain any trailing bzip2/stream padding so the full archive is hashed
	if _, drainErr := io.Copy(io.Discard, pr); drainErr != nil {
		return fmt.Errorf("drain %s archive stream: %w", task.name, drainErr)
	}

	if task.expectedSHA256 != "" {
		computed := hex.EncodeToString(hasher.Sum(nil))
		if computed != task.expectedSHA256 {
			return fmt.Errorf("%w (%s): expected %s, got %s", ErrChecksumMismatch, task.name, task.expectedSHA256, computed)
		}
	}

	for _, sf := range staged {
		if renErr := os.Rename(sf.stagePath, sf.targetPath); renErr != nil {
			return fmt.Errorf("rename extracted file %s: %w", sf.targetPath, renErr)
		}
	}

	return nil
}

func stripTopLevelDir(entryName string) (string, bool) {
	cleaned := strings.TrimPrefix(filepath.ToSlash(entryName), "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	slashIdx := strings.IndexByte(cleaned, '/')
	if slashIdx < 0 {
		return "", false
	}
	rel := strings.TrimSpace(cleaned[slashIdx+1:])
	if rel == "" {
		return "", false
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", false
	}
	return rel, true
}


