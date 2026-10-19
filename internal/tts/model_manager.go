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
	KokoroFP32ArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-multi-lang-v1_0.tar.bz2"
	KokoroFP32ArchiveSHA256       = "c5f7e2d2caf082bc1d20fb70334a61d99d20b484500aad32e7cf84c128ea3298"
	KokoroFP32ArchiveSize   int64 = 334000000

	// KokoroINT8ArchiveURL is the upstream sherpa-onnx release archive containing model.int8.onnx (INT8 for CPU SIMD fallback).
	KokoroINT8ArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-int8-multi-lang-v1_0.tar.bz2"
	KokoroINT8ArchiveSHA256       = "4c3052abaa60943a341f193888cf6abd68787dae6ab8ae5c925a706caa247e4e"
	KokoroINT8ArchiveSize   int64 = 126000000

	// SupertonicArchiveURL is the upstream sherpa-onnx Supertonic INT8 model archive (10 voices, 44.1 kHz, ultra-fast CPU).
	SupertonicArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/sherpa-onnx-supertonic-tts-int8-2026-03-06.tar.bz2"
	SupertonicArchiveSHA256       = "8c74359f63edd5045d47747f65331f0f6dbcbc91d7e898dd756d631295fe3259"
	SupertonicArchiveSize   int64 = 84692981

	// KittenMiniArchiveURL is the upstream sherpa-onnx Kitten v0.8 Mini model archive (8 voices, 24 kHz).
	KittenMiniArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kitten-mini-en-v0_8.tar.bz2"
	KittenMiniArchiveSHA256       = "518f9b130320f690d5b5476df77bde4215fca67773cda16710318e5081234b9d"
	KittenMiniArchiveSize   int64 = 67547594

	// KittenNanoArchiveURL is the upstream sherpa-onnx Kitten v0.8 Nano INT8 model archive (8 voices, 24 kHz, ultra-low RAM).
	KittenNanoArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kitten-nano-en-v0_8-int8.tar.bz2"
	KittenNanoArchiveSHA256       = "6fa5be852612ce761094ba74ee6123b4fc4acfefa79bf64dc63acae4a83af2fd"
	KittenNanoArchiveSize   int64 = 31220690

	// PiperArchiveURL is the upstream sherpa-onnx Piper LibriTTS-High INT8 model archive (904 speakers, 22.05 kHz).
	PiperArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_US-libritts-high-int8.tar.bz2"
	PiperArchiveSHA256       = "c35f249498fe9406c5ca3b6db1fc16749d7ee415ff2a09d5da9334206c2e0026"
	PiperArchiveSize   int64 = 36406088
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
		if IsAnyModelFamilyInstalled(c) {
			return c
		}
	}

	// Host OS data directory
	configDir, err := os.UserConfigDir()
	if err == nil {
		newPath := filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "models", "kokoro-en")
		if IsAnyModelFamilyInstalled(newPath) {
			return newPath
		}
		for _, legacy := range []string{
			filepath.Join(configDir, "TownCrier", "models", "kokoro-en"),
			filepath.Join(configDir, "com.github.mrpoundsign.octochorpebot", "models", "kokoro-en"),
			filepath.Join(configDir, "OctochorpeBot", "models", "kokoro-en"),
		} {
			if IsAnyModelFamilyInstalled(legacy) {
				return legacy
			}
		}
		return newPath
	}

	return "models/kokoro-en"
}

// IsAnyModelFamilyInstalled reports whether Kokoro or any sibling model family directory is installed relative to kokoroDir.
func IsAnyModelFamilyInstalled(kokoroDir string) bool {
	if IsCPUModelInstalled(kokoroDir) || IsGPUModelInstalled(kokoroDir) {
		return true
	}
	for _, spec := range AllModelFamilies {
		if IsModelFamilyInstalled(kokoroDir, spec.ID, false) {
			return true
		}
	}
	return false
}

