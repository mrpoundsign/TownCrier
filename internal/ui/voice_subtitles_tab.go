package ui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/mrpoundsign/TownCrier/internal/ws"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/stt"
	"github.com/mrpoundsign/TownCrier/pkg/translate"
)

// VoiceSubtitleSilenceOptions lists selectable Silero VAD pause thresholds in milliseconds.
var VoiceSubtitleSilenceOptions = []string{
	"300 ms (Fast)",
	"450 ms (Default)",
	"650 ms (Relaxed)",
	"900 ms (Long Pause)",
}

// VoiceSubtitleThreadOptions lists selectable Whisper helper/CPU thread limits.
var VoiceSubtitleThreadOptions = []string{
	"Auto (Smart Cap — Recommended)",
	"1 Thread (Lowest CPU Impact)",
	"2 Threads (4-Core Laptop Friendly)",
	"4 Threads",
	"8 Threads",
}

// VoiceSubtitleFadeOptions lists selectable OBS subtitle auto-fade durations.
var VoiceSubtitleFadeOptions = []string{
	"5 seconds",
	"8 seconds",
	"10 seconds (Default)",
	"15 seconds",
	"30 seconds",
	"Never Fade",
}

// VoiceSubtitleScaleOptions lists selectable OBS subtitle font scales.
var VoiceSubtitleScaleOptions = []string{
	"75%",
	"100% (Default)",
	"125%",
	"150%",
	"200%",
}

// FormatSpokenLangOption formats an stt.SpokenLanguage into a UI dropdown option string.
func FormatSpokenLangOption(code string) string {
	norm := stt.NormalizeSpokenLang(code)
	if norm == "" {
		norm = "en"
	}
	return fmt.Sprintf("%s (%s)", stt.SpokenLanguageName(norm), norm)
}

// SpokenLangSelectOptions returns all selectable spoken language options for the Add Spoken Language pulldown.
func SpokenLangSelectOptions() []string {
	opts := make([]string, len(stt.SupportedSpokenLanguages))
	for i, l := range stt.SupportedSpokenLanguages {
		opts[i] = fmt.Sprintf("%s (%s)", l.Name, l.Code)
	}
	return opts
}

// ParseSpokenLangOption extracts the ISO-639-1 code from a SpokenLangSelectOptions entry.
func ParseSpokenLangOption(label string) string {
	trimmed := strings.TrimSpace(label)
	if idx := strings.LastIndexByte(trimmed, '('); idx >= 0 && strings.HasSuffix(trimmed, ")") {
		return stt.NormalizeSpokenLang(trimmed[idx+1 : len(trimmed)-1])
	}
	return stt.NormalizeSpokenLang(trimmed)
}

func formatSilenceOption(ms int) string {
	switch {
	case ms <= 350:
		return "300 ms (Fast)"
	case ms <= 520:
		return "450 ms (Default)"
	case ms <= 750:
		return "650 ms (Relaxed)"
	default:
		return "900 ms (Long Pause)"
	}
}

func parseSilenceOption(label string) int {
	var ms int
	if _, err := fmt.Sscanf(label, "%d", &ms); err == nil && ms >= 150 {
		return ms
	}
	return config.DefaultVoiceSubtitleMinSilence
}

func formatSubtitleFadeOption(sec int) string {
	if sec < 0 {
		return "Never Fade"
	}
	switch sec {
	case 5:
		return "5 seconds"
	case 8:
		return "8 seconds"
	case 15:
		return "15 seconds"
	case 30:
		return "30 seconds"
	default:
		return "10 seconds (Default)"
	}
}

func parseSubtitleFadeOption(label string) int {
	if strings.EqualFold(strings.TrimSpace(label), "Never Fade") {
		return -1
	}
	var sec int
	if _, err := fmt.Sscanf(label, "%d", &sec); err == nil && sec > 0 {
		return sec
	}
	return config.DefaultVoiceSubtitleFadeSeconds
}

func formatSubtitleScaleOption(pct int) string {
	switch pct {
	case 75:
		return "75%"
	case 125:
		return "125%"
	case 150:
		return "150%"
	case 200:
		return "200%"
	default:
		return "100% (Default)"
	}
}

func parseSubtitleScaleOption(label string) int {
	clean := strings.TrimSpace(label)
	if prefix, _, ok := strings.Cut(clean, "%"); ok {
		if v, err := strconv.Atoi(strings.TrimSpace(prefix)); err == nil && v >= 50 && v <= 300 {
			return v
		}
	}
	return config.DefaultVoiceSubtitleScale
}

func formatSTTThreadsOption(threads int) string {
	switch threads {
	case 1:
		return "1 Thread (Lowest CPU Impact)"
	case 2:
		return "2 Threads (4-Core Laptop Friendly)"
	case 4:
		return "4 Threads"
	case 8:
		return "8 Threads"
	default:
		return "Auto (Smart Cap — Recommended)"
	}
}

func parseSTTThreadsOption(label string) int {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(label), "%d", &n); err == nil && n >= 1 && n <= 16 {
		return n
	}
	return 0
}

