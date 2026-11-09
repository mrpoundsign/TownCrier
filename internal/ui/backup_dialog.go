package ui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/app"
	"github.com/mrpoundsign/TownCrier/internal/eventlog"
	"github.com/mrpoundsign/TownCrier/internal/logging"
	"github.com/mrpoundsign/TownCrier/pkg/config"
)

const (
	ImportModeOptionSelectiveMerge = "Selective Merge (Recommended — Combine with existing settings)"
	ImportModeOptionFullRestore    = "Full Restore (Replace settings with backup)"
)

// BackupArchive holds the parsed contents of a TownCrier .zip (or legacy .json) backup file.
type BackupArchive struct {
	Manifest      *SupportManifest
	Bundle        *config.BackupBundle
	DatabaseBytes []byte
	HasLogFile    bool
}

// ParseBackupArchiveBytes parses either a .zip backup archive or a standalone .json backup file.
func ParseBackupArchiveBytes(data []byte) (*BackupArchive, error) {
	if len(data) == 0 {
		return nil, errors.New("backup file is empty")
	}

	// Check for ZIP magic header ("PK\x03\x04")
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04 {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("invalid backup zip archive: %w", err)
		}

		arch := &BackupArchive{}
		for _, zf := range zr.File {
			base := strings.ToLower(filepath.Base(zf.Name))
			rc, openErr := zf.Open()
			if openErr != nil {
				return nil, fmt.Errorf("read zip entry %q: %w", zf.Name, openErr)
			}
			entryBytes, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read zip entry %q: %w", zf.Name, readErr)
			}

			switch base {
			case "config-backup.json", "config.json":
				bundle, parseErr := config.ParseBackup(entryBytes)
				if parseErr != nil {
					return nil, fmt.Errorf("parse %s in backup zip: %w", zf.Name, parseErr)
				}
				arch.Bundle = bundle
			case "events.db":
				if len(entryBytes) > 0 {
					arch.DatabaseBytes = entryBytes
				}
			case "support-manifest.json", "manifest.json":
				var m SupportManifest
				if json.Unmarshal(entryBytes, &m) == nil {
					arch.Manifest = &m
				}
			case "towncrier.log":
				arch.HasLogFile = true
			}
		}

		if arch.Bundle == nil && len(arch.DatabaseBytes) == 0 {
			return nil, errors.New("backup zip does not contain a TownCrier configuration (config-backup.json) or event database (events.db)")
		}
		return arch, nil
	}

	// Fallback: parse as standalone JSON backup bundle or raw config.json
	bundle, err := config.ParseBackup(data)
	if err != nil {
		return nil, err
	}
	return &BackupArchive{
		Bundle: bundle,
	}, nil
}

// ReadBackupArchiveFile reads and parses a .zip or .json backup from disk.
func ReadBackupArchiveFile(path string) (*BackupArchive, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("backup file path cannot be empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup file: %w", err)
	}
	return ParseBackupArchiveBytes(data)
}

// BackupImportDialog manages the interactive confirmation and selective merge options for importing a backup archive.
type BackupImportDialog struct {
	window     fyne.Window
	session    *app.Session
	archive    *BackupArchive
	bundle     *config.BackupBundle
	sourcePath string
	onApply    func(newCfg *config.Config, modelsChanged bool)
	onStatus   func(string)

	SummaryLabel              *widget.Label
	IncludeConfigCheck        *widget.Check
	RestoreDBCheck            *widget.Check
	ModeSelect                *widget.Select
	ViewersCheck              *widget.Check
	BlockedAndBotsCheck       *widget.Check
	RulesAndPlatformsCheck    *widget.Check
	LiveLogsCheck             *widget.Check
	TranslationCheck          *widget.Check
	PreserveMachinePathsCheck *widget.Check
	ImportBtn                 *widget.Button
	CancelBtn                 *widget.Button
	Content                   fyne.CanvasObject
	modal                     dialog.Dialog
}

// NewBackupImportDialog constructs a BackupImportDialog for a config.BackupBundle.
func NewBackupImportDialog(
	win fyne.Window,
	session *app.Session,
	bundle *config.BackupBundle,
	sourcePath string,
	onApply func(newCfg *config.Config, modelsChanged bool),
	onStatus func(string),
) *BackupImportDialog {
	return NewBackupArchiveImportDialog(win, session, &BackupArchive{Bundle: bundle}, sourcePath, onApply, onStatus)
}

// NewBackupArchiveImportDialog constructs a BackupImportDialog for a parsed BackupArchive (.zip or .json).
func NewBackupArchiveImportDialog(
	win fyne.Window,
	session *app.Session,
	archive *BackupArchive,
	sourcePath string,
	onApply func(newCfg *config.Config, modelsChanged bool),
	onStatus func(string),
) *BackupImportDialog {
	var bundle *config.BackupBundle
	if archive != nil {
		bundle = archive.Bundle
	}
	d := &BackupImportDialog{
		window:     win,
		session:    session,
		archive:    archive,
		bundle:     bundle,
		sourcePath: sourcePath,
		onApply:    onApply,
		onStatus:   onStatus,
	}
	d.buildUI()
	return d
}

