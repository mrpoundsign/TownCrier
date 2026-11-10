package tts

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrChecksumMismatch = errors.New("downloaded file checksum mismatch")

	// KokoroFP32ArchiveURL is the upstream sherpa-onnx release archive containing model.onnx (FP32 for DirectML GPU) and shared assets.
	// SHA-256 digests are resolved dynamically from GitHub's Release API and recorded in <modelDir>/.manifest.json on disk.
	KokoroFP32ArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-multi-lang-v1_0.tar.bz2"
	KokoroFP32ArchiveSHA256       = ""
	KokoroFP32ArchiveSize   int64 = 349886476

	// KokoroINT8ArchiveURL is the upstream sherpa-onnx release archive containing model.int8.onnx (INT8 for CPU SIMD fallback).
	KokoroINT8ArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-int8-multi-lang-v1_0.tar.bz2"
	KokoroINT8ArchiveSHA256       = ""
	KokoroINT8ArchiveSize   int64 = 132353410

	// SupertonicArchiveURL is the upstream sherpa-onnx Supertonic INT8 model archive (10 voices, 44.1 kHz, ultra-fast CPU).
	SupertonicArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/sherpa-onnx-supertonic-tts-int8-2026-03-06.tar.bz2"
	SupertonicArchiveSHA256       = ""
	SupertonicArchiveSize   int64 = 84692981

	// KittenMiniArchiveURL is the upstream sherpa-onnx Kitten v0.8 Mini model archive (8 voices, 24 kHz).
	KittenMiniArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kitten-mini-en-v0_8.tar.bz2"
	KittenMiniArchiveSHA256       = ""
	KittenMiniArchiveSize   int64 = 67547594

	// KittenNanoArchiveURL is the upstream sherpa-onnx Kitten v0.8 Nano INT8 model archive (8 voices, 24 kHz, ultra-low RAM).
	KittenNanoArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kitten-nano-en-v0_8-int8.tar.bz2"
	KittenNanoArchiveSHA256       = ""
	KittenNanoArchiveSize   int64 = 31220690

	// PiperArchiveURL is the upstream sherpa-onnx Piper LibriTTS-High INT8 model archive (904 speakers, 22.05 kHz).
	PiperArchiveURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_US-libritts-high-int8.tar.bz2"
	PiperArchiveSHA256       = ""
	PiperArchiveSize   int64 = 36406088

	// TTSModelsReleaseAPIURL is the GitHub Releases API endpoint for upstream sherpa-onnx TTS model assets.
	TTSModelsReleaseAPIURL = "https://api.github.com/repos/k2-fsa/sherpa-onnx/releases/tags/tts-models"

	manifestMu sync.Mutex
)

// DefaultModelDir resolves the target directory for Kokoro model files.
// It places models in the user's local cache/app data directory (%LOCALAPPDATA% on Windows,
// ~/.cache on Linux) to prevent multi-hundred-megabyte binary weights from bloating roaming profiles.
// If models already exist in roaming (%APPDATA%) or legacy paths, it automatically migrates them to local cache.
func DefaultModelDir() string {
	var targetDir string
	if cacheDir, err := os.UserCacheDir(); err == nil {
		targetDir = filepath.Join(cacheDir, "com.github.mrpoundsign.towncrier", "models", "kokoro-en")
	} else if configDir, err := os.UserConfigDir(); err == nil {
		targetDir = filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "models", "kokoro-en")
	} else {
		targetDir = filepath.Join("models", "kokoro-en")
	}

	if IsAnyModelFamilyInstalled(targetDir) {
		return targetDir
	}

	// Host OS roaming data directory: check for existing installs or legacy paths and migrate to local cache
	if configDir, err := os.UserConfigDir(); err == nil {
		for _, legacyKokoro := range []string{
			filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "models", "kokoro-en"),
			filepath.Join(configDir, "TownCrier", "models", "kokoro-en"),
			filepath.Join(configDir, "com.github.mrpoundsign.octochorpebot", "models", "kokoro-en"),
			filepath.Join(configDir, "OctochorpeBot", "models", "kokoro-en"),
		} {
			if legacyKokoro != targetDir && IsAnyModelFamilyInstalled(legacyKokoro) {
				if migrateModelsDir(filepath.Dir(legacyKokoro), filepath.Dir(targetDir)) {
					return targetDir
				}
				// If directory-level migration failed (e.g. across volumes), return legacy path as fallback
				return legacyKokoro
			}
		}
	}

	return targetDir
}