// isLegacyKokoroV019Dir reports whether dir contains old kokoro-en-v0_19 assets (11-speaker voices.bin ~5.75 MB).
func isLegacyKokoroV019Dir(dir string) bool {
	if fileExists(filepath.Join(dir, "lexicon-us-en.txt")) {
		return false
	}
	voicesPath := filepath.Join(dir, "voices.bin")
	info, err := os.Stat(voicesPath)
	if err == nil {
		// v0.19 voices.bin is 5,750,784 bytes (~5.75 MB).
		// v1.0 voices.bin is 27,701,760 bytes (~27.7 MB).
		if info.Size() >= 5*1024*1024 && info.Size() <= 7*1024*1024 {
			return true
		}
	}
	return false
}

// hasKokoroSharedAssets checks if the Kokoro v1.0 shared assets (voices.bin 53-spk, tokens.txt, espeak-ng-data) are present.
func hasKokoroSharedAssets(dir string) bool {
	if !hasSharedAssets(dir) {
		return false
	}
	if isLegacyKokoroV019Dir(dir) {
		return false
	}
	return true
}

// IsCPUModelInstalled checks if the INT8 CPU Kokoro model (model.int8.onnx) and shared assets are present.
func IsCPUModelInstalled(dir string) bool {
	return fileExists(filepath.Join(dir, "model.int8.onnx")) && hasKokoroSharedAssets(dir)
}

// IsGPUModelInstalled checks if the FP32 DirectML GPU Kokoro model (model.onnx) and shared assets are present.
func IsGPUModelInstalled(dir string) bool {
	return fileExists(filepath.Join(dir, "model.onnx")) && hasKokoroSharedAssets(dir)
}

// IsKokoroInstalledForMode checks if the required Kokoro model files for the requested mode (GPU vs CPU) are installed.
func IsKokoroInstalledForMode(dir string, useGPU bool) bool {
	if useGPU {
		return IsGPUModelInstalled(dir)
	}
	return IsCPUModelInstalled(dir)
}

// IsModelInstalled checks if all required Kokoro model assets (both FP32 GPU and INT8 CPU models + shared assets) are present in the directory.
func IsModelInstalled(dir string) bool {
	return IsCPUModelInstalled(dir) && IsGPUModelInstalled(dir)
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
	return DownloadKokoroModelForMode(destDir, true, onProgress)
}

