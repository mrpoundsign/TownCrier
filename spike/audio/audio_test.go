package main

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// TestEmergencyStopMechanism tests that an atomic abort signal halts audio output within 50ms.
func TestEmergencyStopMechanism(t *testing.T) {
	sampleRate := 24000
	tone := GenerateSineWave(2*time.Second, sampleRate)

	var (
		cursor      atomic.Uint64
		aborted     atomic.Bool
		abortTime   time.Time
		silenceTime time.Time
	)

	// Simulated audio callback loop at 10ms intervals (typical audio buffer size: 240 frames @ 24kHz)
	framesPerCallback := 240
	buffer := make([]byte, framesPerCallback*4)

	callback := func(pOutput []byte) {
		numFrames := len(pOutput) / 4
		outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pOutput[0])), numFrames)

		if aborted.Load() {
			if silenceTime.IsZero() && !abortTime.IsZero() {
				silenceTime = time.Now()
			}
			for i := range outSamples {
				outSamples[i] = 0
			}
			return
		}

		currentPos := int(cursor.Load())
		for i := range numFrames {
			idx := currentPos + i
			if idx < len(tone) {
				outSamples[i] = tone[idx]
			} else {
				outSamples[i] = 0
			}
		}
		cursor.Store(uint64(currentPos + numFrames))
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				callback(buffer)
			case <-done:
				return
			}
		}
	}()

	// Let audio play for 50ms
	time.Sleep(50 * time.Millisecond)

	// Trigger emergency abort
	abortTime = time.Now()
	aborted.Store(true)

	// Wait up to 100ms for silence to take effect
	time.Sleep(100 * time.Millisecond)
	close(done)

	if silenceTime.IsZero() {
		t.Fatal("silence was never triggered after abort")
	}

	latency := silenceTime.Sub(abortTime)
	t.Logf("Emergency stop latency: %v", latency)

	if latency > 50*time.Millisecond {
		t.Errorf("emergency stop exceeded 50ms: got %v", latency)
	}

	// Verify buffer is now silent (all zero samples)
	numFrames := len(buffer) / 4
	outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&buffer[0])), numFrames)
	for i, s := range outSamples {
		if s != 0 {
			t.Errorf("sample at index %d is non-zero after abort: %f", i, s)
			break
		}
	}
}
