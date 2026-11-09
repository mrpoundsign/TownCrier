package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// BackupFormat identifies a portable TownCrier configuration backup bundle.
	BackupFormat = "towncrier-config-backup"

	// CurrentBundleVersion is the envelope version for BackupBundle JSON files.
	CurrentBundleVersion = 1

	// ImportModeSelectiveMerge merges selected portable sections into the active configuration.
	ImportModeSelectiveMerge = "selective_merge"

	// ImportModeFullRestore replaces the active configuration with the backup configuration.
	ImportModeFullRestore = "full_restore"
)

// BackupBundle wraps an exported Config with metadata and schema versioning.
// All sensitive tokens and API keys are scrubbed prior to serialization.
type BackupBundle struct {
	Format        string    `json:"format"`
	BundleVersion int       `json:"bundle_version"`
	AppVersion    string    `json:"app_version,omitempty"`
	SchemaVersion int       `json:"schema_version"`
	ExportedAt    time.Time `json:"exported_at"`
	Config        *Config   `json:"config"`
}

// BackupSummary provides human-readable item counts for the Import Backup confirmation dialog.
type BackupSummary struct {
	AppVersion          string
	SchemaVersion       int
	ExportedAt          time.Time
	ViewerCount         int
	BlockedWordCount    int
	CustomBotCount      int
	LiveLogPresetCount  int
	ConfiguredPlatforms []string
	RequiredModels      []string
}

// ImportOptions controls how ApplyBackup merges or restores a BackupBundle into the current Config.
type ImportOptions struct {
	Mode                        string
	IncludeViewers              bool
	IncludeBlockedWordsAndBots  bool
	IncludeChatRulesAndPlatforms bool
	IncludeLiveLogs             bool
	IncludeTranslation          bool
	PreserveMachinePaths        bool
}

// DefaultImportOptions returns recommended defaults for importing a configuration backup across PCs.
func DefaultImportOptions() ImportOptions {
	return ImportOptions{
		Mode:                        ImportModeSelectiveMerge,
		IncludeViewers:              true,
		IncludeBlockedWordsAndBots:  true,
		IncludeChatRulesAndPlatforms: true,
		IncludeLiveLogs:             true,
		IncludeTranslation:          true,
		PreserveMachinePaths:        true,
	}
}

// ScrubSecrets returns a deep clone of cfg with all inline API keys and auth tokens removed.
func ScrubSecrets(cfg *Config) *Config {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	clean := cfg.Clone()
	clean.WebSocket.AuthToken = ""
	clean.Translation.APIKey = ""
	for i := range clean.Translation.Providers {
		clean.Translation.Providers[i].APIKey = ""
	}
	return clean
}

// ExportBackup serializes a sanitized configuration into a pretty-printed BackupBundle JSON document.
func ExportBackup(cfg *Config, appVersion string) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("cannot export nil configuration")
	}
	clean := ScrubSecrets(cfg)
	if clean.Version <= 0 {
		clean.Version = CurrentVersion
	}
	bundle := BackupBundle{
		Format:        BackupFormat,
		BundleVersion: CurrentBundleVersion,
		AppVersion:    strings.TrimSpace(appVersion),
		SchemaVersion: clean.Version,
		ExportedAt:    time.Now().UTC().Truncate(time.Second),
		Config:        clean,
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal backup bundle: %w", err)
	}
	return data, nil
}

// WriteBackupFile writes a sanitized configuration backup bundle atomically to the specified path.
func WriteBackupFile(path string, cfg *Config, appVersion string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("cannot write backup: explicit path must be provided")
	}
	if err := checkTestPathWriteAllowed(path); err != nil {
		return err
	}
	data, err := ExportBackup(cfg, appVersion)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	tmpFile := filepath.Join(dir, fmt.Sprintf(".backup.tmp.%d", os.Getpid()))
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("write temporary backup: %w", err)
	}
	if err := os.Rename(tmpFile, path); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("atomic rename backup: %w", err)
	}
	return nil
}