// DownloadKokoroModelForMode downloads only the required Kokoro archive(s) for the requested execution mode.
// When useGPU is false, only the 98.5 MB INT8 archive (model.int8.onnx + shared assets) is downloaded.
// When useGPU is true, the FP32 archive (model.onnx + shared assets) and INT8 fallback are downloaded.
func DownloadKokoroModelForMode(destDir string, useGPU bool, onProgress DownloadProgressFunc) error {
	if useGPU {
		if IsModelInstalled(destDir) {
			return nil
		}
	} else {
		if IsCPUModelInstalled(destDir) {
			return nil
		}
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create model directory %s: %w", destDir, err)
	}

	if isLegacyKokoroV019Dir(destDir) {
		// Clean up stale v0.19 model files so mismatched speaker count ONNX / voices.bin are never paired.
		for _, f := range []string{"model.onnx", "model.int8.onnx", "voices.bin", "tokens.txt"} {
			_ = os.Remove(filepath.Join(destDir, f))
		}
	}

	sharedPresent := hasKokoroSharedAssets(destDir)
	needFP32 := useGPU && (!fileExists(filepath.Join(destDir, "model.onnx")) || !sharedPresent)
	needINT8 := !fileExists(filepath.Join(destDir, "model.int8.onnx")) || (!useGPU && !sharedPresent)

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

	if !IsKokoroInstalledForMode(destDir, useGPU) {
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

// ModelFamilyDir resolves the directory for a specific model family relative to the base Kokoro model directory.
func ModelFamilyDir(kokoroDir string, family string) string {
	if kokoroDir == "" {
		kokoroDir = DefaultModelDir()
	}
	fam := strings.ToLower(strings.TrimSpace(family))
	if fam == "" || fam == ModelKokoro || fam == ModelKokoroIntl {
		return kokoroDir
	}
	spec, ok := ModelSpecByID(fam)
	if !ok {
		return kokoroDir
	}
	parent := filepath.Dir(kokoroDir)
	return filepath.Join(parent, spec.SubDir)
}

// IsModelFamilyInstalled reports whether all required files for the given model family are installed on disk.
func IsModelFamilyInstalled(kokoroDir string, family string, useGPU bool) bool {
	fam := strings.ToLower(strings.TrimSpace(family))
	dir := ModelFamilyDir(kokoroDir, fam)
	switch fam {
	case "", ModelKokoro, ModelKokoroIntl:
		return IsKokoroInstalledForMode(dir, useGPU) || IsCPUModelInstalled(dir)
	case ModelSupertonic:
		for _, f := range []string{
			"duration_predictor.int8.onnx",
			"text_encoder.int8.onnx",
			"vector_estimator.int8.onnx",
			"vocoder.int8.onnx",
			"tts.json",
			"unicode_indexer.bin",
			"voice.bin",
		} {
			if !fileExists(filepath.Join(dir, f)) {
				return false
			}
		}
		return true
	case ModelKittenMini:
		return (fileExists(filepath.Join(dir, "model.fp16.onnx")) || fileExists(filepath.Join(dir, "model.onnx"))) && hasSharedAssets(dir)
	case ModelKittenNano:
		return fileExists(filepath.Join(dir, "model.int8.onnx")) && hasSharedAssets(dir)
	case ModelPiper:
		espeakInfo, err := os.Stat(filepath.Join(dir, "espeak-ng-data"))
		return err == nil && espeakInfo.IsDir() &&
			fileExists(filepath.Join(dir, "en_US-libritts-high.onnx")) &&
			fileExists(filepath.Join(dir, "tokens.txt"))
	default:
		return false
	}
}

// DownloadModelFamily downloads and extracts the archive for the requested model family if not already installed.
func DownloadModelFamily(kokoroDir string, family string, useGPU bool, onProgress DownloadProgressFunc) error {
	fam := strings.ToLower(strings.TrimSpace(family))
	dir := ModelFamilyDir(kokoroDir, fam)
	if fam == "" || fam == ModelKokoro || fam == ModelKokoroIntl {
		return DownloadKokoroModelForMode(dir, useGPU, onProgress)
	}
	if IsModelFamilyInstalled(kokoroDir, fam, useGPU) {
		return nil
	}

	var task archiveTask
	switch fam {
	case ModelSupertonic:
		task = archiveTask{
			name:           ModelSupertonic,
			url:            SupertonicArchiveURL,
			expectedSHA256: SupertonicArchiveSHA256,
			expectedSize:   SupertonicArchiveSize,
		}
	case ModelKittenMini:
		task = archiveTask{
			name:           ModelKittenMini,
			url:            KittenMiniArchiveURL,
			expectedSHA256: KittenMiniArchiveSHA256,
			expectedSize:   KittenMiniArchiveSize,
		}
	case ModelKittenNano:
		task = archiveTask{
			name:           ModelKittenNano,
			url:            KittenNanoArchiveURL,
			expectedSHA256: KittenNanoArchiveSHA256,
			expectedSize:   KittenNanoArchiveSize,
		}
	case ModelPiper:
		task = archiveTask{
			name:           ModelPiper,
			url:            PiperArchiveURL,
			expectedSHA256: PiperArchiveSHA256,
			expectedSize:   PiperArchiveSize,
		}
	default:
		return fmt.Errorf("unsupported model family: %s", family)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create model directory %s: %w", dir, err)
	}

	var downloadedBytes atomic.Int64
	var totalAtomic atomic.Int64
	totalAtomic.Store(task.expectedSize)

	err := downloadAndExtractTarBz2(context.Background(), task, dir, &totalAtomic, func(delta int64) {
		cur := downloadedBytes.Add(delta)
		tot := max(cur, totalAtomic.Load())
		if onProgress != nil {
			onProgress(cur, tot)
		}
	})
	if err != nil {
		return err
	}
	if !IsModelFamilyInstalled(kokoroDir, fam, useGPU) {
		return fmt.Errorf("model family %s extraction finished but required files are missing in %s", fam, dir)
	}
	return nil
}