// SetSTTGPUDeviceEnumerator overrides Vulkan GPU enumeration on SettingsView (used in headless unit tests).
func (v *SettingsView) SetSTTGPUDeviceEnumerator(fn func() ([]stt.GPUDevice, error)) {
	v.mu.Lock()
	v.sttGPUEnum = fn
	v.mu.Unlock()
}

func formatActiveSTTProviderBadge(prov string) string {
	trimmed := strings.TrimSpace(prov)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "vulkan:") {
		return strings.TrimSpace(trimmed[len("vulkan:"):])
	}
	return trimmed
}

func (v *SettingsView) refreshSTTComputeDeviceList(forceRefresh bool) {
	if v.vsComputeDeviceSelect == nil {
		return
	}

	var devs []stt.GPUDevice
	v.mu.Lock()
	enumFn := v.sttGPUEnum
	v.mu.Unlock()

	if forceRefresh && enumFn == nil {
		devs, _ = stt.RefreshGPUDevices()
	} else if enumFn != nil {
		if forceRefresh {
			// If using default enumerator, trigger a fresh probe
			_, _ = stt.RefreshGPUDevices()
		}
		devs, _ = enumFn()
	}

	const (
		optAuto = "Auto (Best Vulkan GPU / iGPU)"
		optIGPU = "Integrated iGPU Only (Save Gaming GPU)"
		optDGPU = "Discrete GPU Only (Fastest)"
		optCPU  = "CPU Only"
	)

	opts := []string{optAuto, optIGPU, optDGPU}
	devMap := map[string]string{
		optAuto: stt.ComputeDeviceAuto,
		optIGPU: stt.ComputeDeviceIGPU,
		optDGPU: stt.ComputeDeviceDGPU,
		optCPU:  stt.ComputeDeviceCPU,
	}

	for _, d := range devs {
		lbl := d.UILabel()
		val := d.ConfigValue()
		if _, exists := devMap[lbl]; !exists {
			opts = append(opts, lbl)
			devMap[lbl] = val
		}
	}
	opts = append(opts, optCPU)

	cfg := v.currentConfig()
	wantVal := stt.ComputeDeviceAuto
	if cfg != nil {
		wantVal = config.NormalizeVoiceSubtitleComputeDevice(cfg.VoiceSubtitles.ComputeDevice)
	}

	selectedLabel := optAuto
	matched := false
	for _, lbl := range opts {
		if strings.EqualFold(devMap[lbl], wantVal) {
			selectedLabel = lbl
			matched = true
			break
		}
	}
	if !matched && strings.HasPrefix(strings.ToLower(wantVal), "vulkan:") {
		// Preserve saved specific GPU name even if temporarily disconnected
		customLbl := strings.TrimSpace(wantVal[len("vulkan:"):]) + " (Saved GPU)"
		opts = slices.Insert(opts, len(opts)-1, customLbl)
		devMap[customLbl] = wantVal
		selectedLabel = customLbl
	}

	v.mu.Lock()
	v.vsComputeDeviceMap = devMap
	v.mu.Unlock()

	prevInit := v.initializing
	v.initializing = true
	v.vsComputeDeviceSelect.Options = opts
	v.vsComputeDeviceSelect.SetSelected(selectedLabel)
	v.vsComputeDeviceSelect.Refresh()
	v.initializing = prevInit
}

func (v *SettingsView) effectiveSTTModelDir() string {
	v.mu.Lock()
	dir := v.sttModelDir
	v.mu.Unlock()
	if strings.TrimSpace(dir) != "" {
		return dir
	}
	if v.session != nil {
		return v.session.STTModelDir()
	}
	if testing.Testing() {
		return ""
	}
	return stt.DefaultSTTModelDir()
}

// SyncSTTStatus updates the live VAD/Whisper status label on the Voice Subtitles tab.
func (v *SettingsView) SyncSTTStatus(speaking bool, transcribing bool, errText string) {
	fyne.Do(func() {
		if v.vsSTTStatusLabel == nil {
			return
		}
		if strings.TrimSpace(errText) != "" {
			v.vsSTTStatusLabel.SetText("⚠️ " + strings.TrimSpace(errText))
			return
		}
		switch {
		case transcribing:
			v.vsSTTStatusLabel.SetText("⏳ Transcribing & translating...")
		case speaking:
			v.vsSTTStatusLabel.SetText("🎙️ Speaking detected...")
		default:
			cfg := v.currentConfig()
			fam := config.DefaultVoiceSubtitleModel
			if cfg != nil {
				fam = config.NormalizeVoiceSubtitleModel(cfg.VoiceSubtitles.ModelFamily)
			}
			if stt.IsModelInstalled(v.effectiveSTTModelDir(), fam) {
				status := "🟢 Ready"
				if v.session != nil {
					if prov := formatActiveSTTProviderBadge(v.session.ActiveSTTProvider()); prov != "" {
						status += " • " + prov
					}
					if ms := v.session.LastSTTDecodeMs(); ms > 0 {
						status += fmt.Sprintf(" • %d ms", ms)
					}
				}
				v.vsSTTStatusLabel.SetText(status)
			} else {
				v.vsSTTStatusLabel.SetText("⚪ Model Not Installed")
			}
		}
	})
}

