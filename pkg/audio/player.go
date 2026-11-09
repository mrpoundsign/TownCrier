package audio

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/gen2brain/malgo"
)

var (
	ErrDeviceClosed = errors.New("audio device is closed")
)

// PlayOptions configures per-utterance gain and constant-power stereo panning.
type PlayOptions struct {
	Gain float32 // Multiplier (<= 0 defaults to 1.0; clamped to [0.0, 2.0])
	Pan  float32 // -1.0 (full left) .. 0.0 (center) .. +1.0 (full right)
}

// Player defines the contract for audio playback and panic control.
type Player interface {
	Play(samples []float32, sampleRate int) error
	PlayWithOptions(samples []float32, sampleRate int, opts PlayOptions) error
	Stop()
	PanicStop()
	ResetPanic()
	SwitchDevice(targetDeviceIDHex string) error
	SetVolume(vol float32)
	Volume() float32
	IsPlaying() bool
	Close() error
	SetFallbackListener(fn func(reason string))
}

// DuckablePlayer is an optional extension of Player that supports real-time volume ducking
// (e.g., attenuating TTS gain while the streamer's microphone VAD gate is active).
type DuckablePlayer interface {
	SetDuckMultiplier(mult float32)
	DuckMultiplier() float32
}

// DevicePlayer implements Player using miniaudio via malgo.
type DevicePlayer struct {
	ctx          *malgo.AllocatedContext
	device       *malgo.Device
	sampleRate   uint32
	targetDevHex string

	mu           sync.Mutex
	buffer       []float32
	cursor       int
	aborted      atomic.Bool
	closed       bool
	volumeBits   atomic.Uint32
	duckSet      atomic.Bool
	duckBits     atomic.Uint32
	fallbackFunc func(reason string)
}

// NewDevicePlayer creates an audio player targeting the specified device ID (empty for system default).
func NewDevicePlayer(targetDeviceIDHex string, defaultSampleRate int) (*DevicePlayer, error) {
	if testing.Testing() {
		return nil, errors.New("hardware audio playback is disabled during unit tests")
	}
	if defaultSampleRate <= 0 {
		defaultSampleRate = 24000
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}

	p := &DevicePlayer{
		ctx:          ctx,
		sampleRate:   uint32(defaultSampleRate),
		targetDevHex: targetDeviceIDHex,
	}
	p.SetVolume(1.0)

	if err := p.initDevice(targetDeviceIDHex, false); err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, err
	}

	return p, nil
}

func (p *DevicePlayer) initDevice(deviceIDHex string, isFallback bool) error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatF32
	deviceConfig.Playback.Channels = 2
	deviceConfig.SampleRate = p.sampleRate
	deviceConfig.Alsa.NoMMap = 1

	if deviceIDHex != "" && !isFallback {
		// Attempt to match device ID from enumeration
		devices, err := p.ctx.Devices(malgo.Playback)
		if err == nil {
			for _, dev := range devices {
				devHex := hex.EncodeToString(dev.ID[:])
				if stringsEqualOrContains(devHex, deviceIDHex) {
					deviceConfig.Playback.DeviceID = dev.ID.Pointer()
					break
				}
			}
		}
	}

	callbacks := malgo.DeviceCallbacks{
		Data: p.onAudioCallback,
		Stop: func() {
			// Callback when device stops
		},
	}

	device, err := malgo.InitDevice(p.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		if !isFallback {
			// Fallback to default device (D31)
			if fbErr := p.initDevice("", true); fbErr == nil {
				p.notifyFallback("Failed to open requested audio device; fell back to default device")
				return nil
			}
		}
		return fmt.Errorf("init playback device: %w", err)
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		if !isFallback {
			if fbErr := p.initDevice("", true); fbErr == nil {
				p.notifyFallback("Failed to start requested audio device; fell back to default device")
				return nil
			}
		}
		return fmt.Errorf("start playback device: %w", err)
	}

	p.device = device
	return nil
}

func stringsEqualOrContains(s1, s2 string) bool {
	return s1 == s2 || (len(s1) > 0 && len(s2) > 0 && (len(s1) >= len(s2) && s1[:len(s2)] == s2))
}

