package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yatagai-mm/byrd"
)

const (
	// AlertSampleRate is the normalized sample rate (24 kHz) for alert audio buffers.
	AlertSampleRate = 24000
)

var (
	defaultChimeOnce    sync.Once
	defaultChimeSamples []float32
)

// DefaultChimeSamples returns a pre-synthesized 24 kHz mono float32 two-tone harmonic chime
// (A5 880 Hz -> D6 1174.66 Hz, ~140ms total) with smooth attack and exponential decay.
func DefaultChimeSamples() ([]float32, int) {
	defaultChimeOnce.Do(func() {
		const durationSec = 0.140
		totalSamples := int(AlertSampleRate * durationSec)
		splitSample := int(AlertSampleRate * 0.055) // 55ms first note, 85ms second note
		buf := make([]float32, totalSamples)

		for i := range totalSamples {
			var freq float64
			var localIdx int
			var noteLen int
			if i < splitSample {
				freq = 880.0 // A5
				localIdx = i
				noteLen = splitSample
			} else {
				freq = 1174.66 // D6
				localIdx = i - splitSample
				noteLen = totalSamples - splitSample
			}

			t := float64(localIdx) / float64(AlertSampleRate)
			// 3ms linear attack to avoid clicks + smooth exponential decay
			attackSamples := int(0.003 * AlertSampleRate)
			env := 1.0
			if localIdx < attackSamples {
				env = float64(localIdx) / float64(attackSamples)
			}
			progress := float64(localIdx) / float64(noteLen)
			decay := math.Exp(-4.2 * progress)

			// Fundamental + subtle octave harmonic for a warm bell-like tone
			fundamental := math.Sin(2.0 * math.Pi * freq * t)
			harmonic := 0.22 * math.Sin(2.0*math.Pi*(freq*2.0)*t)
			sample := 0.32 * env * decay * (fundamental + harmonic)
			buf[i] = float32(sample)
		}
		defaultChimeSamples = buf
	})
	return defaultChimeSamples, AlertSampleRate
}

// AlertSoundCache caches decoded custom alert audio files by path and modification time.
type AlertSoundCache struct {
	mu      sync.Mutex
	path    string
	modTime time.Time
	samples []float32
	rate    int
}

// NewAlertSoundCache creates an AlertSoundCache instance.
func NewAlertSoundCache() *AlertSoundCache {
	return &AlertSoundCache{}
}

// Resolve returns the PCM samples and sample rate for customPath (using the mtime cache),
// or returns DefaultChimeSamples() when customPath is empty.
func (c *AlertSoundCache) Resolve(customPath string) ([]float32, int, error) {
	trimmed := strings.TrimSpace(customPath)
	if trimmed == "" {
		samples, rate := DefaultChimeSamples()
		return samples, rate, nil
	}

	info, err := os.Stat(trimmed)
	if err != nil {
		return nil, 0, fmt.Errorf("stat alert sound %q: %w", trimmed, err)
	}

	c.mu.Lock()
	if c.path == trimmed && c.modTime.Equal(info.ModTime()) && len(c.samples) > 0 {
		samples := c.samples
		rate := c.rate
		c.mu.Unlock()
		return samples, rate, nil
	}
	c.mu.Unlock()

	samples, rate, err := LoadAlertSoundFile(trimmed)
	if err != nil {
		return nil, 0, err
	}

	c.mu.Lock()
	c.path = trimmed
	c.modTime = info.ModTime()
	c.samples = samples
	c.rate = rate
	c.mu.Unlock()

	return samples, rate, nil
}

// LoadAlertSoundFile decodes a .wav or .mp3 file into 24 kHz mono float32 PCM samples.
func LoadAlertSoundFile(path string) ([]float32, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read alert sound file: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".wav", ".wave":
		return DecodeWAVBytes(data)
	case ".mp3":
		return DecodeMP3Bytes(data)
	default:
		if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
			return DecodeWAVBytes(data)
		}
		return DecodeMP3Bytes(data)
	}
}