func (d *BackupImportDialog) buildUI() {
	fileName := filepath.Base(d.sourcePath)
	if strings.TrimSpace(fileName) == "" || fileName == "." {
		fileName = "backup.zip"
	}

	hasCfg := d.bundle != nil && d.bundle.Config != nil
	hasDB := d.archive != nil && len(d.archive.DatabaseBytes) > 0

	var summaryLines []string
	if hasCfg {
		sum := config.SummarizeBackup(d.bundle)
		verText := sum.AppVersion
		if verText == "" {
			verText = fmt.Sprintf("Schema v%d", sum.SchemaVersion)
		} else {
			verText = fmt.Sprintf("v%s (Schema v%d)", strings.TrimPrefix(verText, "v"), sum.SchemaVersion)
		}
		timeText := "Unknown time"
		if !sum.ExportedAt.IsZero() {
			timeText = sum.ExportedAt.Local().Format("2006-01-02 15:04:05")
		}
		platText := "None"
		if len(sum.ConfiguredPlatforms) > 0 {
			platText = strings.Join(sum.ConfiguredPlatforms, ", ")
		}
		summaryLines = append(summaryLines,
			fmt.Sprintf("File: %s • Version: %s • Exported: %s", fileName, verText, timeText),
			fmt.Sprintf("Config Contents: %d chatter voice overrides, %d blocked words, %d custom bots, %d Live Log presets (Platforms: %s)",
				sum.ViewerCount, sum.BlockedWordCount, sum.CustomBotCount, sum.LiveLogPresetCount, platText),
		)
	} else {
		summaryLines = append(summaryLines, "File: "+fileName)
	}
	if hasDB {
		dbKB := max(len(d.archive.DatabaseBytes)/1024, 1)
		summaryLines = append(summaryLines, fmt.Sprintf("Database Contents: Event Log SQLite Database (events.db • ~%d KB)", dbKB))
	}

	d.SummaryLabel = widget.NewLabel(strings.Join(summaryLines, "\n"))
	d.SummaryLabel.Wrapping = fyne.TextWrapWord

	var sum config.BackupSummary
	if hasCfg {
		sum = config.SummarizeBackup(d.bundle)
	}

	d.IncludeConfigCheck = widget.NewCheck("Import Configuration & Chatter Profiles", nil)
	d.IncludeConfigCheck.SetChecked(hasCfg)
	if !hasCfg {
		d.IncludeConfigCheck.Disable()
	}

	if hasDB {
		dbKB := max(len(d.archive.DatabaseBytes)/1024, 1)
		d.RestoreDBCheck = widget.NewCheck(
			fmt.Sprintf("Restore Event Log & Chat History Database (events.db • ~%d KB — backs up current DB first)", dbKB),
			nil,
		)
		d.RestoreDBCheck.SetChecked(true)
	}

	d.ViewersCheck = widget.NewCheck(fmt.Sprintf("Chatter Voice & Speed Overrides (%d profiles)", sum.ViewerCount), nil)
	d.ViewersCheck.SetChecked(true)

	d.BlockedAndBotsCheck = widget.NewCheck(fmt.Sprintf("Blocked Words (%d) & Custom Bot Lists (%d)", sum.BlockedWordCount, sum.CustomBotCount), nil)
	d.BlockedAndBotsCheck.SetChecked(true)

	d.RulesAndPlatformsCheck = widget.NewCheck("Global Chat Rules & Platform Alert Templates/Settings", nil)
	d.RulesAndPlatformsCheck.SetChecked(true)

	d.LiveLogsCheck = widget.NewCheck(fmt.Sprintf("Live Log Filter Presets (%d tabs)", sum.LiveLogPresetCount), nil)
	d.LiveLogsCheck.SetChecked(true)

	d.TranslationCheck = widget.NewCheck("Translation Preferences & Provider Chain (preserves local API keys)", nil)
	d.TranslationCheck.SetChecked(true)

	d.PreserveMachinePathsCheck = widget.NewCheck(
		"Preserve this PC's audio output device, hardware preset & local file paths (Recommended)",
		nil,
	)
	d.PreserveMachinePathsCheck.SetChecked(true)

	syncControlStates := func() {
		cfgEnabled := hasCfg && (d.IncludeConfigCheck == nil || d.IncludeConfigCheck.Checked)
		if d.ModeSelect != nil {
			if cfgEnabled {
				d.ModeSelect.Enable()
			} else {
				d.ModeSelect.Disable()
			}
		}
		if d.PreserveMachinePathsCheck != nil {
			if cfgEnabled {
				d.PreserveMachinePathsCheck.Enable()
			} else {
				d.PreserveMachinePathsCheck.Disable()
			}
		}
		isSelective := cfgEnabled && (d.ModeSelect == nil || d.ModeSelect.Selected != ImportModeOptionFullRestore)
		checks := []*widget.Check{
			d.ViewersCheck,
			d.BlockedAndBotsCheck,
			d.RulesAndPlatformsCheck,
			d.LiveLogsCheck,
			d.TranslationCheck,
		}
		for _, chk := range checks {
			if chk == nil {
				continue
			}
			if isSelective {
				chk.Enable()
			} else {
				chk.Disable()
			}
		}
	}

	d.ModeSelect = widget.NewSelect(
		[]string{ImportModeOptionSelectiveMerge, ImportModeOptionFullRestore},
		func(_ string) {
			syncControlStates()
		},
	)
	d.ModeSelect.SetSelected(ImportModeOptionSelectiveMerge)
	d.IncludeConfigCheck.OnChanged = func(_ bool) {
		syncControlStates()
	}
	syncControlStates()

	d.CancelBtn = widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
		if d.modal != nil {
			d.modal.Hide()
		}
	})

	d.ImportBtn = widget.NewButtonWithIcon("Import & Apply Backup", theme.ConfirmIcon(), func() {
		_, err := d.TriggerImport()
		if err != nil {
			if d.onStatus != nil {
				d.onStatus("⚠️ Backup import failed: " + err.Error())
			}
			if d.window != nil {
				dialog.ShowError(err, d.window)
			}
			return
		}
		if d.modal != nil {
			d.modal.Hide()
		}
	})
	d.ImportBtn.Importance = widget.HighImportance

	bodyItems := []fyne.CanvasObject{
		d.SummaryLabel,
		widget.NewSeparator(),
	}

	if hasCfg {
		if hasDB {
			bodyItems = append(bodyItems, d.IncludeConfigCheck)
		}
		modeRow := container.NewBorder(
			nil, nil,
			widget.NewLabelWithStyle("Configuration Mode:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			nil,
			d.ModeSelect,
		)
		sectionsBox := container.NewVBox(
			widget.NewLabelWithStyle("Sections to Merge (Selective Merge Mode):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			d.ViewersCheck,
			d.BlockedAndBotsCheck,
			d.RulesAndPlatformsCheck,
			d.LiveLogsCheck,
			d.TranslationCheck,
		)
		bodyItems = append(bodyItems,
			modeRow,
			sectionsBox,
			d.PreserveMachinePathsCheck,
		)
	}

	if hasDB && d.RestoreDBCheck != nil {
		if hasCfg {
			bodyItems = append(bodyItems, widget.NewSeparator())
		}
		bodyItems = append(bodyItems, d.RestoreDBCheck)
	}

	btnRow := container.NewHBox(
		layout.NewSpacer(),
		d.CancelBtn,
		d.ImportBtn,
	)

	bodyItems = append(bodyItems,
		widget.NewSeparator(),
		widget.NewLabelWithStyle(
			"Note: OAuth credentials and API keys in Windows Credential Manager are never overwritten by backups.",
			fyne.TextAlignLeading,
			fyne.TextStyle{Italic: true},
		),
		widget.NewSeparator(),
		btnRow,
	)

	d.Content = container.NewVBox(bodyItems...)
}

// Options returns the config.ImportOptions currently selected in the dialog UI.
func (d *BackupImportDialog) Options() config.ImportOptions {
	mode := config.ImportModeSelectiveMerge
	if d.ModeSelect != nil && d.ModeSelect.Selected == ImportModeOptionFullRestore {
		mode = config.ImportModeFullRestore
	}
	return config.ImportOptions{
		Mode:                        mode,
		IncludeViewers:              d.ViewersCheck == nil || d.ViewersCheck.Checked,
		IncludeBlockedWordsAndBots:  d.BlockedAndBotsCheck == nil || d.BlockedAndBotsCheck.Checked,
		IncludeChatRulesAndPlatforms: d.RulesAndPlatformsCheck == nil || d.RulesAndPlatformsCheck.Checked,
		IncludeLiveLogs:             d.LiveLogsCheck == nil || d.LiveLogsCheck.Checked,
		IncludeTranslation:          d.TranslationCheck == nil || d.TranslationCheck.Checked,
		PreserveMachinePaths:        d.PreserveMachinePathsCheck == nil || d.PreserveMachinePathsCheck.Checked,
	}
}

// TriggerImport applies the configured import options (and optional database restore) to the session.
func (d *BackupImportDialog) TriggerImport() (*config.Config, error) {
	importCfg := d.bundle != nil && d.bundle.Config != nil && (d.IncludeConfigCheck == nil || d.IncludeConfigCheck.Checked)
	restoreDB := d.archive != nil && len(d.archive.DatabaseBytes) > 0 && d.RestoreDBCheck != nil && d.RestoreDBCheck.Checked

	if !importCfg && !restoreDB {
		return nil, errors.New("select at least one item (Configuration or Event Log Database) to import")
	}

	var curCfg *config.Config
	if d.session != nil {
		curCfg = d.session.Config()
	} else {
		curCfg = config.DefaultConfig()
	}
	oldModels := slices.Clone(curCfg.TTS.EffectiveEnabledModels())
	newCfg := curCfg.Clone()

	if importCfg {
		applied, err := config.ApplyBackup(curCfg, d.bundle, d.Options())
		if err != nil {
			return nil, err
		}
		newCfg = applied
		if d.session != nil {
			// Snapshot existing config on disk before applying full restore so user can roll back via Schema Snapshots
			if cs := d.session.ConfigStore(); cs != nil && cs.Location() != "" && cs.Location() != "memory" {
				if rawBytes, readErr := os.ReadFile(cs.Location()); readErr == nil && len(rawBytes) > 0 {
					_ = config.CreateConfigBackupSnapshot(cs.Location(), rawBytes, curCfg.Version)
				}
			}
			if err := d.session.UpdateConfig(func(c *config.Config) {
				*c = *newCfg.Clone()
			}); err != nil {
				return nil, err
			}
		}
	}

	if restoreDB && d.session != nil {
		if elog := d.session.EventLogger(); elog != nil {
			if err := elog.RestoreFromBytes(d.archive.DatabaseBytes); err != nil {
				return nil, fmt.Errorf("restore event log database: %w", err)
			}
		}
	}

	modelsChanged := !slices.Equal(oldModels, newCfg.TTS.EffectiveEnabledModels())
	if d.onApply != nil {
		d.onApply(newCfg, modelsChanged)
	}
	if d.onStatus != nil {
		switch {
		case importCfg && restoreDB:
			d.onStatus(fmt.Sprintf("✓ Backup imported (%d chatter profiles, %d blocked words, %d custom bots + Event Log database restored).",
				len(newCfg.Viewers), len(newCfg.Safety.BlockedWords), len(newCfg.Bots.CustomBots)))
		case restoreDB:
			d.onStatus("✓ Event Log database restored from backup.")
		default:
			d.onStatus(fmt.Sprintf("✓ Configuration backup imported (%d chatter profiles, %d blocked words, %d custom bots).",
				len(newCfg.Viewers), len(newCfg.Safety.BlockedWords), len(newCfg.Bots.CustomBots)))
		}
	}
	return newCfg, nil
}

// Show displays the BackupImportDialog as a modal over the parent window.
func (d *BackupImportDialog) Show() {
	if d.window == nil {
		return
	}
	d.modal = dialog.NewCustomWithoutButtons("Import / Restore Backup", d.Content, d.window)
	d.modal.Resize(fyne.NewSize(640, 490))
	d.modal.Show()
}

// ShowBackupImportFilePicker opens a file open dialog to select a backup .zip (or .json) file and launches BackupImportDialog.
func ShowBackupImportFilePicker(
	win fyne.Window,
	session *app.Session,
	onApply func(newCfg *config.Config, modelsChanged bool),
	onStatus func(string),
) {
	if win == nil {
		return
	}
	openDlg := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			if onStatus != nil {
				onStatus("⚠️ Failed to open backup file: " + err.Error())
			}
			return
		}
		if reader == nil {
			return
		}
		srcPath := reader.URI().Path()
		_ = reader.Close()

		arch, parseErr := ReadBackupArchiveFile(srcPath)
		if parseErr != nil {
			if onStatus != nil {
				onStatus("⚠️ Invalid backup file: " + parseErr.Error())
			}
			dialog.ShowError(parseErr, win)
			return
		}

		impDlg := NewBackupArchiveImportDialog(win, session, arch, srcPath, onApply, onStatus)
		impDlg.Show()
	}, win)

	openDlg.SetFilter(storage.NewExtensionFileFilter([]string{".zip", ".json"}))
	openDlg.Resize(fyne.NewSize(720, 500))
	openDlg.Show()
}