func (p *DevicePlayer) notifyFallback(reason string) {
	p.mu.Lock()
	fn := p.fallbackFunc
	p.mu.Unlock()
	if fn != nil {
		go fn(reason)
	}
}

// SetFallbackListener registers a callback invoked if device disconnection/fallback occurs (D31).
func (p *DevicePlayer) SetFallbackListener(fn func(reason string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fallbackFunc = fn
}

// onAudioCallback handles miniaudio PCM buffer requests in the real-time audio thread.
func (p *DevicePlayer) onAudioCallback(pOutput, pInput []byte, frameCount uint32) {
	numFrames := int(frameCount)
	if numFrames <= 0 || len(pOutput) < numFrames*4 {
		return
	}

	channels := 1
	if len(pOutput) >= numFrames*8 {
		channels = 2
	}
	numSamples := numFrames * channels
	outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pOutput[0])), numSamples)

	if p.aborted.Load() {
		for i := range outSamples {
			outSamples[i] = 0
		}
		return
	}

	vol := p.Volume() * p.DuckMultiplier()

	p.mu.Lock()
	defer p.mu.Unlock()

	if channels == 2 {
		for i := range numSamples {
			if p.cursor < len(p.buffer) {
				outSamples[i] = p.buffer[p.cursor] * vol
				p.cursor++
			} else {
				outSamples[i] = 0
			}
		}
	} else {
		for i := range numFrames {
			switch {
			case p.cursor+1 < len(p.buffer):
				l := p.buffer[p.cursor]
				r := p.buffer[p.cursor+1]
				outSamples[i] = 0.5 * (l + r) * vol
				p.cursor += 2
			case p.cursor < len(p.buffer):
				outSamples[i] = p.buffer[p.cursor] * vol
				p.cursor++
			default:
				outSamples[i] = 0
			}
		}
	}

	// Reclaim buffer memory if all queued audio has finished playing
	if p.cursor >= len(p.buffer) && len(p.buffer) > 0 {
		p.buffer = p.buffer[:0]
		p.cursor = 0
	}
}

// ResamplePCM converts mono float32 PCM samples from srcRate to dstRate using linear interpolation.
func ResamplePCM(samples []float32, srcRate, dstRate int) []float32 {
	if len(samples) == 0 || srcRate <= 0 || dstRate <= 0 || srcRate == dstRate {
		return samples
	}
	ratio := float64(srcRate) / float64(dstRate)
	outLen := int(float64(len(samples)) / ratio)
	if outLen <= 0 {
		return samples
	}
	out := make([]float32, outLen)
	lastIdx := len(samples) - 1
	for i := range outLen {
		srcPos := float64(i) * ratio
		idx := int(srcPos)
		if idx >= lastIdx {
			out[i] = samples[lastIdx]
			continue
		}
		frac := float32(srcPos - float64(idx))
		out[i] = samples[idx]*(1.0-frac) + samples[idx+1]*frac
	}
	return out
}

// PanMonoToStereo converts mono float32 PCM samples into interleaved 2-channel stereo [L, R, L, R, ...]
// using constant-power stereo panning normalized so Pan == 0.0 yields 1.0 * gain on both channels.
func PanMonoToStereo(mono []float32, gain, pan float32) (leftGain, rightGain float32, stereo []float32) {
	if gain <= 0 {
		gain = 1.0
	} else if gain > 2.0 {
		gain = 2.0
	}
	if pan < -1.0 {
		pan = -1.0
	} else if pan > 1.0 {
		pan = 1.0
	}
	if pan == 0.0 {
		leftGain = gain
		rightGain = gain
	} else {
		angle := (float64(pan) + 1.0) * (math.Pi / 4.0)
		scale := float32(math.Sqrt2) * gain
		leftGain = float32(math.Cos(angle)) * scale
		rightGain = float32(math.Sin(angle)) * scale
	}
	stereo = make([]float32, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s * leftGain
		stereo[i*2+1] = s * rightGain
	}
	return leftGain, rightGain, stereo
}

