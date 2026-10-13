package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const testMessage = "Hello streamer, welcome to the stream! Thanks for having me and let's play games today."

func runBenchmark(modelDir string, numThreads int, voiceID int) (*sherpa.GeneratedAudio, error) {
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
	fmt.Printf("\n--- Benchmark (NumThreads = %d) ---\n", numThreads)
	fmt.Printf("Model Init Time: %v\n", initDuration)
	fmt.Printf("Message (%d words): %q\n", wordCount, testMessage)

	// Measure TTFA (Time To First Audio) using progress callback
	var ttfa time.Duration
	firstChunkReceived := false
	genStart := time.Now()

	audio := tts.GenerateWithProgressCallback(testMessage, voiceID, 1.0, func(samples []float32, progress float32) bool {
		if !firstChunkReceived && len(samples) > 0 {
			ttfa = time.Since(genStart)
			firstChunkReceived = true
		}
		return true // return true to continue generation
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
		fmt.Printf("Time To First Audio (TTFA): %v (chunked callback not triggered; full batch)\n", totalDuration)
	}
	fmt.Printf("Total Generation Time: %v\n", totalDuration)
	fmt.Printf("Real-Time Factor (RTF): %.3fx (lower is faster; < 1.0 is faster than real-time)\n", rtf)

	return audio, nil
}

func main() {
	modelDir := "models/kokoro-en"
	fmt.Println("=== Kokoro-82M int8 TTS Benchmark ===")

	// Benchmark with NumThreads = 1
	audio1, err := runBenchmark(modelDir, 1, 0)
	if err != nil {
		fmt.Printf("Benchmark 1 error: %v\n", err)
		return
	}

	// Benchmark with NumThreads = 2
	audio2, err := runBenchmark(modelDir, 2, 0)
	if err != nil {
		fmt.Printf("Benchmark 2 error: %v\n", err)
		return
	}

	// Write generated audio from run 2 to WAV for verification
	outWav := "spike/output_test.wav"
	if err := WriteWav(outWav, audio2.Samples, audio2.SampleRate); err != nil {
		fmt.Printf("Failed to write WAV: %v\n", err)
	} else {
		fmt.Printf("\nSaved sample audio to %s\n", outWav)
	}

	_ = audio1
}