// ParseBackup parses either a BackupBundle JSON envelope or a raw Config JSON file.
func ParseBackup(data []byte) (*BackupBundle, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, errors.New("backup file is empty")
	}

	var probe struct {
		Format        string          `json:"format"`
		BundleVersion int             `json:"bundle_version"`
		AppVersion    string          `json:"app_version"`
		SchemaVersion int             `json:"schema_version"`
		ExportedAt    time.Time       `json:"exported_at"`
		Config        json.RawMessage `json:"config"`
		Version       int             `json:"version"`
		TTS           json.RawMessage `json:"tts"`
		Platforms     json.RawMessage `json:"platforms"`
		Viewers       json.RawMessage `json:"viewers"`
	}
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return nil, fmt.Errorf("invalid backup JSON: %w", err)
	}

	var rawCfgBytes []byte
	schemaVer := probe.SchemaVersion
	appVer := probe.AppVersion
	exportedAt := probe.ExportedAt
	bundleVer := probe.BundleVersion
	if bundleVer <= 0 {
		bundleVer = CurrentBundleVersion
	}

	switch {
	case probe.Format == BackupFormat || len(probe.Config) > 0:
		if probe.Format != "" && probe.Format != BackupFormat {
			return nil, fmt.Errorf("unsupported backup format %q", probe.Format)
		}
		if len(probe.Config) == 0 || string(probe.Config) == "null" {
			return nil, errors.New("backup bundle is missing 'config' object")
		}
		rawCfgBytes = probe.Config
	case probe.Version > 0 || len(probe.TTS) > 0 || len(probe.Platforms) > 0 || len(probe.Viewers) > 0:
		rawCfgBytes = []byte(trimmed)
		schemaVer = probe.Version
	default:
		return nil, errors.New("unrecognized JSON file: not a TownCrier backup or config.json")
	}

	cfg := DefaultConfig()
	cfg.Version = 0
	cfg.TTS.Preset = ""
	cfg.TTS.EnabledModels = nil
	cfg.Translation.Providers = nil
	if err := json.Unmarshal(rawCfgBytes, cfg); err != nil {
		return nil, fmt.Errorf("parse backup config payload: %w", err)
	}

	if schemaVer <= 0 {
		schemaVer = cfg.Version
	}
	if schemaVer <= 0 {
		schemaVer = 1
	}
	cfg.LoadedSchemaVersion = schemaVer

	if err := migrate(cfg); err != nil {
		return nil, fmt.Errorf("migrate backup config: %w", err)
	}
	cfg = ScrubSecrets(cfg)
	cfg.LoadedSchemaVersion = schemaVer

	return &BackupBundle{
		Format:        BackupFormat,
		BundleVersion: bundleVer,
		AppVersion:    appVer,
		SchemaVersion: schemaVer,
		ExportedAt:    exportedAt,
		Config:        cfg,
	}, nil
}

// ReadBackupFile reads and validates a BackupBundle from disk.
func ReadBackupFile(path string) (*BackupBundle, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("backup file path cannot be empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup file: %w", err)
	}
	return ParseBackup(data)
}

// SummarizeBackup computes counts and metadata for a parsed BackupBundle.
func SummarizeBackup(bundle *BackupBundle) BackupSummary {
	if bundle == nil || bundle.Config == nil {
		return BackupSummary{}
	}
	cfg := bundle.Config
	var platforms []string
	for _, key := range []string{"twitch", "youtube", "tiktok", "kick"} {
		if p, ok := cfg.Platforms[key]; ok {
			if p.Enabled || strings.TrimSpace(p.AccountName) != "" || p.EverEnabled {
				platforms = append(platforms, key)
			}
		}
	}

	models := slices.Clone(cfg.TTS.EffectiveEnabledModels())
	return BackupSummary{
		AppVersion:          bundle.AppVersion,
		SchemaVersion:       bundle.SchemaVersion,
		ExportedAt:          bundle.ExportedAt,
		ViewerCount:         len(cfg.Viewers),
		BlockedWordCount:    len(cfg.Safety.BlockedWords),
		CustomBotCount:      len(cfg.Bots.CustomBots),
		LiveLogPresetCount:  len(cfg.Logging.LiveLogs),
		ConfiguredPlatforms: platforms,
		RequiredModels:      models,
	}
}

