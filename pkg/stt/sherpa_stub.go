//go:build nosherpa

package stt

import "time"

// SherpaWhisperEngine is a stub when built with -tags nosherpa (e.g. standalone setup installers).
type SherpaWhisperEngine struct {
	family string
}

func NewSherpaWhisperEngine(family string, _ string, _ int, _ bool) (*SherpaWhisperEngine, error) {
	return &SherpaWhisperEngine{family: NormalizeModelFamily(family)}, ErrEngineClosed
}

func (e *SherpaWhisperEngine) ModelFamily() string               { return e.family }
func (e *SherpaWhisperEngine) Provider() string                  { return "none" }
func (e *SherpaWhisperEngine) SetIdleTimeout(_ time.Duration)    {}
func (e *SherpaWhisperEngine) Close() error                      { return nil }
func (e *SherpaWhisperEngine) Transcribe([]float32, int, []string) (Transcript, error) {
	return Transcript{}, ErrEngineClosed
}

// SherpaSegmenter is a stub when built with -tags nosherpa.
type SherpaSegmenter struct{}

func NewSherpaSegmenter(_ SegmenterConfig) (*SherpaSegmenter, error) {
	return nil, ErrEngineClosed
}

func (s *SherpaSegmenter) FeedSamples(_ []float32) {}
func (s *SherpaSegmenter) Flush()                  {}
func (s *SherpaSegmenter) Reset()                  {}
func (s *SherpaSegmenter) IsSpeech() bool          { return false }
func (s *SherpaSegmenter) Close() error            { return nil }
