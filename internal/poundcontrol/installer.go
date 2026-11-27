package poundcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// PluginUUID is the PoundControl WASM plugin UUID declared by integrations/poundcontrol-wasm.
	PluginUUID = "com.github.mrpoundsign.towncrier.wasm"
	// InstalledFileName matches PoundControl's managed filename convention (sanitizeWasmFileName(uuid) + ".wasm").
	InstalledFileName = "com.github.mrpoundsign.towncrier.wasm.wasm"
	// ReleaseAssetName is the WASM release asset filename attached to TownCrier GitHub Releases.
	ReleaseAssetName = "poundcontrol-towncrier.wasm"
	// RepositoryURL is the GitHub repository URL recorded in PoundControl's .installed.json provenance metadata.
	RepositoryURL = "https://github.com/mrpoundsign/TownCrier"

	legacyFileName      = "towncrier.wasm"
	installMetaFileName = ".installed.json"
)

// PluginStatus describes the installation and version state of the PoundControl TownCrier WASM plugin.
type PluginStatus struct {
	Installed            bool
	InstalledVersion     string
	BundledVersion       string
	NeedsUpdate          bool
	PoundControlDetected bool
}

type installRecord struct {
	UUID             string    `json:"uuid"`
	Filename         string    `json:"filename"`
	Source           string    `json:"source"`
	Repository       string    `json:"repository,omitempty"`
	AssetName        string    `json:"assetName,omitempty"`
	InstalledVersion string    `json:"installedVersion,omitempty"`
	InstalledAt      time.Time `json:"installedAt"`
}

type installMetadataStore struct {
	Plugins map[string]installRecord `json:"plugins"`
}

// DefaultWasmDir returns PoundControl's managed WebAssembly plugins directory
// (%APPDATA%\PoundControl\wasm on Windows, or <UserConfigDir>/PoundControl/wasm elsewhere).
func DefaultWasmDir() (string, error) {
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "PoundControl", "wasm"), nil
		}
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("poundcontrol: resolve user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "PoundControl", "wasm"), nil
}

func resolveWasmDir(targetWasmDir string) (string, error) {
	dir := strings.TrimSpace(targetWasmDir)
	if dir != "" {
		return filepath.Clean(dir), nil
	}
	if testing.Testing() {
		return "", errors.New("poundcontrol: refusing to access default user directory during unit tests")
	}
	return DefaultWasmDir()
}

// IsPoundControlInstalled reports whether PoundControl appears to be installed or configured on this host.
func IsPoundControlInstalled(targetWasmDir string) bool {
	if testing.Testing() && strings.TrimSpace(targetWasmDir) == "" {
		return false
	}
	dir, err := resolveWasmDir(targetWasmDir)
	if err != nil {
		return false
	}
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		return true
	}
	parent := filepath.Dir(dir)
	if info, statErr := os.Stat(parent); statErr == nil && info.IsDir() {
		return true
	}
	return false
}