// ApplyBackup merges or restores a BackupBundle onto current according to opts.
// Neither current nor bundle.Config is mutated; a newly migrated Config is returned.
func ApplyBackup(current *Config, bundle *BackupBundle, opts ImportOptions) (*Config, error) {
	if bundle == nil || bundle.Config == nil {
		return nil, errors.New("cannot apply nil backup bundle")
	}
	if current == nil {
		current = DefaultConfig()
	}

	cur := current.Clone()
	inc := bundle.Config.Clone()

	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = ImportModeSelectiveMerge
	}

	if mode == ImportModeFullRestore {
		out := inc.Clone()
		restorePreservedSecrets(cur, out)
		if opts.PreserveMachinePaths {
			restoreMachineSpecificSettings(cur, out)
		}
		if err := migrate(out); err != nil {
			return nil, err
		}
		out.LoadedSchemaVersion = CurrentVersion
		return out, nil
	}

	// Selective Merge mode
	out := cur.Clone()

	if opts.IncludeViewers {
		if out.Viewers == nil {
			out.Viewers = make(map[string]ViewerConfig)
		}
		for k, v := range inc.Viewers {
			accs := v.EffectiveAccounts()
			v.Accounts = accs
			if len(accs) > 0 && v.UserID == "" {
				v.Platform = accs[0].Platform
				v.UserID = accs[0].UserID
			}
			key := ViewerKey(v.Platform, v.UserID)
			if key == ":" || v.UserID == "" {
				key = strings.ToLower(strings.TrimSpace(k))
			}
			out.Viewers[key] = v
		}
	}

	if opts.IncludeBlockedWordsAndBots {
		out.Safety.BlockedWords = mergeStringListCaseInsensitive(out.Safety.BlockedWords, inc.Safety.BlockedWords)
		if inc.Safety.BlockDefaultBadWords {
			out.Safety.BlockDefaultBadWords = true
		}
		out.Bots.CustomBots = mergeStringListLower(out.Bots.CustomBots, inc.Bots.CustomBots)
		out.Bots.IgnoreKnownBots = inc.Bots.IgnoreKnownBots
	}

	if opts.IncludeChatRulesAndPlatforms {
		out.GlobalChatRules = inc.GlobalChatRules
		if inc.Platforms != nil {
			clonedPlats := make(map[string]PlatformConfig, len(inc.Platforms))
			for k, p := range inc.Platforms {
				// If current already had an AccountName configured and the backup's AccountName is blank, preserve current's AccountName
				if curPlat, ok := cur.Platforms[k]; ok && strings.TrimSpace(p.AccountName) == "" && strings.TrimSpace(curPlat.AccountName) != "" {
					p.AccountName = curPlat.AccountName
				}
				clonedPlats[k] = p
			}
			out.Platforms = clonedPlats
		}
	}

	if opts.IncludeLiveLogs {
		out.Logging.LiveLogs = mergeLiveLogPresets(out.Logging.LiveLogs, inc.Logging.LiveLogs)
	}

	if opts.IncludeTranslation {
		out.Translation = inc.Translation
		if opts.PreserveMachinePaths {
			out.Translation.ChimeSoundPath = cur.Translation.ChimeSoundPath
		}
		restorePreservedTranslationSecrets(cur, out)
	}

	if err := migrate(out); err != nil {
		return nil, err
	}
	out.LoadedSchemaVersion = CurrentVersion
	return out, nil
}

func restorePreservedSecrets(cur, out *Config) {
	if cur == nil || out == nil {
		return
	}
	if strings.TrimSpace(out.WebSocket.AuthToken) == "" {
		out.WebSocket.AuthToken = cur.WebSocket.AuthToken
	}
	restorePreservedTranslationSecrets(cur, out)
}

