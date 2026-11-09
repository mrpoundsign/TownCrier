package audio

import (
	"math"
)

// EarconKind specifies a synthesized spatial audio cue for stream state transitions or mentions.
type EarconKind string

const (
	// EarconStreamOnline is a warm ascending two-tone chime played when a watched channel goes live.
	EarconStreamOnline EarconKind = "stream_online"
	// EarconStreamOffline is a soft descending two-tone chime played when a watched channel goes offline.
	EarconStreamOffline EarconKind = "stream_offline"
	// EarconMention is a crisp double-tap ping played when a message @mentions or replies to the listener.
	EarconMention EarconKind = "mention"
)

type toneSegment struct {
	freqHz     float64
	durationMs int
	gain       float64
}

// SynthesizeEarcon generates mono float32 PCM samples for the requested earcon kind at sampleRate (defaults to 24000 Hz).
func SynthesizeEarcon(kind EarconKind, sampleRate int) []float32 {
	if sampleRate <= 0 {
		sampleRate = 24000
	}

	var segments []toneSegment
	switch kind {
	case EarconStreamOnline:
		// C5 (523.25 Hz) -> G5 (783.99 Hz)
		segments = []toneSegment{
			{freqHz: 523.25, durationMs: 90, gain: 0.25},
			{freqHz: 783.99, durationMs: 140, gain: 0.28},
		}
	case EarconStreamOffline:
		// G5 (783.99 Hz) -> C5 (523.25 Hz)
		segments = []toneSegment{
			{freqHz: 783.99, durationMs: 90, gain: 0.22},
			{freqHz: 523.25, durationMs: 140, gain: 0.20},
		}
	case EarconMention:
		// E5 (659.25 Hz) -> gap -> A5 (880.0 Hz)
		segments = []toneSegment{
			{freqHz: 659.25, durationMs: 65, gain: 0.30},
			{freqHz: 0, durationMs: 30, gain: 0.0},
			{freqHz: 880.00, durationMs: 110, gain: 0.32},
		}
	default:
		segments = []toneSegment{
			{freqHz: 659.25, durationMs: 90, gain: 0.25},
		}
	}

	var totalSamples int
	for _, seg := range segments {
		totalSamples += (sampleRate * seg.durationMs) / 1000
	}
	out := make([]float32, 0, totalSamples)

	attackSamples := max((sampleRate*8)/1000, 1)

	for _, seg := range segments {
		n := (sampleRate * seg.durationMs) / 1000
		if n <= 0 {
			continue
		}
		if seg.freqHz <= 0 || seg.gain <= 0 {
			out = append(out, make([]float32, n)...)
			continue
		}
		for i := range n {
			t := float64(i) / float64(sampleRate)
			var env float64
			if i < attackSamples {
				env = float64(i) / float64(attackSamples)
			} else {
				progress := float64(i-attackSamples) / float64(max(n-attackSamples, 1))
				env = math.Exp(-3.2 * progress)
			}
			// Fundamental + subtle octave harmonic for warmth
			wave := 0.85*math.Sin(2.0*math.Pi*seg.freqHz*t) + 0.15*math.Sin(4.0*math.Pi*seg.freqHz*t)
			out = append(out, float32(wave*env*seg.gain))
		}
	}
	return out
}