// SyncVoiceSubtitlePreview renders the latest stacked multi-language subtitle event in the live preview panel.
func (v *SettingsView) SyncVoiceSubtitlePreview(ev ws.SubtitleEvent) {
	fyne.Do(func() {
		if v.vsLivePreviewLabel == nil {
			return
		}
		if ev.Kind == "clear" {
			v.vsLivePreviewLabel.SetText("Cleared active subtitle card.")
			return
		}
		if len(ev.Lines) == 0 {
			return
		}
		var b strings.Builder
		for i, ln := range ev.Lines {
			if i > 0 {
				b.WriteByte('\n')
			}
			tag := ""
			if ln.IsOriginal {
				tag = " (Spoken)"
			}
			fmt.Fprintf(&b, "[%s]%s  %s", ln.Label, tag, ln.Text)
		}
		v.vsLivePreviewLabel.SetText(b.String())
	})
}

func (v *SettingsView) refreshVoiceSubtitleModelStatus() {
	if v.vsModelStatusLabel == nil || v.vsDownloadModelBtn == nil {
		return
	}
	cfg := v.currentConfig()
	fam := config.DefaultVoiceSubtitleModel
	if cfg != nil {
		fam = config.NormalizeVoiceSubtitleModel(cfg.VoiceSubtitles.ModelFamily)
	}
	spec, _ := stt.ModelSpecByID(fam)
	dir := v.effectiveSTTModelDir()
	ggmlInstalled := stt.IsGGMLModelInstalled(dir, fam)
	legacyInstalled := stt.IsSherpaModelInstalled(dir, fam)

	switch {
	case ggmlInstalled:
		v.vsModelStatusLabel.TextStyle = fyne.TextStyle{}
		v.vsModelStatusLabel.SetText(fmt.Sprintf("✓ %s + Silero VAD installed (Vulkan GPU / iGPU / CPU ready, SHA-256 verified).", spec.DisplayName))
		v.vsDownloadModelBtn.SetText("✓ GPU Model Installed")
		v.vsDownloadModelBtn.Importance = widget.LowImportance
		v.vsDownloadModelBtn.Disable()
		if !testing.Testing() {
			go func(checkDir, checkFam string, checkSpec stt.ModelSpec) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				updateAvail, _, _ := stt.CheckSTTModelUpdate(ctx, checkDir, checkFam)
				if !updateAvail {
					return
				}
				fyne.Do(func() {
					curCfg := v.currentConfig()
					if curCfg == nil || config.NormalizeVoiceSubtitleModel(curCfg.VoiceSubtitles.ModelFamily) != checkFam {
						return
					}
					if v.vsModelStatusLabel == nil || v.vsDownloadModelBtn == nil {
						return
					}
					v.vsModelStatusLabel.TextStyle = fyne.TextStyle{Bold: true}
					v.vsModelStatusLabel.SetText(fmt.Sprintf("🔄 MODEL UPDATE AVAILABLE: A newer SHA-256 verified version of %s is available.\n👉 Click '🔄 Update GPU / iGPU Model (~%d MB)' on the right to update.", checkSpec.DisplayName, checkSpec.ApproxSizeMB))
					v.vsDownloadModelBtn.SetText(fmt.Sprintf("🔄 Update GPU / iGPU Model (~%d MB)", checkSpec.ApproxSizeMB))
					v.vsDownloadModelBtn.Importance = widget.HighImportance
					v.vsDownloadModelBtn.Enable()
					v.vsModelStatusLabel.Refresh()
					v.vsDownloadModelBtn.Refresh()
				})
			}(dir, fam, spec)
		}
	case legacyInstalled:
		v.vsModelStatusLabel.TextStyle = fyne.TextStyle{Bold: true}
		v.vsModelStatusLabel.SetText(fmt.Sprintf("⚠️ GPU / iGPU LOCKED: Only legacy CPU-only weights are installed for %s.\n👉 Click '⬇ Upgrade to GPU / iGPU Model (~%d MB)' on the right to unlock GPU & iGPU selection!", spec.DisplayName, spec.ApproxSizeMB))
		v.vsDownloadModelBtn.SetText(fmt.Sprintf("⬇ Upgrade to GPU / iGPU Model (~%d MB)", spec.ApproxSizeMB))
		v.vsDownloadModelBtn.Importance = widget.HighImportance
		v.vsDownloadModelBtn.Enable()
	default:
		v.vsModelStatusLabel.TextStyle = fyne.TextStyle{Bold: true}
		v.vsModelStatusLabel.SetText(fmt.Sprintf("⚠️ MODEL NOT INSTALLED: GPU / iGPU selection is locked until %s is downloaded.\n👉 Click '⬇ Download GPU / iGPU Model (~%d MB)' on the right to install!", spec.DisplayName, spec.ApproxSizeMB))
		v.vsDownloadModelBtn.SetText(fmt.Sprintf("⬇ Download GPU / iGPU Model (~%d MB)", spec.ApproxSizeMB))
		v.vsDownloadModelBtn.Importance = widget.HighImportance
		v.vsDownloadModelBtn.Enable()
	}
	v.vsModelStatusLabel.Refresh()
	v.vsDownloadModelBtn.Refresh()
	v.syncComputeDeviceLockState(ggmlInstalled, legacyInstalled, spec)
}

