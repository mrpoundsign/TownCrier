package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mrpoundsign/TownCrier/internal/ws"
	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/stt"
)

// SetSTTModelDir overrides the directory where Silero VAD and Whisper models are stored (used in tests).
func (s *Session) SetSTTModelDir(dir string) {
	s.sttMu.Lock()
	s.sttModelDir = strings.TrimSpace(dir)
	s.sttMu.Unlock()
}

// STTModelDir returns the active directory where Silero VAD and Whisper models are stored.
func (s *Session) STTModelDir() string {
	s.sttMu.Lock()
	dir := s.sttModelDir
	s.sttMu.Unlock()
	if dir != "" {
		return dir
	}
	if testing.Testing() {
		return ""
	}
	return stt.DefaultSTTModelDir()
}

// SetSTTFactories overrides the STT engine and VAD segmenter constructors (used in headless unit tests).
func (s *Session) SetSTTFactories(
	engineFn func(family, baseDir string, numThreads int, useGPU bool) (stt.Engine, error),
	segFn func(cfg stt.SegmenterConfig) (stt.Segmenter, error),
) {
	s.sttMu.Lock()
	s.sttEngineFactory = engineFn
	s.sttSegFactory = segFn
	s.sttMu.Unlock()
}

// SetOnVoiceSubtitle registers a UI listener invoked whenever a voice subtitle event is emitted or cleared.
func (s *Session) SetOnVoiceSubtitle(fn func(ev ws.SubtitleEvent)) {
	s.sttMu.Lock()
	s.onVoiceSubtitle = fn
	s.sttMu.Unlock()
}

// SetOnSTTStatus registers a UI listener invoked when live speech detection, transcription state, or STT errors change.
func (s *Session) SetOnSTTStatus(fn func(speaking bool, transcribing bool, errText string)) {
	s.sttMu.Lock()
	s.onSTTStatus = fn
	s.sttMu.Unlock()
}

func (s *Session) notifySTTStatus(errText string) {
	s.sttMu.Lock()
	fn := s.onSTTStatus
	s.sttMu.Unlock()
	if fn != nil {
		fn(s.sttSpeaking.Load(), s.sttTranscribing.Load(), errText)
	}
}

// ActiveSTTProvider returns the active compute backend string of the loaded STT engine (or "" when unloaded).
func (s *Session) ActiveSTTProvider() string {
	s.sttMu.Lock()
	eng := s.sttEngine
	s.sttMu.Unlock()
	if eng == nil {
		return ""
	}
	return eng.Provider()
}

// LastSTTDecodeMs returns the decode latency in milliseconds of the most recent recognized utterance (or 0).
func (s *Session) LastSTTDecodeMs() int64 {
	return s.sttLastDecodeMs.Load()
}

// ReloadSTTEngine re-evaluates and loads the configured Silero VAD and Whisper model (e.g. after a model download completes).
func (s *Session) ReloadSTTEngine() {
	s.syncVoiceSubtitles(s.Config())
	s.syncMicMonitor(s.Config())
}

// SnapshotOverlaySubtitles implements ws.SubtitlesOverlayProvider, returning layout preferences and the latest subtitle card.
func (s *Session) SnapshotOverlaySubtitles() ws.SubtitleOverlaySnapshot {
	cfg := s.Config()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	vs := cfg.VoiceSubtitles
	config.NormalizeVoiceSubtitlesConfig(&vs)

	s.sttMu.Lock()
	var latestCopy *ws.SubtitleEvent
	if s.latestSubtitle != nil {
		cp := *s.latestSubtitle
		cp.Lines = slices.Clone(s.latestSubtitle.Lines)
		latestCopy = &cp
	}
	s.sttMu.Unlock()

	customCSS := vs.OverlayCustomCSS
	if customCSS == "" && vs.OverlayStyleJSON == "" {
		customCSS = cfg.WebSocket.OverlayCustomCSS
	}

	return ws.SubtitleOverlaySnapshot{
		Position:        vs.OverlayPosition,
		Align:           vs.OverlayAlign,
		FadeSeconds:     vs.OverlayFadeSeconds,
		Scale:           vs.OverlayScale,
		TargetLanguages: slices.Clone(vs.TargetLanguages),
		CustomCSS:       customCSS,
		Speaking:        s.sttSpeaking.Load(),
		Latest:          latestCopy,
	}
}

