package audio

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

const defaultPlaybackStallTimeout = 2 * time.Second

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
	Pause()
	Resume()
	ResumeFromSentenceStart()
	RewindSentence()
	IsPaused() bool
	MarkSentenceBoundary()
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

	mu                 sync.Mutex
	buffer             []float32
	cursor             int
	sentenceBoundaries []int
	pendingBoundary    bool
	paused             atomic.Bool
	aborted            atomic.Bool
	closed             bool
	closedAtomic       atomic.Bool
	volumeBits         atomic.Uint32
	duckSet            atomic.Bool
	duckBits           atomic.Uint32
	fallbackFunc       func(reason string)

	lastCallbackNano atomic.Int64
	playStartedNano  atomic.Int64
	deviceStopped    atomic.Bool
	intentionalStop  atomic.Bool
	recovering       atomic.Bool
	recoveryAttempts atomic.Int32
	stallTimeoutDur  time.Duration
	nowFn            func() time.Time
	reinitFn         func() error
}

func (p *DevicePlayer) now() time.Time {
	if p.nowFn != nil {
		return p.nowFn()
	}
	return time.Now()
}

func (p *DevicePlayer) stallTimeout() time.Duration {
	if p.stallTimeoutDur > 0 {
		return p.stallTimeoutDur
	}
	return defaultPlaybackStallTimeout
}

