package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ComputeDeviceAuto = "auto"
	ComputeDeviceIGPU = "igpu"
	ComputeDeviceDGPU = "dgpu"
	ComputeDeviceCPU  = "cpu"
)

// GPUDevice describes a Vulkan/GPU compute adapter enumerated by towncrier-whisper.
type GPUDevice struct {
	GPUIndex      int    `json:"gpu_index"`
	VKIndex       int    `json:"vk_index"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Type          string `json:"type"` // "dgpu" or "igpu"
	MemoryFreeMB  uint64 `json:"memory_free_mb"`
	MemoryTotalMB uint64 `json:"memory_total_mb"`
}

// ConfigValue returns the stable ComputeDevice configuration key for this adapter.
func (d GPUDevice) ConfigValue() string {
	desc := strings.TrimSpace(d.Description)
	if desc != "" {
		return "vulkan:" + desc
	}
	return fmt.Sprintf("vulkan:%d", d.GPUIndex)
}

// UILabel formats the GPU adapter for display in the Voice Subtitles Compute Device selector.
func (d GPUDevice) UILabel() string {
	kind := "Discrete GPU"
	if strings.EqualFold(d.Type, "igpu") {
		kind = "Integrated iGPU"
	}
	desc := strings.TrimSpace(d.Description)
	if desc == "" {
		desc = d.Name
	}
	if d.MemoryTotalMB >= 256 {
		return fmt.Sprintf("%s (%s • %d MB)", desc, kind, d.MemoryTotalMB)
	}
	return fmt.Sprintf("%s (%s)", desc, kind)
}

var (
	gpuDevicesMu     sync.Mutex
	cachedGPUDevices []GPUDevice
	gpuDevicesCached bool
	gpuEnumOverride  func() ([]GPUDevice, error)
)

// SetGPUDeviceEnumerator overrides Vulkan GPU enumeration (used in headless unit tests).
func SetGPUDeviceEnumerator(fn func() ([]GPUDevice, error)) {
	gpuDevicesMu.Lock()
	gpuEnumOverride = fn
	cachedGPUDevices = nil
	gpuDevicesCached = false
	gpuDevicesMu.Unlock()
}

// NormalizeComputeDevice normalizes a compute device mode string ("auto", "igpu", "dgpu", "cpu", or "vulkan:<name>").
func NormalizeComputeDevice(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "", "auto", "default":
		return ComputeDeviceAuto
	case "igpu", "integrated":
		return ComputeDeviceIGPU
	case "dgpu", "discrete", "gpu":
		return ComputeDeviceDGPU
	case "cpu", "off", "none", "cpu_only":
		return ComputeDeviceCPU
	}
	if strings.HasPrefix(lower, "vulkan:") {
		spec := strings.TrimSpace(trimmed[len("vulkan:"):])
		if spec != "" {
			return "vulkan:" + spec
		}
	}
	return ComputeDeviceAuto
}

// SmartSTTThreads computes the optimal helper/CPU thread count for speech decoding.
// When configured <= 0 ("Auto"), 4-thread CPUs (e.g. Core i5-7300HQ) are capped to 2 threads so games and OBS are never starved.
func SmartSTTThreads(configured int, logicalCPUs int, usingGPU bool) int {
	if logicalCPUs <= 0 {
		logicalCPUs = runtime.NumCPU()
	}
	if configured > 0 {
		return min(max(1, configured), 16)
	}
	if usingGPU {
		return min(max(1, logicalCPUs/2), 2)
	}
	return min(max(1, logicalCPUs/2), 4)
}

// ResolveHelperBinaryPath locates the bundled towncrier-whisper helper executable.
func ResolveHelperBinaryPath() (string, error) {
	if envBin := strings.TrimSpace(os.Getenv("TOWNCRIER_WHISPER_BIN")); envBin != "" {
		if fileExistsNonEmpty(envBin) {
			return envBin, nil
		}
	}

	binName := "towncrier-whisper"
	if runtime.GOOS == "windows" {
		binName = "towncrier-whisper.exe"
	}

	if exePath, err := os.Executable(); err == nil && exePath != "" {
		candidate := filepath.Join(filepath.Dir(exePath), binName)
		if fileExistsNonEmpty(candidate) {
			return candidate, nil
		}
	}

	for _, rel := range []string{
		filepath.Join("build", "bin", binName),
		binName,
	} {
		if abs, err := filepath.Abs(rel); err == nil && fileExistsNonEmpty(abs) {
			return abs, nil
		}
	}

	if lp, err := exec.LookPath(binName); err == nil && lp != "" {
		return lp, nil
	}
	return "", fmt.Errorf("towncrier-whisper helper binary (%s) not found", binName)
}

// EnumerateGPUDevices returns cached Vulkan GPU/iGPU adapters detected on the host.
func EnumerateGPUDevices() ([]GPUDevice, error) {
	gpuDevicesMu.Lock()
	if gpuDevicesCached {
		out := append([]GPUDevice(nil), cachedGPUDevices...)
		gpuDevicesMu.Unlock()
		return out, nil
	}
	override := gpuEnumOverride
	gpuDevicesMu.Unlock()

	if override != nil {
		devs, err := override()
		if err != nil {
			return nil, err
		}
		gpuDevicesMu.Lock()
		cachedGPUDevices = append([]GPUDevice(nil), devs...)
		gpuDevicesCached = true
		gpuDevicesMu.Unlock()
		return devs, nil
	}

	return RefreshGPUDevices()
}

// RefreshGPUDevices re-runs towncrier-whisper --list-devices to refresh the Vulkan GPU adapter list.
func RefreshGPUDevices() ([]GPUDevice, error) {
	gpuDevicesMu.Lock()
	override := gpuEnumOverride
	gpuDevicesMu.Unlock()
	if override != nil {
		devs, err := override()
		if err == nil {
			gpuDevicesMu.Lock()
			cachedGPUDevices = append([]GPUDevice(nil), devs...)
			gpuDevicesCached = true
			gpuDevicesMu.Unlock()
		}
		return devs, err
	}

	binPath, err := ResolveHelperBinaryPath()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "--list-devices")
	configureSubprocess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("enumerate Vulkan GPUs via %s: %w", filepath.Base(binPath), err)
	}

	var devs []GPUDevice
	if err := json.Unmarshal(out, &devs); err != nil {
		return nil, fmt.Errorf("parse Vulkan GPU device list: %w", err)
	}

	gpuDevicesMu.Lock()
	cachedGPUDevices = append([]GPUDevice(nil), devs...)
	gpuDevicesCached = true
	gpuDevicesMu.Unlock()
	return devs, nil
}

// SelectGPUDevice resolves the user's ComputeDevice preference against the available Vulkan adapters.
// When "igpu" is explicitly selected and no integrated GPU exists, it returns (nil, false) so a discrete gaming GPU is left untouched.
func SelectGPUDevice(mode string, devices []GPUDevice) (*GPUDevice, bool) {
	norm := NormalizeComputeDevice(mode)
	if norm == ComputeDeviceCPU || len(devices) == 0 {
		return nil, false
	}

	pickBestDGPU := func() *GPUDevice {
		var best *GPUDevice
		for i := range devices {
			if strings.EqualFold(devices[i].Type, "dgpu") {
				if best == nil || devices[i].MemoryTotalMB > best.MemoryTotalMB {
					best = &devices[i]
				}
			}
		}
		return best
	}

	pickBestIGPU := func() *GPUDevice {
		for i := range devices {
			if strings.EqualFold(devices[i].Type, "igpu") {
				cp := devices[i]
				return &cp
			}
		}
		return nil
	}

	switch norm {
	case ComputeDeviceIGPU:
		if igpu := pickBestIGPU(); igpu != nil {
			cp := *igpu
			return &cp, true
		}
		return nil, false
	case ComputeDeviceDGPU:
		if dgpu := pickBestDGPU(); dgpu != nil {
			cp := *dgpu
			return &cp, true
		}
		return nil, false
	case ComputeDeviceAuto:
		if dgpu := pickBestDGPU(); dgpu != nil {
			cp := *dgpu
			return &cp, true
		}
		if igpu := pickBestIGPU(); igpu != nil {
			cp := *igpu
			return &cp, true
		}
		cp := devices[0]
		return &cp, true
	}

	if strings.HasPrefix(norm, "vulkan:") {
		spec := strings.TrimSpace(norm[len("vulkan:"):])
		for i := range devices {
			if strings.EqualFold(strings.TrimSpace(devices[i].Description), spec) ||
				strings.EqualFold(strings.TrimSpace(devices[i].Name), spec) {
				cp := devices[i]
				return &cp, true
			}
		}
		if idx, err := strconv.Atoi(spec); err == nil {
			for i := range devices {
				if devices[i].GPUIndex == idx || devices[i].VKIndex == idx {
					cp := devices[i]
					return &cp, true
				}
			}
		}
		// Requested GPU is no longer present (e.g., eGPU unplugged) -> fall back safely to Auto
		return SelectGPUDevice(ComputeDeviceAuto, devices)
	}

	return SelectGPUDevice(ComputeDeviceAuto, devices)
}
