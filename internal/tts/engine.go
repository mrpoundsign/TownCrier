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

// KnownKokoroVoices maps voice ID names to speaker IDs.
var KnownKokoroVoices = map[string]int{
	"af_bella":    0,
	"af_sarah":    1,
	"am_adam":     2,
	"am_michael":  3,
	"bf_emma":     4,
	"bf_isabella": 5,
	"bm_george":   6,
	"bm_lewis":    7,
	"af_nicole":   8,
	"af_sky":      9,
	"am_eric":     10,
}

// KokoroVoiceNames returns the sorted list of predefined voice names.
var KokoroVoiceNames = []string{
	"af_bella",
	"af_nicole",
	"af_sarah",
	"af_sky",
	"am_adam",
	"am_eric",
	"am_michael",
	"bf_emma",
	"bf_isabella",
	"bm_george",
	"bm_lewis",
}

// Engine defines the contract for offline speech synthesis.
type Engine interface {
	Synthesize(text string, voiceID string, speed float32) ([]float32, int, error)
	Voices() []string
	NumSpeakers() int
	Close() error
}

// ResolveSpeakerID resolves a string voice ID to an integer speaker index, falling back to 0.
func ResolveSpeakerID(voiceID string) int {
	v := strings.ToLower(strings.TrimSpace(voiceID))
	if id, ok := KnownKokoroVoices[v]; ok {
		return id
	}
	return 0
}