// DefaultSharedCacheDir resolves the shared local cache directory (%LOCALAPPDATA%\com.github.mrpoundsign.towncrier\cache
// on Windows, ~/.cache/com.github.mrpoundsign.towncrier/cache on Linux) so companion apps like Hearsay and TownCrier
// can share frequency-gated audio and translation caches.
func DefaultSharedCacheDir() string {
	if cacheDir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cacheDir, "com.github.mrpoundsign.towncrier", "cache")
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(configDir, "com.github.mrpoundsign.towncrier", "cache")
	}
	return "cache"
}

// migrateModelsDir moves the contents of legacyModelsDir into targetModelsDir.
func migrateModelsDir(legacyModelsDir, targetModelsDir string) bool {
	if legacyModelsDir == targetModelsDir {
		return true
	}
	if err := os.MkdirAll(targetModelsDir, 0755); err != nil {
		return false
	}

	// First attempt atomic rename of the entire models directory if target is empty
	targetEntries, err := os.ReadDir(targetModelsDir)
	if err == nil && len(targetEntries) == 0 {
		_ = os.Remove(targetModelsDir) // Remove newly created empty dir so os.Rename can place legacyModelsDir
		if err := os.Rename(legacyModelsDir, targetModelsDir); err == nil {
			return true
		}
		// Re-create targetModelsDir if rename failed
		_ = os.MkdirAll(targetModelsDir, 0755)
	}

	// Move individual model family directories
	entries, err := os.ReadDir(legacyModelsDir)
	if err != nil {
		return false
	}

	success := true
	for _, entry := range entries {
		src := filepath.Join(legacyModelsDir, entry.Name())
		dst := filepath.Join(targetModelsDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			success = false
		}
	}

	// If all entries were moved successfully, remove the empty legacy models dir
	if success {
		_ = os.Remove(legacyModelsDir)
	}

	return success
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

// IsKokoroInstalledForMode checks if Kokoro model assets (both FP32 GPU and INT8 CPU fallback models + shared assets) are installed.
func IsKokoroInstalledForMode(dir string, _ bool) bool {
	return IsModelInstalled(dir)
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

// DownloadKokoroModelForMode downloads only the required Kokoro archive(s) for the requested execution mode.
// When useGPU is false, only the 98.5 MB INT8 archive (model.int8.onnx + shared assets) is downloaded.
// When useGPU is true, the FP32 archive (model.onnx + shared assets) and INT8 fallback are downloaded.
func DownloadKokoroModelForMode(destDir string, useGPU bool, onProgress DownloadProgressFunc) error {
	return DownloadKokoroModelForModeWithContext(context.Background(), destDir, useGPU, onProgress)
}

// DownloadKokoroModelForModeWithContext downloads Kokoro archives with context cancellation support.
// It ensures both the FP32 GPU model and INT8 CPU fallback model are installed in a unified download.
func DownloadKokoroModelForModeWithContext(parentCtx context.Context, destDir string, useGPU bool, onProgress DownloadProgressFunc) error {
	if IsModelInstalled(destDir) {
		return nil
	}
	return downloadKokoroModelWithContext(parentCtx, destDir, useGPU, false, onProgress)
}

func downloadKokoroModelWithContext(parentCtx context.Context, destDir string, _ bool, force bool, onProgress DownloadProgressFunc) error {
	if !force && IsModelInstalled(destDir) {
		return nil
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

	sharedPresent := !force && hasKokoroSharedAssets(destDir)
	needFP32 := force || !fileExists(filepath.Join(destDir, "model.onnx")) || !sharedPresent
	needINT8 := force || !fileExists(filepath.Join(destDir, "model.int8.onnx")) || !sharedPresent

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

	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)
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
		if onProgress == nil {
			downloadedBytes.Add(delta)
			return
		}
		progMu.Lock()
		cur := downloadedBytes.Add(delta)
		tot := max(cur, totalAtomic.Load())
		onProgress(cur, tot)
		progMu.Unlock()
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

	computed := hex.EncodeToString(hasher.Sum(nil))
	expected := task.expectedSHA256
	if expected == "" {
		expected = fetchGitHubReleaseAssetDigest(ctx, task.url)
	}
	if expected != "" && !strings.EqualFold(computed, expected) {
		return fmt.Errorf("%w (%s): expected %s, got %s", ErrChecksumMismatch, task.name, expected, computed)
	}

	for _, sf := range staged {
		if renErr := os.Rename(sf.stagePath, sf.targetPath); renErr != nil {
			return fmt.Errorf("rename extracted file %s: %w", sf.targetPath, renErr)
		}
	}

	recordModelManifestHash(destDir, path.Base(task.url), computed)
	return nil
}

// ModelManifest records the installed archive SHA-256 digests and timestamp in <modelDir>/.manifest.json.
type ModelManifest struct {
	Archives    map[string]string `json:"archives"`
	InstalledAt string            `json:"installed_at"`
}

func recordModelManifestHash(destDir, archiveName, sha256Hex string) {
	if destDir == "" || archiveName == "" || sha256Hex == "" {
		return
	}
	manifestMu.Lock()
	defer manifestMu.Unlock()

	manifestPath := filepath.Join(destDir, ".manifest.json")
	var m ModelManifest
	if data, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	if m.Archives == nil {
		m.Archives = make(map[string]string)
	}
	m.Archives[archiveName] = strings.ToLower(sha256Hex)
	m.InstalledAt = time.Now().UTC().Format(time.RFC3339)

	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(manifestPath, data, 0644)
	}
}

// ReadModelManifest loads the recorded .manifest.json from a model family directory.
func ReadModelManifest(dir string) (*ModelManifest, error) {
	if dir == "" {
		return nil, errors.New("empty model directory")
	}
	manifestMu.Lock()
	defer manifestMu.Unlock()

	manifestPath := filepath.Join(dir, ".manifest.json")
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- model directory path is managed by application configuration
	if err != nil {
		return nil, err
	}
	var m ModelManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type githubReleaseAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func fetchGitHubReleaseAssets(ctx context.Context) ([]githubReleaseAsset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TTSModelsReleaseAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub release API returned HTTP %d", resp.StatusCode)
	}
	var release struct {
		Assets []githubReleaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&release); err != nil {
		return nil, err
	}
	for i := range release.Assets {
		release.Assets[i].Digest = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(release.Assets[i].Digest)), "sha256:")
	}
	return release.Assets, nil
}

