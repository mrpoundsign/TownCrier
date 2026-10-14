package audio

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/gen2brain/malgo"
)

var (
	ErrDeviceClosed = errors.New("audio device is closed")
)

// Player defines the contract for audio playback and panic control.
type Player interface {
	Play(samples []float32, sampleRate int) error
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
	fallbackFunc func(reason string)
}

// NewDevicePlayer creates an audio player targeting the specified device ID (empty for system default).
func NewDevicePlayer(targetDeviceIDHex string, defaultSampleRate int) (*DevicePlayer, error) {
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
	deviceConfig.Playback.Channels = 1
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
	bytesNeeded := numFrames * 4
	if len(pOutput) < bytesNeeded {
		return
	}

	outSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pOutput[0])), numFrames)

	if p.aborted.Load() {
		for i := range outSamples {
			outSamples[i] = 0
		}
		return
	}

	vol := p.Volume()

	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range numFrames {
		if p.cursor < len(p.buffer) {
			outSamples[i] = p.buffer[p.cursor] * vol
			p.cursor++
		} else {
			outSamples[i] = 0
		}
	}

	// Reclaim buffer memory if all queued audio has finished playing
	if p.cursor >= len(p.buffer) && len(p.buffer) > 0 {
		p.buffer = p.buffer[:0]
		p.cursor = 0
	}
}

// Play queues PCM float32 samples into the player.
func (p *DevicePlayer) Play(samples []float32, sampleRate int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrDeviceClosed
	}

	p.aborted.Store(false)
	p.buffer = append(p.buffer, samples...)
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