func restorePreservedTranslationSecrets(cur, out *Config) {
	if cur == nil || out == nil {
		return
	}
	if strings.TrimSpace(out.Translation.APIKey) == "" {
		out.Translation.APIKey = cur.Translation.APIKey
	}
	curKeys := make(map[string]string, len(cur.Translation.Providers))
	for _, p := range cur.Translation.Providers {
		if strings.TrimSpace(p.APIKey) != "" {
			curKeys[NormalizeTranslationProvider(p.Provider)] = p.APIKey
		}
	}
	for i := range out.Translation.Providers {
		prov := NormalizeTranslationProvider(out.Translation.Providers[i].Provider)
		if strings.TrimSpace(out.Translation.Providers[i].APIKey) == "" {
			if k, ok := curKeys[prov]; ok {
				out.Translation.Providers[i].APIKey = k
			}
		}
	}
}

func restoreMachineSpecificSettings(cur, out *Config) {
	if cur == nil || out == nil {
		return
	}
	out.Audio.DeviceID = cur.Audio.DeviceID
	out.Audio.DeviceName = cur.Audio.DeviceName
	out.TTS.ModelPath = cur.TTS.ModelPath
	out.TTS.UseGPU = cur.TTS.UseGPU
	out.TTS.NumThreads = cur.TTS.NumThreads
	out.TTS.Preset = cur.TTS.Preset
	out.TTS.AlertSoundPath = cur.TTS.AlertSoundPath
	out.Translation.ChimeSoundPath = cur.Translation.ChimeSoundPath
	out.Logging.DBPath = cur.Logging.DBPath
}

func mergeStringListCaseInsensitive(existing, incoming []string) []string {
	seen := make(map[string]bool, len(existing)+len(incoming))
	var out []string
	for _, s := range existing {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, trimmed)
		}
	}
	for _, s := range incoming {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, trimmed)
		}
	}
	return out
}

