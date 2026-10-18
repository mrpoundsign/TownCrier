package tts

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// SherpaEngine wraps sherpa-onnx OfflineTts for Kokoro speech synthesis.
type SherpaEngine struct {
	mu             sync.Mutex
	tts            *sherpa.OfflineTts
	modelDir       string
	numThreads     int
	useGPU         bool
	maxClauseWords int
	idleTimeout    time.Duration
	idleTimer      *time.Timer
	sampleRate     int
	numSpk         int
	provider       string
	closed         bool
}

// NewSherpaEngine creates a new SherpaEngine instance from the specified model directory.
// If useGPU is true, it attempts to use DirectML with the floating-point model (model.onnx),
// falling back to CPU with the quantized model (model.int8.onnx) if unavailable.
func NewSherpaEngine(modelDir string, numThreads int, useGPU bool) (*SherpaEngine, error) {
	if numThreads <= 0 {
		numThreads = 4
	}

	e := &SherpaEngine{
		modelDir:       modelDir,
		numThreads:     numThreads,
		useGPU:         useGPU,
		maxClauseWords: DefaultMaxClauseWords,
	}

	if err := e.ensureLoadedLocked(); err != nil {
		return nil, err
	}

	return e, nil
}

func (e *SherpaEngine) ensureLoadedLocked() error {
	if e.closed {
		return ErrEngineClosed
	}
	if e.tts != nil {
		return nil
	}

	voicesFile := filepath.Join(e.modelDir, "voices.bin")
	tokensFile := filepath.Join(e.modelDir, "tokens.txt")
	dataDir := filepath.Join(e.modelDir, "espeak-ng-data")

	var modelFile string
	provider := "cpu"

	if e.useGPU {
		// Prefer full floating-point model for DirectML GPU execution
		fpModel := filepath.Join(e.modelDir, "model.onnx")
		if _, err := os.Stat(fpModel); err == nil {
			modelFile = fpModel
		} else {
			modelFile = filepath.Join(e.modelDir, "model.int8.onnx")
		}
		provider = "directml"
	} else {
		// Prefer INT8 quantized model for CPU SIMD execution
		int8Model := filepath.Join(e.modelDir, "model.int8.onnx")
		if _, err := os.Stat(int8Model); err == nil {
			modelFile = int8Model
		} else {
			modelFile = filepath.Join(e.modelDir, "model.onnx")
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
			NumThreads: e.numThreads,
			Debug:      0,
			Provider:   provider,
		},
	}

	tts := sherpa.NewOfflineTts(config)
	if tts == nil && e.useGPU {
		// Fallback to CPU with INT8 model if DirectML fails on host system
		config.Model.Provider = "cpu"
		int8Model := filepath.Join(e.modelDir, "model.int8.onnx")
		if _, err := os.Stat(int8Model); err == nil {
			config.Model.Kokoro.Model = int8Model
		}
		tts = sherpa.NewOfflineTts(config)
		provider = "cpu"
	}

	if tts == nil {
		return fmt.Errorf("initialize sherpa-onnx OfflineTts from %s: %w", e.modelDir, ErrModelNotFound)
	}

	sampleRate := tts.SampleRate()
	if sampleRate <= 0 {
		sampleRate = 24000
	}

	e.tts = tts
	e.sampleRate = sampleRate
	e.numSpk = tts.NumSpeakers()
	e.provider = provider

	ReclaimOSMemory()
	return nil
}

// Provider returns the active execution provider ("directml" or "cpu").
func (e *SherpaEngine) Provider() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.provider
}

// SetMaxClauseWords configures the maximum words per synthesized prosodic clause.
func (e *SherpaEngine) SetMaxClauseWords(maxWords int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if maxWords <= 0 {
		maxWords = DefaultMaxClauseWords
	}
	e.maxClauseWords = maxWords
}

// MaxClauseWords returns the configured maximum words per synthesized prosodic clause.
func (e *SherpaEngine) MaxClauseWords() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.maxClauseWords <= 0 {
		return DefaultMaxClauseWords
	}
	return e.maxClauseWords
}

