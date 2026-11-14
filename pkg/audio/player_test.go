package audio

import (
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

func TestPlayerVolume(t *testing.T) {
	p := &DevicePlayer{}
	p.SetVolume(0.5)
	if p.Volume() != 0.5 {
		t.Errorf("expected 0.5, got %f", p.Volume())
	}

	p.SetVolume(-0.2)
	if p.Volume() != 0.0 {
		t.Errorf("expected 0.0 for negative input, got %f", p.Volume())
	}

	p.SetVolume(1.8)
	if p.Volume() != 1.0 {
		t.Errorf("expected 1.0 for overflow input, got %f", p.Volume())
	}
}

func TestPlayerCallbackAndPanicStop(t *testing.T) {
	p := &DevicePlayer{}
	p.SetVolume(0.5)

	samples := make([]float32, 1000)
	for i := range samples {
		samples[i] = 1.0
	}

	if err := p.Play(samples, 24000); err != nil {
		t.Fatalf("Play failed: %v", err)
	}

	if !p.IsPlaying() {
		t.Error("expected IsPlaying to be true after queueing audio")
	}

	// Simulate audio callback for 100 frames
	frameCount := 100
	buf := make([]byte, frameCount*4)
	p.onAudioCallback(buf, nil, uint32(frameCount))

	outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), frameCount)
	for i, s := range outSamples {
		// 1.0 * volume(0.5) = 0.5
		if s != 0.5 {
			t.Fatalf("sample at %d: expected 0.5 with gain, got %f", i, s)
		}
	}

	// Trigger emergency panic mute
	start := time.Now()
	p.Stop()
	latency := time.Since(start)

	if latency > 50*time.Millisecond {
		t.Errorf("Stop exceeded 50ms: %v", latency)
	}

	if p.IsPlaying() {
		t.Error("expected IsPlaying to be false after Stop")
	}

	// Subsequent callback must be immediately zeroed
	p.onAudioCallback(buf, nil, uint32(frameCount))
	for i, s := range outSamples {
		if s != 0.0 {
			t.Fatalf("sample at %d: expected 0.0 after stop, got %f", i, s)
		}
	}
}

func TestPlayerPauseResume(t *testing.T) {
	p := &DevicePlayer{}
	p.SetVolume(1.0)

	samples := make([]float32, 1000)
	for i := range samples {
		samples[i] = 0.8
	}

	if err := p.Play(samples, 24000); err != nil {
		t.Fatalf("Play failed: %v", err)
	}

	// Play 50 stereo frames (100 samples)
	frameCount := 50
	buf := make([]byte, frameCount*2*4)
	p.onAudioCallback(buf, nil, uint32(frameCount))

	p.mu.Lock()
	cursorAfter50 := p.cursor
	p.mu.Unlock()
	if cursorAfter50 != 100 {
		t.Fatalf("expected cursor=100 after 50 stereo frames, got %d", cursorAfter50)
	}

	// Pause playback
	p.Pause()
	if !p.IsPaused() {
		t.Fatal("expected IsPaused to be true after Pause()")
	}
	if !p.IsPlaying() {
		t.Fatal("expected IsPlaying to remain true while paused with pending samples")
	}

	// Audio callback while paused must emit silence and NOT advance cursor
	p.onAudioCallback(buf, nil, uint32(frameCount))
	outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), frameCount*2)
	for i, s := range outSamples {
		if s != 0.0 {
			t.Fatalf("sample at %d: expected 0.0 while paused, got %f", i, s)
		}
	}

	p.mu.Lock()
	cursorWhilePaused := p.cursor
	p.mu.Unlock()
	if cursorWhilePaused != 100 {
		t.Fatalf("cursor advanced while paused: expected 100, got %d", cursorWhilePaused)
	}

	// Resume playback
	p.Resume()
	if p.IsPaused() {
		t.Fatal("expected IsPaused to be false after Resume()")
	}

	// Audio callback should resume reading from cursor 100
	p.onAudioCallback(buf, nil, uint32(frameCount))
	for i, s := range outSamples {
		if s < 0.79 || s > 0.81 {
			t.Fatalf("sample at %d: expected ~0.8 after resume, got %f", i, s)
		}
	}

	p.mu.Lock()
	cursorAfterResume := p.cursor
	p.mu.Unlock()
	if cursorAfterResume != 200 {
		t.Fatalf("expected cursor=200 after resuming, got %d", cursorAfterResume)
	}
}

