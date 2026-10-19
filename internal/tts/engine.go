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
	{ID: "af_heart", Label: "Heart — US Female (af_heart)", SIDv019: 0, SIDv10: 3},
	{ID: "af_alloy", Label: "Alloy — US Female (af_alloy)", SIDv019: 0, SIDv10: 0},
	{ID: "af_aoede", Label: "Aoede — US Female (af_aoede)", SIDv019: 1, SIDv10: 1},
	{ID: "af_bella", Label: "Bella — US Female (af_bella)", SIDv019: 1, SIDv10: 2},
	{ID: "af_jessica", Label: "Jessica — US Female (af_jessica)", SIDv019: 2, SIDv10: 4},
	{ID: "af_kore", Label: "Kore — US Female (af_kore)", SIDv019: 2, SIDv10: 5},
	{ID: "af_nicole", Label: "Nicole — US Female (af_nicole)", SIDv019: 2, SIDv10: 6},
	{ID: "af_nova", Label: "Nova — US Female (af_nova)", SIDv019: 3, SIDv10: 7},
	{ID: "af_river", Label: "River — US Female (af_river)", SIDv019: 3, SIDv10: 8},
	{ID: "af_sarah", Label: "Sarah — US Female (af_sarah)", SIDv019: 3, SIDv10: 9},
	{ID: "af_sky", Label: "Sky — US Female (af_sky)", SIDv019: 4, SIDv10: 10},
	{ID: "am_adam", Label: "Adam — US Male (am_adam)", SIDv019: 5, SIDv10: 11},
	{ID: "am_echo", Label: "Echo — US Male (am_echo)", SIDv019: 5, SIDv10: 12},
	{ID: "am_eric", Label: "Eric — US Male (am_eric)", SIDv019: 5, SIDv10: 13},
	{ID: "am_fenrir", Label: "Fenrir — US Male (am_fenrir)", SIDv019: 6, SIDv10: 14},
	{ID: "am_liam", Label: "Liam — US Male (am_liam)", SIDv019: 6, SIDv10: 15},
	{ID: "am_michael", Label: "Michael — US Male (am_michael)", SIDv019: 6, SIDv10: 16},
	{ID: "am_onyx", Label: "Onyx — US Male (am_onyx)", SIDv019: 6, SIDv10: 17},
	{ID: "am_puck", Label: "Puck — US Male (am_puck)", SIDv019: 6, SIDv10: 18},
	{ID: "am_santa", Label: "Santa — US Male (am_santa)", SIDv019: 5, SIDv10: 19},
	{ID: "bf_alice", Label: "Alice — UK Female (bf_alice)", SIDv019: 7, SIDv10: 20},
	{ID: "bf_emma", Label: "Emma — UK Female (bf_emma)", SIDv019: 7, SIDv10: 21},
	{ID: "bf_isabella", Label: "Isabella — UK Female (bf_isabella)", SIDv019: 8, SIDv10: 22},
	{ID: "bf_lily", Label: "Lily — UK Female (bf_lily)", SIDv019: 8, SIDv10: 23},
	{ID: "bm_daniel", Label: "Daniel — UK Male (bm_daniel)", SIDv019: 9, SIDv10: 24},
	{ID: "bm_fable", Label: "Fable — UK Male (bm_fable)", SIDv019: 9, SIDv10: 25},
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
	"ef_dora":     28,
	"em_alex":     29,
	"em_santa":    30,
	"ff_siwis":    31,
	"hf_alpha":    32,
	"hf_beta":     33,
	"hm_omega":    34,
	"hm_psi":      35,
	"if_sara":     36,
	"im_nicola":   37,
	"jf_alpha":    38,
	"jf_gongitsune": 39,
	"jf_nezumi":   40,
	"jf_tebukuro": 41,
	"jm_kumo":     42,
	"pf_dora":     43,
	"pm_alex":     44,
	"pm_santa":    45,
	"zf_xiaobei":  46,
	"zf_xiaoni":   47,
	"zf_xiaoxiao": 48,
	"zf_xiaoyi":   49,
	"zm_yunjian":  50,
	"zm_yunxi":    51,
	"zm_yunyang":  52,
}

// KokoroVoiceNames lists all predefined voice IDs.
var KokoroVoiceNames = []string{
	"af",
	"af_heart",
	"af_alloy",
	"af_aoede",
	"af_bella",
	"af_jessica",
	"af_kore",
	"af_nicole",
	"af_nova",
	"af_river",
	"af_sarah",
	"af_sky",
	"am_adam",
	"am_echo",
	"am_eric",
	"am_fenrir",
	"am_liam",
	"am_michael",
	"am_onyx",
	"am_puck",
	"am_santa",
	"bf_alice",
	"bf_emma",
	"bf_isabella",
	"bf_lily",
	"bm_daniel",
	"bm_fable",
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

// VoiceDisplayName returns the human-friendly display label for a voice ID across all model families.
func VoiceDisplayName(voiceID string) string {
	id := VoiceIDFromLabel(voiceID)
	for _, v := range AllVoiceOptions() {
		if v.ID == id {
			return v.Label
		}
	}
	if id == "" {
		return "Bella — US Female (af_bella)"
	}
	return id
}

// VoiceIDFromLabel extracts the canonical voice ID (e.g. "af_bella" or "supertonic:f1") from a display label or raw ID.
func VoiceIDFromLabel(labelOrID string) string {
	s := strings.TrimSpace(labelOrID)
	if s == "" {
		return DefaultVoiceID
	}
	lower := strings.ToLower(s)
	for _, v := range AllVoiceOptions() {
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
	v = strings.TrimPrefix(v, "kokoro:")
	v = strings.TrimPrefix(v, "kokoro_intl:")
	if numSpeakers >= 53 || numSpeakers <= 0 {
		if id, ok := KnownKokoroV10Voices[v]; ok {
			if numSpeakers <= 0 || id < numSpeakers {
				return id
			}
		}
	}
	if id, ok := KnownKokoroVoices[v]; ok {
		if numSpeakers <= 0 || id < numSpeakers {
			return id
		}
	}
	return 0
}

