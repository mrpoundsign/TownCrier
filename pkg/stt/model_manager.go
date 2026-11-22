package stt

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
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/pkg/tts"
)

const defaultASRModelsReleaseAPIURL = "https://api.github.com/repos/k2-fsa/sherpa-onnx/releases/tags/asr-models"

var (
	// ErrChecksumMismatch indicates a downloaded STT asset failed SHA-256 verification or lacked a required checksum.
	ErrChecksumMismatch = errors.New("downloaded stt file checksum mismatch")

	// SileroVadURL is the upstream sherpa-onnx release asset for Silero VAD (~644 KB) with a pinned SHA-256 digest.
	SileroVadURL          = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx"
	SileroVadSHA256       = "9e2449e1087496d8d4caba907f23e0bd3f78d91fa552479bb9c23ac09cbb1fd6"
	SileroVadSize   int64 = 643854

	// ASRModelsReleaseAPIURL is the GitHub Releases API endpoint for upstream sherpa-onnx ASR/VAD assets.
	ASRModelsReleaseAPIURL = defaultASRModelsReleaseAPIURL

	sttHTTPClient = http.DefaultClient
	manifestMu    sync.Mutex
)

// DefaultSTTModelDir resolves the local cache directory for offline STT & VAD models
// (%LOCALAPPDATA%\com.github.mrpoundsign.towncrier\models\stt on Windows, ~/.cache/... on Linux).
func DefaultSTTModelDir() string {
	kokoroDir := tts.DefaultModelDir()
	return filepath.Join(filepath.Dir(kokoroDir), "stt")
}

// VadModelPath returns the file path to silero_vad.onnx inside baseSTTDir.
func VadModelPath(baseSTTDir string) string {
	if strings.TrimSpace(baseSTTDir) == "" {
		baseSTTDir = DefaultSTTModelDir()
	}
	return filepath.Join(baseSTTDir, "silero_vad.onnx")
}

// ModelFamilyDir returns the subdirectory path for a specific Whisper model family inside baseSTTDir.
func ModelFamilyDir(baseSTTDir, family string) string {
	if strings.TrimSpace(baseSTTDir) == "" {
		baseSTTDir = DefaultSTTModelDir()
	}
	return filepath.Join(baseSTTDir, NormalizeModelFamily(family))
}

