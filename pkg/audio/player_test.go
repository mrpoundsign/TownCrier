package audio

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
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