// GetSubtitlesOverlayStyle returns the persisted Voice Subtitles overlay CSS and style JSON.
func (s *Session) GetSubtitlesOverlayStyle() (customCSS, styleJSON string) {
	cfg := s.Config()
	if cfg == nil {
		return "", ""
	}
	return cfg.VoiceSubtitles.OverlayCustomCSS, cfg.VoiceSubtitles.OverlayStyleJSON
}

// SaveSubtitlesOverlayStyle persists updated Voice Subtitles overlay CSS and style JSON to config.
func (s *Session) SaveSubtitlesOverlayStyle(customCSS, styleJSON string) error {
	return s.UpdateConfig(func(c *config.Config) {
		c.VoiceSubtitles.OverlayCustomCSS = customCSS
		c.VoiceSubtitles.OverlayStyleJSON = styleJSON
	})
}

// ClearVoiceSubtitles resets active VAD buffers, discards queued utterances, and clears the OBS /overlay/subtitles card.
func (s *Session) ClearVoiceSubtitles() {
	s.sttMu.Lock()
	seg := s.sttSegmenter
	uttCh := s.sttUttCh
	s.latestSubtitle = nil
	subFn := s.onVoiceSubtitle
	s.sttMu.Unlock()

	if uttCh != nil {
	drainLoop:
		for {
			select {
			case <-uttCh:
			default:
				break drainLoop
			}
		}
	}
	if seg != nil {
		seg.Reset()
	}
	s.sttSpeaking.Store(false)
	s.sttTranscribing.Store(false)
	s.notifySTTStatus("")

	clearEv := ws.SubtitleEvent{
		Kind:      "clear",
		Timestamp: time.Now().UTC(),
	}
	if s.wsServer != nil {
		s.wsServer.BroadcastSubtitleEvent(clearEv)
	}
	if subFn != nil {
		subFn(clearEv)
	}
}

func (s *Session) handleCapturedMicSamples(samples []float32) {
	if len(samples) == 0 || s.IsPanicked() {
		return
	}
	s.mu.RLock()
	enabled := s.running && s.cfg != nil && s.cfg.VoiceSubtitles.Enabled
	s.mu.RUnlock()
	if !enabled {
		return
	}

	s.sttMu.Lock()
	seg := s.sttSegmenter
	s.sttMu.Unlock()
	if seg != nil {
		seg.FeedSamples(samples)
	}
}

func (s *Session) syncVoiceSubtitles(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if !testing.Testing() {
		cfgCopy := cfg.Clone()
		go s.syncVoiceSubtitlesSync(cfgCopy)
		return
	}
	s.syncVoiceSubtitlesSync(cfg)
}

