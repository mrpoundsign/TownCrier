//go:build nosherpa

package tts

import "time"

// SherpaEngine is a stub when built with -tags nosherpa (e.g. for standalone setup installers
// that only use package tts for downloading model packs without linking sherpa-onnx C libraries).
type SherpaEngine struct {
	family string
}

func NewSherpaEngine(modelDir string, numThreads int, useGPU bool) (*SherpaEngine, error) {
	return NewFamilySherpaEngine(ModelKokoro, modelDir, numThreads, useGPU)
}

func NewFamilySherpaEngine(family string, _ string, _ int, _ bool) (*SherpaEngine, error) {
	return &SherpaEngine{family: family}, ErrEngineClosed
}

func (e *SherpaEngine) Family() string                                         { return e.family }
func (e *SherpaEngine) Provider() string                                       { return "none" }
func (e *SherpaEngine) SetMaxClauseWords(_ int)                                {}
func (e *SherpaEngine) MaxClauseWords() int                                    { return DefaultMaxClauseWords }
func (e *SherpaEngine) SetIdleTimeout(_ time.Duration)                         {}
func (e *SherpaEngine) SynthesizeStream(string, string, float32, StreamCallback) error {
	return ErrEngineClosed
}
func (e *SherpaEngine) Synthesize(string, string, float32) ([]float32, int, error) {
	return nil, 0, ErrEngineClosed
}
func (e *SherpaEngine) Voices() []string { return nil }
func (e *SherpaEngine) NumSpeakers() int { return 0 }
func (e *SherpaEngine) Close() error     { return nil }