func (v *SettingsView) syncComputeDeviceLockState(ggmlInstalled, legacyInstalled bool, spec stt.ModelSpec) {
	if v.vsComputeDeviceSelect == nil {
		return
	}
	if ggmlInstalled {
		v.vsComputeDeviceSelect.Enable()
		v.refreshSTTComputeDeviceList(false)
		return
	}

	lockedMsg := fmt.Sprintf("🔒 CPU Only — Click '⬇ Download GPU / iGPU Model (~%d MB)' Above to Unlock GPU / iGPU", spec.ApproxSizeMB)
	if legacyInstalled {
		lockedMsg = fmt.Sprintf("🔒 CPU Only — Click '⬇ Upgrade to GPU / iGPU Model (~%d MB)' Above to Unlock GPU / iGPU", spec.ApproxSizeMB)
	}

	prevInit := v.initializing
	v.initializing = true
	v.vsComputeDeviceSelect.Options = []string{lockedMsg}
	v.vsComputeDeviceSelect.SetSelected(lockedMsg)
	v.vsComputeDeviceSelect.Disable()
	v.vsComputeDeviceSelect.Refresh()
	v.initializing = prevInit
}

func (v *SettingsView) syncVoiceSubtitlesControlsFromConfig() {
	cfg := v.currentConfig()
	if cfg == nil {
		return
	}
	vs := cfg.VoiceSubtitles
	config.NormalizeVoiceSubtitlesConfig(&vs)

	if v.vsModelSelect != nil {
		v.vsModelSelect.SetSelected(stt.ModelLabelForID(vs.ModelFamily))
	}
	v.refreshSTTComputeDeviceList(false)
	if v.vsThreadsSelect != nil {
		v.vsThreadsSelect.SetSelected(formatSTTThreadsOption(vs.NumThreads))
	}
	if v.vsSilenceSelect != nil {
		v.vsSilenceSelect.SetSelected(formatSilenceOption(vs.MinSilenceMs))
	}
	if v.vsOverlayPosSelect != nil {
		if vs.OverlayPosition == "top" {
			v.vsOverlayPosSelect.SetSelected("Top")
		} else {
			v.vsOverlayPosSelect.SetSelected("Bottom")
		}
	}
	if v.vsOverlayAlignSelect != nil {
		if vs.OverlayAlign == "left" {
			v.vsOverlayAlignSelect.SetSelected("Left")
		} else {
			v.vsOverlayAlignSelect.SetSelected("Center")
		}
	}
	if v.vsOverlayFadeSelect != nil {
		v.vsOverlayFadeSelect.SetSelected(formatSubtitleFadeOption(vs.OverlayFadeSeconds))
	}
	if v.vsOverlayScaleSelect != nil {
		v.vsOverlayScaleSelect.SetSelected(formatSubtitleScaleOption(vs.OverlayScale))
	}
	if v.vsRefreshSpokenList != nil {
		v.vsRefreshSpokenList()
	}
	if v.vsRefreshTargetList != nil {
		v.vsRefreshTargetList()
	}
	v.refreshVoiceSubtitleModelStatus()
}

