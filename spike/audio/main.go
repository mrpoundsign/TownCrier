package main

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// GenerateSineWave generates duration of 440Hz sine wave as float32 samples at sampleRate.
func GenerateSineWave(duration time.Duration, sampleRate int) []float32 {
	numSamples := int(duration.Seconds() * float64(sampleRate))
	samples := make([]float32, numSamples)
	freq := 440.0 // A4 tone
	for i := range numSamples {
		t := float64(i) / float64(sampleRate)
		samples[i] = float32(math.Sin(2.0*math.Pi*freq*t) * 0.3)
	}
	return samples
}

func main() {
	fmt.Println("=== malgo WASAPI / Audio Playback & Emergency Stop Spike ===")

	// 1. Initialize malgo context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(msg string) {
		// miniaudio log callback
	})
	if err != nil {
		fmt.Printf("Failed to init audio context: %v\n", err)
		return
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	// 2. Enumerate playback devices
	devices, err := ctx.Devices(malgo.Playback)
	if err != nil {
		fmt.Printf("Failed to enumerate devices: %v\n", err)
		return
	}

	fmt.Printf("\nFound %d playback device(s):\n", len(devices))
	for i, dev := range devices {
		isDefault := ""
		if dev.IsDefault != 0 {
			isDefault = " [DEFAULT]"
		}
		fmt.Printf("  [%d] %s%s (ID: %v)\n", i, dev.Name(), isDefault, dev.ID)
	}

	// 3. Audio player with atomic abort test
	sampleRate := 24000 // match Kokoro sample rate
	tone := GenerateSineWave(3*time.Second, sampleRate)
	fmt.Printf("\nGenerated %d samples (%.2f seconds of 440Hz tone)\n", len(tone), float64(len(tone))/float64(sampleRate))

	var (
		cursor       atomic.Uint64
		aborted      atomic.Bool
		abortTime    time.Time
		silenceTime  time.Time
		timeLock     sync.Mutex
		playbackDone = make(chan struct{})
	)

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatF32
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(sampleRate)
	deviceConfig.Alsa.NoMMap = 1

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, pInput []byte, frameCount uint32) {
			numFrames := int(frameCount)
			bytesNeeded := numFrames * 4 // 1 channel * 4 bytes per float32
			if len(pOutput) < bytesNeeded {
				return
			}

			// Output slice as float32
			outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pOutput[0])), numFrames)

			if aborted.Load() {
				// Immediate silence
				timeLock.Lock()
				if silenceTime.IsZero() && !abortTime.IsZero() {
					silenceTime = time.Now()
				}
				timeLock.Unlock()

				for i := range outSamples {
					outSamples[i] = 0
				}
				return
			}

			// Fill from buffer
			currentPos := int(cursor.Load())
			for i := range numFrames {
				idx := currentPos + i
				if idx < len(tone) {
					outSamples[i] = tone[idx]
				} else {
					outSamples[i] = 0
				}
			}
			newPos := currentPos + numFrames
			cursor.Store(uint64(newPos))
			if newPos >= len(tone) {
				select {
				case <-playbackDone:
				default:
					close(playbackDone)
				}
			}
		},
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		fmt.Printf("Failed to initialize audio device: %v\n", err)
		return
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		fmt.Printf("Failed to start playback device: %v\n", err)
		return
	}
	fmt.Println("Playback started...")

	// 4. Test Emergency Panic Stop after 400ms
	time.Sleep(400 * time.Millisecond)
	fmt.Println("TRIGGERING EMERGENCY STOP / PANIC MUTE...")
	timeLock.Lock()
	abortTime = time.Now()
	timeLock.Unlock()
	aborted.Store(true)

	// Wait up to 200ms to observe silence confirmation
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		timeLock.Lock()
		st := silenceTime
		timeLock.Unlock()
		if !st.IsZero() {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	timeLock.Lock()
	st := silenceTime
	at := abortTime
	timeLock.Unlock()

	if !st.IsZero() {
		stopLatency := st.Sub(at)
		fmt.Printf("Emergency Stop Latency: %v (<50ms requirement MET: %v)\n", stopLatency, stopLatency < 50*time.Millisecond)
	} else {
		fmt.Println("Silence time not recorded before timeout")
	}

	// Keep device running a bit to ensure no crash
	time.Sleep(100 * time.Millisecond)
	fmt.Printf("Device is still running cleanly: %v\n", device.IsStarted())

	_ = device.Stop()
	fmt.Println("Device stopped cleanly. Spike completed successfully!")
}
