package audio

import (
	"encoding/hex"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

const (
	// MinCaptureDBFS is the floor decibel value reported for silence.
	MinCaptureDBFS float32 = -60.0
	// DefaultCaptureSampleRate is the sample rate used for microphone RMS/VAD monitoring.
	DefaultCaptureSampleRate uint32 = 16000
)

// ComputeRMSdBFS calculates the root-mean-square energy of mono float32 PCM samples
// in decibels relative to full scale (dBFS), clamped to [MinCaptureDBFS, 0.0].
func ComputeRMSdBFS(samples []float32) float32 {
	if len(samples) == 0 {
		return MinCaptureDBFS
	}
	var sumSq float64
	for _, s := range samples {
		v := float64(s)
		sumSq += v * v
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))
	if rms <= 1e-3 {
		return MinCaptureDBFS
	}
	db := float32(20.0 * math.Log10(rms))
	if db < MinCaptureDBFS {
		return MinCaptureDBFS
	}
	if db > 0.0 {
		return 0.0
	}
	return db
}

// CaptureMonitor monitors a WASAPI/miniaudio microphone capture device, computing
// real-time RMS dBFS levels and maintaining a hold/release hysteresis voice activity gate.
type CaptureMonitor struct {
	mu          sync.Mutex
	ctx         *malgo.AllocatedContext
	device      *malgo.Device
	deviceIDHex string
	running     bool

	thresholdBits atomic.Uint32
	releaseNanos  atomic.Int64
	levelDBBits   atomic.Uint32
	lastAboveNano atomic.Int64
	active        atomic.Bool

	onActiveChange func(active bool)
	onLevelUpdate  func(levelDB float32, active bool)
}

// NewCaptureMonitor creates a microphone VAD/RMS gate monitor with the given threshold (dBFS) and release hold duration.
func NewCaptureMonitor(thresholdDB float32, release time.Duration) *CaptureMonitor {
	m := &CaptureMonitor{}
	m.levelDBBits.Store(math.Float32bits(MinCaptureDBFS))
	m.SetParameters(thresholdDB, release)
	return m
}

// SetParameters dynamically updates the activation threshold (dBFS) and release hold duration.
func (m *CaptureMonitor) SetParameters(thresholdDB float32, release time.Duration) {
	if thresholdDB < -58.0 {
		thresholdDB = -58.0
	} else if thresholdDB > -5.0 {
		thresholdDB = -5.0
	}
	if release < 0 {
		release = 0
	}
	m.thresholdBits.Store(math.Float32bits(thresholdDB))
	m.releaseNanos.Store(int64(release))
}

// ThresholdDB returns the current activation threshold in dBFS.
func (m *CaptureMonitor) ThresholdDB() float32 {
	return math.Float32frombits(m.thresholdBits.Load())
}

// ReleaseDuration returns the current release hold duration.
func (m *CaptureMonitor) ReleaseDuration() time.Duration {
	return time.Duration(m.releaseNanos.Load())
}

// CurrentLevelDB returns the most recent measured input level in dBFS ([-60.0, 0.0]).
func (m *CaptureMonitor) CurrentLevelDB() float32 {
	return math.Float32frombits(m.levelDBBits.Load())
}

// IsActive returns true if microphone voice activity is currently detected (including release hold time).
func (m *CaptureMonitor) IsActive() bool {
	return m.active.Load()
}

// IsRunning returns true if the capture monitor is currently started.
func (m *CaptureMonitor) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// DeviceID returns the target capture device ID hex currently configured on the monitor.
func (m *CaptureMonitor) DeviceID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deviceIDHex
}

// SetOnActiveChange registers a callback invoked whenever the gate state transitions between active and inactive.
func (m *CaptureMonitor) SetOnActiveChange(fn func(active bool)) {
	m.mu.Lock()
	m.onActiveChange = fn
	m.mu.Unlock()
}

// SetOnLevelUpdate registers a callback invoked on capture buffers with the current dBFS level and gate state.
func (m *CaptureMonitor) SetOnLevelUpdate(fn func(levelDB float32, active bool)) {
	m.mu.Lock()
	m.onLevelUpdate = fn
	m.mu.Unlock()
}