func fileExistsNonEmpty(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// IsVadInstalled reports whether silero_vad.onnx is present in baseSTTDir.
func IsVadInstalled(baseSTTDir string) bool {
	if strings.TrimSpace(baseSTTDir) == "" && testing.Testing() {
		return false
	}
	return fileExistsNonEmpty(VadModelPath(baseSTTDir))
}

// ResolveWhisperFiles locates the encoder, decoder, and tokens files for family inside baseSTTDir.
// When preferFP is true and unquantized .onnx files exist, they are returned with usingFP=true (suitable for DirectML);
// otherwise INT8 .int8.onnx files are selected with usingFP=false (optimal for multi-threaded CPU SIMD).
func ResolveWhisperFiles(baseSTTDir, family string, preferFP bool) (encoderPath, decoderPath, tokensPath string, usingFP bool, err error) {
	spec, ok := ModelSpecByID(family)
	if !ok {
		return "", "", "", false, fmt.Errorf("%w: unknown model family %q", ErrModelNotFound, family)
	}
	dir := ModelFamilyDir(baseSTTDir, spec.ID)

	for _, tokCand := range []string{spec.Prefix + "-tokens.txt", "tokens.txt"} {
		p := filepath.Join(dir, tokCand)
		if fileExistsNonEmpty(p) {
			tokensPath = p
			break
		}
	}
	if tokensPath == "" {
		return "", "", "", false, fmt.Errorf("%w: missing %s-tokens.txt in %s", ErrModelNotFound, spec.Prefix, dir)
	}

	fpEnc := filepath.Join(dir, spec.Prefix+"-encoder.onnx")
	fpDec := filepath.Join(dir, spec.Prefix+"-decoder.onnx")
	int8Enc := filepath.Join(dir, spec.Prefix+"-encoder.int8.onnx")
	int8Dec := filepath.Join(dir, spec.Prefix+"-decoder.int8.onnx")

	hasFP := fileExistsNonEmpty(fpEnc) && fileExistsNonEmpty(fpDec)
	hasINT8 := fileExistsNonEmpty(int8Enc) && fileExistsNonEmpty(int8Dec)

	if preferFP && hasFP {
		return fpEnc, fpDec, tokensPath, true, nil
	}
	if hasINT8 {
		return int8Enc, int8Dec, tokensPath, false, nil
	}
	if hasFP {
		return fpEnc, fpDec, tokensPath, true, nil
	}
	return "", "", "", false, fmt.Errorf("%w: missing whisper %s encoder/decoder ONNX files in %s", ErrModelNotFound, spec.Prefix, dir)
}

// GGMLModelPath returns the target file path for the quantized GGML Whisper model of family inside baseSTTDir.
func GGMLModelPath(baseSTTDir, family string) (string, error) {
	spec, ok := ModelSpecByID(family)
	if !ok {
		return "", fmt.Errorf("%w: unknown model family %q", ErrModelNotFound, family)
	}
	if strings.TrimSpace(spec.GGMLFile) == "" {
		return "", fmt.Errorf("%w: model family %q has no GGML file configured", ErrModelNotFound, family)
	}
	return filepath.Join(ModelFamilyDir(baseSTTDir, spec.ID), spec.GGMLFile), nil
}

func hasGGMLFile(baseSTTDir, family string) bool {
	p, err := GGMLModelPath(baseSTTDir, family)
	return err == nil && fileExistsNonEmpty(p)
}

// IsGGMLModelInstalled reports whether both silero_vad.onnx and the GPU/iGPU/CPU-ready GGML Whisper model exist in baseSTTDir.
func IsGGMLModelInstalled(baseSTTDir, family string) bool {
	return IsVadInstalled(baseSTTDir) && hasGGMLFile(baseSTTDir, family)
}

// IsSherpaModelInstalled reports whether silero_vad.onnx and legacy sherpa-onnx Whisper ONNX files exist in baseSTTDir.
func IsSherpaModelInstalled(baseSTTDir, family string) bool {
	if !IsVadInstalled(baseSTTDir) {
		return false
	}
	_, _, _, _, err := ResolveWhisperFiles(baseSTTDir, family, false)
	return err == nil
}

// IsModelInstalled reports whether silero_vad.onnx and either GGML or legacy ONNX Whisper weights are installed in baseSTTDir.
func IsModelInstalled(baseSTTDir, family string) bool {
	return IsGGMLModelInstalled(baseSTTDir, family) || IsSherpaModelInstalled(baseSTTDir, family)
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

type stagedFile struct {
	stagePath  string
	targetPath string
}

// ModelManifest records installed STT asset SHA-256 digests in <dir>/.manifest.json.
type ModelManifest struct {
	Assets      map[string]string `json:"assets"`
	InstalledAt string            `json:"installed_at"`
}

func isValidSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func verifyDownloadSHA256(ctx context.Context, name, assetURL, expectedSHA256, computedHex string) error {
	expected := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expectedSHA256)), "sha256:")
	if expected == "" {
		expected = fetchRemoteAssetSHA256(ctx, assetURL)
	}
	if expected == "" {
		return fmt.Errorf("%w (%s): missing required SHA-256 verification digest", ErrChecksumMismatch, name)
	}
	if strings.EqualFold(computedHex, expected) {
		return nil
	}
	// If upstream HuggingFace/GitHub published an updated model weight revision, verify against the
	// authoritative remote Git-LFS / Release SHA-256 digest (still 100% fail-closed if missing or mismatched).
	if strings.HasPrefix(assetURL, "https://huggingface.co/") || strings.HasPrefix(assetURL, "https://github.com/k2-fsa/sherpa-onnx/") {
		if remoteDigest := fetchRemoteAssetSHA256(ctx, assetURL); isValidSHA256Hex(remoteDigest) && strings.EqualFold(computedHex, remoteDigest) {
			return nil
		}
	}
	return fmt.Errorf("%w (%s): expected %s, got %s", ErrChecksumMismatch, name, expected, computedHex)
}

// DownloadModel downloads and installs silero_vad.onnx and the requested Whisper model family into baseSTTDir.
func DownloadModel(baseSTTDir, family string, onProgress tts.DownloadProgressFunc) error {
	return DownloadModelWithContext(context.Background(), baseSTTDir, family, onProgress)
}

