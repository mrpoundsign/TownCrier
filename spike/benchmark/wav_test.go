package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteWav(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "test.wav")

	sampleRate := 24000
	samples := []float32{0.0, 0.5, -0.5, 1.0, -1.0}

	err := WriteWav(wavPath, samples, sampleRate)
	if err != nil {
		t.Fatalf("WriteWav failed: %v", err)
	}

	data, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	// Minimum WAV header is 44 bytes + 5 samples * 2 bytes = 54 bytes
	expectedSize := 44 + len(samples)*2
	if len(data) != expectedSize {
		t.Fatalf("expected file size %d, got %d", expectedSize, len(data))
	}

	// Verify RIFF header
	if string(data[0:4]) != "RIFF" {
		t.Errorf("expected 'RIFF', got %q", string(data[0:4]))
	}
	if string(data[8:12]) != "WAVE" {
		t.Errorf("expected 'WAVE', got %q", string(data[8:12]))
	}
	if string(data[12:16]) != "fmt " {
		t.Errorf("expected 'fmt ', got %q", string(data[12:16]))
	}

	sr := binary.LittleEndian.Uint32(data[24:28])
	if sr != uint32(sampleRate) {
		t.Errorf("expected sample rate %d, got %d", sampleRate, sr)
	}

	bits := binary.LittleEndian.Uint16(data[34:36])
	if bits != 16 {
		t.Errorf("expected 16 bits per sample, got %d", bits)
	}

	if string(data[36:40]) != "data" {
		t.Errorf("expected 'data', got %q", string(data[36:40]))
	}
}