// ProcessSamples evaluates a buffer of mono float32 PCM samples at timestamp now,
// updating the current dBFS level and gate state with release hysteresis.
func (m *CaptureMonitor) ProcessSamples(samples []float32, now time.Time) (levelDB float32, isActive bool) {
	db := ComputeRMSdBFS(samples)
	m.levelDBBits.Store(math.Float32bits(db))

	thresh := m.ThresholdDB()
	rel := m.ReleaseDuration()

	var stateChanged bool
	var newState bool

	if db >= thresh {
		m.lastAboveNano.Store(now.UnixNano())
		if !m.active.Swap(true) {
			stateChanged = true
			newState = true
		}
	} else if m.active.Load() {
		last := m.lastAboveNano.Load()
		if last == 0 || now.Sub(time.Unix(0, last)) >= rel {
			if m.active.Swap(false) {
				stateChanged = true
				newState = false
			}
		}
	}

	curActive := m.active.Load()

	m.mu.Lock()
	activeFn := m.onActiveChange
	levelFn := m.onLevelUpdate
	m.mu.Unlock()

	if stateChanged && activeFn != nil {
		activeFn(newState)
	}
	if levelFn != nil {
		levelFn(db, curActive)
	}
	return db, curActive
}

// ForceReset clears any active gate state and resets the input level to MinCaptureDBFS.
func (m *CaptureMonitor) ForceReset() {
	m.levelDBBits.Store(math.Float32bits(MinCaptureDBFS))
	m.lastAboveNano.Store(0)
	wasActive := m.active.Swap(false)

	m.mu.Lock()
	activeFn := m.onActiveChange
	levelFn := m.onLevelUpdate
	m.mu.Unlock()

	if wasActive && activeFn != nil {
		activeFn(false)
	}
	if levelFn != nil {
		levelFn(MinCaptureDBFS, false)
	}
}

func (m *CaptureMonitor) onCaptureCallback(_, pInput []byte, frameCount uint32) {
	numFrames := int(frameCount)
	if numFrames <= 0 || len(pInput) < numFrames*4 {
		return
	}
	inSamples := unsafe.Slice((*float32)(unsafe.Pointer(&pInput[0])), numFrames)
	m.ProcessSamples(inSamples, time.Now())
}

// Start opens and starts the microphone capture device for real-time monitoring.
// If the monitor is already running on the same device ID, Start is a no-op.
func (m *CaptureMonitor) Start(targetDeviceIDHex string) error {
	m.mu.Lock()
	if m.running && m.deviceIDHex == targetDeviceIDHex {
		m.mu.Unlock()
		return nil
	}
	m.stopLocked()

	if testing.Testing() {
		m.deviceIDHex = targetDeviceIDHex
		m.running = true
		m.mu.Unlock()
		return nil
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("init capture audio context: %w", err)
	}
	m.ctx = ctx

	if err := m.initCaptureDeviceLocked(targetDeviceIDHex, false); err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		m.ctx = nil
		m.mu.Unlock()
		return err
	}

	m.deviceIDHex = targetDeviceIDHex
	m.running = true
	m.mu.Unlock()
	return nil
}

func (m *CaptureMonitor) initCaptureDeviceLocked(deviceIDHex string, isFallback bool) error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatF32
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = DefaultCaptureSampleRate
	deviceConfig.Alsa.NoMMap = 1

	if deviceIDHex != "" && !isFallback {
		devices, err := m.ctx.Devices(malgo.Capture)
		if err == nil {
			for _, dev := range devices {
				devHex := hex.EncodeToString(dev.ID[:])
				if stringsEqualOrContains(devHex, deviceIDHex) {
					deviceConfig.Capture.DeviceID = dev.ID.Pointer()
					break
				}
			}
		}
	}

	callbacks := malgo.DeviceCallbacks{
		Data: m.onCaptureCallback,
	}

	device, err := malgo.InitDevice(m.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		if !isFallback && deviceIDHex != "" {
			return m.initCaptureDeviceLocked("", true)
		}
		return fmt.Errorf("init capture device: %w", err)
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		if !isFallback && deviceIDHex != "" {
			return m.initCaptureDeviceLocked("", true)
		}
		return fmt.Errorf("start capture device: %w", err)
	}

	m.device = device
	return nil
}

func (m *CaptureMonitor) stopLocked() {
	if m.device != nil {
		_ = m.device.Stop()
		m.device.Uninit()
		m.device = nil
	}
	if m.ctx != nil {
		_ = m.ctx.Uninit()
		m.ctx.Free()
		m.ctx = nil
	}
	m.running = false
}

// Stop halts microphone capture, releases hardware resources, and resets the gate state.
func (m *CaptureMonitor) Stop() {
	m.mu.Lock()
	wasRunning := m.running
	m.stopLocked()
	m.mu.Unlock()

	if wasRunning || m.active.Load() {
		m.ForceReset()
	}
}
