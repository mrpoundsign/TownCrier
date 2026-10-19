package tts

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	ModelKokoro     = "kokoro"
	ModelKokoroIntl = "kokoro_intl"
	ModelSupertonic = "supertonic"
	ModelKittenMini = "kitten_mini"
	ModelKittenNano = "kitten_nano"
	ModelPiper      = "piper"
)

const (
	PresetUltraLow = "ultra_low"
	PresetFastLean = "fast_lean"
	PresetBalanced = "balanced"
	PresetBeefy    = "beefy"
	PresetCustom   = "custom"
)

// ModelFamilySpec describes a downloadable neural TTS model pack and its resource characteristics.
type ModelFamilySpec struct {
	ID             string
	Name           string
	ShortName      string
	SubDir         string
	DownloadSizeMB int
	IdleRAMMB      int
	ActiveRAMMB    int
	GPUIdleRAMMB   int
	GPUActiveRAMMB int
	CPURTF         float64 // Real-Time Factor on 4-thread CPU (lower = faster)
	GPURTF         float64 // Real-Time Factor on DirectML GPU
	SupportsGPU    bool
	Summary        string
	Voices         []VoiceOption
}

// SupertonicVoiceCatalog lists the 10 built-in voices in Supertonic INT8 (0..9 = F1..F5, M1..M5).
var SupertonicVoiceCatalog = []VoiceOption{
	{ID: "supertonic:f1", Label: "Supertonic F1 — Warm US Female (supertonic:f1)", SIDv019: 0},
	{ID: "supertonic:f2", Label: "Supertonic F2 — Bright US Female (supertonic:f2)", SIDv019: 1},
	{ID: "supertonic:f3", Label: "Supertonic F3 — Calm US Female (supertonic:f3)", SIDv019: 2},
	{ID: "supertonic:f4", Label: "Supertonic F4 — Expressive US Female (supertonic:f4)", SIDv019: 3},
	{ID: "supertonic:f5", Label: "Supertonic F5 — Clear US Female (supertonic:f5)", SIDv019: 4},
	{ID: "supertonic:m1", Label: "Supertonic M1 — Light Tenor Male (supertonic:m1)", SIDv019: 5},
	{ID: "supertonic:m2", Label: "Supertonic M2 — Deep US Male (supertonic:m2)", SIDv019: 6},
	{ID: "supertonic:m3", Label: "Supertonic M3 — Calm Baritone Male (supertonic:m3)", SIDv019: 7},
	{ID: "supertonic:m4", Label: "Supertonic M4 — Narrator US Male (supertonic:m4)", SIDv019: 8},
	{ID: "supertonic:m5", Label: "Supertonic M5 — Deep Casual Male (supertonic:m5)", SIDv019: 9},
}

// KittenMiniVoiceCatalog lists the 8 built-in voices in Kitten v0.8 Mini (odd=female, even=male per model metadata).
var KittenMiniVoiceCatalog = []VoiceOption{
	{ID: "kitten_mini:f1", Label: "Kitten Mini F1 — Bright US Female (kitten_mini:f1)", SIDv019: 1},
	{ID: "kitten_mini:f2", Label: "Kitten Mini F2 — Soft US Female (kitten_mini:f2)", SIDv019: 3},
	{ID: "kitten_mini:f3", Label: "Kitten Mini F3 — Clear US Female (kitten_mini:f3)", SIDv019: 5},
	{ID: "kitten_mini:f4", Label: "Kitten Mini F4 — High Anime Female (kitten_mini:f4)", SIDv019: 7},
	{ID: "kitten_mini:m1", Label: "Kitten Mini M1 — Deep US Male (kitten_mini:m1)", SIDv019: 2},
	{ID: "kitten_mini:m2", Label: "Kitten Mini M2 — Tenor US Male (kitten_mini:m2)", SIDv019: 0},
	{ID: "kitten_mini:m3", Label: "Kitten Mini M3 — Light Tenor Male (kitten_mini:m3)", SIDv019: 4},
	{ID: "kitten_mini:m4", Label: "Kitten Mini M4 — Androgynous / High Pitch (kitten_mini:m4)", SIDv019: 6},
}

