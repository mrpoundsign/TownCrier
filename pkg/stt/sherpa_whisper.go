//go:build !nosherpa

package stt

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"

	"github.com/mrpoundsign/TownCrier/pkg/tts"
)

// SherpaWhisperEngine wraps sherpa-onnx OfflineRecognizer for local multilingual Whisper speech recognition.
type SherpaWhisperEngine struct {
	mu           sync.Mutex
	recognizer   *sherpa.OfflineRecognizer
	activeConfig sherpa.OfflineRecognizerConfig
	activeLang   string
	family       string
	baseSTTDir   string
	numThreads   int
	useGPU       bool
	provider     string
	providerAtom atomic.Value
	idleTimeout  time.Duration
	idleTimer    *time.Timer
	closed       bool
}

// NewSherpaWhisperEngine initializes a local offline CPU Whisper recognizer for legacy sherpa-onnx ONNX weights.
func NewSherpaWhisperEngine(family string, baseSTTDir string, numThreads int, useGPU bool) (*SherpaWhisperEngine, error) {
	if numThreads <= 0 {
		numThreads = SmartSTTThreads(0, 0, false)
	}
	normFamily := NormalizeModelFamily(family)
	if strings.TrimSpace(baseSTTDir) == "" {
		baseSTTDir = DefaultSTTModelDir()
	}

	e := &SherpaWhisperEngine{
		family:     normFamily,
		baseSTTDir: baseSTTDir,
		numThreads: numThreads,
		useGPU:     useGPU,
	}

	e.mu.Lock()
	err := e.ensureLoadedLocked("")
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (e *SherpaWhisperEngine) ensureLoadedLocked(whisperLang string) error {
	if e.closed {
		return ErrEngineClosed
	}
	if e.recognizer != nil {
		if e.activeLang != whisperLang {
			e.activeConfig.ModelConfig.Whisper.Language = whisperLang
			e.recognizer.SetConfig(&e.activeConfig)
			e.activeLang = whisperLang
		}
		return nil
	}

	// Bundled sherpa-onnx-go binaries are CPU-only; always prefer INT8 ONNX weights on CPU so FP32 weights
	// are never silently loaded onto CPU threads.
	encPath, decPath, tokPath, _, err := ResolveWhisperFiles(e.baseSTTDir, e.family, false)
	if err != nil {
		return err
	}

	provider := "cpu"
	cfg := sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: DefaultSampleRate,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OfflineModelConfig{
			Whisper: sherpa.OfflineWhisperModelConfig{
				Encoder:      encPath,
				Decoder:      decPath,
				Language:     whisperLang,
				Task:         "transcribe",
				TailPaddings: -1,
			},
			Tokens:     tokPath,
			NumThreads: e.numThreads,
			Debug:      0,
			Provider:   provider,
		},
		DecodingMethod: "greedy_search",
	}

	rec := sherpa.NewOfflineRecognizer(&cfg)
	if rec == nil {
		return fmt.Errorf("initialize sherpa-onnx OfflineRecognizer (%s) from %s: %w", e.family, e.baseSTTDir, ErrModelNotFound)
	}

	e.recognizer = rec
	e.activeConfig = cfg
	e.activeLang = whisperLang
	e.provider = fmt.Sprintf("cpu-onnx (%d threads)", e.numThreads)
	e.providerAtom.Store(e.provider)
	tts.ReclaimOSMemory()
	return nil
}

// ModelFamily returns the active Whisper model family ID.
func (e *SherpaWhisperEngine) ModelFamily() string {
	return e.family
}

// Provider returns the active ONNX execution provider without locking e.mu during active decoding.
func (e *SherpaWhisperEngine) Provider() string {
	if v, ok := e.providerAtom.Load().(string); ok {
		return v
	}
	return ""
}

// SetIdleTimeout configures automatic native model unloading after d of inactivity (0 disables).
func (e *SherpaWhisperEngine) SetIdleTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.idleTimeout = d
	e.resetIdleTimerLocked()
}

func (e *SherpaWhisperEngine) resetIdleTimerLocked() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	if e.idleTimeout > 0 && !e.closed && e.recognizer != nil {
		e.idleTimer = time.AfterFunc(e.idleTimeout, e.unloadIfIdle)
	}
}

func (e *SherpaWhisperEngine) unloadIfIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.recognizer == nil {
		return
	}
	log.Printf("[STT] Unloading idle Whisper engine (%s) after %v of inactivity to reclaim RAM...", e.family, e.idleTimeout)
	sherpa.DeleteOfflineRecognizer(e.recognizer)
	e.recognizer = nil
	tts.ReclaimOSMemory()
}