// SetIdleTimeout configures automatic native model unloading after d of inactivity (0 disables).
func (e *SherpaEngine) SetIdleTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.idleTimeout = d
	e.resetIdleTimerLocked()
}

func (e *SherpaEngine) resetIdleTimerLocked() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	if e.idleTimeout > 0 && !e.closed && e.tts != nil {
		d := e.idleTimeout
		e.idleTimer = time.AfterFunc(d, e.unloadIfIdle)
	}
}

func (e *SherpaEngine) unloadIfIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.tts == nil {
		return
	}
	log.Printf("[TTS] Unloading idle Kokoro engine after %v of inactivity to reclaim RAM...", e.idleTimeout)
	sherpa.DeleteOfflineTts(e.tts)
	e.tts = nil
	ReclaimOSMemory()
}

// SynthesizeStream splits input text into prosodic clauses and invokes onChunk for each clause.
// This bounds ONNX Runtime intermediate tensor memory and dramatically lowers Time-To-First-Audio.
func (e *SherpaEngine) SynthesizeStream(text string, voiceID string, speed float32, onChunk StreamCallback) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrEngineClosed
	}
	maxWords := e.maxClauseWords
	if maxWords <= 0 {
		maxWords = DefaultMaxClauseWords
	}
	sr := e.sampleRate
	if sr <= 0 {
		sr = 24000
	}
	e.mu.Unlock()

	if text == "" {
		return nil
	}

	if speed <= 0.2 {
		speed = 1.0
	} else if speed > 3.0 {
		speed = 3.0
	}

	clauses := SplitIntoClauses(text, maxWords)
	if len(clauses) == 0 {
		return nil
	}

	for i, clause := range clauses {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return ErrEngineClosed
		}
		if err := e.ensureLoadedLocked(); err != nil {
			e.mu.Unlock()
			return err
		}
		sid := ResolveSpeakerIDForModel(voiceID, e.numSpk)
		audio := e.tts.Generate(clause, sid, speed)
		e.resetIdleTimerLocked()
		e.mu.Unlock()

		if audio == nil || len(audio.Samples) == 0 {
			return ErrSynthesisFailed
		}

		chunkSamples := audio.Samples
		chunkSR := audio.SampleRate
		if chunkSR <= 0 {
			chunkSR = sr
		}

		// Append a brief ~30ms prosodic pause between clauses (except the final clause)
		if i < len(clauses)-1 {
			padLen := chunkSR * 30 / 1000
			if padLen > 0 {
				padded := make([]float32, len(chunkSamples)+padLen)
				copy(padded, chunkSamples)
				chunkSamples = padded
			}
		}

		if onChunk != nil && !onChunk(chunkSamples, chunkSR) {
			return nil
		}
	}

	return nil
}

// Synthesize produces float32 PCM samples and sample rate for the input text.
func (e *SherpaEngine) Synthesize(text string, voiceID string, speed float32) ([]float32, int, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, 0, ErrEngineClosed
	}
	sr := e.sampleRate
	if sr <= 0 {
		sr = 24000
	}
	e.mu.Unlock()

	if text == "" {
		return []float32{}, sr, nil
	}

	var allSamples []float32
	outSR := sr

	err := e.SynthesizeStream(text, voiceID, speed, func(samples []float32, sampleRate int) bool {
		if sampleRate > 0 {
			outSR = sampleRate
		}
		allSamples = append(allSamples, samples...)
		return true
	})
	if err != nil {
		return nil, 0, err
	}
	if len(allSamples) == 0 {
		return nil, 0, ErrSynthesisFailed
	}

	return allSamples, outSR, nil
}

// Voices returns available Kokoro voice names.
func (e *SherpaEngine) Voices() []string {
	return KokoroVoiceNames
}

// NumSpeakers returns the total speaker count in the loaded model.
func (e *SherpaEngine) NumSpeakers() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.numSpk
}

// Close destroys the native sherpa-onnx TTS engine and reclaims OS working set memory.
func (e *SherpaEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}
	e.closed = true

	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}

	if e.tts != nil {
		sherpa.DeleteOfflineTts(e.tts)
		e.tts = nil
	}

	ReclaimOSMemory()
	return nil
}