func (s *Session) syncVoiceSubtitlesSync(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.sttSyncMu.Lock()
	defer s.sttSyncMu.Unlock()

	vs := cfg.VoiceSubtitles
	config.NormalizeVoiceSubtitlesConfig(&vs)

	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()

	if !running || !vs.Enabled {
		s.stopVoiceSubtitlesLocked(true)
		return
	}

	baseDir := s.STTModelDir()
	s.sttMu.Lock()
	hasCustomFactories := s.sttEngineFactory != nil || s.sttSegFactory != nil
	s.sttMu.Unlock()

	if !hasCustomFactories && !stt.IsModelInstalled(baseDir, vs.ModelFamily) {
		s.stopVoiceSubtitlesLocked(false)
		s.notifySTTStatus("Model not installed — click 'Download Model' to enable offline recognition.")
		return
	}

	wantEngineKey := fmt.Sprintf("%s|%s|%d|%s", vs.ModelFamily, baseDir, vs.NumThreads, vs.ComputeDevice)
	wantSegKey := fmt.Sprintf("%s|%.3f|%d|%d", baseDir, vs.VadThreshold, vs.MinSilenceMs, vs.MaxSpeechSeconds)

	s.sttMu.Lock()
	needNewEngine := s.sttEngine == nil || s.sttEngineKey != wantEngineKey
	needNewSeg := s.sttSegmenter == nil || s.sttSegKey != wantSegKey
	engFactory := s.sttEngineFactory
	segFactory := s.sttSegFactory
	oldEng := s.sttEngine
	oldSeg := s.sttSegmenter
	needWorker := s.sttStopCh == nil
	if needWorker {
		s.sttUttCh = make(chan []float32, 8)
		s.sttStopCh = make(chan struct{})
		uttCh := s.sttUttCh
		stopCh := s.sttStopCh
		s.sttWg.Go(func() {
			s.voiceSubtitleWorker(uttCh, stopCh)
		})
	}
	s.sttMu.Unlock()

	var newEng stt.Engine
	if needNewEngine {
		var err error
		if engFactory != nil {
			newEng, err = engFactory(vs.ModelFamily, baseDir, vs.NumThreads, vs.ComputeDevice != "cpu")
		} else {
			newEng, err = stt.NewBestEngine(vs.ModelFamily, baseDir, vs.NumThreads, vs.ComputeDevice)
		}
		if err != nil {
			log.Printf("[Session] Failed to load STT engine (%s): %v", vs.ModelFamily, err)
			s.notifySTTStatus("STT model load failed: " + err.Error())
			return
		}
		s.sttLastDecodeMs.Store(0)
	}

	var newSeg stt.Segmenter
	if needNewSeg {
		segCfg := stt.SegmenterConfig{
			VadModelPath:     stt.VadModelPath(baseDir),
			SampleRate:       stt.DefaultSampleRate,
			Threshold:        vs.VadThreshold,
			MinSilenceMs:     vs.MinSilenceMs,
			MinSpeechMs:      stt.DefaultMinSpeechMs,
			MaxSpeechSeconds: vs.MaxSpeechSeconds,
			NumThreads:       1,
			OnSpeechState:    s.handleSTTSpeechState,
			OnSegment:        s.enqueueUtteranceSamples,
		}
		var err error
		if segFactory != nil {
			newSeg, err = segFactory(segCfg)
		} else {
			newSeg, err = stt.NewSherpaSegmenter(segCfg)
		}
		if err != nil {
			if newEng != nil {
				_ = newEng.Close()
			}
			log.Printf("[Session] Failed to start Silero VAD segmenter: %v", err)
			s.notifySTTStatus("VAD init failed: " + err.Error())
			return
		}
	}

	s.sttMu.Lock()
	if needNewEngine {
		s.sttEngine = newEng
		s.sttEngineKey = wantEngineKey
	} else {
		oldEng = nil
	}
	if s.sttEngine != nil {
		s.sttEngine.SetIdleTimeout(time.Duration(vs.IdleUnloadMinutes) * time.Minute)
	}
	if needNewSeg {
		s.sttSegmenter = newSeg
		s.sttSegKey = wantSegKey
	} else {
		oldSeg = nil
	}
	s.sttMu.Unlock()

	if oldSeg != nil {
		go func(seg stt.Segmenter) {
			_ = seg.Close()
		}(oldSeg)
	}
	if oldEng != nil {
		go func(eng stt.Engine) {
			_ = eng.Close()
		}(oldEng)
	}
	s.notifySTTStatus("")
}

func (s *Session) stopVoiceSubtitles() {
	s.sttSyncMu.Lock()
	defer s.sttSyncMu.Unlock()
	s.stopVoiceSubtitlesLocked(true)
}

func (s *Session) stopVoiceSubtitlesLocked(broadcastClear bool) {
	s.sttMu.Lock()
	stopCh := s.sttStopCh
	s.sttStopCh = nil
	s.sttUttCh = nil
	seg := s.sttSegmenter
	s.sttSegmenter = nil
	s.sttSegKey = ""
	eng := s.sttEngine
	s.sttEngine = nil
	s.sttEngineKey = ""
	hadSubtitle := s.latestSubtitle != nil
	s.latestSubtitle = nil
	subFn := s.onVoiceSubtitle
	s.sttMu.Unlock()

	if stopCh != nil {
		close(stopCh)
		s.sttWg.Wait()
	}
	if seg != nil {
		_ = seg.Close()
	}
	if eng != nil {
		_ = eng.Close()
	}

	wasSpeaking := s.sttSpeaking.Swap(false)
	s.sttTranscribing.Store(false)
	s.notifySTTStatus("")

	if broadcastClear && (hadSubtitle || wasSpeaking) {
		clearEv := ws.SubtitleEvent{
			Kind:      "clear",
			Timestamp: time.Now().UTC(),
		}
		if s.wsServer != nil {
			s.wsServer.BroadcastSubtitleEvent(clearEv)
		}
		if subFn != nil {
			subFn(clearEv)
		}
	}
}

func (s *Session) handleSTTSpeechState(speaking bool) {
	if s.IsPanicked() {
		speaking = false
	}
	prev := s.sttSpeaking.Swap(speaking)
	s.notifySTTStatus("")
	if prev != speaking && s.wsServer != nil {
		s.wsServer.BroadcastSubtitleEvent(ws.SubtitleEvent{
			Kind:      "speaking",
			Speaking:  speaking,
			Timestamp: time.Now().UTC(),
		})
	}
}