func TestPlayerSentenceBoundaryRewind(t *testing.T) {
	p := &DevicePlayer{sampleRate: 24000}
	p.SetVolume(1.0)

	// Chunk 1: 100 mono samples -> 200 stereo samples in buffer (offset 0..200)
	chunk1 := make([]float32, 100)
	for i := range chunk1 {
		chunk1[i] = 0.1
	}
	p.MarkSentenceBoundary()
	if err := p.Play(chunk1, 24000); err != nil {
		t.Fatalf("Play chunk1 failed: %v", err)
	}

	// Chunk 2: 200 mono samples -> 400 stereo samples in buffer (offset 200..600)
	chunk2 := make([]float32, 200)
	for i := range chunk2 {
		chunk2[i] = 0.2
	}
	p.MarkSentenceBoundary()
	if err := p.Play(chunk2, 24000); err != nil {
		t.Fatalf("Play chunk2 failed: %v", err)
	}

	// Chunk 3: 300 mono samples -> 600 stereo samples in buffer (offset 600..1200)
	chunk3 := make([]float32, 300)
	for i := range chunk3 {
		chunk3[i] = 0.3
	}
	p.MarkSentenceBoundary()
	if err := p.Play(chunk3, 24000); err != nil {
		t.Fatalf("Play chunk3 failed: %v", err)
	}

	p.mu.Lock()
	if len(p.sentenceBoundaries) != 3 || p.sentenceBoundaries[0] != 0 || p.sentenceBoundaries[1] != 200 || p.sentenceBoundaries[2] != 600 {
		p.mu.Unlock()
		t.Fatalf("unexpected sentenceBoundaries: %v", p.sentenceBoundaries)
	}
	// Simulate playback having advanced to sample 450 (middle of chunk 2)
	p.cursor = 450
	p.mu.Unlock()

	p.Pause()
	if !p.IsPaused() {
		t.Fatal("expected paused")
	}

	// Resume from sentence start should rewind to 200 (start of chunk 2)
	p.ResumeFromSentenceStart()
	if p.IsPaused() {
		t.Fatal("expected unpaused after ResumeFromSentenceStart")
	}

	p.mu.Lock()
	cur := p.cursor
	p.mu.Unlock()
	if cur != 200 {
		t.Fatalf("expected cursor rewound to 200 (start of chunk 2), got %d", cur)
	}

	// Test RewindSentence when cursor is well past threshold vs within threshold
	// At cursor 200 (at start of chunk 2), RewindSentence should jump to chunk 1 (0)
	p.RewindSentence()
	p.mu.Lock()
	cur = p.cursor
	p.mu.Unlock()
	if cur != 0 {
		t.Fatalf("expected cursor rewound to 0 (chunk 1), got %d", cur)
	}

	// Simulate playback finished (cursor at end of buffer) -> ResumeFromSentenceStart must stay idle
	p.mu.Lock()
	p.cursor = len(p.buffer)
	p.mu.Unlock()
	p.Pause()
	p.ResumeFromSentenceStart()
	if p.IsPlaying() {
		t.Fatal("expected player to remain idle when resuming after buffer completion")
	}
	p.mu.Lock()
	cur = p.cursor
	bufLen := len(p.buffer)
	p.mu.Unlock()
	if cur != 0 || bufLen != 0 {
		t.Fatalf("expected cleared buffer and cursor=0 after finished ResumeFromSentenceStart, got cursor=%d bufLen=%d", cur, bufLen)
	}
}

func TestDeviceFallbackNotification(t *testing.T) {
	p := &DevicePlayer{}
	var notified atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	p.SetFallbackListener(func(reason string) {
		notified.Store(true)
		wg.Done()
	})

	p.notifyFallback("test fallback")
	wg.Wait()

	if !notified.Load() {
		t.Error("expected fallback listener to be notified")
	}
}

