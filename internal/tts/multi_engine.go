package tts

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// MultiEngine manages one or more SherpaEngine instances across enabled model families
// ("kokoro", "supertonic", "kitten_mini", "kitten_nano", "piper") and routes synthesis
// requests based on the voice ID's family prefix.
type MultiEngine struct {
	mu            sync.RWMutex
	engines       map[string]*SherpaEngine
	order         []string
	primaryFamily string
	closed        bool
}

// NewMultiEngine loads all enabled and installed model families from baseKokoroDir.
func NewMultiEngine(
	baseKokoroDir string,
	enabledModels []string,
	numThreads int,
	useGPU bool,
	maxClauseWords int,
	idleUnloadMinutes int,
) (*MultiEngine, error) {
	if len(enabledModels) == 0 {
		enabledModels = []string{ModelKokoro}
	}
	if numThreads <= 0 {
		numThreads = 4
	}
	if maxClauseWords <= 0 {
		maxClauseWords = DefaultMaxClauseWords
	}

	m := &MultiEngine{
		engines: make(map[string]*SherpaEngine, len(enabledModels)),
	}

	var firstErr error
	for _, rawFam := range enabledModels {
		fam := strings.ToLower(strings.TrimSpace(rawFam))
		if fam == "" {
			continue
		}
		if _, exists := m.engines[fam]; exists {
			continue
		}
		if fam == ModelKokoro || fam == ModelKokoroIntl {
			otherFam := ModelKokoro
			if fam == ModelKokoro {
				otherFam = ModelKokoroIntl
			}
			if existing, ok := m.engines[otherFam]; ok {
				m.engines[fam] = existing
				m.order = append(m.order, fam)
				continue
			}
		}
		dir := ModelFamilyDir(baseKokoroDir, fam)
		eng, err := NewFamilySherpaEngine(fam, dir, numThreads, useGPU)
		if err != nil {
			log.Printf("[TTS] Could not load model family %q from %s: %v", fam, dir, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		eng.SetMaxClauseWords(maxClauseWords)
		if idleUnloadMinutes > 0 {
			eng.SetIdleTimeout(time.Duration(idleUnloadMinutes) * time.Minute)
		}
		m.engines[fam] = eng
		m.order = append(m.order, fam)
		if m.primaryFamily == "" {
			m.primaryFamily = fam
		}
	}

	// Fallback: if none of the requested families could be loaded, try loading Kokoro from baseKokoroDir
	if len(m.engines) == 0 {
		eng, err := NewFamilySherpaEngine(ModelKokoro, baseKokoroDir, numThreads, useGPU)
		if err != nil {
			if firstErr != nil {
				return nil, firstErr
			}
			return nil, err
		}
		eng.SetMaxClauseWords(maxClauseWords)
		if idleUnloadMinutes > 0 {
			eng.SetIdleTimeout(time.Duration(idleUnloadMinutes) * time.Minute)
		}
		m.engines[ModelKokoro] = eng
		m.order = append(m.order, ModelKokoro)
		m.primaryFamily = ModelKokoro
	}

	return m, nil
}

// LoadedFamilies returns the ordered list of currently loaded model family IDs.
func (m *MultiEngine) LoadedFamilies() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.order))
	copy(out, m.order)
	return out
}

// Provider returns a summary of active execution providers across loaded engines.
func (m *MultiEngine) Provider() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.order) == 0 {
		return "none"
	}
	if len(m.order) == 1 {
		fam := m.order[0]
		if eng, ok := m.engines[fam]; ok {
			if spec, sOK := ModelSpecByID(fam); sOK {
				return fmt.Sprintf("%s (%s)", spec.ShortName, eng.Provider())
			}
			return eng.Provider()
		}
	}
	parts := make([]string, 0, len(m.order))
	for _, fam := range m.order {
		eng := m.engines[fam]
		if spec, ok := ModelSpecByID(fam); ok {
			parts = append(parts, fmt.Sprintf("%s:%s", spec.ShortName, eng.Provider()))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%s", fam, eng.Provider()))
		}
	}
	return strings.Join(parts, ", ")
}

