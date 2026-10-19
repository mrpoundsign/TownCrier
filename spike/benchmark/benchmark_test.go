package main

import (
	"os"
	"path/filepath"
	"testing"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

func TestKokoroSynthesis(t *testing.T) {
	// Look for models dir relative to repo root
	modelDir := filepath.Join("..", "..", "models", "kokoro-en")
	if _, err := os.Stat(filepath.Join(modelDir, "model.int8.onnx")); os.IsNotExist(err) {
		t.Skip("model files not found in models/kokoro-en, skipping synthesis test")
	}

	lexPath := filepath.Join(modelDir, "lexicon-us-en.txt")
	var lexicon string
	if _, err := os.Stat(lexPath); err == nil {
		lexicon = lexPath
	}

	config := &sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:       filepath.Join(modelDir, "model.int8.onnx"),
				Voices:      filepath.Join(modelDir, "voices.bin"),
				Tokens:      filepath.Join(modelDir, "tokens.txt"),
				DataDir:     filepath.Join(modelDir, "espeak-ng-data"),
				LengthScale: 1.0,
				Lexicon:     lexicon,
			},
			NumThreads: 2,
			Debug:      0,
			Provider:   "cpu",
		},
	}

	tts := sherpa.NewOfflineTts(config)
	if tts == nil {
		t.Fatal("failed to initialize OfflineTts")
	}
	defer sherpa.DeleteOfflineTts(tts)

	if tts.SampleRate() != 24000 {
		t.Errorf("expected sample rate 24000, got %d", tts.SampleRate())
	}
	if tts.NumSpeakers() <= 0 {
		t.Errorf("expected > 0 speakers, got %d", tts.NumSpeakers())
	}

	audio := tts.Generate("Testing synthesis.", 0, 1.0)
	if audio == nil {
		t.Fatal("synthesis returned nil audio")
	}
	if len(audio.Samples) == 0 {
		t.Fatal("synthesis returned empty samples")
	}
	if audio.SampleRate != 24000 {
		t.Errorf("expected sample rate 24000, got %d", audio.SampleRate)
	}
}