func TestPanMonoToStereoAndPlayWithOptions(t *testing.T) {
	mono := []float32{1.0, -0.5}

	// 1. Center pan (0.0) produces equal 1.0 * gain on both L and R channels
	lGain, rGain, stereoCenter := PanMonoToStereo(mono, 1.0, 0.0)
	if lGain != 1.0 || rGain != 1.0 {
		t.Fatalf("expected center gains (1.0, 1.0), got (%f, %f)", lGain, rGain)
	}
	if len(stereoCenter) != 4 || stereoCenter[0] != 1.0 || stereoCenter[1] != 1.0 || stereoCenter[2] != -0.5 || stereoCenter[3] != -0.5 {
		t.Fatalf("unexpected stereoCenter: %v", stereoCenter)
	}

	// 2. Full left pan (-1.0) routes signal to Left channel and zeroes Right channel
	lLeft, rLeft, _ := PanMonoToStereo(mono, 1.0, -1.0)
	if lLeft <= 1.4 || rLeft > 0.0001 {
		t.Fatalf("expected full left pan (L>1.4, R≈0), got (%f, %f)", lLeft, rLeft)
	}

	// 3. Full right pan (+1.0) routes signal to Right channel and zeroes Left channel
	lRight, rRight, _ := PanMonoToStereo(mono, 1.0, 1.0)
	if lRight > 0.0001 || rRight <= 1.4 {
		t.Fatalf("expected full right pan (L≈0, R>1.4), got (%f, %f)", lRight, rRight)
	}

	// 4. Stereo callback verification via PlayWithOptions
	p := &DevicePlayer{}
	p.SetVolume(1.0)
	if err := p.PlayWithOptions([]float32{1.0, 1.0}, 24000, PlayOptions{Gain: 0.8, Pan: -0.5}); err != nil {
		t.Fatalf("PlayWithOptions failed: %v", err)
	}
	frameCount := 2
	stereoBuf := make([]byte, frameCount*2*4)
	p.onAudioCallback(stereoBuf, nil, uint32(frameCount))
	out := unsafe.Slice((*float32)(unsafe.Pointer(&stereoBuf[0])), frameCount*2)
	if out[0] <= out[1] {
		t.Fatalf("expected left channel (%f) > right channel (%f) for Pan=-0.5", out[0], out[1])
	}
}

func TestSynthesizeEarcon(t *testing.T) {
	for _, kind := range []EarconKind{EarconStreamOnline, EarconStreamOffline, EarconMention} {
		samples := SynthesizeEarcon(kind, 24000)
		if len(samples) == 0 {
			t.Fatalf("SynthesizeEarcon(%q) returned empty slice", kind)
		}
		var maxAbs float32
		for _, s := range samples {
			if s < 0 {
				s = -s
			}
			if s > maxAbs {
				maxAbs = s
			}
		}
		if maxAbs <= 0.05 || maxAbs > 1.0 {
			t.Errorf("SynthesizeEarcon(%q) peak amplitude %f out of expected range (0.05..1.0]", kind, maxAbs)
		}
	}
}