// Transcribe decodes mono float32 PCM samples into cleaned text and resolved ISO-639-1 spoken language.
// If spokenLangs contains exactly 1 valid language, Whisper is locked to that language; otherwise Whisper
// runs multilingual language identification per utterance.
func (e *SherpaWhisperEngine) Transcribe(samples []float32, sampleRate int, spokenLangs []string) (Transcript, error) {
	if len(samples) == 0 {
		return Transcript{}, nil
	}
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}

	normSpoken := NormalizeSpokenLanguageList(spokenLangs, nil)
	whisperLangHint := ""
	if len(normSpoken) == 1 {
		whisperLangHint = normSpoken[0]
	}

	start := time.Now()

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return Transcript{}, ErrEngineClosed
	}
	if err := e.ensureLoadedLocked(whisperLangHint); err != nil {
		e.mu.Unlock()
		return Transcript{}, err
	}

	stream := sherpa.NewOfflineStream(e.recognizer)
	if stream == nil {
		e.resetIdleTimerLocked()
		e.mu.Unlock()
		return Transcript{}, ErrTranscribeFailed
	}

	stream.AcceptWaveform(sampleRate, samples)
	e.recognizer.Decode(stream)
	rawRes := stream.GetResult()
	sherpa.DeleteOfflineStream(stream)
	e.resetIdleTimerLocked()
	e.mu.Unlock()

	latency := time.Since(start)
	audioDur := time.Duration(int64(len(samples)) * int64(time.Second) / int64(sampleRate))

	if rawRes == nil {
		return Transcript{AudioDuration: audioDur, Latency: latency}, nil
	}

	rawText := strings.TrimSpace(rawRes.Text)
	cleaned, ok := CleanTranscript(rawText)
	if !ok {
		return Transcript{
			RawText:       rawText,
			AudioDuration: audioDur,
			Latency:       latency,
		}, nil
	}

	rawLang := strings.TrimSpace(rawRes.Lang)
	if rawLang == "" {
		for i := range min(len(rawRes.Tokens), 4) {
			tok := strings.TrimSpace(rawRes.Tokens[i])
			if strings.HasPrefix(tok, "<|") && strings.HasSuffix(tok, "|>") {
				if cand := NormalizeSpokenLang(tok); cand != "" {
					rawLang = cand
					break
				}
			}
		}
	}

	resolvedLang := ResolveSpokenLang(rawLang, cleaned, normSpoken)
	log.Printf("[STT] Whisper (%s/%s, %dms audio, %dms decode): raw_lang=%q resolved=%q text=%q",
		e.family, e.provider, audioDur.Milliseconds(), latency.Milliseconds(), rawRes.Lang, resolvedLang, cleaned)
	return Transcript{
		Text:          cleaned,
		RawText:       rawText,
		DetectedLang:  resolvedLang,
		AudioDuration: audioDur,
		Latency:       latency,
	}, nil
}

// Close releases the native sherpa-onnx OfflineRecognizer and reclaims OS memory.
func (e *SherpaWhisperEngine) Close() error {
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
	if e.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(e.recognizer)
		e.recognizer = nil
	}
	tts.ReclaimOSMemory()
	return nil
}

type vadCmdKind int

const (
	vadCmdSamples vadCmdKind = iota
	vadCmdFlush
	vadCmdReset
)

type vadCmd struct {
	kind    vadCmdKind
	samples []float32
	done    chan struct{}
}

// SherpaSegmenter runs Silero VAD on a dedicated background goroutine so real-time audio callbacks never block.
type SherpaSegmenter struct {
	cfg      SegmenterConfig
	vad      *sherpa.VoiceActivityDetector
	cmdCh    chan vadCmd
	stopCh   chan struct{}
	wg       sync.WaitGroup
	speaking atomic.Bool
	closed   atomic.Bool
}

// NewSherpaSegmenter creates and starts a background Silero VAD segmenter worker.
func NewSherpaSegmenter(cfg SegmenterConfig) (*SherpaSegmenter, error) {
	if strings.TrimSpace(cfg.VadModelPath) == "" {
		cfg.VadModelPath = VadModelPath("")
	}
	if !fileExistsNonEmpty(cfg.VadModelPath) {
		return nil, fmt.Errorf("%w: missing silero_vad.onnx at %s", ErrModelNotFound, cfg.VadModelPath)
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = DefaultSampleRate
	}
	if cfg.Threshold <= 0.05 || cfg.Threshold >= 0.99 {
		cfg.Threshold = DefaultVadThreshold
	}
	if cfg.MinSilenceMs <= 0 {
		cfg.MinSilenceMs = DefaultMinSilenceMs
	}
	if cfg.MinSpeechMs <= 0 {
		cfg.MinSpeechMs = DefaultMinSpeechMs
	}
	if cfg.MaxSpeechSeconds <= 0 {
		cfg.MaxSpeechSeconds = DefaultMaxSpeechSec
	}
	if cfg.NumThreads <= 0 {
		cfg.NumThreads = 1
	}

	winSize := 512
	if cfg.SampleRate == 8000 {
		winSize = 256
	}

	vadCfg := sherpa.VadModelConfig{
		SileroVad: sherpa.SileroVadModelConfig{
			Model:              cfg.VadModelPath,
			Threshold:          cfg.Threshold,
			MinSilenceDuration: float32(cfg.MinSilenceMs) / 1000.0,
			MinSpeechDuration:  float32(cfg.MinSpeechMs) / 1000.0,
			WindowSize:         winSize,
			MaxSpeechDuration:  float32(cfg.MaxSpeechSeconds),
		},
		SampleRate: cfg.SampleRate,
		NumThreads: cfg.NumThreads,
		Provider:   "cpu",
		Debug:      0,
	}

	bufSeconds := max(float32(cfg.MaxSpeechSeconds)*2.5, 30.0)
	vad := sherpa.NewVoiceActivityDetector(&vadCfg, bufSeconds)
	if vad == nil {
		return nil, fmt.Errorf("initialize sherpa-onnx VoiceActivityDetector from %s: %w", cfg.VadModelPath, ErrModelNotFound)
	}

	seg := &SherpaSegmenter{
		cfg:    cfg,
		vad:    vad,
		cmdCh:  make(chan vadCmd, 256),
		stopCh: make(chan struct{}),
	}
	seg.wg.Go(seg.workerLoop)
	return seg, nil
}