// fetchGitHubReleaseAssetDigest queries GitHub's official release metadata for k2-fsa/sherpa-onnx
// to retrieve the live server-computed SHA-256 digest for an asset on the tts-models release tag.
func fetchGitHubReleaseAssetDigest(ctx context.Context, archiveURL string) string {
	const prefix = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/"
	if !strings.HasPrefix(archiveURL, prefix) {
		return ""
	}
	assetName := path.Base(archiveURL)
	if assetName == "" || assetName == "." || assetName == "/" {
		return ""
	}
	assets, err := fetchGitHubReleaseAssets(ctx)
	if err != nil {
		return ""
	}
	for _, a := range assets {
		if a.Name == assetName {
			return a.Digest
		}
	}
	return ""
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
		return IsKokoroInstalledForMode(dir, useGPU)
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
	return DownloadModelFamilyWithContext(context.Background(), kokoroDir, family, useGPU, onProgress)
}

// DownloadModelFamilyWithContext downloads and extracts the archive with context cancellation support.
func DownloadModelFamilyWithContext(ctx context.Context, kokoroDir string, family string, useGPU bool, onProgress DownloadProgressFunc) error {
	return downloadModelFamilyWithContext(ctx, kokoroDir, family, useGPU, false, onProgress)
}

// ForceUpdateModelFamily downloads and extracts the latest archive(s) for the requested model family,
// overwriting any existing installation on disk.
func ForceUpdateModelFamily(kokoroDir string, family string, useGPU bool, onProgress DownloadProgressFunc) error {
	return ForceUpdateModelFamilyWithContext(context.Background(), kokoroDir, family, useGPU, onProgress)
}

// ForceUpdateModelFamilyWithContext downloads and extracts the latest archive(s) for the requested model family
// with context cancellation support, overwriting any existing installation on disk.
func ForceUpdateModelFamilyWithContext(ctx context.Context, kokoroDir string, family string, useGPU bool, onProgress DownloadProgressFunc) error {
	return downloadModelFamilyWithContext(ctx, kokoroDir, family, useGPU, true, onProgress)
}

