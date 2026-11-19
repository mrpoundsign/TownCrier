package stt

import (
	"errors"
	"strings"
	"time"
)

var (
	// ErrEngineClosed is returned when operations are attempted on a closed STT engine or segmenter.
	ErrEngineClosed = errors.New("stt engine is closed")
	// ErrModelNotFound is returned when required Silero VAD or Whisper ONNX model files are missing on disk.
	ErrModelNotFound = errors.New("stt model files not found")
	// ErrTranscribeFailed is returned when Whisper decoding fails or produces no valid audio stream.
	ErrTranscribeFailed = errors.New("stt transcription failed")
)

const (
	ModelWhisperTiny  = "whisper_tiny"
	ModelWhisperBase  = "whisper_base"
	ModelWhisperSmall = "whisper_small"
	ModelWhisperTurbo = "whisper_turbo"

	DefaultModelFamily = ModelWhisperTurbo
	DefaultSampleRate  = 16000

	DefaultVadThreshold float32 = 0.50
	DefaultMinSilenceMs int     = 450
	DefaultMinSpeechMs  int     = 250
	DefaultMaxSpeechSec int     = 12
)

// ModelSpec describes an offline multilingual Whisper model pack (quantized GGML for Vulkan GPU/iGPU/CPU + legacy ONNX support).
type ModelSpec struct {
	ID            string
	Prefix        string // filename prefix inside legacy sherpa-onnx archives ("tiny", "base", "small", "turbo")
	DisplayName   string
	Label         string
	GGMLFile      string
	GGMLURL       string
	GGMLSHA256    string
	GGMLSize      int64
	ArchiveURL    string
	ArchiveSHA256 string
	ArchiveSize   int64
	ApproxSizeMB  int
	SupportsFP32  bool
	Recommended   bool
	Description   string
}

// AllModelSpecs lists all supported offline multilingual Whisper model families in UI display order.
// Every model carries a compile-time pinned SHA-256 digest verified fail-closed before installation,
// and executes natively on Vulkan Discrete GPUs (NVIDIA/AMD/Intel), Integrated iGPUs, and CPU.
var AllModelSpecs = []ModelSpec{
	{
		ID:           ModelWhisperTurbo,
		Prefix:       "turbo",
		DisplayName:  "Whisper Large-v3-Turbo (Q5_0)",
		Label:        "Whisper Large-v3-Turbo (Q5_0) — Best Taglish & Multilingual (~547 MB)",
		GGMLFile:     "ggml-large-v3-turbo-q5_0.bin",
		GGMLURL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo-q5_0.bin",
		GGMLSHA256:   "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2",
		GGMLSize:     574041195,
		ArchiveURL:   "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-turbo.tar.bz2",
		ArchiveSize:  563790207,
		ApproxSizeMB: 547,
		SupportsFP32: true,
		Recommended:  true,
		Description:  "Compressed 4-layer decoder from Whisper Large-v3. Highest accuracy for Filipino/Tagalog, Taglish, Japanese, and English on Vulkan GPUs, modern iGPUs, and fast CPUs.",
	},
	{
		ID:           ModelWhisperSmall,
		Prefix:       "small",
		DisplayName:  "Whisper Small (Q5_1)",
		Label:        "Whisper Small (Q5_1) — Balanced iGPU / GPU / CPU (~181 MB)",
		GGMLFile:     "ggml-small-q5_1.bin",
		GGMLURL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small-q5_1.bin",
		GGMLSHA256:   "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb",
		GGMLSize:     190085487,
		ArchiveURL:   "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-small.tar.bz2",
		ArchiveSize:  639387718,
		ApproxSizeMB: 181,
		SupportsFP32: true,
		Recommended:  false,
		Description:  "Great balance of multilingual accuracy and low memory bandwidth — ideal sweet spot for laptop Intel/AMD iGPUs and older GPUs.",
	},
	{
		ID:           ModelWhisperBase,
		Prefix:       "base",
		DisplayName:  "Whisper Base (Q5_1)",
		Label:        "Whisper Base (Q5_1) — Fast & Lightweight for Older iGPUs (~57 MB)",
		GGMLFile:     "ggml-base-q5_1.bin",
		GGMLURL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base-q5_1.bin",
		GGMLSHA256:   "422f1ae452ade6f30a004d7e5c6a43195e4433bc370bf23fac9cc591f01a8898",
		GGMLSize:     59707625,
		ArchiveURL:   "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-base.tar.bz2",
		ArchiveSize:  207557382,
		ApproxSizeMB: 57,
		SupportsFP32: true,
		Recommended:  false,
		Description:  "Very fast multilingual model for older integrated GPUs (e.g. Intel HD 630) and low-RAM laptops.",
	},
	{
		ID:           ModelWhisperTiny,
		Prefix:       "tiny",
		DisplayName:  "Whisper Tiny (Q5_1)",
		Label:        "Whisper Tiny (Q5_1) — Ultra-Light Baseline (~31 MB)",
		GGMLFile:     "ggml-tiny-q5_1.bin",
		GGMLURL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny-q5_1.bin",
		GGMLSHA256:   "818710568da3ca15689e31a743197b520007872ff9576237bda97bd1b469c3d7",
		GGMLSize:     32152673,
		ArchiveURL:   "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-tiny.tar.bz2",
		ArchiveSize:  116204861,
		ApproxSizeMB: 31,
		SupportsFP32: true,
		Recommended:  false,
		Description:  "Smallest footprint for quick latency testing on constrained hardware (lower accuracy on non-English speech).",
	},
}