func (m *MultiEngine) resolveEngineAndVoice(voiceID string) (*SherpaEngine, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed || len(m.engines) == 0 {
		return nil, "", ErrEngineClosed
	}

	targetFam := VoiceFamily(voiceID)
	if eng, ok := m.engines[targetFam]; ok {
		return eng, voiceID, nil
	}

	// If "kitten_mini" was requested and only "kitten_nano" is loaded (or vice versa), cross-route seamlessly
	switch targetFam {
	case ModelKittenMini:
		if eng, ok := m.engines[ModelKittenNano]; ok {
			return eng, strings.Replace(voiceID, "kitten_mini:", "kitten_nano:", 1), nil
		}
	case ModelKittenNano:
		if eng, ok := m.engines[ModelKittenMini]; ok {
			return eng, strings.Replace(voiceID, "kitten_nano:", "kitten_mini:", 1), nil
		}
	}

	// Fallback to primary loaded engine when a voice belongs to a disabled/unloaded model pack
	primary := m.engines[m.primaryFamily]
	if primary == nil {
		for _, eng := range m.engines {
			primary = eng
			break
		}
	}
	if primary == nil {
		return nil, "", ErrEngineClosed
	}

	fallbackVoice := DefaultVoiceID
	if voices := primary.Voices(); len(voices) > 0 {
		fallbackVoice = voices[0]
	}
	log.Printf("[TTS] Voice %q belongs to disabled model %q; falling back to %q on %q",
		voiceID, targetFam, fallbackVoice, primary.Family())
	return primary, fallbackVoice, nil
}

// SynthesizeStream routes clause-by-clause streaming synthesis to the appropriate model family engine.
func (m *MultiEngine) SynthesizeStream(text string, voiceID string, speed float32, onChunk StreamCallback) error {
	eng, resolvedVoice, err := m.resolveEngineAndVoice(voiceID)
	if err != nil {
		return err
	}
	return eng.SynthesizeStream(text, resolvedVoice, speed, onChunk)
}

// Synthesize routes synthesis to the appropriate model family engine.
func (m *MultiEngine) Synthesize(text string, voiceID string, speed float32) ([]float32, int, error) {
	eng, resolvedVoice, err := m.resolveEngineAndVoice(voiceID)
	if err != nil {
		return nil, 0, err
	}
	return eng.Synthesize(text, resolvedVoice, speed)
}

// Voices returns the combined voice IDs of all currently loaded model families.
func (m *MultiEngine) Voices() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []string
	for _, fam := range m.order {
		if spec, ok := ModelSpecByID(fam); ok && len(spec.Voices) > 0 {
			for _, v := range spec.Voices {
				all = append(all, v.ID)
			}
		} else if eng, ok := m.engines[fam]; ok {
			all = append(all, eng.Voices()...)
		}
	}
	return all
}

// NumSpeakers returns the total speaker count across all loaded model families.
func (m *MultiEngine) NumSpeakers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[*SherpaEngine]struct{}, len(m.engines))
	total := 0
	for _, eng := range m.engines {
		if eng == nil {
			continue
		}
		if _, ok := seen[eng]; ok {
			continue
		}
		seen[eng] = struct{}{}
		total += eng.NumSpeakers()
	}
	return total
}

// Close closes all loaded model family engines and reclaims OS working set memory.
func (m *MultiEngine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	seen := make(map[*SherpaEngine]struct{}, len(m.engines))
	for fam, eng := range m.engines {
		if eng != nil {
			if _, ok := seen[eng]; !ok {
				seen[eng] = struct{}{}
				_ = eng.Close()
			}
		}
		delete(m.engines, fam)
	}
	m.order = nil

	ReclaimOSMemory()
	return nil
}