func familyExpectedArchives(family string) ([]archiveTask, error) {
	fam := strings.ToLower(strings.TrimSpace(family))
	switch fam {
	case "", ModelKokoro, ModelKokoroIntl:
		return []archiveTask{
			{
				name:           "fp32",
				url:            KokoroFP32ArchiveURL,
				expectedSHA256: KokoroFP32ArchiveSHA256,
				expectedSize:   KokoroFP32ArchiveSize,
			},
			{
				name:           "int8",
				url:            KokoroINT8ArchiveURL,
				expectedSHA256: KokoroINT8ArchiveSHA256,
				expectedSize:   KokoroINT8ArchiveSize,
				onlyFile:       "model.int8.onnx",
			},
		}, nil
	case ModelSupertonic:
		return []archiveTask{{
			name:           ModelSupertonic,
			url:            SupertonicArchiveURL,
			expectedSHA256: SupertonicArchiveSHA256,
			expectedSize:   SupertonicArchiveSize,
		}}, nil
	case ModelKittenMini:
		return []archiveTask{{
			name:           ModelKittenMini,
			url:            KittenMiniArchiveURL,
			expectedSHA256: KittenMiniArchiveSHA256,
			expectedSize:   KittenMiniArchiveSize,
		}}, nil
	case ModelKittenNano:
		return []archiveTask{{
			name:           ModelKittenNano,
			url:            KittenNanoArchiveURL,
			expectedSHA256: KittenNanoArchiveSHA256,
			expectedSize:   KittenNanoArchiveSize,
		}}, nil
	case ModelPiper:
		return []archiveTask{{
			name:           ModelPiper,
			url:            PiperArchiveURL,
			expectedSHA256: PiperArchiveSHA256,
			expectedSize:   PiperArchiveSize,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported model family: %s", family)
	}
}

func downloadModelFamilyWithContext(ctx context.Context, kokoroDir string, family string, useGPU bool, force bool, onProgress DownloadProgressFunc) error {
	fam := strings.ToLower(strings.TrimSpace(family))
	dir := ModelFamilyDir(kokoroDir, fam)
	if fam == "" || fam == ModelKokoro || fam == ModelKokoroIntl {
		return downloadKokoroModelWithContext(ctx, dir, useGPU, force, onProgress)
	}
	if !force && IsModelFamilyInstalled(kokoroDir, fam, useGPU) {
		return nil
	}

	tasks, err := familyExpectedArchives(fam)
	if err != nil || len(tasks) == 0 {
		return fmt.Errorf("unsupported model family: %s", family)
	}
	task := tasks[0]

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create model directory %s: %w", dir, err)
	}

	var downloadedBytes atomic.Int64
	var totalAtomic atomic.Int64
	totalAtomic.Store(task.expectedSize)

	if ctx == nil {
		ctx = context.Background()
	}
	err = downloadAndExtractTarBz2(ctx, task, dir, &totalAtomic, func(delta int64) {
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

// ModelUpdateStatus describes the installation and update state of a voice model family.
type ModelUpdateStatus struct {
	Family          string
	Installed       bool
	UpdateAvailable bool
	Reason          string
	InstalledAt     string
	LocalDigest     string
	RemoteDigest    string
}

// InspectLocalModelStatus performs an instant offline check of the installed files and .manifest.json
// for a model family to detect legacy versions (e.g. Kokoro v0.19), partial installs, or archive version changes.
func InspectLocalModelStatus(kokoroDir string, family string, useGPU bool) ModelUpdateStatus {
	fam := strings.ToLower(strings.TrimSpace(family))
	if fam == "" {
		fam = ModelKokoro
	}
	dir := ModelFamilyDir(kokoroDir, fam)
	installed := IsModelFamilyInstalled(kokoroDir, fam, useGPU)

	if !installed && (fam == ModelKokoro || fam == ModelKokoroIntl) {
		if isLegacyKokoroV019Dir(dir) {
			return ModelUpdateStatus{
				Family:          fam,
				Installed:       true,
				UpdateAvailable: true,
				Reason:          "Legacy Kokoro v0.19 installed (v1.0 update available)",
			}
		}
		if IsCPUModelInstalled(dir) || IsGPUModelInstalled(dir) {
			return ModelUpdateStatus{
				Family:          fam,
				Installed:       true,
				UpdateAvailable: true,
				Reason:          "Incomplete CPU/GPU Kokoro model pack",
			}
		}
	}

	if !installed {
		return ModelUpdateStatus{Family: fam, Installed: false}
	}

	status := ModelUpdateStatus{
		Family:    fam,
		Installed: true,
	}

	manifest, err := ReadModelManifest(dir)
	if err != nil || manifest == nil || len(manifest.Archives) == 0 {
		return status
	}
	status.InstalledAt = manifest.InstalledAt

	tasks, err := familyExpectedArchives(fam)
	if err != nil {
		return status
	}
	for _, t := range tasks {
		archiveName := path.Base(t.url)
		localHash, ok := manifest.Archives[archiveName]
		if !ok || localHash == "" {
			status.UpdateAvailable = true
			status.Reason = fmt.Sprintf("Newer archive version configured (%s)", archiveName)
			return status
		}
		if status.LocalDigest == "" {
			status.LocalDigest = localHash
		}
		if t.expectedSHA256 != "" && !strings.EqualFold(localHash, t.expectedSHA256) {
			status.UpdateAvailable = true
			status.RemoteDigest = strings.ToLower(t.expectedSHA256)
			status.Reason = "Updated checksum for " + archiveName
			return status
		}
	}
	return status
}

// InspectAllLocalModelStatuses returns the offline ModelUpdateStatus for every supported model family.
func InspectAllLocalModelStatuses(kokoroDir string, useGPU bool) map[string]ModelUpdateStatus {
	out := make(map[string]ModelUpdateStatus, len(AllModelFamilies))
	for _, spec := range AllModelFamilies {
		out[spec.ID] = InspectLocalModelStatus(kokoroDir, spec.ID, useGPU)
	}
	return out
}

// CheckModelUpdates queries the upstream GitHub Releases API (k2-fsa/sherpa-onnx tts-models)
// and compares remote archive SHA-256 digests against local .manifest.json records for all installed model packs.
func CheckModelUpdates(ctx context.Context, kokoroDir string, useGPU bool) (map[string]ModelUpdateStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	statuses := InspectAllLocalModelStatuses(kokoroDir, useGPU)

	assets, err := fetchGitHubReleaseAssets(ctx)
	if err != nil {
		return statuses, err
	}
	remoteByName := make(map[string]githubReleaseAsset, len(assets))
	for _, a := range assets {
		remoteByName[a.Name] = a
	}

	for _, spec := range AllModelFamilies {
		st := statuses[spec.ID]
		if !st.Installed || st.UpdateAvailable {
			continue
		}
		dir := ModelFamilyDir(kokoroDir, spec.ID)
		manifest, _ := ReadModelManifest(dir)
		tasks, err := familyExpectedArchives(spec.ID)
		if err != nil {
			continue
		}

		for _, t := range tasks {
			archiveName := path.Base(t.url)
			remoteAsset, foundRemote := remoteByName[archiveName]
			remoteDigest := strings.ToLower(t.expectedSHA256)
			if foundRemote && remoteAsset.Digest != "" {
				remoteDigest = remoteAsset.Digest
			}

			var localDigest string
			if manifest != nil && manifest.Archives != nil {
				localDigest = strings.ToLower(strings.TrimSpace(manifest.Archives[archiveName]))
			}
			if st.LocalDigest == "" && localDigest != "" {
				st.LocalDigest = localDigest
			}
			if st.RemoteDigest == "" && remoteDigest != "" {
				st.RemoteDigest = remoteDigest
			}

			if localDigest == "" {
				st.UpdateAvailable = true
				st.Reason = "Installed before version tracking (update available to verify latest weights)"
				break
			}
			if remoteDigest != "" && !strings.EqualFold(localDigest, remoteDigest) {
				st.UpdateAvailable = true
				st.RemoteDigest = remoteDigest
				st.Reason = fmt.Sprintf("Upstream model weights updated (%s)", archiveName)
				break
			}
		}
		statuses[spec.ID] = st
	}

	// Keep KokoroIntl update status synchronized with Kokoro since they share the same model directory
	if kokoroSt, ok := statuses[ModelKokoro]; ok {
		if intlSt, okIntl := statuses[ModelKokoroIntl]; okIntl && intlSt.Installed {
			intlSt.UpdateAvailable = kokoroSt.UpdateAvailable
			intlSt.Reason = kokoroSt.Reason
			intlSt.LocalDigest = kokoroSt.LocalDigest
			intlSt.RemoteDigest = kokoroSt.RemoteDigest
			statuses[ModelKokoroIntl] = intlSt
		}
	}

	return statuses, nil
}

// OutdatedModelFamilies returns the deduplicated list of model family IDs in display order
// that have UpdateAvailable == true (collapsing ModelKokoroIntl into ModelKokoro since they share weights).
func OutdatedModelFamilies(statuses map[string]ModelUpdateStatus) []string {
	var out []string
	seenKokoro := false
	for _, spec := range AllModelFamilies {
		st, ok := statuses[spec.ID]
		if !ok || !st.UpdateAvailable {
			continue
		}
		if spec.ID == ModelKokoro || spec.ID == ModelKokoroIntl {
			if seenKokoro {
				continue
			}
			seenKokoro = true
			out = append(out, ModelKokoro)
			continue
		}
		out = append(out, spec.ID)
	}
	return out
}