// NormalizeModelFamily returns a canonical Whisper model family ID, defaulting to ModelWhisperTurbo.
func NormalizeModelFamily(family string) string {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case ModelWhisperTiny, "tiny":
		return ModelWhisperTiny
	case ModelWhisperBase, "base":
		return ModelWhisperBase
	case ModelWhisperSmall, "small":
		return ModelWhisperSmall
	case ModelWhisperTurbo, "turbo", "large-v3-turbo", "whisper-large-v3-turbo":
		return ModelWhisperTurbo
	default:
		return DefaultModelFamily
	}
}

// ModelSpecByID looks up a ModelSpec by model family ID (falling back to DefaultModelFamily if empty).
func ModelSpecByID(family string) (ModelSpec, bool) {
	norm := NormalizeModelFamily(family)
	if strings.TrimSpace(family) != "" && norm == DefaultModelFamily {
		clean := strings.ToLower(strings.TrimSpace(family))
		if clean != ModelWhisperTurbo && clean != "turbo" && clean != "large-v3-turbo" && clean != "whisper-large-v3-turbo" {
			return ModelSpec{}, false
		}
	}
	for _, spec := range AllModelSpecs {
		if spec.ID == norm {
			return spec, true
		}
	}
	return ModelSpec{}, false
}

// ModelDisplayLabels returns the ordered UI dropdown labels for all supported Whisper models.
func ModelDisplayLabels() []string {
	labels := make([]string, len(AllModelSpecs))
	for i, s := range AllModelSpecs {
		labels[i] = s.Label
	}
	return labels
}

// ModelLabelForID returns the UI dropdown label for a Whisper model family ID.
func ModelLabelForID(family string) string {
	norm := NormalizeModelFamily(family)
	for _, s := range AllModelSpecs {
		if s.ID == norm {
			return s.Label
		}
	}
	return AllModelSpecs[0].Label
}

// ModelIDFromLabel resolves a UI dropdown label or raw ID back to a canonical Whisper model family ID.
func ModelIDFromLabel(labelOrID string) string {
	trimmed := strings.TrimSpace(labelOrID)
	for _, s := range AllModelSpecs {
		if strings.EqualFold(s.Label, trimmed) || strings.EqualFold(s.DisplayName, trimmed) || strings.EqualFold(s.ID, trimmed) {
			return s.ID
		}
	}
	return NormalizeModelFamily(trimmed)
}

// SpokenLanguage represents a selectable language that the speaker may use on microphone.
type SpokenLanguage struct {
	Code string
	Name string
}

// SupportedSpokenLanguages lists Whisper-supported spoken languages in UI selection order.
var SupportedSpokenLanguages = []SpokenLanguage{
	{Code: "en", Name: "English"},
	{Code: "tl", Name: "Tagalog (Filipino)"},
	{Code: "ja", Name: "Japanese"},
	{Code: "es", Name: "Spanish"},
	{Code: "fr", Name: "French"},
	{Code: "de", Name: "German"},
	{Code: "it", Name: "Italian"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "ko", Name: "Korean"},
	{Code: "zh", Name: "Chinese (Mandarin)"},
	{Code: "ru", Name: "Russian"},
	{Code: "hi", Name: "Hindi"},
	{Code: "id", Name: "Indonesian"},
	{Code: "vi", Name: "Vietnamese"},
	{Code: "th", Name: "Thai"},
	{Code: "ar", Name: "Arabic"},
	{Code: "nl", Name: "Dutch"},
	{Code: "pl", Name: "Polish"},
	{Code: "tr", Name: "Turkish"},
	{Code: "uk", Name: "Ukrainian"},
}