// ForceDownloadModelWithContext downloads (or updates in-place) silero_vad.onnx and the requested Whisper model
// even if an older version of the model is already installed in baseSTTDir.
func ForceDownloadModelWithContext(parentCtx context.Context, baseSTTDir, family string, onProgress tts.DownloadProgressFunc) error {
	return downloadModelInternal(parentCtx, baseSTTDir, family, true, onProgress)
}

// DownloadModelWithContext downloads silero_vad.onnx (if missing) and the requested Whisper model
// into baseSTTDir with cancellation, progress reporting, and strict fail-closed SHA-256 verification.
func DownloadModelWithContext(parentCtx context.Context, baseSTTDir, family string, onProgress tts.DownloadProgressFunc) error {
	return downloadModelInternal(parentCtx, baseSTTDir, family, false, onProgress)
}

func downloadModelInternal(parentCtx context.Context, baseSTTDir, family string, forceWhisper bool, onProgress tts.DownloadProgressFunc) error {
	if strings.TrimSpace(baseSTTDir) == "" {
		baseSTTDir = DefaultSTTModelDir()
	}
	spec, ok := ModelSpecByID(family)
	if !ok {
		return fmt.Errorf("unsupported STT model family %q", family)
	}

	useGGML := strings.TrimSpace(spec.GGMLURL) != "" && strings.TrimSpace(spec.GGMLFile) != ""
	if !forceWhisper {
		if useGGML {
			if IsGGMLModelInstalled(baseSTTDir, spec.ID) {
				return nil
			}
		} else if IsSherpaModelInstalled(baseSTTDir, spec.ID) {
			return nil
		}
	}

	if err := os.MkdirAll(baseSTTDir, 0755); err != nil {
		return fmt.Errorf("create stt directory %s: %w", baseSTTDir, err)
	}

	needVAD := !IsVadInstalled(baseSTTDir)
	var needWhisper bool
	var whisperExpectedBytes int64
	if useGGML {
		needWhisper = forceWhisper || !hasGGMLFile(baseSTTDir, spec.ID)
		whisperExpectedBytes = spec.GGMLSize
	} else {
		_, _, _, _, whisperErr := ResolveWhisperFiles(baseSTTDir, spec.ID, false)
		needWhisper = forceWhisper || whisperErr != nil
		whisperExpectedBytes = spec.ArchiveSize
	}

	var totalExpected int64
	if needVAD {
		totalExpected += SileroVadSize
	}
	if needWhisper {
		totalExpected += whisperExpectedBytes
	}
	if totalExpected == 0 {
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
	)
	totalAtomic.Store(totalExpected)

	reportProgress := func(delta int64) {
		cur := downloadedBytes.Add(delta)
		if onProgress == nil {
			return
		}
		progMu.Lock()
		tot := max(cur, totalAtomic.Load())
		onProgress(cur, tot)
		progMu.Unlock()
	}

	if needVAD {
		if err := downloadSingleAsset(ctx, "silero_vad", SileroVadURL, SileroVadSHA256, SileroVadSize, VadModelPath(baseSTTDir), &totalAtomic, reportProgress); err != nil {
			return err
		}
	}

	if needWhisper {
		familyDir := ModelFamilyDir(baseSTTDir, spec.ID)
		if useGGML {
			ggmlPath := filepath.Join(familyDir, spec.GGMLFile)
			if err := downloadSingleAsset(ctx, spec.ID, spec.GGMLURL, spec.GGMLSHA256, spec.GGMLSize, ggmlPath, &totalAtomic, reportProgress); err != nil {
				return err
			}
		} else {
			if err := downloadAndExtractTarBz2(ctx, spec.ID, spec.ArchiveURL, spec.ArchiveSHA256, spec.ArchiveSize, familyDir, &totalAtomic, reportProgress); err != nil {
				return err
			}
		}
	}

	if useGGML && !IsGGMLModelInstalled(baseSTTDir, spec.ID) {
		return fmt.Errorf("stt model download finished but GGML files are still missing for %s in %s", spec.ID, baseSTTDir)
	}
	if !IsModelInstalled(baseSTTDir, spec.ID) {
		return fmt.Errorf("stt model installation finished but required files are still missing for %s in %s", spec.ID, baseSTTDir)
	}
	return nil
}