func (p *DevicePlayer) handleUnexpectedStop() {
	if p.intentionalStop.Load() || p.closedAtomic.Load() {
		return
	}
	p.deviceStopped.Store(true)
	log.Printf("[Audio] Playback device stopped unexpectedly; will attempt recovery")
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
		matched := false
		devices, err := p.ctx.Devices(malgo.Playback)
		if err == nil {
			for _, dev := range devices {
				if MatchDeviceID(dev.ID, deviceIDHex) {
					deviceConfig.Playback.DeviceID = dev.ID.Pointer()
					matched = true
					break
				}
			}
		}
		if !matched {
			p.notifyFallback("Requested audio device not found; fell back to default device")
		}
	}

	callbacks := malgo.DeviceCallbacks{
		Data: p.onAudioCallback,
		Stop: p.handleUnexpectedStop,
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
	p.lastCallbackNano.Store(p.now().UnixNano())

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

	if p.aborted.Load() || p.paused.Load() {
		for i := range outSamples {
			outSamples[i] = 0
		}
		return
	}

	vol := p.Volume() * p.DuckMultiplier()

	p.mu.Lock()
	defer p.mu.Unlock()

	advanced := false
	if channels == 2 {
		for i := range numSamples {
			if p.cursor < len(p.buffer) {
				outSamples[i] = p.buffer[p.cursor] * vol
				p.cursor++
				advanced = true
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
				advanced = true
			case p.cursor < len(p.buffer):
				outSamples[i] = p.buffer[p.cursor] * vol
				p.cursor++
				advanced = true
			default:
				outSamples[i] = 0
			}
		}
	}
	if advanced {
		p.recoveryAttempts.Store(0)
	}

	// Reclaim buffer memory if all queued audio has finished playing
	if p.cursor >= len(p.buffer) && len(p.buffer) > 0 {
		p.buffer = p.buffer[:0]
		p.cursor = 0
		p.sentenceBoundaries = p.sentenceBoundaries[:0]
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

func (p *DevicePlayer) clearIfFinishedLocked() {
	if p.cursor >= len(p.buffer) && len(p.buffer) > 0 {
		p.buffer = p.buffer[:0]
		p.cursor = 0
		p.sentenceBoundaries = p.sentenceBoundaries[:0]
	}
}

func (p *DevicePlayer) flushBufferLocked() {
	p.buffer = p.buffer[:0]
	p.cursor = 0
	p.sentenceBoundaries = p.sentenceBoundaries[:0]
	p.pendingBoundary = false
}

// Play queues PCM float32 samples into the player at center pan and unity gain,
// automatically resampling if sampleRate differs from device sampleRate.
func (p *DevicePlayer) Play(samples []float32, sampleRate int) error {
	return p.PlayWithOptions(samples, sampleRate, PlayOptions{Gain: 1.0, Pan: 0.0})
}

// PlayWithOptions queues mono PCM float32 samples into the player with per-utterance gain and stereo panning,
// automatically resampling if sampleRate differs from device sampleRate.
func (p *DevicePlayer) PlayWithOptions(samples []float32, sampleRate int, opts PlayOptions) error {
	if p.deviceStopped.Load() && (p.ctx != nil || p.reinitFn != nil) {
		if p.recovering.CompareAndSwap(false, true) {
			if err := p.reinitPlaybackDevice(); err != nil {
				log.Printf("[Audio] Playback device reinit before Play failed: %v", err)
			} else {
				p.deviceStopped.Store(false)
			}
			p.recovering.Store(false)
		}
	}

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
	p.recoveryAttempts.Store(0)
	p.clearIfFinishedLocked()
	if len(p.buffer) == 0 {
		p.playStartedNano.Store(p.now().UnixNano())
	}
	if p.pendingBoundary {
		boundary := len(p.buffer)
		if len(p.sentenceBoundaries) == 0 || p.sentenceBoundaries[len(p.sentenceBoundaries)-1] != boundary {
			p.sentenceBoundaries = append(p.sentenceBoundaries, boundary)
		}
		p.pendingBoundary = false
	}
	p.buffer = append(p.buffer, stereo...)
	return nil
}

// Stop executes an Emergency Panic Mute (<50ms): flushes audio queue and zeroes outputs immediately.
func (p *DevicePlayer) Stop() {
	p.aborted.Store(true)
	p.paused.Store(false)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.buffer = p.buffer[:0]
	p.cursor = 0
	p.sentenceBoundaries = p.sentenceBoundaries[:0]
	p.pendingBoundary = false
}

// Pause pauses audio playback mid-buffer, emitting silence without advancing the cursor.
func (p *DevicePlayer) Pause() {
	p.paused.Store(true)
}

// Resume unpauses audio playback from the current cursor position.
func (p *DevicePlayer) Resume() {
	p.playStartedNano.Store(p.now().UnixNano())
	p.paused.Store(false)
}

// ResumeFromSentenceStart rewinds the playback cursor to the beginning of the currently playing sentence, then unpauses.
func (p *DevicePlayer) ResumeFromSentenceStart() {
	p.mu.Lock()
	p.clearIfFinishedLocked()
	if len(p.buffer) > 0 && !p.aborted.Load() {
		targetBoundary := 0
		for _, b := range p.sentenceBoundaries {
			if b <= p.cursor {
				targetBoundary = b
			} else {
				break
			}
		}
		p.cursor = targetBoundary
	}
	p.mu.Unlock()
	p.playStartedNano.Store(p.now().UnixNano())
	p.paused.Store(false)
}

// RewindSentence rewinds the playback cursor to the start of the current sentence or previous sentence.
func (p *DevicePlayer) RewindSentence() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sentenceBoundaries) == 0 {
		p.cursor = 0
		return
	}

	threshold := int(p.sampleRate) * 2 / 2 // 500ms in interleaved stereo samples
	currentIdx := 0
	for i, b := range p.sentenceBoundaries {
		if b <= p.cursor {
			currentIdx = i
		} else {
			break
		}
	}

	targetBoundary := p.sentenceBoundaries[currentIdx]
	if currentIdx > 0 && p.cursor-targetBoundary <= threshold {
		p.cursor = p.sentenceBoundaries[currentIdx-1]
	} else {
		p.cursor = targetBoundary
	}
}

// IsPaused returns true if playback is currently paused.
func (p *DevicePlayer) IsPaused() bool {
	return p.paused.Load()
}

// MarkSentenceBoundary records the start of a new sentence or clause in the active buffer.
func (p *DevicePlayer) MarkSentenceBoundary() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.clearIfFinishedLocked()
	p.pendingBoundary = true
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

