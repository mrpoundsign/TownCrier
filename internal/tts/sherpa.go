package tts

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// SherpaEngine wraps sherpa-onnx OfflineTts for Kokoro speech synthesis.
type SherpaEngine struct {
	mu         sync.Mutex
	tts        *sherpa.OfflineTts
	sampleRate int
	numSpk     int
	provider   string
	closed     bool
}

// NewSherpaEngine creates a new SherpaEngine instance from the specified model directory.
// If useGPU is true, it attempts to use DirectML with the floating-point model (model.onnx),
// falling back to CPU with the quantized model (model.int8.onnx) if unavailable.
func NewSherpaEngine(modelDir string, numThreads int, useGPU bool) (*SherpaEngine, error) {
	if numThreads <= 0 {
		numThreads = 4
	}

	voicesFile := filepath.Join(modelDir, "voices.bin")
	tokensFile := filepath.Join(modelDir, "tokens.txt")
	dataDir := filepath.Join(modelDir, "espeak-ng-data")

	var modelFile string
	provider := "cpu"

	if useGPU {
		// Prefer full floating-point model for DirectML GPU execution
		fpModel := filepath.Join(modelDir, "model.onnx")
		if _, err := os.Stat(fpModel); err == nil {
			modelFile = fpModel
		} else {
			modelFile = filepath.Join(modelDir, "model.int8.onnx")
		}
		provider = "directml"
	} else {
		// Prefer INT8 quantized model for CPU SIMD execution
		int8Model := filepath.Join(modelDir, "model.int8.onnx")
		if _, err := os.Stat(int8Model); err == nil {
			modelFile = int8Model
		} else {
			modelFile = filepath.Join(modelDir, "model.onnx")
		}
	}

	config := &sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:       modelFile,
				Voices:      voicesFile,
				Tokens:      tokensFile,
				DataDir:     dataDir,
				LengthScale: 1.0,
			},
			NumThreads: numThreads,
			Debug:      0,
			Provider:   provider,
		},
	}

	tts := sherpa.NewOfflineTts(config)
	if tts == nil && useGPU {
		// Fallback to CPU with INT8 model if DirectML fails on host system
		config.Model.Provider = "cpu"
		int8Model := filepath.Join(modelDir, "model.int8.onnx")
		if _, err := os.Stat(int8Model); err == nil {
			config.Model.Kokoro.Model = int8Model
		}
		tts = sherpa.NewOfflineTts(config)
		provider = "cpu"
	}

	if tts == nil {
		return nil, fmt.Errorf("initialize sherpa-onnx OfflineTts from %s: %w", modelDir, ErrModelNotFound)
	}

	sampleRate := tts.SampleRate()
	if sampleRate <= 0 {
		sampleRate = 24000
	}

	return &SherpaEngine{
		tts:        tts,
		sampleRate: sampleRate,
		numSpk:     tts.NumSpeakers(),
		provider:   provider,
	}, nil
}

// Provider returns the active execution provider ("directml" or "cpu").
func (e *SherpaEngine) Provider() string {
	return e.provider
}

// Synthesize produces float32 PCM samples and sample rate for the input text.
func (e *SherpaEngine) Synthesize(text string, voiceID string, speed float32) ([]float32, int, error) {
	if text == "" {
		return []float32{}, e.sampleRate, nil
	}

	if speed <= 0.2 {
		speed = 1.0
	} else if speed > 3.0 {
		speed = 3.0
	}

	sid := ResolveSpeakerIDForModel(voiceID, e.numSpk)

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || e.tts == nil {
		return nil, 0, ErrEngineClosed
	}

	audio := e.tts.Generate(text, sid, speed)
	if audio == nil || len(audio.Samples) == 0 {
		return nil, 0, ErrSynthesisFailed
	}

	return audio.Samples, audio.SampleRate, nil
}

// Voices returns available Kokoro voice names.
func (e *SherpaEngine) Voices() []string {
	return KokoroVoiceNames
}

// NumSpeakers returns the total speaker count in the loaded model.
func (e *SherpaEngine) NumSpeakers() int {
	return e.numSpk
}

// Close destroys the native sherpa-onnx TTS engine.
func (e *SherpaEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}
	e.closed = true

	if e.tts != nil {
		sherpa.DeleteOfflineTts(e.tts)
		e.tts = nil
	}
	return nil
}