// IsPluginInstalled reports whether the TownCrier WASM plugin is present in targetWasmDir.
func IsPluginInstalled(targetWasmDir string) bool {
	if testing.Testing() && strings.TrimSpace(targetWasmDir) == "" {
		return false
	}
	dir, err := resolveWasmDir(targetWasmDir)
	if err != nil {
		return false
	}
	for _, name := range []string{InstalledFileName, legacyFileName} {
		cand := filepath.Join(dir, name)
		if info, statErr := os.Stat(cand); statErr == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// InstalledVersion reads the installed version for TownCrier from PoundControl's .installed.json metadata.
func InstalledVersion(targetWasmDir string) string {
	if testing.Testing() && strings.TrimSpace(targetWasmDir) == "" {
		return ""
	}
	dir, err := resolveWasmDir(targetWasmDir)
	if err != nil {
		return ""
	}
	records, err := loadInstallMetadata(dir)
	if err != nil {
		return ""
	}
	for k, rec := range records {
		if strings.EqualFold(k, PluginUUID) || strings.EqualFold(rec.UUID, PluginUUID) {
			return normalizeVersion(rec.InstalledVersion)
		}
	}
	return ""
}

// CheckPluginStatus inspects targetWasmDir and compares any installed plugin version against bundledVersion.
func CheckPluginStatus(targetWasmDir, bundledVersion string) PluginStatus {
	normBundled := normalizeVersion(bundledVersion)
	detected := IsPoundControlInstalled(targetWasmDir)
	installed := IsPluginInstalled(targetWasmDir)
	if !installed {
		return PluginStatus{
			Installed:            false,
			BundledVersion:       normBundled,
			PoundControlDetected: detected,
		}
	}
	instVer := InstalledVersion(targetWasmDir)
	needsUpdate := instVer == "" || (normBundled != "" && compareVersions(normBundled, instVer) > 0)
	return PluginStatus{
		Installed:            true,
		InstalledVersion:     instVer,
		BundledVersion:       normBundled,
		NeedsUpdate:          needsUpdate,
		PoundControlDetected: detected,
	}
}

// FindSidecarWasm searches exeDir for a bundled or sidecar TownCrier PoundControl WASM file.
func FindSidecarWasm(exeDir string) string {
	dir := strings.TrimSpace(exeDir)
	if dir == "" {
		return ""
	}
	for _, name := range []string{ReleaseAssetName, legacyFileName} {
		cand := filepath.Join(dir, name)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() && info.Size() > 0 {
			return cand
		}
	}
	return ""
}

// InstallWasmFile atomically installs srcWasmPath into targetWasmDir, removes any legacy towncrier.wasm,
// and updates PoundControl's .installed.json metadata so PoundControl's auto-updater can track future releases.
func InstallWasmFile(srcWasmPath, targetWasmDir, version string) (string, error) {
	srcPath := filepath.Clean(strings.TrimSpace(srcWasmPath))
	if srcPath == "" || srcPath == "." {
		return "", errors.New("poundcontrol: source wasm path is required")
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", fmt.Errorf("poundcontrol: stat source wasm: %w", err)
	}
	if info.IsDir() || info.Size() <= 0 {
		return "", fmt.Errorf("poundcontrol: invalid source wasm file %q", srcPath)
	}

	destDir, err := resolveWasmDir(targetWasmDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("poundcontrol: create wasm directory: %w", err)
	}

	destPath := filepath.Join(destDir, InstalledFileName)
	if err := copyFileAtomically(srcPath, destPath, destDir); err != nil {
		return "", err
	}

	legacyPath := filepath.Join(destDir, legacyFileName)
	if !strings.EqualFold(legacyPath, destPath) {
		_ = os.Remove(legacyPath)
	}

	records, _ := loadInstallMetadata(destDir)
	if records == nil {
		records = make(map[string]installRecord)
	}
	records[PluginUUID] = installRecord{
		UUID:             PluginUUID,
		Filename:         InstalledFileName,
		Source:           "github",
		Repository:       RepositoryURL,
		AssetName:        ReleaseAssetName,
		InstalledVersion: normalizeVersion(version),
		InstalledAt:      time.Now().UTC(),
	}
	if err := saveInstallMetadata(destDir, records); err != nil {
		return "", fmt.Errorf("poundcontrol: save install metadata: %w", err)
	}

	return destPath, nil
}

func copyFileAtomically(srcPath, destPath, baseDir string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("poundcontrol: open source wasm: %w", err)
	}
	defer func() { _ = src.Close() }()

	tmpFile, err := os.CreateTemp(baseDir, ".stage-*.wasm.tmp")
	if err != nil {
		return fmt.Errorf("poundcontrol: create temp wasm: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, src); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("poundcontrol: write staged wasm: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("poundcontrol: close staged wasm: %w", err)
	}

	_ = os.Remove(destPath)
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("poundcontrol: activate wasm plugin: %w", err)
	}
	cleanupTmp = false
	return nil
}

func loadInstallMetadata(baseDir string) (map[string]installRecord, error) {
	metaPath := filepath.Join(baseDir, installMetaFileName)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]installRecord{}, nil
		}
		return nil, err
	}
	var store installMetadataStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, err
	}
	if store.Plugins == nil {
		return map[string]installRecord{}, nil
	}
	return store.Plugins, nil
}

func saveInstallMetadata(baseDir string, records map[string]installRecord) error {
	encoded, err := json.MarshalIndent(installMetadataStore{Plugins: records}, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	tmpFile, err := os.CreateTemp(baseDir, ".installed-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(encoded); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	metaPath := filepath.Join(baseDir, installMetaFileName)
	_ = os.Remove(metaPath)
	if err := os.Rename(tmpPath, metaPath); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return strings.TrimSpace(v)
}

func compareVersions(v1, v2 string) int {
	n1 := normalizeVersion(v1)
	n2 := normalizeVersion(v2)
	if n1 == n2 {
		return 0
	}
	core1, pre1, _ := strings.Cut(n1, "-")
	core2, pre2, _ := strings.Cut(n2, "-")
	p1 := strings.Split(core1, ".")
	p2 := strings.Split(core2, ".")
	maxLen := max(len(p1), len(p2))
	for i := range maxLen {
		var a, b int
		if i < len(p1) {
			a, _ = strconv.Atoi(p1[i])
		}
		if i < len(p2) {
			b, _ = strconv.Atoi(p2[i])
		}
		if a > b {
			return 1
		}
		if a < b {
			return -1
		}
	}
	if pre1 == "" && pre2 != "" {
		return 1
	}
	if pre1 != "" && pre2 == "" {
		return -1
	}
	return strings.Compare(pre1, pre2)
}