func TestCaptureMonitorRMSAndReleaseHysteresis(t *testing.T) {
	// 1. Silence produces MinCaptureDBFS (-60.0 dBFS)
	if db := ComputeRMSdBFS(make([]float32, 160)); db != MinCaptureDBFS {
		t.Fatalf("expected silence dBFS %v, got %v", MinCaptureDBFS, db)
	}

	// 2. Full-scale DC/square wave of 0.5 amplitude -> 20*log10(0.5) ≈ -6.02 dBFS
	loudSamples := make([]float32, 160)
	for i := range loudSamples {
		loudSamples[i] = 0.5
	}
	loudDB := ComputeRMSdBFS(loudSamples)
	if loudDB < -6.5 || loudDB > -5.5 {
		t.Fatalf("expected loudDB ≈ -6.02 dBFS, got %v", loudDB)
	}

	// 3. Hysteresis gate with -35 dB threshold and 600ms release hold
	mon := NewCaptureMonitor(-35.0, 600*time.Millisecond)
	var transitions []bool
	mon.SetOnActiveChange(func(active bool) {
		transitions = append(transitions, active)
	})

	t0 := time.Now()
	quietSamples := make([]float32, 160)

	// Initial quiet frame -> inactive
	_, active := mon.ProcessSamples(quietSamples, t0)
	if active || mon.IsActive() {
		t.Fatal("expected monitor to remain inactive on quiet frame")
	}

	// Speaking frame -> immediately active
	_, active = mon.ProcessSamples(loudSamples, t0.Add(20*time.Millisecond))
	if !active || !mon.IsActive() {
		t.Fatal("expected monitor to activate immediately when RMS >= threshold")
	}

	// Quiet frame 300ms later (< 600ms release hold) -> still held active
	_, active = mon.ProcessSamples(quietSamples, t0.Add(320*time.Millisecond))
	if !active || !mon.IsActive() {
		t.Fatal("expected monitor to remain held active during 600ms release window")
	}

	// Quiet frame 650ms after speech stopped (>= 600ms release hold) -> released to inactive
	_, active = mon.ProcessSamples(quietSamples, t0.Add(670*time.Millisecond))
	if active || mon.IsActive() {
		t.Fatal("expected monitor to release to inactive after 600ms of silence")
	}

	if len(transitions) != 2 || !transitions[0] || transitions[1] {
		t.Fatalf("expected transitions [true, false], got %v", transitions)
	}

	// 4. Verify DevicePlayer ducking attenuation in onAudioCallback
	p := &DevicePlayer{}
	p.SetVolume(1.0)
	p.SetDuckMultiplier(0.20)
	if p.DuckMultiplier() != 0.20 {
		t.Fatalf("expected DuckMultiplier 0.20, got %v", p.DuckMultiplier())
	}
	if err := p.Play([]float32{1.0, 1.0, 1.0, 1.0}, 24000); err != nil {
		t.Fatalf("Play failed: %v", err)
	}
	frameCount := 4
	buf := make([]byte, frameCount*4)
	p.onAudioCallback(buf, nil, uint32(frameCount))
	outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), frameCount)
	for i, s := range outSamples {
		if s < 0.199 || s > 0.201 {
			t.Fatalf("sample[%d]: expected 0.20 ducked output, got %f", i, s)
		}
	}
}

func TestFormatAndMatchDeviceID(t *testing.T) {
	var devA, devB malgo.DeviceID
	copy(devA[:], "{0.0.0.00000000}.{11111111-2222-3333-4444-555555555555}")
	copy(devB[:], "{0.0.0.00000000}.{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}")

	canonicalA := FormatDeviceID(devA)
	fullHexA := hex.EncodeToString(devA[:])
	// Simulate legacy fmt.Sprintf("%x", devA) double-hex encoding
	legacyDoubleHexA := fmt.Sprintf("%x", devA)

	if canonicalA == legacyDoubleHexA {
		t.Fatalf("expected FormatDeviceID (%q) to differ from double-hex fmt.Sprintf(%q)", canonicalA, legacyDoubleHexA)
	}

	// 1. Canonical trimmed single-hex matches devA and not devB
	if !MatchDeviceID(devA, canonicalA) {
		t.Fatalf("expected MatchDeviceID to match canonical ID %q", canonicalA)
	}
	if MatchDeviceID(devB, canonicalA) {
		t.Fatalf("expected devB not to match devA canonical ID")
	}

	// 2. Full 256-byte single-hex matches devA
	if !MatchDeviceID(devA, fullHexA) {
		t.Fatalf("expected MatchDeviceID to match full 256-byte hex ID")
	}

	// 3. Legacy double-hex ID from older config.json matches devA and not devB
	if !MatchDeviceID(devA, legacyDoubleHexA) {
		t.Fatalf("expected MatchDeviceID to match legacy double-hex ID %q", legacyDoubleHexA)
	}
	if MatchDeviceID(devB, legacyDoubleHexA) {
		t.Fatalf("expected devB not to match devA legacy double-hex ID")
	}

	// 4. Short prefix of another device must NOT false-positive match if non-zero bytes follow
	var devShort, devLong malgo.DeviceID
	devShort[0] = 0x01
	devLong[0] = 0x01
	devLong[1] = 0x02
	if MatchDeviceID(devLong, FormatDeviceID(devShort)) {
		t.Fatalf("expected devLong (0102...) not to match devShort (01)")
	}
}