func mergeStringListLower(existing, incoming []string) []string {
	seen := make(map[string]bool, len(existing)+len(incoming))
	var out []string
	for _, s := range existing {
		trimmed := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
		if trimmed == "" {
			continue
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	for _, s := range incoming {
		trimmed := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
		if trimmed == "" {
			continue
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	return out
}

func mergeLiveLogPresets(existing, incoming []LiveLogPreset) []LiveLogPreset {
	out := slices.Clone(existing)
	for _, inc := range incoming {
		matchedIdx := -1
		incID := strings.TrimSpace(inc.ID)
		incName := strings.ToLower(strings.TrimSpace(inc.Name))
		for i, cur := range out {
			if incID != "" && strings.EqualFold(strings.TrimSpace(cur.ID), incID) {
				matchedIdx = i
				break
			}
			if incName != "" && strings.ToLower(strings.TrimSpace(cur.Name)) == incName {
				matchedIdx = i
				break
			}
		}
		if matchedIdx >= 0 {
			out[matchedIdx] = inc
		} else {
			out = append(out, inc)
		}
	}
	return out
}

// SchemaBackupInfo describes an automatic pre-migration schema backup file on disk.
type SchemaBackupInfo struct {
	Path          string
	SchemaVersion int
	CreatedAt     time.Time
	SizeBytes     int64
}

func checkTestPathWriteAllowed(path string) error {
	if !testing.Testing() {
		return nil
	}
	tempDir := filepath.Clean(os.TempDir())
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(tempDir, cleanPath)
	isTemp := err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
	if !isTemp {
		return errors.New("refusing to write outside of temporary directory during unit tests")
	}
	return nil
}

// CreateConfigBackupSnapshot writes a timestamped schema-versioned backup (`<stem>.YYYY-MM-DD-HHMMSS-v<ver>.json`) of rawData before overwriting configPath.
func CreateConfigBackupSnapshot(configPath string, rawData []byte, oldVersion int) error {
	return createPreMigrationConfigBackup(configPath, rawData, oldVersion)
}

func createPreMigrationConfigBackup(configPath string, rawData []byte, oldVersion int) error {
	if strings.TrimSpace(configPath) == "" || len(rawData) == 0 {
		return nil
	}
	if err := checkTestPathWriteAllowed(configPath); err != nil {
		return err
	}
	if oldVersion <= 0 {
		oldVersion = 1
	}

	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = "config"
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	ts := time.Now().UTC().Format("2006-01-02-150405")
	backupName := fmt.Sprintf("%s.%s-v%d.json", stem, ts, oldVersion)
	backupPath := filepath.Join(dir, backupName)

	for seq := 1; ; seq++ {
		if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
			break
		}
		backupName = fmt.Sprintf("%s.%s-%d-v%d.json", stem, ts, seq, oldVersion)
		backupPath = filepath.Join(dir, backupName)
	}

	if err := os.WriteFile(backupPath, rawData, 0o600); err != nil {
		return err
	}

	return pruneOldConfigBackups(dir, stem, maxConfigBackups)
}

// ParseSchemaBackupFilename extracts the schema version and UTC timestamp from `<stem>.YYYY-MM-DD-HHMMSS[-seq]-v<ver>.json` (or legacy `.bak.json`).
func ParseSchemaBackupFilename(fileName, stem, suffix string) (int, time.Time, bool) {
	// Format 1 (canonical): <stem>.YYYY-MM-DD-HHMMSS[-seq]-v<ver>.json
	dotPrefix := stem + "."
	if strings.HasPrefix(fileName, dotPrefix) && strings.HasSuffix(fileName, ".json") && !strings.HasSuffix(fileName, ".bak.json") && fileName != stem+".json" {
		mid := strings.TrimSuffix(strings.TrimPrefix(fileName, dotPrefix), ".json")
		if vIdx := strings.LastIndex(mid, "-v"); vIdx > 0 {
			tsPart := mid[:vIdx]
			verStr := mid[vIdx+2:]
			if ver, err := strconv.Atoi(verStr); err == nil && ver > 0 && len(tsPart) >= 17 {
				if t, tErr := time.ParseInLocation("2006-01-02-150405", tsPart[:17], time.UTC); tErr == nil {
					return ver, t, true
				}
			}
		}
	}

	// Format 2 (legacy): <stem>.v<ver>.<ts><suffix>
	prefix := stem + ".v"
	if !strings.HasPrefix(fileName, prefix) || !strings.HasSuffix(fileName, suffix) {
		return 0, time.Time{}, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(fileName, prefix), suffix)
	verStr, tsPart, ok := strings.Cut(mid, ".")
	if !ok || verStr == "" || tsPart == "" {
		return 0, time.Time{}, false
	}
	ver, err := strconv.Atoi(verStr)
	if err != nil || ver <= 0 {
		return 0, time.Time{}, false
	}
	// Strip optional collision sequence suffix (e.g. "20261108-180000000-1")
	parts := strings.Split(tsPart, "-")
	var parsedTime time.Time
	if len(parts) >= 2 && len(parts[0]) == 8 && len(parts[1]) >= 6 {
		secStr := parts[0] + "-" + parts[1][:6]
		if t, tErr := time.ParseInLocation("20060102-150405", secStr, time.UTC); tErr == nil {
			parsedTime = t
		}
	}
	return ver, parsedTime, true
}

// ListConfigBackups returns all automatic schema backups for configPath, sorted newest-first.
func ListConfigBackups(configPath string) ([]SchemaBackupInfo, error) {
	if strings.TrimSpace(configPath) == "" || configPath == "memory" {
		return nil, nil
	}
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = "config"
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var infos []SchemaBackupInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ver, ts, ok := ParseSchemaBackupFilename(e.Name(), stem, ".bak.json")
		if !ok {
			continue
		}
		fullPath := filepath.Join(dir, e.Name())
		var size int64
		if fi, statErr := e.Info(); statErr == nil {
			size = fi.Size()
			if ts.IsZero() {
				ts = fi.ModTime().UTC()
			}
		}
		infos = append(infos, SchemaBackupInfo{
			Path:          fullPath,
			SchemaVersion: ver,
			CreatedAt:     ts,
			SizeBytes:     size,
		})
	}

	// Sort newest-first (by filename descending for deterministic tie-breaking)
	sort.Slice(infos, func(i, j int) bool {
		if !infos[i].CreatedAt.Equal(infos[j].CreatedAt) {
			return infos[i].CreatedAt.After(infos[j].CreatedAt)
		}
		return infos[i].Path > infos[j].Path
	})
	return infos, nil
}

func pruneOldConfigBackups(dir, stem string, keep int) error {
	if keep <= 0 {
		keep = maxConfigBackups
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	type item struct {
		name string
		ver  int
	}
	var all []item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ver, _, ok := ParseSchemaBackupFilename(e.Name(), stem, ".bak.json")
		if ok {
			all = append(all, item{name: e.Name(), ver: ver})
		}
	}

	if len(all) <= keep {
		return nil
	}

	// Sort oldest-first by filename (timestamp is lexicographically sortable)
	sort.Slice(all, func(i, j int) bool {
		return all[i].name < all[j].name
	})

	// Always protect the latest backup of each distinct schema version so downgrade restores remain available
	latestForVer := make(map[int]string)
	for _, it := range all {
		latestForVer[it.ver] = it.name
	}
	protected := make(map[string]bool, len(latestForVer))
	for _, name := range latestForVer {
		protected[name] = true
	}

	remaining := len(all)
	for _, it := range all {
		if remaining <= keep {
			break
		}
		if protected[it.name] {
			continue
		}
		_ = os.Remove(filepath.Join(dir, it.name))
		remaining--
	}
	return nil
}

// CheckConfigSchemaRegression inspects whether the loaded configuration on disk was saved by a newer
// schema version than CurrentVersion, and locates the best compatible automatic backup (schema <= CurrentVersion).
func CheckConfigSchemaRegression(cfg *Config, configPath string) (savedVersion int, supportedVersion int, candidate *SchemaBackupInfo, regressed bool) {
	supportedVersion = CurrentVersion
	if cfg != nil && cfg.LoadedSchemaVersion > 0 {
		savedVersion = cfg.LoadedSchemaVersion
	} else if strings.TrimSpace(configPath) != "" && configPath != "memory" {
		if data, err := os.ReadFile(configPath); err == nil {
			var probe struct {
				Version int `json:"version"`
			}
			if json.Unmarshal(data, &probe) == nil && probe.Version > 0 {
				savedVersion = probe.Version
			}
		}
	}
	if savedVersion <= supportedVersion {
		return savedVersion, supportedVersion, nil, false
	}

	backups, _ := ListConfigBackups(configPath)
	for i := range backups {
		b := backups[i]
		if b.SchemaVersion <= supportedVersion {
			if candidate == nil || b.SchemaVersion > candidate.SchemaVersion {
				bCopy := b
				candidate = &bCopy
			}
		}
	}
	return savedVersion, supportedVersion, candidate, true
}

// RestoreConfigBackup restores an automatic schema backup file into configPath and returns the loaded Config.
func RestoreConfigBackup(backupPath, configPath string) (*Config, error) {
	if strings.TrimSpace(backupPath) == "" {
		return nil, errors.New("backup path cannot be empty")
	}
	if strings.TrimSpace(configPath) == "" || configPath == "memory" {
		return nil, errors.New("config path cannot be empty or memory")
	}
	if err := checkTestPathWriteAllowed(configPath); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, fmt.Errorf("read config schema backup: %w", err)
	}

	cfg := DefaultConfig()
	cfg.Version = 0
	cfg.TTS.Preset = ""
	cfg.TTS.EnabledModels = nil
	cfg.Translation.Providers = nil
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config schema backup: %w", err)
	}
	if err := migrate(cfg); err != nil {
		return nil, fmt.Errorf("migrate restored config: %w", err)
	}
	cfg.LoadedSchemaVersion = CurrentVersion

	if err := Save(configPath, cfg); err != nil {
		return nil, fmt.Errorf("save restored config: %w", err)
	}
	return cfg, nil
}