// KittenNanoVoiceCatalog lists the 8 built-in voices in Kitten v0.8 Nano INT8 (odd=female, even=male per model metadata).
var KittenNanoVoiceCatalog = []VoiceOption{
	{ID: "kitten_nano:f1", Label: "Kitten Nano F1 — Bright US Female (kitten_nano:f1)", SIDv019: 1},
	{ID: "kitten_nano:f2", Label: "Kitten Nano F2 — Soft US Female (kitten_nano:f2)", SIDv019: 3},
	{ID: "kitten_nano:f3", Label: "Kitten Nano F3 — Clear US Female (kitten_nano:f3)", SIDv019: 5},
	{ID: "kitten_nano:f4", Label: "Kitten Nano F4 — High Anime Female (kitten_nano:f4)", SIDv019: 7},
	{ID: "kitten_nano:m1", Label: "Kitten Nano M1 — Deep US Male (kitten_nano:m1)", SIDv019: 2},
	{ID: "kitten_nano:m2", Label: "Kitten Nano M2 — Tenor US Male (kitten_nano:m2)", SIDv019: 0},
	{ID: "kitten_nano:m3", Label: "Kitten Nano M3 — Light Tenor Male (kitten_nano:m3)", SIDv019: 4},
	{ID: "kitten_nano:m4", Label: "Kitten Nano M4 — Androgynous / High Pitch (kitten_nano:m4)", SIDv019: 6},
}

// PiperVoiceCatalog lists curated speaker voices from Piper LibriTTS-High INT8 (904 speakers total).
var PiperVoiceCatalog = []VoiceOption{
	{ID: "piper:spk_0", Label: "Piper LibriTTS — Clear US Female (piper:spk_0)", SIDv019: 0},
	{ID: "piper:spk_1", Label: "Piper LibriTTS — Warm US Male (piper:spk_1)", SIDv019: 1},
	{ID: "piper:spk_2", Label: "Piper LibriTTS — Soft US Female (piper:spk_2)", SIDv019: 2},
	{ID: "piper:spk_3", Label: "Piper LibriTTS — Deep US Male (piper:spk_3)", SIDv019: 3},
	{ID: "piper:spk_4", Label: "Piper LibriTTS — Classic UK Female (piper:spk_4)", SIDv019: 4},
	{ID: "piper:spk_5", Label: "Piper LibriTTS — Classic UK Male (piper:spk_5)", SIDv019: 5},
	{ID: "piper:spk_10", Label: "Piper LibriTTS — Storyteller Female (piper:spk_10)", SIDv019: 10},
	{ID: "piper:spk_20", Label: "Piper LibriTTS — Storyteller Male (piper:spk_20)", SIDv019: 20},
}

