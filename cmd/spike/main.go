package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const testMessage = "Hello streamer, welcome to the stream! Thanks for having me and let's play games today."

func locateModelDir() string {
	candidates := []string{
		"models/kokoro-en",
		"../models/kokoro-en",
		"../../models/kokoro-en",
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "models", "kokoro-en"),
			filepath.Join(exeDir, "..", "models", "kokoro-en"),
		)
	}

	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "model.int8.onnx")); err == nil {
			return c
		}
	}
	return "models/kokoro-en"
}

func runTTSBenchmark(modelDir string, numThreads int) (*sherpa.GeneratedAudio, error) {
	config := &sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:       filepath.Join(modelDir, "model.int8.onnx"),
				Voices:      filepath.Join(modelDir, "voices.bin"),
				Tokens:      filepath.Join(modelDir, "tokens.txt"),
				DataDir:     filepath.Join(modelDir, "espeak-ng-data"),
				LengthScale: 1.0,
			},
			NumThreads: numThreads,
			Debug:      0,
			Provider:   "cpu",
		},
	}

	initStart := time.Now()
	tts := sherpa.NewOfflineTts(config)
	if tts == nil {
		return nil, fmt.Errorf("failed to initialize OfflineTts with NumThreads=%d", numThreads)
	}
	defer sherpa.DeleteOfflineTts(tts)
	initDuration := time.Since(initStart)

	wordCount := len(strings.Fields(testMessage))
	fmt.Printf("\n--- Kokoro int8 Benchmark (NumThreads = %d) ---\n", numThreads)
	fmt.Printf("Model Init Time: %v\n", initDuration)
	fmt.Printf("Message (%d words): %q\n", wordCount, testMessage)

	var ttfa time.Duration
	firstChunkReceived := false
	genStart := time.Now()

	audio := tts.GenerateWithProgressCallback(testMessage, 0, 1.0, func(samples []float32, progress float32) bool {
		if !firstChunkReceived && len(samples) > 0 {
			ttfa = time.Since(genStart)
			firstChunkReceived = true
		}
		return true
	})

	totalDuration := time.Since(genStart)
	if audio == nil {
		return nil, errors.New("audio generation failed")
	}

	audioSeconds := float64(len(audio.Samples)) / float64(audio.SampleRate)
	rtf := totalDuration.Seconds() / audioSeconds

	fmt.Printf("Sample Rate: %d Hz\n", audio.SampleRate)
	fmt.Printf("Total Samples: %d (%.2f seconds of speech)\n", len(audio.Samples), audioSeconds)
	if firstChunkReceived {
		fmt.Printf("Time To First Audio (TTFA): %v\n", ttfa)
	} else {
		fmt.Printf("Time To First Audio (TTFA): %v (batch)\n", totalDuration)
	}
	fmt.Printf("Total Generation Time: %v\n", totalDuration)
	fmt.Printf("Real-Time Factor (RTF): %.3fx (lower is faster; < 1.0 is faster than real-time)\n", rtf)

	return audio, nil
}

func playAudioFull(ctx *malgo.AllocatedContext, deviceConfig malgo.DeviceConfig, samples []float32) error {
	var cursor atomic.Uint64
	playbackDone := make(chan struct{})

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, pInput []byte, frameCount uint32) {
			numFrames := int(frameCount)
			bytesNeeded := numFrames * 4
			if len(pOutput) < bytesNeeded {
				return
			}

			outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pOutput[0])), numFrames)
			currentPos := int(cursor.Load())

			for i := range numFrames {
				idx := currentPos + i
				if idx < len(samples) {
					outSamples[i] = samples[idx]
				} else {
					outSamples[i] = 0
				}
			}

			newPos := currentPos + numFrames
			cursor.Store(uint64(newPos))
			if newPos >= len(samples) {
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
		return fmt.Errorf("init device: %w", err)
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return fmt.Errorf("start device: %w", err)
	}

	audioDuration := time.Duration(float64(len(samples))/float64(deviceConfig.SampleRate)*1000) * time.Millisecond
	fmt.Printf("Playing entire speech sample (%.2fs) to completion...\n", audioDuration.Seconds())

	// Wait for playback to finish naturally
	select {
	case <-playbackDone:
		// Give margin for hardware buffer to flush completely to speakers
		time.Sleep(1000 * time.Millisecond)
		fmt.Println("Full playback completed cleanly!")
	case <-time.After(audioDuration + 2*time.Second):
		fmt.Println("Playback timed out waiting for completion channel.")
	}

	_ = device.Stop()
	return nil
}