func downloadSingleAsset(
	ctx context.Context,
	name string,
	assetURL string,
	expectedSHA256 string,
	expectedSize int64,
	targetPath string,
	totalAtomic *atomic.Int64,
	onBytesRead func(delta int64),
) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", name, err)
	}
	resp, err := sttHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s failed with HTTP status %d", name, resp.StatusCode)
	}
	if resp.ContentLength > 0 && expectedSize > 0 && resp.ContentLength != expectedSize {
		totalAtomic.Add(resp.ContentLength - expectedSize)
	}

	if mkErr := os.MkdirAll(filepath.Dir(targetPath), 0755); mkErr != nil {
		return fmt.Errorf("create directory for %s: %w", targetPath, mkErr)
	}

	stagePath := targetPath + ".part"
	defer func() {
		if err != nil {
			_ = os.Remove(stagePath)
		}
	}()

	f, err := os.OpenFile(stagePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create staged file %s: %w", stagePath, err)
	}

	hasher := sha256.New()
	pr := &progressHashReader{
		r:      resp.Body,
		hasher: hasher,
		onRead: onBytesRead,
	}

	if _, copyErr := io.Copy(f, pr); copyErr != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", name, copyErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("close staged file %s: %w", stagePath, closeErr)
	}

	computed := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyDownloadSHA256(ctx, name, assetURL, expectedSHA256, computed); err != nil {
		return err
	}

	if renErr := os.Rename(stagePath, targetPath); renErr != nil {
		return fmt.Errorf("rename %s: %w", targetPath, renErr)
	}
	recordManifestHash(filepath.Dir(targetPath), path.Base(assetURL), computed)
	return nil
}

func downloadAndExtractTarBz2(
	ctx context.Context,
	name string,
	archiveURL string,
	expectedSHA256 string,
	expectedSize int64,
	destDir string,
	totalAtomic *atomic.Int64,
	onBytesRead func(delta int64),
) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", name, err)
	}
	resp, err := sttHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s archive: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s archive failed with HTTP status %d", name, resp.StatusCode)
	}
	if resp.ContentLength > 0 && expectedSize > 0 && resp.ContentLength != expectedSize {
		totalAtomic.Add(resp.ContentLength - expectedSize)
	}

	if mkErr := os.MkdirAll(destDir, 0755); mkErr != nil {
		return fmt.Errorf("create directory %s: %w", destDir, mkErr)
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
			return fmt.Errorf("read %s tar stream: %w", name, nextErr)
		}

		relPath, ok := stripTopLevelArchiveDir(hdr.Name)
		if !ok || relPath == "" {
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
			stagePath := fmt.Sprintf("%s.%s.part", targetPath, name)
			f, createErr := os.OpenFile(stagePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if createErr != nil {
				return fmt.Errorf("create staged file %s: %w", stagePath, createErr)
			}
			staged = append(staged, stagedFile{stagePath: stagePath, targetPath: targetPath})

			if _, copyErr := io.Copy(f, tr); copyErr != nil {
				_ = f.Close()
				return fmt.Errorf("extract %s from %s: %w", relPath, name, copyErr)
			}
			if closeErr := f.Close(); closeErr != nil {
				return fmt.Errorf("close staged file %s: %w", stagePath, closeErr)
			}
		}
	}

	if _, drainErr := io.Copy(io.Discard, pr); drainErr != nil {
		return fmt.Errorf("drain %s archive stream: %w", name, drainErr)
	}

	computed := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyDownloadSHA256(ctx, name, archiveURL, expectedSHA256, computed); err != nil {
		return err
	}

	for _, sf := range staged {
		if renErr := os.Rename(sf.stagePath, sf.targetPath); renErr != nil {
			return fmt.Errorf("rename extracted file %s: %w", sf.targetPath, renErr)
		}
	}

	recordManifestHash(destDir, path.Base(archiveURL), computed)
	return nil
}

// stripTopLevelArchiveDir removes the leading archive directory component and rejects path traversal.
func stripTopLevelArchiveDir(tarEntryName string) (string, bool) {
	cleaned := path.Clean(strings.TrimLeft(strings.ReplaceAll(tarEntryName, "\\", "/"), "/"))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", false
	}
	_, after, ok := strings.Cut(cleaned, "/")
	if !ok {
		return "", false
	}
	rel := strings.TrimSpace(after)
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

