package tts

import (
	"errors"
	"strings"
)

var (
	ErrEngineClosed    = errors.New("tts engine is closed")
	ErrSynthesisFailed = errors.New("tts synthesis failed")
	ErrModelNotFound   = errors.New("tts model files not found")
)

// DefaultVoiceID is the default voice used across TownCrier.
const DefaultVoiceID = "af_bella"

// VoiceOption describes a Kokoro neural voice with its internal ID, human-friendly display label, and speaker indices.
type VoiceOption struct {
	ID      string
	Label   string
	SIDv019 int // Speaker index in kokoro-en-v0_19 (11 speakers)
	SIDv10  int // Speaker index in kokoro-multi-lang-v1_0 (53 speakers)
}

// KokoroVoiceCatalog lists the built-in English Kokoro voices with human-readable labels.
var KokoroVoiceCatalog = []VoiceOption{
	{ID: "af", Label: "Default Blend — US Female (af)", SIDv019: 0, SIDv10: 3},
	{ID: "af_bella", Label: "Bella — US Female (af_bella)", SIDv019: 1, SIDv10: 2},
	{ID: "af_nicole", Label: "Nicole — US Female (af_nicole)", SIDv019: 2, SIDv10: 6},
	{ID: "af_sarah", Label: "Sarah — US Female (af_sarah)", SIDv019: 3, SIDv10: 9},
	{ID: "af_sky", Label: "Sky — US Female (af_sky)", SIDv019: 4, SIDv10: 10},
	{ID: "am_adam", Label: "Adam — US Male (am_adam)", SIDv019: 5, SIDv10: 11},
	{ID: "am_michael", Label: "Michael — US Male (am_michael)", SIDv019: 6, SIDv10: 16},
	{ID: "bf_emma", Label: "Emma — UK Female (bf_emma)", SIDv019: 7, SIDv10: 21},
	{ID: "bf_isabella", Label: "Isabella — UK Female (bf_isabella)", SIDv019: 8, SIDv10: 22},
	{ID: "bm_george", Label: "George — UK Male (bm_george)", SIDv019: 9, SIDv10: 26},
	{ID: "bm_lewis", Label: "Lewis — UK Male (bm_lewis)", SIDv019: 10, SIDv10: 27},
}

// KnownKokoroVoices maps voice ID names to speaker IDs in kokoro-en-v0_19.
var KnownKokoroVoices = map[string]int{
	"af":          0,
	"af_bella":    1,
	"af_nicole":   2,
	"af_sarah":    3,
	"af_sky":      4,
	"am_adam":     5,
	"am_michael":  6,
	"bf_emma":     7,
	"bf_isabella": 8,
	"bm_george":   9,
	"bm_lewis":    10,
}

// KnownKokoroV10Voices maps voice ID names to speaker IDs in kokoro-multi-lang-v1_0 (53 speakers).
var KnownKokoroV10Voices = map[string]int{
	"af":          3, // af_heart
	"af_alloy":    0,
	"af_aoede":    1,
	"af_bella":    2,
	"af_heart":    3,
	"af_jessica":  4,
	"af_kore":     5,
	"af_nicole":   6,
	"af_nova":     7,
	"af_river":    8,
	"af_sarah":    9,
	"af_sky":      10,
	"am_adam":     11,
	"am_echo":     12,
	"am_eric":     13,
	"am_fenrir":   14,
	"am_liam":     15,
	"am_michael":  16,
	"am_onyx":     17,
	"am_puck":     18,
	"am_santa":    19,
	"bf_alice":    20,
	"bf_emma":     21,
	"bf_isabella": 22,
	"bf_lily":     23,
	"bm_daniel":   24,
	"bm_fable":    25,
	"bm_george":   26,
	"bm_lewis":    27,
}

// KokoroVoiceNames lists all predefined voice IDs.
var KokoroVoiceNames = []string{
	"af",
	"af_bella",
	"af_nicole",
	"af_sarah",
	"af_sky",
	"am_adam",
	"am_michael",
	"bf_emma",
	"bf_isabella",
	"bm_george",
	"bm_lewis",
}

// VoiceDisplayLabels returns the human-friendly display strings for UI Select dropdowns.
func VoiceDisplayLabels() []string {
	labels := make([]string, len(KokoroVoiceCatalog))
	for i, v := range KokoroVoiceCatalog {
		labels[i] = v.Label
	}
	return labels
}

// VoiceDisplayName returns the human-friendly display label for a voice ID.
func VoiceDisplayName(voiceID string) string {
	id := VoiceIDFromLabel(voiceID)
	for _, v := range KokoroVoiceCatalog {
		if v.ID == id {
			return v.Label
		}
	}
	if id == "" {
		return "Bella — US Female (af_bella)"
	}
	return id
}

// VoiceIDFromLabel extracts the canonical voice ID (e.g. "af_bella") from a display label or raw ID.
func VoiceIDFromLabel(labelOrID string) string {
	s := strings.TrimSpace(labelOrID)
	if s == "" {
		return DefaultVoiceID
	}
	lower := strings.ToLower(s)
	for _, v := range KokoroVoiceCatalog {
		if strings.EqualFold(v.Label, s) || v.ID == lower {
			return v.ID
		}
	}
	// Check if label ends with "(id)"
	if openIdx := strings.LastIndexByte(lower, '('); openIdx != -1 && strings.HasSuffix(lower, ")") {
		extracted := strings.TrimSpace(lower[openIdx+1 : len(lower)-1])
		if extracted != "" {
			return extracted
		}
	}
	return lower
}

// Engine defines the contract for offline speech synthesis.
type Engine interface {
	Synthesize(text string, voiceID string, speed float32) ([]float32, int, error)
	Voices() []string
	NumSpeakers() int
	Close() error
}

// StreamCallback receives synthesized audio chunks (clauses) as they are generated.
// Returning false aborts synthesis of remaining clauses immediately.
type StreamCallback func(samples []float32, sampleRate int) bool

// StreamingEngine is an optional extension of Engine that supports clause-by-clause
// synthesis streaming to reduce peak RAM and Time-To-First-Audio (TTFA).
type StreamingEngine interface {
	Engine
	SynthesizeStream(text string, voiceID string, speed float32, onChunk StreamCallback) error
}

// ResolveSpeakerID resolves a string voice ID or display label to an integer speaker index for v0.19, falling back to 0.
func ResolveSpeakerID(voiceID string) int {
	return ResolveSpeakerIDForModel(voiceID, 11)
}

// ResolveSpeakerIDForModel resolves a voice ID or display label to the speaker index matching the loaded model's speaker count.
func ResolveSpeakerIDForModel(voiceID string, numSpeakers int) int {
	v := VoiceIDFromLabel(voiceID)
	if numSpeakers >= 53 {
		if id, ok := KnownKokoroV10Voices[v]; ok && id < numSpeakers {
			return id
		}
	}
	if id, ok := KnownKokoroVoices[v]; ok {
		if numSpeakers <= 0 || id < numSpeakers {
			return id
		}
	}
	return 0
}