// ShowSchemaSnapshotsDialog displays all automatic pre-migration and pre-restore snapshots for both Config and Event Log DB.
func ShowSchemaSnapshotsDialog(
	win fyne.Window,
	session *app.Session,
	onApply func(newCfg *config.Config, dbRestored bool),
	onStatus func(string),
) {
	if win == nil || session == nil {
		return
	}

	var configPath string
	if cs := session.ConfigStore(); cs != nil {
		configPath = cs.Location()
	}
	cfgBackups, _ := config.ListConfigBackups(configPath)

	var dbBackups []eventlog.DBBackupInfo
	if elog := session.EventLogger(); elog != nil {
		dbBackups, _ = elog.ListBackupInfos()
	}

	var modal dialog.Dialog

	cfgBox := container.NewVBox()
	if len(cfgBackups) == 0 {
		cfgBox.Add(widget.NewLabelWithStyle("No automatic configuration snapshots found yet.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
	} else {
		for _, b := range cfgBackups {
			bInfo := b
			tsLabel := bInfo.CreatedAt.Local().Format("2006-01-02 15:04:05")
			lbl := widget.NewLabel(fmt.Sprintf("Config Schema v%d • %s • %s (%d bytes)",
				bInfo.SchemaVersion, tsLabel, filepath.Base(bInfo.Path), bInfo.SizeBytes))
			restoreBtn := widget.NewButtonWithIcon("Restore Config", theme.HistoryIcon(), func() {
				restoredCfg, err := config.RestoreConfigBackup(bInfo.Path, configPath)
				if err != nil {
					if onStatus != nil {
						onStatus("⚠️ Failed to restore config snapshot: " + err.Error())
					}
					dialog.ShowError(err, win)
					return
				}
				_ = session.ReloadConfig(restoredCfg)
				if onApply != nil {
					onApply(restoredCfg, false)
				}
				if onStatus != nil {
					onStatus(fmt.Sprintf("✓ Restored configuration snapshot from Schema v%d (%s).", bInfo.SchemaVersion, filepath.Base(bInfo.Path)))
				}
				if modal != nil {
					modal.Hide()
				}
			})
			cfgBox.Add(container.NewBorder(nil, nil, nil, restoreBtn, lbl))
		}
	}

	dbBox := container.NewVBox()
	if len(dbBackups) == 0 {
		dbBox.Add(widget.NewLabelWithStyle("No automatic Event Log database snapshots found yet.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
	} else {
		for _, b := range dbBackups {
			bInfo := b
			tsLabel := bInfo.CreatedAt.Local().Format("2006-01-02 15:04:05")
			lbl := widget.NewLabel(fmt.Sprintf("Database Schema v%d • %s • %s (%d KB)",
				bInfo.SchemaVersion, tsLabel, filepath.Base(bInfo.Path), max(bInfo.SizeBytes/1024, 1)))
			restoreBtn := widget.NewButtonWithIcon("Restore DB", theme.HistoryIcon(), func() {
				elog := session.EventLogger()
				if elog == nil {
					return
				}
				if err := elog.RestoreBackup(bInfo.Path); err != nil {
					if onStatus != nil {
						onStatus("⚠️ Failed to restore database snapshot: " + err.Error())
					}
					dialog.ShowError(err, win)
					return
				}
				if onApply != nil {
					onApply(session.Config(), true)
				}
				if onStatus != nil {
					onStatus(fmt.Sprintf("✓ Restored Event Log database snapshot from Schema v%d (%s).", bInfo.SchemaVersion, filepath.Base(bInfo.Path)))
				}
				if modal != nil {
					modal.Hide()
				}
			})
			dbBox.Add(container.NewBorder(nil, nil, nil, restoreBtn, lbl))
		}
	}

	closeBtn := widget.NewButton("Close", func() {
		if modal != nil {
			modal.Hide()
		}
	})

	infoLbl := widget.NewLabelWithStyle(
		fmt.Sprintf("TownCrier automatically snapshots your configuration (Current Schema v%d) and Event Log database (Current Schema v%d) before schema upgrades and full restores.",
			config.CurrentVersion, eventlog.CurrentSchemaVersion),
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	infoLbl.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(
		infoLbl,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Configuration Snapshots:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		cfgBox,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Event Log Database Snapshots:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		dbBox,
		widget.NewSeparator(),
		container.NewHBox(layout.NewSpacer(), closeBtn),
	)

	modal = dialog.NewCustomWithoutButtons("Automatic Schema Snapshots & Rollback", newPanelVScroll(body), win)
	modal.Resize(fyne.NewSize(660, 420))
	modal.Show()
}

// CheckAndOfferSchemaDowngradeRestore inspects both the loaded Config and Event Log SQLite database
// to see if either was saved by a newer schema version than this build supports (indicating an app downgrade).
// If a regression is detected, it presents an interactive restore prompt and returns true.
func CheckAndOfferSchemaDowngradeRestore(
	win fyne.Window,
	session *app.Session,
	onApply func(newCfg *config.Config, dbRestored bool),
	onStatus func(string),
) bool {
	if session == nil {
		return false
	}

	var configPath string
	if cs := session.ConfigStore(); cs != nil {
		configPath = cs.Location()
	}

	cfgSavedVer, cfgSupVer, cfgCandidate, cfgRegressed := config.CheckConfigSchemaRegression(session.Config(), configPath)

	var dbSavedVer, dbSupVer int
	var dbCandidate *eventlog.DBBackupInfo
	var dbRegressed bool
	if elog := session.EventLogger(); elog != nil {
		dbSavedVer, dbSupVer, dbCandidate, dbRegressed = elog.CheckSchemaRegression()
	}

	if !cfgRegressed && !dbRegressed {
		return false
	}

	if win == nil {
		return true
	}

	var details []string
	var restoreConfigCheck *widget.Check
	var restoreDBCheck *widget.Check

	if cfgRegressed {
		if cfgCandidate != nil {
			details = append(details, fmt.Sprintf(
				"• Configuration file is Schema v%d (this version of TownCrier uses Schema v%d). Found compatible backup: %s (Schema v%d).",
				cfgSavedVer, cfgSupVer, filepath.Base(cfgCandidate.Path), cfgCandidate.SchemaVersion,
			))
			restoreConfigCheck = widget.NewCheck(
				fmt.Sprintf("Restore Configuration backup (%s — Schema v%d)", filepath.Base(cfgCandidate.Path), cfgCandidate.SchemaVersion),
				nil,
			)
			restoreConfigCheck.SetChecked(true)
		} else {
			details = append(details, fmt.Sprintf(
				"• Configuration file is Schema v%d (this version uses Schema v%d). No prior v%d backup file was found in the config folder.",
				cfgSavedVer, cfgSupVer, cfgSupVer,
			))
		}
	}

	if dbRegressed {
		if dbCandidate != nil {
			details = append(details, fmt.Sprintf(
				"• Event Log database is Schema v%d (this version of TownCrier uses Schema v%d). Found compatible backup: %s (Schema v%d).",
				dbSavedVer, dbSupVer, filepath.Base(dbCandidate.Path), dbCandidate.SchemaVersion,
			))
			restoreDBCheck = widget.NewCheck(
				fmt.Sprintf("Restore Event Log Database backup (%s — Schema v%d)", filepath.Base(dbCandidate.Path), dbCandidate.SchemaVersion),
				nil,
			)
			restoreDBCheck.SetChecked(true)
		} else {
			details = append(details, fmt.Sprintf(
				"• Event Log database is Schema v%d (this version uses Schema v%d). No prior v%d backup file was found in the logs folder.",
				dbSavedVer, dbSupVer, dbSupVer,
			))
		}
	}

	headerLbl := widget.NewLabelWithStyle(
		"Newer Data Schema Detected (Downgrade Protection)",
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)
	descLbl := widget.NewLabel(
		"Your saved configuration or event log database was created by a newer schema version of TownCrier.\n\n" +
			strings.Join(details, "\n"),
	)
	descLbl.Wrapping = fyne.TextWrapWord

	contentBox := container.NewVBox(
		headerLbl,
		descLbl,
	)

	hasAnyCandidate := restoreConfigCheck != nil || restoreDBCheck != nil
	if hasAnyCandidate {
		contentBox.Add(widget.NewSeparator())
		if restoreConfigCheck != nil {
			contentBox.Add(restoreConfigCheck)
		}
		if restoreDBCheck != nil {
			contentBox.Add(restoreDBCheck)
		}
	}

	var modal dialog.Dialog
	continueBtn := widget.NewButton("Keep Current Files", func() {
		if modal != nil {
			modal.Hide()
		}
	})

	btnRow := container.NewHBox(layout.NewSpacer(), continueBtn)
	if hasAnyCandidate {
		restoreBtn := widget.NewButtonWithIcon("Restore Selected Backup(s)", theme.HistoryIcon(), func() {
			var restoredCfg *config.Config
			var dbRestored bool

			if restoreConfigCheck != nil && restoreConfigCheck.Checked && cfgCandidate != nil {
				c, err := config.RestoreConfigBackup(cfgCandidate.Path, configPath)
				if err != nil {
					if onStatus != nil {
						onStatus("⚠️ Failed to restore config backup: " + err.Error())
					}
					dialog.ShowError(err, win)
					return
				}
				_ = session.ReloadConfig(c)
				restoredCfg = c
			}

			if restoreDBCheck != nil && restoreDBCheck.Checked && dbCandidate != nil {
				if elog := session.EventLogger(); elog != nil {
					if err := elog.RestoreBackup(dbCandidate.Path); err != nil {
						if onStatus != nil {
							onStatus("⚠️ Failed to restore database backup: " + err.Error())
						}
						dialog.ShowError(err, win)
						return
					}
					dbRestored = true
				}
			}

			if restoredCfg == nil {
				restoredCfg = session.Config()
			}
			if onApply != nil {
				onApply(restoredCfg, dbRestored)
			}
			if onStatus != nil {
				onStatus("✓ Restored compatible pre-upgrade schema backup(s).")
			}
			if modal != nil {
				modal.Hide()
			}
		})
		restoreBtn.Importance = widget.HighImportance
		btnRow.Add(restoreBtn)
	}

	contentBox.Add(widget.NewSeparator())
	contentBox.Add(btnRow)

	modal = dialog.NewCustomWithoutButtons("Downgrade Schema Protection", contentBox, win)
	modal.Resize(fyne.NewSize(620, 340))
	modal.Show()
	return true
}

// SupportBackupOptions specifies which items to package into a TownCrier .zip backup archive.
type SupportBackupOptions struct {
	IncludeConfig   bool
	IncludeDatabase bool
	IncludeLogs     bool
	UserNotes       string
	LogFilePath     string // Optional override for testing; defaults to logging.LogPath()
}

// SupportManifest records metadata inside support-manifest.json at the root of the backup .zip.
type SupportManifest struct {
	AppVersion          string    `json:"app_version"`
	CreatedAt           time.Time `json:"created_at"`
	OS                  string    `json:"os"`
	Arch                string    `json:"arch"`
	NumCPU              int       `json:"num_cpu"`
	GoVersion           string    `json:"go_version"`
	ConfigSchemaVersion int       `json:"config_schema_version"`
	DBSchemaVersion     int       `json:"db_schema_version"`
	IncludedFiles       []string  `json:"included_files"`
	Warnings            []string  `json:"warnings,omitempty"`
}

// WriteSupportBackupZip packages the selected configuration, SQLite database, and optional logs
// into a compressed .zip stream on w and returns the list of archived filenames.
func WriteSupportBackupZip(w io.Writer, session *app.Session, opts SupportBackupOptions) ([]string, error) {
	if w == nil {
		return nil, errors.New("destination writer cannot be nil")
	}
	if !opts.IncludeConfig && !opts.IncludeLogs && !opts.IncludeDatabase && strings.TrimSpace(opts.UserNotes) == "" {
		return nil, errors.New("select at least one item (Configuration, Event Database, or Application Log) to include in the backup")
	}

	zw := zip.NewWriter(w)
	var included []string
	var warnings []string

	addBytesToZip := func(name string, data []byte) error {
		hdr := &zip.FileHeader{
			Name:     name,
			Method:   zip.Deflate,
			Modified: time.Now().UTC(),
		}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}

	// 1. Optional Notes
	if notes := strings.TrimSpace(opts.UserNotes); notes != "" {
		if err := addBytesToZip("support-notes.txt", []byte(notes+"\n")); err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("write support-notes.txt: %w", err)
		}
		included = append(included, "support-notes.txt")
	}

	// 2. Sanitized Configuration Bundle (secrets & API keys scrubbed)
	if opts.IncludeConfig {
		var cfg *config.Config
		if session != nil {
			cfg = session.Config()
		} else {
			cfg = config.DefaultConfig()
		}
		cfgBytes, err := config.ExportBackup(cfg, AppVersion)
		if err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("export sanitized config: %w", err)
		}
		if err := addBytesToZip("config-backup.json", cfgBytes); err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("archive config-backup.json: %w", err)
		}
		included = append(included, "config-backup.json")
	}

	// 3. Application Log (towncrier.log — optional)
	if opts.IncludeLogs {
		logging.Sync()
		logPath := strings.TrimSpace(opts.LogFilePath)
		if logPath == "" {
			logPath = logging.LogPath()
		}
		logBytes, err := os.ReadFile(logPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Application log file not found or unreadable (%s): %v", filepath.Base(logPath), err))
		} else {
			if err := addBytesToZip("towncrier.log", logBytes); err != nil {
				_ = zw.Close()
				return nil, fmt.Errorf("archive towncrier.log: %w", err)
			}
			included = append(included, "towncrier.log")
		}
	}

	// 4. SQLite Event Log Database (WAL-checkpointed consistent snapshot)
	if opts.IncludeDatabase {
		var elog *eventlog.Manager
		if session != nil {
			elog = session.EventLogger()
		}
		if elog == nil {
			warnings = append(warnings, "Event log database is not active in this session.")
		} else {
			hdr := &zip.FileHeader{
				Name:     "events.db",
				Method:   zip.Deflate,
				Modified: time.Now().UTC(),
			}
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				_ = zw.Close()
				return nil, fmt.Errorf("create events.db zip entry: %w", err)
			}
			if snapErr := elog.WriteSnapshot(fw); snapErr != nil {
				warnings = append(warnings, fmt.Sprintf("Could not snapshot events.db: %v", snapErr))
			} else {
				included = append(included, "events.db")
			}
		}
	}

	// 5. Archive Manifest
	included = append(included, "support-manifest.json")
	manifest := SupportManifest{
		AppVersion:          AppVersion,
		CreatedAt:           time.Now().UTC().Truncate(time.Second),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		NumCPU:              runtime.NumCPU(),
		GoVersion:           runtime.Version(),
		ConfigSchemaVersion: config.CurrentVersion,
		DBSchemaVersion:     eventlog.CurrentSchemaVersion,
		IncludedFiles:       included,
		Warnings:            warnings,
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := addBytesToZip("support-manifest.json", manifestBytes); err != nil {
		_ = zw.Close()
		return nil, fmt.Errorf("write support-manifest.json: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalize backup zip: %w", err)
	}
	return included, nil
}

// DefaultBackupZipFilename formats the canonical default backup .zip filename including timestamp and app version.
func DefaultBackupZipFilename(now time.Time) string {
	ver := strings.TrimPrefix(strings.TrimSpace(AppVersion), "v")
	if ver == "" {
		ver = "0.0.9"
	}
	return fmt.Sprintf("towncrier-backup-%s-v%s.zip", now.Format("2006-01-02-150405"), ver)
}

// SupportBackupDialog allows the user to select which items (Config, Event DB, optional Logs) to export into a backup .zip.
type SupportBackupDialog struct {
	window   fyne.Window
	session  *app.Session
	onStatus func(string)

	IncludeConfigCheck *widget.Check
	IncludeDBCheck     *widget.Check
	IncludeLogsCheck   *widget.Check
	NotesEntry         *widget.Entry
	ExportBtn          *widget.Button
	CancelBtn          *widget.Button
	Content            fyne.CanvasObject
	modal              dialog.Dialog
}

// NewSupportBackupDialog constructs the Export Backup (.zip) dialog.
func NewSupportBackupDialog(win fyne.Window, session *app.Session, onStatus func(string)) *SupportBackupDialog {
	d := &SupportBackupDialog{
		window:   win,
		session:  session,
		onStatus: onStatus,
	}
	d.buildUI()
	return d
}

func (d *SupportBackupDialog) buildUI() {
	descLbl := widget.NewLabel(
		"Select what to include in your backup .zip archive for backing up, migrating between PCs, or sharing.\n" +
			"All OAuth tokens, WebSocket auth tokens, and translation API keys are automatically scrubbed.",
	)
	descLbl.Wrapping = fyne.TextWrapWord

	d.IncludeConfigCheck = widget.NewCheck("Configuration & Chatter Profiles (config-backup.json — secrets & API keys scrubbed)", nil)
	d.IncludeConfigCheck.SetChecked(true)

	dbSizeText := ""
	if d.session != nil && d.session.EventLogger() != nil {
		if sz := d.session.EventLogger().DBFileSize(); sz > 0 {
			dbSizeText = fmt.Sprintf(" (~%d KB)", max(sz/1024, 1))
		}
	}
	d.IncludeDBCheck = widget.NewCheck(fmt.Sprintf("Event Log & Chat History Database (events.db%s)", dbSizeText), nil)
	d.IncludeDBCheck.SetChecked(true)

	logPath := logging.LogPath()
	logSizeText := ""
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 0 {
		logSizeText = fmt.Sprintf(" (~%d KB)", max(fi.Size()/1024, 1))
	}
	d.IncludeLogsCheck = widget.NewCheck(fmt.Sprintf("Application Log (towncrier.log%s — optional, for troubleshooting)", logSizeText), nil)
	d.IncludeLogsCheck.SetChecked(false)

	d.NotesEntry = widget.NewMultiLineEntry()
	d.NotesEntry.SetMinRowsVisible(2)
	d.NotesEntry.SetPlaceHolder("Optional notes (saved as support-notes.txt inside the .zip)...")

	d.CancelBtn = widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), func() {
		if d.modal != nil {
			d.modal.Hide()
		}
	})

	d.ExportBtn = widget.NewButtonWithIcon("Export Backup (.zip)…", theme.DownloadIcon(), func() {
		opts := d.Options()
		if !opts.IncludeConfig && !opts.IncludeLogs && !opts.IncludeDatabase && strings.TrimSpace(opts.UserNotes) == "" {
			if d.onStatus != nil {
				d.onStatus("⚠️ Please select at least one item to include in the backup.")
			}
			return
		}
		if d.modal != nil {
			d.modal.Hide()
		}
		if d.window == nil {
			return
		}

		saveDlg := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				if d.onStatus != nil {
					d.onStatus("⚠️ Backup export failed: " + err.Error())
				}
				return
			}
			if writer == nil {
				return
			}
			defer func() { _ = writer.Close() }()

			files, zipErr := WriteSupportBackupZip(writer, d.session, opts)
			if zipErr != nil {
				if d.onStatus != nil {
					d.onStatus("⚠️ Failed to create backup archive: " + zipErr.Error())
				}
				dialog.ShowError(zipErr, d.window)
				return
			}
			savedPath := writer.URI().Path()
			if d.onStatus != nil {
				d.onStatus(fmt.Sprintf("✓ Backup exported to %s (%d files archived).", savedPath, len(files)))
			}
		}, d.window)

		saveDlg.SetFileName(DefaultBackupZipFilename(time.Now()))
		saveDlg.SetFilter(storage.NewExtensionFileFilter([]string{".zip"}))
		saveDlg.Resize(fyne.NewSize(720, 500))
		saveDlg.Show()
	})
	d.ExportBtn.Importance = widget.HighImportance

	btnRow := container.NewHBox(
		layout.NewSpacer(),
		d.CancelBtn,
		d.ExportBtn,
	)

	d.Content = container.NewVBox(
		descLbl,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Items to Include in Backup (.zip):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		d.IncludeConfigCheck,
		d.IncludeDBCheck,
		d.IncludeLogsCheck,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Optional Notes:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		d.NotesEntry,
		widget.NewSeparator(),
		btnRow,
	)
}

// Options returns the SupportBackupOptions selected in the dialog.
func (d *SupportBackupDialog) Options() SupportBackupOptions {
	var notes string
	if d.NotesEntry != nil {
		notes = d.NotesEntry.Text
	}
	return SupportBackupOptions{
		IncludeConfig:   d.IncludeConfigCheck == nil || d.IncludeConfigCheck.Checked,
		IncludeDatabase: d.IncludeDBCheck == nil || d.IncludeDBCheck.Checked,
		IncludeLogs:     d.IncludeLogsCheck != nil && d.IncludeLogsCheck.Checked,
		UserNotes:       notes,
	}
}

// Show displays the Export Backup (.zip) dialog over the parent window.
func (d *SupportBackupDialog) Show() {
	if d.window == nil {
		return
	}
	d.modal = dialog.NewCustomWithoutButtons("Export Backup (.zip)", d.Content, d.window)
	d.modal.Resize(fyne.NewSize(620, 400))
	d.modal.Show()
}