// KokoroIntlVoiceCatalog lists the 25 multilingual international Kokoro voices (Spanish, French, Hindi, Italian, Japanese, Portuguese, Mandarin).
var KokoroIntlVoiceCatalog = []VoiceOption{
	{ID: "kokoro_intl:ef_dora", Label: "Dora — Spanish Female (kokoro_intl:ef_dora)", SIDv019: 0, SIDv10: 28},
	{ID: "kokoro_intl:em_alex", Label: "Alex — Spanish Male (kokoro_intl:em_alex)", SIDv019: 0, SIDv10: 29},
	{ID: "kokoro_intl:em_santa", Label: "Santa — Spanish Male (kokoro_intl:em_santa)", SIDv019: 0, SIDv10: 30},
	{ID: "kokoro_intl:ff_siwis", Label: "Siwis — French Female (kokoro_intl:ff_siwis)", SIDv019: 0, SIDv10: 31},
	{ID: "kokoro_intl:hf_alpha", Label: "Alpha — Hindi Female (kokoro_intl:hf_alpha)", SIDv019: 0, SIDv10: 32},
	{ID: "kokoro_intl:hf_beta", Label: "Beta — Hindi Female (kokoro_intl:hf_beta)", SIDv019: 0, SIDv10: 33},
	{ID: "kokoro_intl:hm_omega", Label: "Omega — Hindi Male (kokoro_intl:hm_omega)", SIDv019: 0, SIDv10: 34},
	{ID: "kokoro_intl:hm_psi", Label: "Psi — Hindi Male (kokoro_intl:hm_psi)", SIDv019: 0, SIDv10: 35},
	{ID: "kokoro_intl:if_sara", Label: "Sara — Italian Female (kokoro_intl:if_sara)", SIDv019: 0, SIDv10: 36},
	{ID: "kokoro_intl:im_nicola", Label: "Nicola — Italian Male (kokoro_intl:im_nicola)", SIDv019: 0, SIDv10: 37},
	{ID: "kokoro_intl:jf_alpha", Label: "Alpha — Japanese Female (kokoro_intl:jf_alpha)", SIDv019: 0, SIDv10: 38},
	{ID: "kokoro_intl:jf_gongitsune", Label: "Gongitsune — Japanese Female (kokoro_intl:jf_gongitsune)", SIDv019: 0, SIDv10: 39},
	{ID: "kokoro_intl:jf_nezumi", Label: "Nezumi — Japanese Female (kokoro_intl:jf_nezumi)", SIDv019: 0, SIDv10: 40},
	{ID: "kokoro_intl:jf_tebukuro", Label: "Tebukuro — Japanese Female (kokoro_intl:jf_tebukuro)", SIDv019: 0, SIDv10: 41},
	{ID: "kokoro_intl:jm_kumo", Label: "Kumo — Japanese Male (kokoro_intl:jm_kumo)", SIDv019: 0, SIDv10: 42},
	{ID: "kokoro_intl:pf_dora", Label: "Dora — Portuguese Female (kokoro_intl:pf_dora)", SIDv019: 0, SIDv10: 43},
	{ID: "kokoro_intl:pm_alex", Label: "Alex — Portuguese Male (kokoro_intl:pm_alex)", SIDv019: 0, SIDv10: 44},
	{ID: "kokoro_intl:pm_santa", Label: "Santa — Portuguese Male (kokoro_intl:pm_santa)", SIDv019: 0, SIDv10: 45},
	{ID: "kokoro_intl:zf_xiaobei", Label: "Xiaobei — Mandarin Female (kokoro_intl:zf_xiaobei)", SIDv019: 0, SIDv10: 46},
	{ID: "kokoro_intl:zf_xiaoni", Label: "Xiaoni — Mandarin Female (kokoro_intl:zf_xiaoni)", SIDv019: 0, SIDv10: 47},
	{ID: "kokoro_intl:zf_xiaoxiao", Label: "Xiaoxiao — Mandarin Female (kokoro_intl:zf_xiaoxiao)", SIDv019: 0, SIDv10: 48},
	{ID: "kokoro_intl:zf_xiaoyi", Label: "Xiaoyi — Mandarin Female (kokoro_intl:zf_xiaoyi)", SIDv019: 0, SIDv10: 49},
	{ID: "kokoro_intl:zm_yunjian", Label: "Yunjian — Mandarin Male (kokoro_intl:zm_yunjian)", SIDv019: 0, SIDv10: 50},
	{ID: "kokoro_intl:zm_yunxi", Label: "Yunxi — Mandarin Male (kokoro_intl:zm_yunxi)", SIDv019: 0, SIDv10: 51},
	{ID: "kokoro_intl:zm_yunyang", Label: "Yunyang — Mandarin Male (kokoro_intl:zm_yunyang)", SIDv019: 0, SIDv10: 52},
}