var spokenLanguageMap = func() map[string]string {
	m := make(map[string]string, len(SupportedSpokenLanguages))
	for _, l := range SupportedSpokenLanguages {
		m[l.Code] = l.Name
	}
	return m
}()

// NormalizeSpokenLang normalizes language tags (including "fil"/"tgl" -> "tl") to a Whisper ISO-639-1 code.
// Returns "" if the code is unrecognized or empty.
func NormalizeSpokenLang(code string) string {
	clean := strings.ToLower(strings.TrimSpace(code))
	clean = strings.Trim(clean, "<>|") // Whisper sometimes formats tokens like "<|tl|>"
	if idx := strings.IndexAny(clean, "-_"); idx > 0 {
		clean = clean[:idx]
	}
	switch clean {
	case "fil", "tgl", "tagalog", "filipino", "philippino", "philipino":
		clean = "tl"
	case "eng", "english":
		clean = "en"
	case "jpn", "japanese":
		clean = "ja"
	case "spa", "spanish":
		clean = "es"
	case "fra", "fre", "french":
		clean = "fr"
	case "deu", "ger", "german":
		clean = "de"
	case "ita", "italian":
		clean = "it"
	case "por", "portuguese":
		clean = "pt"
	case "kor", "korean":
		clean = "ko"
	case "cmn", "zho", "chi", "chinese":
		clean = "zh"
	case "rus", "russian":
		clean = "ru"
	case "hin", "hindi":
		clean = "hi"
	case "ind", "indonesian":
		clean = "id"
	case "vie", "vietnamese":
		clean = "vi"
	case "tha", "thai":
		clean = "th"
	case "ara", "arb", "arabic":
		clean = "ar"
	case "nld", "dut", "dutch":
		clean = "nl"
	case "pol", "polish":
		clean = "pl"
	case "tur", "turkish":
		clean = "tr"
	case "ukr", "ukrainian":
		clean = "uk"
	}
	if _, ok := spokenLanguageMap[clean]; ok {
		return clean
	}
	return ""
}

// NormalizeSpokenLanguageList deduplicates and normalizes a list of spoken language codes,
// defaulting to ["en", "tl"] only when fallbackDefaults is non-empty and no valid codes were supplied.
func NormalizeSpokenLanguageList(langs []string, fallbackDefaults []string) []string {
	out := make([]string, 0, len(langs))
	seen := make(map[string]struct{}, len(langs))
	for _, raw := range langs {
		code := NormalizeSpokenLang(raw)
		if code == "" {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	if len(out) == 0 && len(fallbackDefaults) > 0 {
		for _, def := range fallbackDefaults {
			code := NormalizeSpokenLang(def)
			if code != "" {
				if _, exists := seen[code]; !exists {
					seen[code] = struct{}{}
					out = append(out, code)
				}
			}
		}
	}
	return out
}

// SpokenLanguageName returns the human-readable name for a normalized spoken language code.
func SpokenLanguageName(code string) string {
	norm := NormalizeSpokenLang(code)
	if name, ok := spokenLanguageMap[norm]; ok {
		return name
	}
	if code == "" {
		return "Auto-Detect"
	}
	return strings.ToUpper(strings.TrimSpace(code))
}

// Transcript represents a completed offline speech recognition result for a VAD segment.
type Transcript struct {
	Text          string
	RawText       string
	DetectedLang  string
	AudioDuration time.Duration
	Latency       time.Duration
}

// Engine defines the contract for offline speech-to-text recognition.
type Engine interface {
	Transcribe(samples []float32, sampleRate int, spokenLangs []string) (Transcript, error)
	ModelFamily() string
	Provider() string
	SetIdleTimeout(d time.Duration)
	Close() error
}

// SegmenterConfig configures neural Voice Activity Detection utterance segmentation.
type SegmenterConfig struct {
	VadModelPath     string
	SampleRate       int
	Threshold        float32
	MinSilenceMs     int
	MinSpeechMs      int
	MaxSpeechSeconds int
	NumThreads       int
	OnSpeechState    func(speaking bool)
	OnSegment        func(samples []float32)
}

// Segmenter consumes streaming 16 kHz mono float32 microphone frames and emits speech utterances.
type Segmenter interface {
	FeedSamples(samples []float32)
	Flush()
	Reset()
	IsSpeech() bool
	Close() error
}