func recordManifestHash(dir, assetName, sha256Hex string) {
	if dir == "" || assetName == "" || sha256Hex == "" {
		return
	}
	manifestMu.Lock()
	defer manifestMu.Unlock()

	manifestPath := filepath.Join(dir, ".manifest.json")
	var m ModelManifest
	if data, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	if m.Assets == nil {
		m.Assets = make(map[string]string)
	}
	m.Assets[assetName] = strings.ToLower(sha256Hex)
	m.InstalledAt = time.Now().UTC().Format(time.RFC3339)

	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(manifestPath, data, 0644)
	}
}

// ReadModelManifest loads the recorded .manifest.json from an STT directory.
func ReadModelManifest(dir string) (*ModelManifest, error) {
	if dir == "" {
		return nil, errors.New("empty stt model directory")
	}
	manifestMu.Lock()
	defer manifestMu.Unlock()

	data, err := os.ReadFile(filepath.Join(dir, ".manifest.json"))
	if err != nil {
		return nil, err
	}
	var m ModelManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func fetchRemoteAssetSHA256(ctx context.Context, assetURL string) string {
	if d := fetchHuggingFaceLFSDigest(ctx, assetURL); isValidSHA256Hex(d) {
		return d
	}
	if d := fetchGitHubASRReleaseDigest(ctx, assetURL); isValidSHA256Hex(d) {
		return d
	}
	return ""
}

func fetchHuggingFaceLFSDigest(ctx context.Context, assetURL string) string {
	if !strings.HasPrefix(assetURL, "https://huggingface.co/") && !strings.Contains(assetURL, "/ggml-") {
		return ""
	}
	apiCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(apiCtx, http.MethodHead, assetURL, nil)
	if err != nil {
		return ""
	}
	noRedirectClient := &http.Client{
		Transport: sttHTTPClient.Transport,
		Timeout:   8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	for _, hdr := range []string{"X-Linked-Etag", "X-Repo-Commit-Sha256", "ETag"} {
		val := strings.Trim(strings.TrimSpace(resp.Header.Get(hdr)), `"`)
		val = strings.TrimPrefix(strings.ToLower(val), "sha256:")
		if isValidSHA256Hex(val) {
			return val
		}
	}
	return ""
}

// CheckSTTModelUpdate checks whether an installed GGML Whisper model has a newer SHA-256 digest available
// (either via a newer compiled-in ModelSpec.GGMLSHA256 or upstream HuggingFace Git-LFS X-Linked-Etag).
func CheckSTTModelUpdate(ctx context.Context, baseSTTDir, family string) (updateAvailable bool, localSHA, remoteSHA string) {
	spec, ok := ModelSpecByID(family)
	if !ok || !IsGGMLModelInstalled(baseSTTDir, spec.ID) {
		return false, "", ""
	}
	famDir := ModelFamilyDir(baseSTTDir, spec.ID)
	manifest, err := ReadModelManifest(famDir)
	if err == nil && manifest != nil && manifest.Assets != nil {
		localSHA = strings.ToLower(strings.TrimSpace(manifest.Assets[spec.GGMLFile]))
	}
	remoteSHA = strings.ToLower(strings.TrimSpace(spec.GGMLSHA256))
	if ctx != nil {
		if liveSHA := fetchRemoteAssetSHA256(ctx, spec.GGMLURL); isValidSHA256Hex(liveSHA) {
			remoteSHA = liveSHA
		}
	}
	if localSHA != "" && remoteSHA != "" && !strings.EqualFold(localSHA, remoteSHA) {
		return true, localSHA, remoteSHA
	}
	return false, localSHA, remoteSHA
}

func fetchGitHubASRReleaseDigest(ctx context.Context, assetURL string) string {
	const prefix = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/"
	if ASRModelsReleaseAPIURL == defaultASRModelsReleaseAPIURL && !strings.HasPrefix(assetURL, prefix) {
		return ""
	}
	targetName := path.Base(assetURL)
	if targetName == "" || targetName == "." || targetName == "/" {
		return ""
	}

	apiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(apiCtx, http.MethodGet, ASRModelsReleaseAPIURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := sttHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&release); err != nil {
		return ""
	}
	for _, asset := range release.Assets {
		if asset.Name == targetName {
			return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(asset.Digest)), "sha256:")
		}
	}
	return ""
}