func (s *SherpaSegmenter) workerLoop() {
	for {
		select {
		case <-s.stopCh:
			return
		case cmd := <-s.cmdCh:
			switch cmd.kind {
			case vadCmdSamples:
				if len(cmd.samples) > 0 && s.vad != nil {
					s.vad.AcceptWaveform(cmd.samples)
					s.syncSpeechStateAndDrain()
				}
			case vadCmdFlush:
				if s.vad != nil {
					s.vad.Flush()
					s.syncSpeechStateAndDrain()
				}
				if cmd.done != nil {
					close(cmd.done)
				}
			case vadCmdReset:
				if s.vad != nil {
					s.vad.Clear()
					s.vad.Reset()
				}
				if s.speaking.Swap(false) && s.cfg.OnSpeechState != nil {
					s.cfg.OnSpeechState(false)
				}
				if cmd.done != nil {
					close(cmd.done)
				}
			}
		}
	}
}

func (s *SherpaSegmenter) syncSpeechStateAndDrain() {
	curSpeech := s.vad.IsSpeech()
	if s.speaking.Swap(curSpeech) != curSpeech && s.cfg.OnSpeechState != nil {
		s.cfg.OnSpeechState(curSpeech)
	}

	for !s.vad.IsEmpty() {
		front := s.vad.Front()
		var segSamples []float32
		if front != nil && len(front.Samples) > 0 {
			segSamples = slices.Clone(front.Samples)
		}
		s.vad.Pop()
		if len(segSamples) > 0 && s.cfg.OnSegment != nil {
			s.cfg.OnSegment(segSamples)
		}
	}

	afterSpeech := s.vad.IsSpeech()
	if s.speaking.Swap(afterSpeech) != afterSpeech && s.cfg.OnSpeechState != nil {
		s.cfg.OnSpeechState(afterSpeech)
	}
}

// FeedSamples enqueues PCM samples for non-blocking background VAD evaluation.
func (s *SherpaSegmenter) FeedSamples(samples []float32) {
	if s.closed.Load() || len(samples) == 0 {
		return
	}
	select {
	case s.cmdCh <- vadCmd{kind: vadCmdSamples, samples: slices.Clone(samples)}:
	default:
		// Drop frame if worker falls far behind rather than stalling the audio capture callback.
	}
}

// Flush forces any buffered trailing speech in Silero VAD to emit immediately.
func (s *SherpaSegmenter) Flush() {
	if s.closed.Load() {
		return
	}
	done := make(chan struct{})
	select {
	case s.cmdCh <- vadCmd{kind: vadCmdFlush, done: done}:
		select {
		case <-done:
		case <-s.stopCh:
		}
	case <-s.stopCh:
	}
}

// Reset discards queued audio frames and resets Silero VAD state immediately (e.g. on Panic Mute).
func (s *SherpaSegmenter) Reset() {
	if s.closed.Load() {
		return
	}
	// Drain queued sample commands first
drainLoop:
	for {
		select {
		case cmd := <-s.cmdCh:
			if cmd.done != nil {
				close(cmd.done)
			}
		default:
			break drainLoop
		}
	}

	done := make(chan struct{})
	select {
	case s.cmdCh <- vadCmd{kind: vadCmdReset, done: done}:
		select {
		case <-done:
		case <-s.stopCh:
		}
	case <-s.stopCh:
	}
}

// IsSpeech reports whether Silero VAD currently detects active speech.
func (s *SherpaSegmenter) IsSpeech() bool {
	return s.speaking.Load()
}

// Close stops the background worker and frees native Silero VAD resources.
func (s *SherpaSegmenter) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	close(s.stopCh)
	s.wg.Wait()
	if s.vad != nil {
		sherpa.DeleteVoiceActivityDetector(s.vad)
		s.vad = nil
	}
	return nil
}