func (s *Session) enqueueUtteranceSamples(samples []float32) {
	if len(samples) == 0 || s.IsPanicked() {
		return
	}
	s.sttMu.Lock()
	uttCh := s.sttUttCh
	s.sttMu.Unlock()
	if uttCh == nil {
		return
	}
	select {
	case uttCh <- samples:
	default:
		// If transcription falls 8+ utterances behind, drop the oldest queued segment to stay near real-time.
		select {
		case <-uttCh:
		default:
		}
		select {
		case uttCh <- samples:
		default:
		}
	}
}

func (s *Session) voiceSubtitleWorker(uttCh <-chan []float32, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		case samples := <-uttCh:
			if len(samples) == 0 || s.IsPanicked() {
				continue
			}
			s.processUtteranceSamples(samples)
		}
	}
}

func (s *Session) processUtteranceSamples(samples []float32) {
	cfg := s.Config()
	if cfg == nil || !cfg.VoiceSubtitles.Enabled {
		return
	}
	vs := cfg.VoiceSubtitles
	config.NormalizeVoiceSubtitlesConfig(&vs)

	s.sttMu.Lock()
	eng := s.sttEngine
	s.sttMu.Unlock()
	if eng == nil {
		return
	}

	s.sttTranscribing.Store(true)
	s.notifySTTStatus("")

	tr, err := eng.Transcribe(samples, stt.DefaultSampleRate, vs.SpokenLanguages)
	if err != nil {
		s.sttTranscribing.Store(false)
		s.notifySTTStatus("Transcription error: " + err.Error())
		return
	}
	if tr.Latency > 0 {
		s.sttLastDecodeMs.Store(tr.Latency.Milliseconds())
	}
	if strings.TrimSpace(tr.Text) == "" || s.IsPanicked() {
		s.sttTranscribing.Store(false)
		s.notifySTTStatus("")
		return
	}

	_, trErr := s.ProcessVoiceSubtitleText(context.Background(), tr.Text, tr.DetectedLang)
	s.sttTranscribing.Store(false)
	if trErr != nil {
		s.notifySTTStatus("Subtitle translation warning: " + trErr.Error())
	} else {
		s.notifySTTStatus("")
	}
}

// ProcessVoiceSubtitleText translates a recognized (or test) phrase across all configured VoiceSubtitles.TargetLanguages,
// updates the active OBS subtitle snapshot, broadcasts to SSE /overlay/subtitles clients, and notifies UI listeners.
func (s *Session) ProcessVoiceSubtitleText(ctx context.Context, text string, detectedLang string) (ws.SubtitleEvent, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ws.SubtitleEvent{}, errors.New("subtitle text cannot be empty")
	}

	cfg := s.Config()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	vs := cfg.VoiceSubtitles
	config.NormalizeVoiceSubtitlesConfig(&vs)

	chain := s.buildTranslationChain(cfg)
	trSvc := s.Translator()
	if trSvc == nil {
		return ws.SubtitleEvent{}, errors.New("translation service unavailable")
	}

	items, trErr := trSvc.TranslateMulti(ctx, trimmed, detectedLang, vs.TargetLanguages, cfg.Translation.RoutingMode, chain)
	lines := make([]ws.SubtitleLine, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.Text) == "" {
			continue
		}
		lines = append(lines, ws.SubtitleLine{
			Lang:       it.Lang,
			Label:      it.LanguageBadge,
			Text:       it.Text,
			IsOriginal: it.IsOriginal,
		})
	}
	log.Printf("[VoiceSubtitles] Source (%s): %q -> %d target lines (err=%v)", detectedLang, trimmed, len(lines), trErr)
	if len(lines) == 0 {
		return ws.SubtitleEvent{}, trErr
	}

	if s.IsPanicked() {
		return ws.SubtitleEvent{}, nil
	}

	ev := ws.SubtitleEvent{
		Kind:         "subtitle",
		ID:           fmt.Sprintf("sub-%d-%d", time.Now().UTC().UnixNano(), s.sttSeq.Add(1)),
		Timestamp:    time.Now().UTC(),
		SourceLang:   detectedLang,
		OriginalText: trimmed,
		Lines:        lines,
	}

	s.sttMu.Lock()
	evCopy := ev
	evCopy.Lines = slices.Clone(ev.Lines)
	s.latestSubtitle = &evCopy
	subFn := s.onVoiceSubtitle
	s.sttMu.Unlock()

	if s.wsServer != nil {
		s.wsServer.BroadcastSubtitleEvent(ev)
	}
	if subFn != nil {
		subFn(ev)
	}
	return ev, trErr
}