func (v *SettingsView) buildVoiceSubtitlesTab() fyne.CanvasObject {
	v.updateConfig(func(c *config.Config) {
		config.NormalizeVoiceSubtitlesConfig(&c.VoiceSubtitles)
	})
	cfg := v.currentConfig()
	vs := cfg.VoiceSubtitles
	config.NormalizeVoiceSubtitlesConfig(&vs)

	makeRow := func(label string, ctrl fyne.CanvasObject) *fyne.Container {
		return container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(185, 36), widget.NewLabel(label)), nil, ctrl)
	}

	// --- Card 1: Microphone, Silero VAD & Offline Whisper Model ---
	v.vsMicDeviceSelect = widget.NewSelect([]string{"Default Microphone"}, func(selected string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		devHex := v.micDeviceMap[selected]
		v.mu.Unlock()

		v.updateConfig(func(c *config.Config) {
			c.Audio.MicDeviceName = selected
			c.Audio.MicDeviceID = devHex
		})
		if v.micDeviceSelect != nil && v.micDeviceSelect.Selected != selected {
			v.initializing = true
			v.micDeviceSelect.SetSelected(selected)
			v.initializing = false
		}
		v.setStatus("✓ Active microphone input: " + selected)
	})

	v.vsMicRefreshBtn = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		v.refreshCaptureDeviceList()
		v.setStatus("Microphone input list refreshed.")
	})
	v.refreshCaptureDeviceList()

	v.vsMicLevelBar = widget.NewProgressBar()
	v.vsMicLevelBar.Min = 0.0
	v.vsMicLevelBar.Max = 1.0
	v.vsMicLevelBar.SetValue(0.0)
	v.vsMicLevelBar.TextFormatter = func() string { return "" }

	v.vsSTTStatusLabel = widget.NewLabel("⚪ Idle")

	v.vsModelSelect = widget.NewSelect(stt.ModelDisplayLabels(), func(selected string) {
		if v.initializing {
			return
		}
		fam := stt.ModelIDFromLabel(selected)
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.ModelFamily = fam
		})
		v.refreshVoiceSubtitleModelStatus()
		v.setStatus("✓ Offline STT model set to " + stt.ModelLabelForID(fam))
	})
	v.vsModelSelect.SetSelected(stt.ModelLabelForID(vs.ModelFamily))

	v.vsModelStatusLabel = widget.NewLabel("")
	v.vsModelStatusLabel.Wrapping = fyne.TextWrapWord

	v.vsDownloadModelBtn = widget.NewButtonWithIcon("Download Model", theme.DownloadIcon(), func() {
		curCfg := v.currentConfig()
		fam := config.NormalizeVoiceSubtitleModel(curCfg.VoiceSubtitles.ModelFamily)
		spec, ok := stt.ModelSpecByID(fam)
		if !ok {
			return
		}
		targetDir := v.effectiveSTTModelDir()

		dlCtx, cancel := context.WithCancel(context.Background())
		modal := NewModelDownloadModal(v.window, spec.DisplayName, spec.ApproxSizeMB, cancel)

		go func() {
			err := stt.ForceDownloadModelWithContext(dlCtx, targetDir, spec.ID, func(cur, tot int64) {
				modal.UpdateProgress(cur, tot)
			})
			cancelled := modal.IsCancelling()
			modal.Dismiss()

			fyne.Do(func() {
				v.refreshVoiceSubtitleModelStatus()
				if cancelled {
					v.setStatus("Model download cancelled.")
					return
				}
				if err != nil {
					v.setStatus("⚠️ STT model download failed: " + err.Error())
					if v.window != nil {
						dialog.ShowError(err, v.window)
					}
					return
				}
				if v.session != nil {
					v.session.ReloadSTTEngine()
				}
				v.SyncSTTStatus(false, false, "")
				v.setStatus("✓ Downloaded and initialized " + spec.DisplayName + ".")
			})
		}()
	})

	v.vsComputeDeviceSelect = widget.NewSelect([]string{"Auto (Best Vulkan GPU / iGPU)"}, func(selected string) {
		if v.initializing {
			return
		}
		v.mu.Lock()
		devVal, ok := v.vsComputeDeviceMap[selected]
		v.mu.Unlock()
		if !ok || devVal == "" {
			devVal = stt.ComputeDeviceAuto
		}
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.ComputeDevice = devVal
			c.VoiceSubtitles.UseGPU = devVal != stt.ComputeDeviceCPU
		})
		v.SyncSTTStatus(false, false, "")
		v.setStatus("✓ Whisper compute device set to " + selected)
	})

	v.vsComputeRefreshBtn = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() {
		cfg := v.currentConfig()
		fam := config.DefaultVoiceSubtitleModel
		if cfg != nil {
			fam = config.NormalizeVoiceSubtitleModel(cfg.VoiceSubtitles.ModelFamily)
		}
		if !stt.IsGGMLModelInstalled(v.effectiveSTTModelDir(), fam) {
			v.refreshVoiceSubtitleModelStatus()
			v.setStatus("⚠️ Click '⬇ Upgrade / Download GPU Model' right above Compute Device first to unlock GPU & iGPU selection.")
			return
		}
		v.refreshSTTComputeDeviceList(true)
		v.setStatus("✓ Refreshed Vulkan GPU / iGPU adapter list.")
	})
	v.refreshSTTComputeDeviceList(false)

	v.vsThreadsSelect = widget.NewSelect(VoiceSubtitleThreadOptions, func(selected string) {
		if v.initializing {
			return
		}
		th := parseSTTThreadsOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.NumThreads = th
		})
		v.SyncSTTStatus(false, false, "")
		v.setStatus("✓ Whisper CPU helper threads set to " + selected)
	})
	v.vsThreadsSelect.SetSelected(formatSTTThreadsOption(vs.NumThreads))

	v.vsSilenceSelect = widget.NewSelect(VoiceSubtitleSilenceOptions, func(selected string) {
		if v.initializing {
			return
		}
		ms := parseSilenceOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.MinSilenceMs = ms
		})
		v.setStatus(fmt.Sprintf("✓ VAD phrase pause set to %d ms.", ms))
	})
	v.vsSilenceSelect.SetSelected(formatSilenceOption(vs.MinSilenceMs))

	// Spoken Languages pulldown + Add button + configured list
	spokenOpts := SpokenLangSelectOptions()
	v.vsSpokenAddSelect = widget.NewSelect(spokenOpts, nil)
	if len(spokenOpts) > 0 {
		v.vsSpokenAddSelect.SetSelected(spokenOpts[0])
	}

	v.vsSpokenListBox = container.NewVBox()
	v.vsRefreshSpokenList = func() {
		if v.vsSpokenListBox == nil {
			return
		}
		cur := v.currentConfig().VoiceSubtitles
		config.NormalizeVoiceSubtitlesConfig(&cur)
		v.vsSpokenListBox.Objects = nil

		for _, code := range cur.SpokenLanguages {
			langCode := code
			badge := widget.NewLabelWithStyle("["+translate.LanguageBadge(langCode)+"]", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			nameLbl := widget.NewLabel(stt.SpokenLanguageName(langCode))
			rmBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				v.updateConfig(func(c *config.Config) {
					config.NormalizeVoiceSubtitlesConfig(&c.VoiceSubtitles)
					if len(c.VoiceSubtitles.SpokenLanguages) <= 1 {
						return
					}
					c.VoiceSubtitles.SpokenLanguages = slices.DeleteFunc(c.VoiceSubtitles.SpokenLanguages, func(s string) bool {
						return s == langCode
					})
				})
				if v.vsRefreshSpokenList != nil {
					v.vsRefreshSpokenList()
				}
				v.setStatus("✓ Removed spoken language: " + stt.SpokenLanguageName(langCode))
			})
			if len(cur.SpokenLanguages) <= 1 {
				rmBtn.Disable()
			}
			row := container.NewBorder(nil, nil, badge, rmBtn, nameLbl)
			v.vsSpokenListBox.Add(row)
		}
		v.vsSpokenListBox.Refresh()
	}

	v.vsAddSpokenBtn = widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() {
		code := ParseSpokenLangOption(v.vsSpokenAddSelect.Selected)
		if code == "" {
			return
		}
		already := false
		v.updateConfig(func(c *config.Config) {
			config.NormalizeVoiceSubtitlesConfig(&c.VoiceSubtitles)
			if slices.Contains(c.VoiceSubtitles.SpokenLanguages, code) {
				already = true
				return
			}
			c.VoiceSubtitles.SpokenLanguages = append(c.VoiceSubtitles.SpokenLanguages, code)
		})
		if already {
			v.setStatus(stt.SpokenLanguageName(code) + " is already in your Spoken Languages list.")
			return
		}
		if v.vsRefreshSpokenList != nil {
			v.vsRefreshSpokenList()
		}
		v.setStatus("✓ Added spoken language: " + stt.SpokenLanguageName(code))
	})
	v.vsAddSpokenBtn.Importance = widget.HighImportance
	v.vsRefreshSpokenList()

	spokenDesc := widget.NewLabelWithStyle(
		"Select all languages you may speak on stream (supports bilingual and mixed-language / code-switching speech). If 1 language is listed, Whisper locks to it; if 2+ are listed, Whisper auto-detects per phrase.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	spokenDesc.Wrapping = fyne.TextWrapWord

	sttCard := widget.NewCard(
		"Offline Speech Recognition (Microphone, VAD & Whisper GPU / iGPU / CPU)",
		"100% local speech-to-text powered by Silero VAD and SHA-256 verified Whisper (Vulkan NVIDIA/AMD/Intel GPUs, iGPUs, or CPU)",
		container.NewVBox(
			makeRow("Microphone Input:", container.NewBorder(nil, nil, nil, v.vsMicRefreshBtn, v.vsMicDeviceSelect)),
			makeRow("Live Mic & Engine:", container.NewBorder(nil, nil, nil, v.vsSTTStatusLabel, v.vsMicLevelBar)),
			widget.NewSeparator(),
			makeRow("Offline Whisper Model:", v.vsModelSelect),
			container.NewBorder(nil, nil, nil, v.vsDownloadModelBtn, v.vsModelStatusLabel),
			makeRow("Compute Device:", container.NewBorder(nil, nil, nil, v.vsComputeRefreshBtn, v.vsComputeDeviceSelect)),
			makeRow("CPU Helper Threads:", v.vsThreadsSelect),
			makeRow("Phrase End Pause:", v.vsSilenceSelect),
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Spoken Languages (Microphone Input)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			spokenDesc,
			makeRow("Add Spoken Language:", container.NewBorder(nil, nil, nil, v.vsAddSpokenBtn, v.vsSpokenAddSelect)),
			v.vsSpokenListBox,
		),
	)

	// --- Card 2: Configured OBS Target Display Languages ---
	targetOpts := TranslationTargetLangOptions()
	v.vsTargetAddSelect = widget.NewSelect(targetOpts, nil)
	if len(targetOpts) > 0 {
		v.vsTargetAddSelect.SetSelected(targetOpts[0])
	}

	moveTargetLang := func(fromIdx, delta int) {
		v.updateConfig(func(c *config.Config) {
			config.NormalizeVoiceSubtitlesConfig(&c.VoiceSubtitles)
			list := c.VoiceSubtitles.TargetLanguages
			toIdx := fromIdx + delta
			if fromIdx < 0 || fromIdx >= len(list) || toIdx < 0 || toIdx >= len(list) {
				return
			}
			list[fromIdx], list[toIdx] = list[toIdx], list[fromIdx]
		})
		if v.vsRefreshTargetList != nil {
			v.vsRefreshTargetList()
		}
	}

	v.vsTargetListBox = container.NewVBox()
	v.vsRefreshTargetList = func() {
		if v.vsTargetListBox == nil {
			return
		}
		cur := v.currentConfig().VoiceSubtitles
		config.NormalizeVoiceSubtitlesConfig(&cur)
		v.vsTargetListBox.Objects = nil

		for i, code := range cur.TargetLanguages {
			rowIdx := i
			langCode := code

			upBtn := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				moveTargetLang(rowIdx, -1)
			})
			if rowIdx == 0 {
				upBtn.Disable()
			}
			downBtn := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
				moveTargetLang(rowIdx, 1)
			})
			if rowIdx == len(cur.TargetLanguages)-1 {
				downBtn.Disable()
			}
			rmBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				v.updateConfig(func(c *config.Config) {
					config.NormalizeVoiceSubtitlesConfig(&c.VoiceSubtitles)
					if len(c.VoiceSubtitles.TargetLanguages) <= 1 {
						return
					}
					c.VoiceSubtitles.TargetLanguages = slices.DeleteFunc(c.VoiceSubtitles.TargetLanguages, func(s string) bool {
						return s == langCode
					})
				})
				if v.vsRefreshTargetList != nil {
					v.vsRefreshTargetList()
				}
				v.setStatus("✓ Removed display language: " + translate.LanguageName(langCode))
			})
			if len(cur.TargetLanguages) <= 1 {
				rmBtn.Disable()
			}

			left := container.NewHBox(
				upBtn,
				downBtn,
				widget.NewLabelWithStyle(fmt.Sprintf("#%d [%s]", rowIdx+1, translate.LanguageBadge(langCode)), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			)
			row := container.NewBorder(nil, nil, left, rmBtn, widget.NewLabel(translate.LanguageName(langCode)))
			v.vsTargetListBox.Add(row)
		}
		v.vsTargetListBox.Refresh()
	}

	v.vsAddTargetBtn = widget.NewButtonWithIcon("Add Language", theme.ContentAddIcon(), func() {
		code := ParseTranslationTargetLangOption(v.vsTargetAddSelect.Selected)
		if code == "" {
			return
		}
		already := false
		v.updateConfig(func(c *config.Config) {
			config.NormalizeVoiceSubtitlesConfig(&c.VoiceSubtitles)
			if slices.Contains(c.VoiceSubtitles.TargetLanguages, code) {
				already = true
				return
			}
			c.VoiceSubtitles.TargetLanguages = append(c.VoiceSubtitles.TargetLanguages, code)
		})
		if already {
			v.setStatus(translate.LanguageName(code) + " is already in your OBS Display Languages list.")
			return
		}
		if v.vsRefreshTargetList != nil {
			v.vsRefreshTargetList()
		}
		v.setStatus("✓ Added OBS display language: " + translate.LanguageName(code))
	})
	v.vsAddTargetBtn.Importance = widget.HighImportance
	v.vsRefreshTargetList()

	v.vsOpenTranslationTabBtn = widget.NewButtonWithIcon("Configure Shared Translation Providers", theme.SettingsIcon(), func() {
		if v.subTabs != nil && v.translationTabItem != nil {
			v.SyncPlatformTabVisibility()
			v.subTabs.Select(v.translationTabItem)
		}
	})

	targetDesc := widget.NewLabelWithStyle(
		"All listed languages are translated concurrently and stacked top-to-bottom on the OBS Subtitle Overlay. Use ▲/▼ to reorder rows.",
		fyne.TextAlignLeading,
		fyne.TextStyle{Italic: true},
	)
	targetDesc.Wrapping = fyne.TextWrapWord

	targetsCard := widget.NewCard(
		"OBS Display Languages (Configured Target List)",
		"Add, remove, and reorder the languages displayed simultaneously on stream",
		container.NewVBox(
			targetDesc,
			makeRow("Select Language:", container.NewBorder(nil, nil, nil, v.vsAddTargetBtn, v.vsTargetAddSelect)),
			v.vsTargetListBox,
			widget.NewSeparator(),
			v.vsOpenTranslationTabBtn,
		),
	)

	// --- Card 3: OBS Subtitle Overlay Layout & Live Preview ---
	v.vsOverlayPosSelect = widget.NewSelect([]string{"Bottom", "Top"}, func(selected string) {
		if v.initializing {
			return
		}
		pos := "bottom"
		if strings.EqualFold(strings.TrimSpace(selected), "Top") {
			pos = "top"
		}
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.OverlayPosition = pos
		})
	})
	if vs.OverlayPosition == "top" {
		v.vsOverlayPosSelect.SetSelected("Top")
	} else {
		v.vsOverlayPosSelect.SetSelected("Bottom")
	}

	v.vsOverlayAlignSelect = widget.NewSelect([]string{"Center", "Left"}, func(selected string) {
		if v.initializing {
			return
		}
		align := "center"
		if strings.EqualFold(strings.TrimSpace(selected), "Left") {
			align = "left"
		}
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.OverlayAlign = align
		})
	})
	if vs.OverlayAlign == "left" {
		v.vsOverlayAlignSelect.SetSelected("Left")
	} else {
		v.vsOverlayAlignSelect.SetSelected("Center")
	}

	v.vsOverlayFadeSelect = widget.NewSelect(VoiceSubtitleFadeOptions, func(selected string) {
		if v.initializing {
			return
		}
		sec := parseSubtitleFadeOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.OverlayFadeSeconds = sec
		})
	})
	v.vsOverlayFadeSelect.SetSelected(formatSubtitleFadeOption(vs.OverlayFadeSeconds))

	v.vsOverlayScaleSelect = widget.NewSelect(VoiceSubtitleScaleOptions, func(selected string) {
		if v.initializing {
			return
		}
		pct := parseSubtitleScaleOption(selected)
		v.updateConfig(func(c *config.Config) {
			c.VoiceSubtitles.OverlayScale = pct
		})
	})
	v.vsOverlayScaleSelect.SetSelected(formatSubtitleScaleOption(vs.OverlayScale))

	v.vsCopyOverlayURLBtn = widget.NewButtonWithIcon("📋 Copy OBS Subtitles URL", theme.ContentCopyIcon(), func() {
		targetURL := v.overlayURL("/overlay/subtitles", nil)
		if app := fyne.CurrentApp(); app != nil && app.Clipboard() != nil {
			app.Clipboard().SetContent(targetURL)
		}
		v.setStatus("✓ Copied OBS Voice Subtitles URL: " + targetURL)
	})
	v.vsCopyOverlayURLBtn.Importance = widget.HighImportance

	v.vsCustomizeStyleBtn = widget.NewButtonWithIcon("🎨 Customize Style", theme.ColorPaletteIcon(), func() {
		v.openOverlayDesignerTab("subtitles")
	})

	v.vsClearSubtitleBtn = widget.NewButtonWithIcon("Clear Active Subtitle", theme.ContentClearIcon(), func() {
		if v.session != nil {
			v.session.ClearVoiceSubtitles()
		}
		v.setStatus("✓ Cleared active OBS voice subtitle.")
	})

	v.vsTestPhraseEntry = NewScrollPassEntry()
	v.vsTestPhraseEntry.SetPlaceHolder("Enter a spoken or mixed-language phrase to test multi-language OBS subtitles...")
	v.vsTestPhraseEntry.SetText("Hello everyone, welcome back to the stream!")

	v.vsLivePreviewLabel = widget.NewLabelWithStyle("Speak into your microphone or click 'Test' to preview stacked multi-language subtitles.", fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	v.vsLivePreviewLabel.Wrapping = fyne.TextWrapWord

	v.vsTestSubtitleBtn = widget.NewButtonWithIcon("Test", theme.MediaPlayIcon(), func() {
		phrase := strings.TrimSpace(v.vsTestPhraseEntry.GetText())
		if phrase == "" {
			phrase = "Hello everyone, welcome back to the stream!"
		}
		v.vsTestSubtitleBtn.Disable()
		v.vsLivePreviewLabel.SetText("⏳ Translating across configured target languages...")

		go func(input string) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()

			cur := v.currentConfig().VoiceSubtitles
			config.NormalizeVoiceSubtitlesConfig(&cur)
			detLang := stt.ResolveSpokenLang("", input, cur.SpokenLanguages)

			var (
				ev  ws.SubtitleEvent
				err error
			)
			if v.session != nil {
				ev, err = v.session.ProcessVoiceSubtitleText(ctx, input, detLang)
			} else {
				svc := translate.NewService()
				items, trErr := svc.TranslateMulti(ctx, input, detLang, cur.TargetLanguages, config.TranslationRoutingPriority, []translate.Options{
					{Provider: config.TranslationProviderMyMemory},
				})
				err = trErr
				ev = ws.SubtitleEvent{Kind: "subtitle", SourceLang: detLang, OriginalText: input}
				for _, it := range items {
					ev.Lines = append(ev.Lines, ws.SubtitleLine{
						Lang:       it.Lang,
						Label:      it.LanguageBadge,
						Text:       it.Text,
						IsOriginal: it.IsOriginal,
					})
				}
			}

			fyne.Do(func() {
				v.vsTestSubtitleBtn.Enable()
				if len(ev.Lines) > 0 {
					v.SyncVoiceSubtitlePreview(ev)
					v.setStatus("✓ Sent multi-language test subtitle to preview & OBS overlay.")
					return
				}
				if err != nil {
					v.vsLivePreviewLabel.SetText("⚠️ Subtitle test error: " + err.Error())
					v.setStatus("⚠️ Subtitle test failed: " + err.Error())
				}
			})
		}(phrase)
	})

	layoutGrid := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, widget.NewLabel("Position:"), nil, v.vsOverlayPosSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Align:"), nil, v.vsOverlayAlignSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Auto-Fade:"), nil, v.vsOverlayFadeSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Scale:"), nil, v.vsOverlayScaleSelect),
	)

	overlayActions := container.NewGridWithColumns(2, v.vsCopyOverlayURLBtn, v.vsCustomizeStyleBtn)

	overlayCard := widget.NewCard(
		"OBS Subtitle Overlay & Live Preview (/overlay/subtitles)",
		"Paste the Browser Source URL into OBS (1200×350 or 1920×1080) to show live multi-language subtitles",
		container.NewVBox(
			layoutGrid,
			overlayActions,
			v.vsClearSubtitleBtn,
			widget.NewSeparator(),
			makeRow("Test Phrase:", container.NewBorder(nil, nil, nil, v.vsTestSubtitleBtn, v.vsTestPhraseEntry)),
			v.vsLivePreviewLabel,
		),
	)

	v.refreshVoiceSubtitleModelStatus()
	v.SyncSTTStatus(false, false, "")

	return newPanelVScroll(container.NewVBox(
		sttCard,
		targetsCard,
		overlayCard,
	))
}