// Play queues PCM float32 samples into the player at center pan and unity gain,
// automatically resampling if sampleRate differs from device sampleRate.
func (p *DevicePlayer) Play(samples []float32, sampleRate int) error {
	return p.PlayWithOptions(samples, sampleRate, PlayOptions{Gain: 1.0, Pan: 0.0})
}

// PlayWithOptions queues mono PCM float32 samples into the player with per-utterance gain and stereo panning,
// automatically resampling if sampleRate differs from device sampleRate.
func (p *DevicePlayer) PlayWithOptions(samples []float32, sampleRate int, opts PlayOptions) error {
	if sampleRate > 0 && p.sampleRate > 0 && sampleRate != int(p.sampleRate) {
		samples = ResamplePCM(samples, sampleRate, int(p.sampleRate))
	}

	_, _, stereo := PanMonoToStereo(samples, opts.Gain, opts.Pan)

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrDeviceClosed
	}

	p.aborted.Store(false)
	if p.cursor > 0 {
		remaining := len(p.buffer) - p.cursor
		if remaining > 0 {
			copy(p.buffer, p.buffer[p.cursor:])
			p.buffer = p.buffer[:remaining]
		} else {
			p.buffer = p.buffer[:0]
		}
		p.cursor = 0
	}
	p.buffer = append(p.buffer, stereo...)
	return nil
}

// Stop executes an Emergency Panic Mute (<50ms): flushes audio queue and zeroes outputs immediately.
func (p *DevicePlayer) Stop() {
	p.aborted.Store(true)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.buffer = p.buffer[:0]
	p.cursor = 0
}

// SetVolume sets the master output volume, clamped between 0.0 and 1.0.
func (p *DevicePlayer) SetVolume(vol float32) {
	if vol < 0.0 {
		vol = 0.0
	} else if vol > 1.0 {
		vol = 1.0
	}
	p.volumeBits.Store(math.Float32bits(vol))
}

// Volume returns current output volume (0.0 to 1.0).
func (p *DevicePlayer) Volume() float32 {
	return math.Float32frombits(p.volumeBits.Load())
}

// SetDuckMultiplier sets the real-time ducking attenuation multiplier, clamped between 0.0 and 1.0.
func (p *DevicePlayer) SetDuckMultiplier(mult float32) {
	if mult < 0.0 {
		mult = 0.0
	} else if mult > 1.0 {
		mult = 1.0
	}
	p.duckBits.Store(math.Float32bits(mult))
	p.duckSet.Store(true)
}

// DuckMultiplier returns the current ducking attenuation multiplier (defaults to 1.0 when unset).
func (p *DevicePlayer) DuckMultiplier() float32 {
	if !p.duckSet.Load() {
		return 1.0
	}
	return math.Float32frombits(p.duckBits.Load())
}

// IsPlaying returns true if audio is actively being played from the buffer.
func (p *DevicePlayer) IsPlaying() bool {
	if p.aborted.Load() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursor < len(p.buffer)
}

// NewPlayer creates an audio player targeting the specified device ID with default 24000Hz Kokoro sample rate.
func NewPlayer(targetDeviceIDHex string) (*DevicePlayer, error) {
	return NewDevicePlayer(targetDeviceIDHex, 24000)
}

// SwitchDevice re-initializes playback on a new device ID.
func (p *DevicePlayer) SwitchDevice(targetDeviceIDHex string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrDeviceClosed
	}

	if p.device != nil {
		p.device.Uninit()
		p.device = nil
	}

	p.targetDevHex = targetDeviceIDHex
	return p.initDevice(targetDeviceIDHex, false)
}

// PanicStop executes an Emergency Panic Mute (<50ms): flushes audio queue and zeroes outputs immediately.
func (p *DevicePlayer) PanicStop() {
	p.Stop()
}

// ResetPanic clears the emergency abort flag so audio can play again.
func (p *DevicePlayer) ResetPanic() {
	p.aborted.Store(false)
}

// Close gracefully closes the audio device and context.
func (p *DevicePlayer) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.Stop()

	if p.device != nil {
		_ = p.device.Stop()
		p.device.Uninit()
		p.device = nil
	}
	if p.ctx != nil {
		_ = p.ctx.Uninit()
		p.ctx.Free()
		p.ctx = nil
	}
	return nil
}