// AllModelFamilies lists all supported neural TTS model packs in display order.
var AllModelFamilies = []ModelFamilySpec{
	{
		ID:             ModelKokoro,
		Name:           "Kokoro-82M Neural (28 Voices — Highest Naturalness)",
		ShortName:      "Kokoro-82M",
		SubDir:         "kokoro-en",
		DownloadSizeMB: 126,
		IdleRAMMB:      211,
		ActiveRAMMB:    285,
		GPUIdleRAMMB:   780,
		GPUActiveRAMMB: 980,
		CPURTF:         0.74,
		GPURTF:         0.09,
		SupportsGPU:    true,
		Summary:        "Best overall prosody (4.45 UTMOS). Fast on DirectML GPU (~0.3s start, ~950 MB RAM); slower on CPU INT8 (~2.7s start, ~285 MB RAM).",
		Voices:         KokoroVoiceCatalog,
	},
	{
		ID:             ModelKokoroIntl,
		Name:           "Kokoro-82M International (25 Voices — ES/FR/HI/IT/JA/PT/ZH)",
		ShortName:      "Kokoro Intl",
		SubDir:         "kokoro-en",
		DownloadSizeMB: 0,
		IdleRAMMB:      211,
		ActiveRAMMB:    285,
		GPUIdleRAMMB:   780,
		GPUActiveRAMMB: 980,
		CPURTF:         0.74,
		GPURTF:         0.09,
		SupportsGPU:    true,
		Summary:        "Adds 25 multilingual international voices (Spanish, French, Hindi, Italian, Japanese, Portuguese, Mandarin). Shares Kokoro-82M engine with zero extra RAM or download overhead.",
		Voices:         KokoroIntlVoiceCatalog,
	},
	{
		ID:             ModelSupertonic,
		Name:           "Supertonic-66M INT8 (10 Voices — Ultra-Fast CPU, 44.1 kHz)",
		ShortName:      "Supertonic INT8",
		SubDir:         "supertonic-en",
		DownloadSizeMB: 81,
		IdleRAMMB:      152,
		ActiveRAMMB:    195,
		GPUIdleRAMMB:   152,
		GPUActiveRAMMB: 195,
		CPURTF:         0.06,
		GPURTF:         0.06,
		SupportsGPU:    false,
		Summary:        "Recommended Streamer Default: 66M-param 6x-compressed flow-matching model. Uses ~50 MB more RAM than Kitten Nano for weights, but ~3x less CPU (~0.2s instant start, 44.1 kHz).",
		Voices:         SupertonicVoiceCatalog,
	},
	{
		ID:             ModelKittenMini,
		Name:           "Kitten v0.8 Mini-80M (8 Voices — Fast Lightweight Kokoro)",
		ShortName:      "Kitten Mini v0.8",
		SubDir:         "kitten-mini-en",
		DownloadSizeMB: 65,
		IdleRAMMB:      137,
		ActiveRAMMB:    215,
		GPUIdleRAMMB:   137,
		GPUActiveRAMMB: 215,
		CPURTF:         0.37,
		GPURTF:         0.37,
		SupportsGPU:    false,
		Summary:        "Distilled 80M Kokoro-style voices (~0.9s CPU start, ~215 MB RAM).",
		Voices:         KittenMiniVoiceCatalog,
	},
	{
		ID:             ModelKittenNano,
		Name:           "Kitten v0.8 Nano-15M INT8 (8 Voices — Smallest RAM Weights)",
		ShortName:      "Kitten Nano INT8",
		SubDir:         "kitten-nano-en",
		DownloadSizeMB: 30,
		IdleRAMMB:      101,
		ActiveRAMMB:    145,
		GPUIdleRAMMB:   101,
		GPUActiveRAMMB: 145,
		CPURTF:         0.18,
		GPURTF:         0.18,
		SupportsGPU:    false,
		Summary:        "Tiny 15M-param (24 MB) weights for lowest RAM (~145 MB), though its iSTFTNet conv vocoder uses ~3x more CPU math per word than Supertonic (~0.4s start).",
		Voices:         KittenNanoVoiceCatalog,
	},
	{
		ID:             ModelPiper,
		Name:           "Piper LibriTTS-High INT8 (Multi-Speaker VITS)",
		ShortName:      "Piper LibriTTS INT8",
		SubDir:         "piper-libritts-en",
		DownloadSizeMB: 35,
		IdleRAMMB:      88,
		ActiveRAMMB:    180,
		GPUIdleRAMMB:   88,
		GPUActiveRAMMB: 180,
		CPURTF:         0.85,
		GPURTF:         0.85,
		SupportsGPU:    false,
		Summary:        "Classic VITS multi-speaker pack (~180 MB RAM, 904 speaker indices available).",
		Voices:         PiperVoiceCatalog,
	},
}

// ModelSpecByID returns the ModelFamilySpec for a model family ID.
func ModelSpecByID(family string) (ModelFamilySpec, bool) {
	fam := strings.ToLower(strings.TrimSpace(family))
	for _, spec := range AllModelFamilies {
		if spec.ID == fam {
			return spec, true
		}
	}
	return ModelFamilySpec{}, false
}

// AllVoiceOptions returns all predefined VoiceOptions across all supported model families.
func AllVoiceOptions() []VoiceOption {
	var all []VoiceOption
	for _, spec := range AllModelFamilies {
		all = append(all, spec.Voices...)
	}
	return all
}

// IsKokoroIntlVoice reports whether voiceID belongs to the Kokoro international voice catalog.
func IsKokoroIntlVoice(voiceID string) bool {
	id := VoiceIDFromLabel(voiceID)
	if strings.HasPrefix(id, ModelKokoroIntl+":") {
		return true
	}
	trimmed := strings.TrimPrefix(id, "kokoro:")
	for _, v := range KokoroIntlVoiceCatalog {
		if v.ID == id || v.ID == ModelKokoroIntl+":"+trimmed || strings.TrimPrefix(v.ID, ModelKokoroIntl+":") == trimmed {
			return true
		}
	}
	return false
}

// VoiceFamily returns the canonical model family ID ("kokoro", "kokoro_intl", "supertonic", "kitten_mini", "kitten_nano", "piper")
// that owns the given voice ID or display label.
func VoiceFamily(voiceID string) string {
	id := VoiceIDFromLabel(voiceID)
	if colonIdx := strings.IndexByte(id, ':'); colonIdx > 0 {
		prefix := id[:colonIdx]
		switch prefix {
		case ModelKokoro, ModelKokoroIntl, ModelSupertonic, ModelKittenMini, ModelKittenNano, ModelPiper:
			return prefix
		case "kitten":
			return ModelKittenMini
		}
	}
	if IsKokoroIntlVoice(id) {
		return ModelKokoroIntl
	}
	return ModelKokoro
}