// DecodeMP3Bytes decodes MP3 audio bytes using pure-Go github.com/yatagai-mm/byrd into 24 kHz mono float32 PCM.
func DecodeMP3Bytes(data []byte) ([]float32, int, error) {
	if len(data) == 0 {
		return nil, 0, errors.New("empty MP3 data")
	}
	dec, err := byrd.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("init mp3 decoder: %w", err)
	}
	pcm, err := dec.BatchDecode()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, fmt.Errorf("decode mp3 audio: %w", err)
	}
	if pcm == nil || len(pcm.Samples) == 0 {
		return nil, 0, errors.New("mp3 file contained no audio samples")
	}

	channels := pcm.Channels
	if channels <= 0 {
		channels = 1
	}
	srcRate := int(pcm.SampleRate)
	if srcRate <= 0 {
		srcRate = 44100
	}

	numFrames := len(pcm.Samples) / channels
	if numFrames <= 0 {
		return nil, 0, errors.New("mp3 file contained no complete audio frames")
	}

	mono := make([]float32, numFrames)
	invChan := 1.0 / float32(channels)
	for i := range numFrames {
		var sum float32
		base := i * channels
		for ch := range channels {
			sum += float32(pcm.Samples[base+ch]) / 32768.0
		}
		mono[i] = sum * invChan
	}

	if srcRate != AlertSampleRate {
		mono = ResamplePCM(mono, srcRate, AlertSampleRate)
	}
	return mono, AlertSampleRate, nil
}

// DecodeWAVBytes decodes RIFF/WAVE PCM (8/16/24/32-bit int or 32-bit float) bytes into 24 kHz mono float32 PCM.
func DecodeWAVBytes(data []byte) ([]float32, int, error) {
	if len(data) < 44 {
		return nil, 0, errors.New("wav file too short")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errors.New("invalid RIFF/WAVE header")
	}

	var (
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
		haveFmt       bool
		pcmData       []byte
	)

	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			if chunkID == "data" && offset < len(data) {
				pcmData = data[offset:]
			}
			break
		}

		chunkBytes := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunkBytes) < 16 {
				return nil, 0, errors.New("wav fmt chunk too short")
			}
			audioFormat = binary.LittleEndian.Uint16(chunkBytes[0:2])
			numChannels = binary.LittleEndian.Uint16(chunkBytes[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunkBytes[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(chunkBytes[14:16])
			haveFmt = true
		case "data":
			pcmData = chunkBytes
		}

		offset += chunkSize
		// RIFF chunks are word-aligned (padded to even byte boundary)
		if chunkSize%2 == 1 && offset < len(data) {
			offset++
		}
	}

	if !haveFmt {
		return nil, 0, errors.New("wav missing fmt chunk")
	}
	if len(pcmData) == 0 {
		return nil, 0, errors.New("wav missing data chunk")
	}
	if numChannels == 0 || sampleRate == 0 {
		return nil, 0, errors.New("wav has invalid channel count or sample rate")
	}
	if audioFormat != 1 && audioFormat != 3 {
		return nil, 0, fmt.Errorf("unsupported wav audio format %d (only PCM=1 and IEEE Float=3 supported)", audioFormat)
	}

	bytesPerSample := int(bitsPerSample) / 8
	if bytesPerSample <= 0 {
		return nil, 0, fmt.Errorf("unsupported wav bits per sample: %d", bitsPerSample)
	}
	channels := int(numChannels)
	frameSize := bytesPerSample * channels
	numFrames := len(pcmData) / frameSize
	if numFrames <= 0 {
		return nil, 0, errors.New("wav contains 0 sample frames")
	}

	mono := make([]float32, numFrames)
	invChan := 1.0 / float32(channels)

	for i := range numFrames {
		frameStart := i * frameSize
		var sum float32
		for ch := range channels {
			sOff := frameStart + ch*bytesPerSample
			var val float32
			switch {
			case audioFormat == 3 && bitsPerSample == 32:
				bits := binary.LittleEndian.Uint32(pcmData[sOff : sOff+4])
				val = math.Float32frombits(bits)
			case audioFormat == 1 && bitsPerSample == 8:
				// 8-bit WAV PCM is unsigned (0..255, center 128)
				val = (float32(pcmData[sOff]) - 128.0) / 128.0
			case audioFormat == 1 && bitsPerSample == 16:
				v := int16(binary.LittleEndian.Uint16(pcmData[sOff : sOff+2]))
				val = float32(v) / 32768.0
			case audioFormat == 1 && bitsPerSample == 24:
				b0 := int32(pcmData[sOff])
				b1 := int32(pcmData[sOff+1]) << 8
				b2 := int32(int8(pcmData[sOff+2])) << 16
				val = float32(b0|b1|b2) / 8388608.0
			case audioFormat == 1 && bitsPerSample == 32:
				v := int32(binary.LittleEndian.Uint32(pcmData[sOff : sOff+4]))
				val = float32(float64(v) / 2147483648.0)
			default:
				return nil, 0, fmt.Errorf("unsupported wav encoding (format=%d, bits=%d)", audioFormat, bitsPerSample)
			}
			sum += val
		}
		mono[i] = sum * invChan
	}

	if int(sampleRate) != AlertSampleRate {
		mono = ResamplePCM(mono, int(sampleRate), AlertSampleRate)
	}
	return mono, AlertSampleRate, nil
}