func (p *DevicePlayer) reinitPlaybackDevice() error {
	if p.reinitFn != nil {
		return p.reinitFn()
	}

	p.mu.Lock()
	if p.closed || p.ctx == nil {
		p.mu.Unlock()
		return ErrDeviceClosed
	}
	oldDev := p.device
	p.device = nil
	target := p.targetDevHex
	p.mu.Unlock()

	if oldDev != nil {
		p.intentionalStop.Store(true)
		_ = oldDev.Stop()
		oldDev.Uninit()
		p.intentionalStop.Store(false)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ctx == nil {
		return ErrDeviceClosed
	}
	return p.initDevice(target, false)
}

func (p *DevicePlayer) handlePlaybackStall(stopped bool) bool {
	if !p.recovering.CompareAndSwap(false, true) {
		return true
	}
	defer p.recovering.Store(false)

	attempt := p.recoveryAttempts.Add(1)
	if attempt > 1 {
		log.Printf("[Audio] Playback device stalled again after recovery; flushing stuck buffer to unblock queue")
		p.mu.Lock()
		p.flushBufferLocked()
		p.mu.Unlock()
		return false
	}

	log.Printf("[Audio] Playback device stalled (stopped=%v); attempting automatic recovery...", stopped)
	if err := p.reinitPlaybackDevice(); err != nil {
		log.Printf("[Audio] Playback device recovery failed (%v); flushing stuck buffer to unblock queue", err)
		p.mu.Lock()
		p.flushBufferLocked()
		p.mu.Unlock()
		return false
	}

	p.deviceStopped.Store(false)
	nowNano := p.now().UnixNano()
	p.playStartedNano.Store(nowNano)
	p.lastCallbackNano.Store(nowNano)
	p.notifyFallback("Audio playback device stalled; automatically reinitialized")
	return true
}

// IsPlaying returns true if audio is actively being played from the buffer.
// If the audio device stopped unexpectedly or callbacks have stalled while unpaused,
// it attempts automatic recovery once and flushes the stuck buffer if recovery fails
// or stalls again so the speech queue never deadlocks.
func (p *DevicePlayer) IsPlaying() bool {
	if p.aborted.Load() {
		return false
	}
	p.mu.Lock()
	playing := p.cursor < len(p.buffer)
	hasBackend := p.device != nil || p.reinitFn != nil
	hasCustomTimeout := p.stallTimeoutDur > 0
	p.mu.Unlock()
	if !playing {
		return false
	}
	if p.paused.Load() {
		return true
	}

	stopped := p.deviceStopped.Load()
	stalled := false
	if !stopped && (hasBackend || hasCustomTimeout) {
		refNano := max(p.lastCallbackNano.Load(), p.playStartedNano.Load())
		if refNano > 0 && p.now().Sub(time.Unix(0, refNano)) >= p.stallTimeout() {
			stalled = true
		}
	}
	if stopped || stalled {
		return p.handlePlaybackStall(stopped)
	}
	return true
}

// NewPlayer creates an audio player targeting the specified device ID with default 24000Hz Kokoro sample rate.
func NewPlayer(targetDeviceIDHex string) (*DevicePlayer, error) {
	return NewDevicePlayer(targetDeviceIDHex, 24000)
}

// SwitchDevice re-initializes playback on a new device ID.
func (p *DevicePlayer) SwitchDevice(targetDeviceIDHex string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrDeviceClosed
	}
	oldDev := p.device
	p.device = nil
	p.targetDevHex = targetDeviceIDHex
	p.mu.Unlock()

	if oldDev != nil {
		p.intentionalStop.Store(true)
		_ = oldDev.Stop()
		oldDev.Uninit()
		p.intentionalStop.Store(false)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrDeviceClosed
	}
	p.deviceStopped.Store(false)
	p.recoveryAttempts.Store(0)
	return p.initDevice(targetDeviceIDHex, false)
}

// PanicStop executes an Emergency Panic Mute (<50ms): flushes audio queue and zeroes outputs immediately.
func (p *DevicePlayer) PanicStop() {
	p.Stop()
}

// ResetPanic clears the emergency abort and pause flags so audio can play again.
func (p *DevicePlayer) ResetPanic() {
	p.aborted.Store(false)
	p.paused.Store(false)
}

// Close gracefully closes the audio device and context.
func (p *DevicePlayer) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.closedAtomic.Store(true)
	dev := p.device
	p.device = nil
	ctx := p.ctx
	p.ctx = nil
	p.mu.Unlock()

	p.Stop()

	if dev != nil {
		p.intentionalStop.Store(true)
		_ = dev.Stop()
		dev.Uninit()
		p.intentionalStop.Store(false)
	}
	if ctx != nil {
		_ = ctx.Uninit()
		ctx.Free()
	}
	return nil
}