// ResolveFamilySpeakerID resolves a voice ID (e.g. "supertonic:m2", "piper:spk_10", "af_bella", "kokoro_intl:ef_dora")
// to its integer speaker index within its model family.
func ResolveFamilySpeakerID(family string, voiceID string, numSpeakers int) int {
	id := VoiceIDFromLabel(voiceID)
	if family == "" || family == ModelKokoro || family == ModelKokoroIntl {
		trimmed := strings.TrimPrefix(id, "kokoro:")
		trimmed = strings.TrimPrefix(trimmed, "kokoro_intl:")
		return ResolveSpeakerIDForModel(trimmed, numSpeakers)
	}

	if spec, ok := ModelSpecByID(family); ok {
		for _, v := range spec.Voices {
			if v.ID == id {
				if numSpeakers <= 0 || v.SIDv019 < numSpeakers {
					return v.SIDv019
				}
			}
		}
	}

	// Support arbitrary Piper speaker IDs like "piper:spk_42"
	if family == ModelPiper {
		raw := strings.TrimPrefix(id, "piper:")
		raw = strings.TrimPrefix(raw, "spk_")
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			if numSpeakers <= 0 || n < numSpeakers {
				return n
			}
		}
	}

	return 0
}

// VoiceDisplayLabelsForModels returns the human-friendly display strings for all voices in the enabled model families.
func VoiceDisplayLabelsForModels(enabledModels []string) []string {
	if len(enabledModels) == 0 {
		return VoiceDisplayLabels()
	}
	enabledSet := make(map[string]struct{}, len(enabledModels))
	for _, m := range enabledModels {
		enabledSet[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}

	var labels []string
	for _, spec := range AllModelFamilies {
		if _, ok := enabledSet[spec.ID]; !ok {
			continue
		}
		for _, v := range spec.Voices {
			labels = append(labels, v.Label)
		}
	}
	if len(labels) == 0 {
		return VoiceDisplayLabels()
	}
	return labels
}

// PresetSpec defines a curated Hardware & Performance Preset.
type PresetSpec struct {
	ID                string
	Label             string
	EnabledModels     []string
	UseGPU            bool
	NumThreads        int
	MaxClauseWords    int
	IdleUnloadMinutes int
	DefaultVoice      string
	Description       string
}

// HardwarePresets lists all selectable hardware presets in the Settings UI.
var HardwarePresets = []PresetSpec{
	{
		ID:                PresetUltraLow,
		Label:             "Ultra-Low Resource — Kitten Nano INT8 (~145 MB RAM, Fast CPU)",
		EnabledModels:     []string{ModelKittenNano},
		UseGPU:            false,
		NumThreads:        2,
		MaxClauseWords:    8,
		IdleUnloadMinutes: 5,
		DefaultVoice:      "kitten_nano:f1",
		Description:       "Lowest possible RAM (~145 MB active, auto-unloads when idle) and fast CPU response (~0.4s).",
	},
	{
		ID:                PresetFastLean,
		Label:             "Fast & Lean CPU (Default) — Supertonic INT8 (~195 MB RAM, Instant ~0.2s CPU Start)",
		EnabledModels:     []string{ModelSupertonic},
		UseGPU:            false,
		NumThreads:        4,
		MaxClauseWords:    12,
		IdleUnloadMinutes: 0,
		DefaultVoice:      "supertonic:f1",
		Description:       "Recommended for streamers: 15x real-time CPU speed (~0.2s start, 0% GPU, 44.1 kHz) with near-Kokoro naturalness.",
	},
	{
		ID:                PresetBalanced,
		Label:             "Studio Quality CPU — Kokoro-82M INT8 (~285 MB RAM, ~3s CPU Start)",
		EnabledModels:     []string{ModelKokoro},
		UseGPU:            false,
		NumThreads:        4,
		MaxClauseWords:    12,
		IdleUnloadMinutes: 0,
		DefaultVoice:      DefaultVoiceID,
		Description:       "Highest voice naturalness (Kokoro 4.45 UTMOS) in ~285 MB RAM on CPU, with ~2.5–3.5s synthesis lead time per sentence.",
	},
	{
		ID:                PresetBeefy,
		Label:             "Beefy Stream Rig — DirectML GPU + Multi-Model Pack (~1.1 GB RAM, Instant Start)",
		EnabledModels:     []string{ModelKokoro, ModelSupertonic, ModelKittenMini, ModelPiper},
		UseGPU:            true,
		NumThreads:        6,
		MaxClauseWords:    16,
		IdleUnloadMinutes: 0,
		DefaultVoice:      DefaultVoiceID,
		Description:       "DirectML GPU acceleration for instant Kokoro speech (~0.3s start) plus Supertonic, Kitten Mini, and Piper voices.",
	},
	{
		ID:          PresetCustom,
		Label:       "Custom Configuration (Manual Hardware & Voice Pack Selection)",
		Description: "Customize GPU acceleration, CPU threads, clause chunking, and voice model packs below.",
	},
}

// PresetByID looks up a PresetSpec by ID, defaulting to PresetFastLean.
func PresetByID(id string) PresetSpec {
	clean := strings.ToLower(strings.TrimSpace(id))
	for _, p := range HardwarePresets {
		if p.ID == clean {
			return p
		}
	}
	return HardwarePresets[1] // PresetFastLean
}

// PresetByLabel looks up a PresetSpec by its dropdown display label.
func PresetByLabel(label string) (PresetSpec, bool) {
	for _, p := range HardwarePresets {
		if p.Label == label {
			return p, true
		}
	}
	return PresetSpec{}, false
}

// ImpactSegment represents one additive colored slice in a stacked hardware impact graph.
type ImpactSegment struct {
	FamilyID string  // "base", "supertonic", "kokoro", "kitten_mini", "kitten_nano", "piper"
	Label    string  // Human-readable contribution label (e.g. "Supertonic INT8: +140 MB")
	Value    float64 // Raw additive value (MB, RTF, GPU load, or seconds)
	Fraction float64 // Normalized width fraction on the bar (0.0..1.0, sum <= 1.0)
}

// HardwareImpact holds the estimated resource meters (0.0..1.0), stacked segments, and explanations for the UI.
type HardwareImpact struct {
	IdleRAMMB       int
	ActiveRAMMB     int
	RAMMeter        float64 // 0.0 (tiny) to 1.0 (~1.2 GB+)
	RAMText         string
	RAMBarText      string
	RAMSegments     []ImpactSegment
	CPUMeter        float64 // 0.0 (light) to 1.0 (heavy CPU compute during speech)
	CPUText         string
	CPUBarText      string
	CPUSegments     []ImpactSegment
	GPUMeter        float64 // 0.0 (none) to 1.0 (DirectML FP32 active)
	GPUText         string
	GPUBarText      string
	GPUSegments     []ImpactSegment
	LatencyMeter    float64 // 0.0 (instant ~0.2s) to 1.0 (slow ~3.5s+ lead time)
	LatencyText     string
	LatencyBarText  string
	LatencySegments []ImpactSegment
	TradeoffNotice  string
}

func normalizeSegments(segs []ImpactSegment, fullScale float64) ([]ImpactSegment, float64) {
	if len(segs) == 0 || fullScale <= 0 {
		return nil, 0
	}
	var sum float64
	for _, s := range segs {
		if s.Value > 0 {
			sum += s.Value
		}
	}
	if sum <= 0 {
		return nil, 0
	}
	denom := max(fullScale, sum)
	out := make([]ImpactSegment, 0, len(segs))
	for _, s := range segs {
		if s.Value <= 0 {
			continue
		}
		s.Fraction = s.Value / denom
		out = append(out, s)
	}
	return out, min(sum/fullScale, 1.0)
}

// EstimateHardwareImpact computes the additive RAM, CPU, GPU, and Startup Latency stacked segments for the given configuration.
func EstimateHardwareImpact(enabledModels []string, useGPU bool, numThreads int) HardwareImpact {
	if len(enabledModels) == 0 {
		enabledModels = []string{ModelSupertonic}
	}
	if numThreads <= 0 {
		numThreads = 4
	}

	// Base Go + Fyne + audio overhead ~55 MB
	const baseRAMMB = 55
	idleMB := baseRAMMB
	activeMB := baseRAMMB
	worstCPURTF := 0.0
	sumCPURTF := 0.0
	sumLeadSec := 0.0
	hasKokoro := false
	hasFastCPUModel := false

	ramSegs := []ImpactSegment{
		{
			FamilyID: "base",
			Label:    fmt.Sprintf("Base App: %d MB", baseRAMMB),
			Value:    float64(baseRAMMB),
		},
	}
	var cpuSegs []ImpactSegment
	var gpuSegs []ImpactSegment
	var latSegs []ImpactSegment

	var ramParts, cpuParts, gpuParts, latParts []string
	ramParts = append(ramParts, fmt.Sprintf("Base %d MB", baseRAMMB))

	kokoroCounted := false
	for _, id := range enabledModels {
		spec, ok := ModelSpecByID(id)
		if !ok {
			continue
		}
		isKokoroFam := spec.ID == ModelKokoro || spec.ID == ModelKokoroIntl
		if isKokoroFam {
			hasKokoro = true
		}
		if spec.ID == ModelSupertonic || spec.ID == ModelKittenNano {
			hasFastCPUModel = true
		}

		if isKokoroFam && kokoroCounted {
			ramParts = append(ramParts, spec.ShortName+" +0 MB (shared)")
			continue
		}
		if isKokoroFam {
			kokoroCounted = true
		}

		var modelIdle, modelActive int
		var rtf, gpuLoad float64

		if useGPU && spec.SupportsGPU {
			modelIdle = spec.GPUIdleRAMMB - baseRAMMB
			modelActive = spec.GPUActiveRAMMB - baseRAMMB
			rtf = spec.GPURTF
			gpuLoad = 0.65
		} else {
			modelIdle = max(spec.IdleRAMMB-baseRAMMB, 35)
			modelActive = max(spec.ActiveRAMMB-baseRAMMB, 50)
			if numThreads > 4 && (spec.ID == ModelKokoro || spec.ID == ModelKokoroIntl) {
				modelActive += (numThreads - 4) * 28
			}
			rtf = spec.CPURTF
			if numThreads < 4 {
				rtf *= 4.0 / float64(max(numThreads, 1)) * 0.7
			}
			gpuLoad = 0.0
		}

		idleMB += modelIdle
		activeMB += modelActive
		sumCPURTF += rtf
		if rtf > worstCPURTF {
			worstCPURTF = rtf
		}

		leadSec := max(rtf*3.5, 0.18)
		sumLeadSec += leadSec

		ramSegs = append(ramSegs, ImpactSegment{
			FamilyID: spec.ID,
			Label:    fmt.Sprintf("%s: +%d MB", spec.ShortName, modelActive),
			Value:    float64(modelActive),
		})
		ramParts = append(ramParts, fmt.Sprintf("%s +%d MB", spec.ShortName, modelActive))

		cpuSegs = append(cpuSegs, ImpactSegment{
			FamilyID: spec.ID,
			Label:    fmt.Sprintf("%s: %.0f%%", spec.ShortName, rtf*100),
			Value:    rtf,
		})
		cpuParts = append(cpuParts, fmt.Sprintf("%s %.0f%%", spec.ShortName, rtf*100))

		if gpuLoad > 0 {
			gpuSegs = append(gpuSegs, ImpactSegment{
				FamilyID: spec.ID,
				Label:    fmt.Sprintf("%s: %.0f%% DirectML", spec.ShortName, gpuLoad*100),
				Value:    gpuLoad,
			})
			gpuParts = append(gpuParts, fmt.Sprintf("%s %.0f%% GPU", spec.ShortName, gpuLoad*100))
		}

		latSegs = append(latSegs, ImpactSegment{
			FamilyID: spec.ID,
			Label:    fmt.Sprintf("%s: ~%.1fs", spec.ShortName, leadSec),
			Value:    leadSec,
		})
		latParts = append(latParts, fmt.Sprintf("%s ~%.1fs", spec.ShortName, leadSec))
	}

	normRAMSegs, ramMeter := normalizeSegments(ramSegs, 1200.0)
	normCPUSegs, cpuMeter := normalizeSegments(cpuSegs, 1.0)
	normGPUSegs, gpuMeter := normalizeSegments(gpuSegs, 1.0)
	normLatSegs, latencyMeter := normalizeSegments(latSegs, 3.5)

	ramTier := "Low"
	if activeMB >= 750 {
		ramTier = "High"
	} else if activeMB >= 400 {
		ramTier = "Moderate"
	}
	ramText := fmt.Sprintf("~%d MB Idle / ~%d MB Active (%s)", idleMB, activeMB, ramTier)
	ramBarText := fmt.Sprintf("~%d MB Total (%s)", activeMB, strings.Join(ramParts, " + "))

	var gpuText, gpuBarText string
	switch {
	case len(normGPUSegs) > 0:
		gpuText = "Active (DirectML FP32 — fast ~0.3s speech start, uses ~700 MB extra RAM)"
		gpuBarText = strings.Join(gpuParts, " + ")
	case useGPU:
		gpuText = "0% GPU (Enabled models use CPU SIMD; only Kokoro-82M uses DirectML)"
		gpuBarText = "0% GPU (Selected models run on CPU)"
	default:
		gpuText = "0% GPU (CPU-only mode — saves ~700 MB RAM)"
		gpuBarText = "CPU Only (0% GPU)"
	}

	speedFactor := 1.0
	if worstCPURTF > 0 {
		speedFactor = 1.0 / worstCPURTF
	}
	var cpuText string
	switch {
	case useGPU && hasKokoro && len(enabledModels) == 1:
		cpuText = "Low (~9% — Kokoro inference offloaded to DirectML GPU)"
	case worstCPURTF <= 0.10:
		cpuText = fmt.Sprintf("Ultra-Low (~%.2fx RTF — ~%.0fx faster than real-time on %d CPU threads)", worstCPURTF, speedFactor, numThreads)
	case worstCPURTF <= 0.30:
		cpuText = fmt.Sprintf("Low-Moderate (~%.2fx peak RTF — ~%.1fx faster than real-time on %d threads; ~3x Supertonic CPU)", worstCPURTF, speedFactor, numThreads)
	case worstCPURTF <= 0.50:
		cpuText = fmt.Sprintf("Moderate (~%.2fx peak RTF — ~%.1fx faster than real-time on %d CPU threads)", worstCPURTF, speedFactor, numThreads)
	default:
		cpuText = fmt.Sprintf("High during synthesis (~%.2fx peak RTF — ~%.1fx real-time on %d CPU threads)", worstCPURTF, speedFactor, numThreads)
	}
	cpuBarText := fmt.Sprintf("%.0f%% Combined CPU Scale (%s)", sumCPURTF*100, strings.Join(cpuParts, " + "))

	estLeadSec := max(worstCPURTF*3.5, 0.18)
	var latencyText string
	switch {
	case estLeadSec <= 0.30:
		latencyText = fmt.Sprintf("Instant (~%.1fs max delay before speaking)", estLeadSec)
	case estLeadSec <= 1.4:
		latencyText = fmt.Sprintf("Fast (~%.1fs max delay before speaking)", estLeadSec)
	default:
		latencyText = fmt.Sprintf("Noticeable Delay (up to ~%.1fs CPU crunch on slowest loaded model)", estLeadSec)
	}
	latencyBarText := strings.Join(latParts, "  |  ")

	var notice string
	switch {
	case len(enabledModels) > 1:
		notice = fmt.Sprintf("💡 Multi-Model Mode: %d voice packs are loaded simultaneously in RAM (~%d MB total). Each model's additive RAM, CPU, GPU, and startup delay is shown in its matching color above.", len(enabledModels), activeMB)
	case !useGPU && hasKokoro && !hasFastCPUModel:
		notice = "💡 Tradeoff Note: Running Kokoro-82M on CPU (Use GPU unchecked) cuts RAM from ~980 MB down to ~285 MB, but its 82M-parameter INT8 network takes ~2.5s–3.5s to crunch a sentence before speaking. Enable Supertonic INT8 alongside or instead of Kokoro for instant (~0.2s) CPU voices."
	case useGPU && hasKokoro:
		notice = "💡 Tradeoff Note: DirectML GPU acceleration makes Kokoro-82M start speaking almost instantly (~0.3s), at the cost of ~800 MB–1.1 GB process RAM for FP32 GPU staging buffers."
	case hasFastCPUModel && !hasKokoro:
		notice = "💡 Tradeoff Note: Supertonic INT8 uses ~6% CPU (~0.06x RTF, ~195 MB RAM) via 6x temporal compression, while Kitten Nano INT8 uses ~18%–25% CPU (~3x more CPU math in its iSTFTNet conv vocoder) in exchange for the smallest 24 MB model weights (~145 MB RAM)."
	default:
		notice = fmt.Sprintf("💡 Active Packs: %d model family(s) loaded in RAM (~%d MB active).", len(enabledModels), activeMB)
	}

	return HardwareImpact{
		IdleRAMMB:       idleMB,
		ActiveRAMMB:     activeMB,
		RAMMeter:        ramMeter,
		RAMText:         ramText,
		RAMBarText:      ramBarText,
		RAMSegments:     normRAMSegs,
		CPUMeter:        cpuMeter,
		CPUText:         cpuText,
		CPUBarText:      cpuBarText,
		CPUSegments:     normCPUSegs,
		GPUMeter:        gpuMeter,
		GPUText:         gpuText,
		GPUBarText:      gpuBarText,
		GPUSegments:     normGPUSegs,
		LatencyMeter:    latencyMeter,
		LatencyText:     latencyText,
		LatencyBarText:  latencyBarText,
		LatencySegments: normLatSegs,
		TradeoffNotice:  notice,
	}
}
