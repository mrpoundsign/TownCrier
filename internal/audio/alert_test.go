package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultChimeSamples(t *testing.T) {
	samples, rate := DefaultChimeSamples()
	if rate != AlertSampleRate {
		t.Fatalf("expected sample rate %d, got %d", AlertSampleRate, rate)
	}
	if len(samples) == 0 {
		t.Fatal("expected non-empty default chime samples")
	}

	var peak float32
	for _, s := range samples {
		abs := float32(math.Abs(float64(s)))
		if abs > peak {
			peak = abs
		}
	}
	if peak <= 0.05 || peak > 1.0 {
		t.Fatalf("expected reasonable chime peak amplitude in (0.05, 1.0], got %f", peak)
	}
}

func TestDecodeWAVBytesAndCache(t *testing.T) {
	// Build a mono 48kHz 16-bit WAV with a 440Hz sine wave
	var buf bytes.Buffer
	const srcRate = 48000
	const frames = 4800 // 0.1s
	dataSize := uint32(frames * 2)

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, 36+dataSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // Mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(srcRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(srcRate*2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, dataSize)
	for i := range frames {
		val := int16(16000 * math.Sin(2*math.Pi*440*float64(i)/float64(srcRate)))
		_ = binary.Write(&buf, binary.LittleEndian, val)
	}

	samples, rate, err := DecodeWAVBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("DecodeWAVBytes failed: %v", err)
	}
	if rate != AlertSampleRate {
		t.Fatalf("expected resampled rate %d, got %d", AlertSampleRate, rate)
	}
	// 4800 frames at 48kHz -> 2400 frames at 24kHz
	if len(samples) != 2400 {
		t.Fatalf("expected 2400 resampled frames, got %d", len(samples))
	}

	// Test AlertSoundCache with file on disk
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "alert.wav")
	if err := os.WriteFile(wavPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cache := NewAlertSoundCache()
	s1, r1, err := cache.Resolve(wavPath)
	if err != nil {
		t.Fatalf("Resolve(wavPath) failed: %v", err)
	}
	if r1 != AlertSampleRate || len(s1) != 2400 {
		t.Fatalf("unexpected cached wav result: rate=%d len=%d", r1, len(s1))
	}

	// Second call hits mtime cache
	s2, _, err := cache.Resolve(wavPath)
	if err != nil {
		t.Fatalf("second Resolve(wavPath) failed: %v", err)
	}
	if &s1[0] != &s2[0] {
		t.Fatal("expected AlertSoundCache to return cached slice when mtime is unchanged")
	}

	// Empty path returns default chime
	chime, cRate, err := cache.Resolve("")
	if err != nil || cRate != AlertSampleRate || len(chime) == 0 {
		t.Fatalf("expected default chime on empty path, got err=%v rate=%d len=%d", err, cRate, len(chime))
	}
}

func TestDecodeMP3Bytes(t *testing.T) {
	// Construct 4 valid MPEG-1 Layer 3 128kbps 44.1kHz Joint Stereo silent frames (417 bytes per frame).
	// Header: 0xFF 0xFB 0x90 0x64 (Sync=FFF, MPEG1, Layer3, NoCRC, 128kbps, 44100Hz, NoPad, JointStereo)
	frame := make([]byte, 417)
	frame[0] = 0xFF
	frame[1] = 0xFB
	frame[2] = 0x90
	frame[3] = 0x64

	var mp3Data bytes.Buffer
	for range 4 {
		mp3Data.Write(frame)
	}

	samples, rate, err := DecodeMP3Bytes(mp3Data.Bytes())
	if err != nil {
		t.Fatalf("DecodeMP3Bytes failed: %v", err)
	}
	if rate != AlertSampleRate {
		t.Fatalf("expected resampled rate %d, got %d", AlertSampleRate, rate)
	}
	if len(samples) == 0 {
		t.Fatal("expected decoded MP3 samples > 0")
	}

	// Also verify LoadAlertSoundFile with .mp3 extension
	tmpDir := t.TempDir()
	mp3Path := filepath.Join(tmpDir, "custom.mp3")
	if err := os.WriteFile(mp3Path, mp3Data.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile mp3 failed: %v", err)
	}
	loaded, loadedRate, err := LoadAlertSoundFile(mp3Path)
	if err != nil {
		t.Fatalf("LoadAlertSoundFile(.mp3) failed: %v", err)
	}
	if loadedRate != AlertSampleRate || len(loaded) != len(samples) {
		t.Fatalf("unexpected loaded mp3 result: rate=%d len=%d (want %d)", loadedRate, len(loaded), len(samples))
	}
	_ = time.Now()
}