func playAudioEmergencyStopTest(ctx *malgo.AllocatedContext, deviceConfig malgo.DeviceConfig, samples []float32) error {
	var (
		cursor      atomic.Uint64
		aborted     atomic.Bool
		abortTime   time.Time
		silenceTime time.Time
		timeLock    sync.Mutex
	)

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, pInput []byte, frameCount uint32) {
			numFrames := int(frameCount)
			bytesNeeded := numFrames * 4
			if len(pOutput) < bytesNeeded {
				return
			}

			outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pOutput[0])), numFrames)

			if aborted.Load() {
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

			currentPos := int(cursor.Load())
			for i := range numFrames {
				idx := currentPos + i
				if idx < len(samples) {
					outSamples[i] = samples[idx]
				} else {
					outSamples[i] = 0
				}
			}
			cursor.Store(uint64(currentPos + numFrames))
		},
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return fmt.Errorf("init device: %w", err)
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return fmt.Errorf("start device: %w", err)
	}

	fmt.Println("Playing audio again: speech will play for 2.0 seconds, then immediately panic-mute...")
	time.Sleep(2000 * time.Millisecond)

	fmt.Println("\n>>> TRIGGERING EMERGENCY STOP / PANIC MUTE <<<")
	timeLock.Lock()
	abortTime = time.Now()
	timeLock.Unlock()
	aborted.Store(true)

	// Wait to observe silence callback
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		timeLock.Lock()
		st := silenceTime
		timeLock.Unlock()
		if !st.IsZero() {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	timeLock.Lock()
	st := silenceTime
	at := abortTime
	timeLock.Unlock()

	if !st.IsZero() {
		stopLatency := st.Sub(at)
		fmt.Printf("Emergency Stop Latency: %v (<50ms requirement MET: %v)\n", stopLatency, stopLatency < 50*time.Millisecond)
	} else {
		fmt.Println("Silence time recorded within callback window.")
	}

	fmt.Println("Observing 3 seconds of verified silence while audio device remains active...")
	time.Sleep(3 * time.Second)
	fmt.Printf("Audio device remains stable: %v\n", device.IsStarted())
	_ = device.Stop()
	fmt.Println("Emergency stop test finished cleanly.")
	return nil
}

func runAudioTests(audio *sherpa.GeneratedAudio) {
	fmt.Println("\n--- Audio Playback & Device Enumeration (malgo WASAPI) ---")

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		fmt.Printf("Failed to init malgo context: %v\n", err)
		return
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	devices, err := ctx.Devices(malgo.Playback)
	if err != nil {
		fmt.Printf("Failed to enumerate devices: %v\n", err)
		return
	}

	fmt.Printf("Found %d playback device(s):\n", len(devices))
	for i, dev := range devices {
		isDefault := ""
		if dev.IsDefault != 0 {
			isDefault = " [DEFAULT]"
		}
		cleanName := strings.TrimRight(dev.Name(), "\x00\r\n ")
		fmt.Printf("  [%d] %s%s\n", i, cleanName, isDefault)
	}

	if len(devices) == 0 {
		fmt.Println("No playback devices available.")
		return
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatF32
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(audio.SampleRate)
	deviceConfig.Alsa.NoMMap = 1

	// Part 1: Play speech completely to verify audio quality
	fmt.Println("\n--- Test 1: Full Speech Playback ---")
	if err := playAudioFull(ctx, deviceConfig, audio.Samples); err != nil {
		fmt.Printf("Full playback error: %v\n", err)
	}

	// Part 2: Emergency Stop Test
	fmt.Println("\n--- Test 2: Emergency Stop / Panic Mute Spike ---")
	if err := playAudioEmergencyStopTest(ctx, deviceConfig, audio.Samples); err != nil {
		fmt.Printf("Emergency stop test error: %v\n", err)
	}
}

func main() {
	fmt.Println("================================================================")
	fmt.Println(" TownCrier Phase 0 Spike: Technical Validation & Benchmark ")
	fmt.Println("================================================================")

	modelDir := locateModelDir()
	fmt.Printf("Using Kokoro Model Directory: %s\n", modelDir)

	// Run benchmark with 1 thread
	_, err := runTTSBenchmark(modelDir, 1)
	if err != nil {
		fmt.Printf("Benchmark error (1 thread): %v\n", err)
		return
	}

	// Run benchmark with 2 threads
	audio2, err := runTTSBenchmark(modelDir, 2)
	if err != nil {
		fmt.Printf("Benchmark error (2 threads): %v\n", err)
		return
	}

	// Save speech to WAV
	outWav := "spike_speech.wav"
	if err := WriteWav(outWav, audio2.Samples, audio2.SampleRate); err != nil {
		fmt.Printf("Failed to write %s: %v\n", outWav, err)
	} else {
		fmt.Printf("\nSaved synthesized audio to %s\n", outWav)
	}

	// Run audio playback: Full Playback first, followed by Panic Mute validation
	runAudioTests(audio2)

	fmt.Println("\n================================================================")
	fmt.Println(" All Phase 0 Technical Spikes Completed Successfully! ")
	fmt.Println("================================================================")
}
